package models

// optimize_notice.go surfaces the model gallery's within-model "optimize" offers
// in the notification bell. The gallery derives the offers client-side from the
// catalog, so a user who never opens Settings > Analysis > Models never learns
// that a better build of an installed model exists for this host (#4423: a
// Raspberry Pi 5 whose stock BirdNET v2.4 build failed every inference was fixed
// by the offered build). The bell notice applies the same offer rule to the
// same recommender pass as the catalog endpoint. It is a snapshot re-evaluated
// at startup, after model topology changes, installs and uninstalls, and after
// location, ModelRegion or primary model path changes.

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/recommend"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// optimizeNoticeDebounce coalesces a burst of topology events (the startup load of
// several models, a variant swap that unloads then loads) into one evaluation.
const optimizeNoticeDebounce = 3 * time.Second

// optimizeNoticeComponent is the notification component for the optimize notice,
// matching the other classifier-originated bell notices.
const optimizeNoticeComponent = "classifier"

// optimizeOffer is one installed model whose host-recommended variant differs
// from the installed one: the ids-only counterpart of the frontend OptimizeOffer
// (frontend/src/lib/utils/variantSelection.ts), produced by the same rule.
type optimizeOffer struct {
	CatalogID     string
	ModelName     string
	FromVariantID string
	ToVariantID   string
}

// noticeService is the slice of the notification service the optimize notice
// uses. It is a seam so tests do not touch the process-wide singleton.
type noticeService interface {
	CreateWithMetadata(notif *notification.Notification) error
	Delete(id string) error
}

// optimizeNotice is the single persistent optimize bell notice per process. id is
// the live notification's ID ("" when none is outstanding) and sig identifies the
// offer set it was raised for, so an unchanged set is a no-op (a notice the user
// deleted is not re-raised until the offers change or the process restarts) while
// a changed set replaces the notice.
type optimizeNotice struct {
	// mu serializes whole evaluations (compute and apply), so two overlapping syncs
	// cannot apply their results out of order.
	mu  sync.Mutex
	id  string
	sig string

	// timerMu guards the debounce timer and the started and stopped flags.
	timerMu sync.Mutex
	timer   *time.Timer
	started bool
	stopped bool
}

// optimizeOffers derives the optimize offers from the visible catalog, the
// installed variant per catalog ID, and the recommender's verdicts. It is pure
// and applies exactly the frontend rule: an entry qualifies when it carries
// variants, is installed, its installed variant differs from the host-recommended
// variant, and that recommended variant is compatible. The installed variant may
// be absent from the catalog (a dropped build); the offer still stands so the user
// can move off it. Offers keep catalog order.
func optimizeOffers(entries []classifier.CatalogEntry, installed map[string]string, byVariant map[string]map[string]recommend.Recommendation, recommended map[string]string) []optimizeOffer {
	var offers []optimizeOffer
	for i := range entries {
		entry := &entries[i]
		if len(entry.Variants) == 0 {
			continue
		}
		installedID, isInstalled := installed[entry.ID]
		recommendedID := recommended[entry.ID]
		if !isInstalled || installedID == "" || recommendedID == "" || installedID == recommendedID {
			continue
		}
		rec, ok := byVariant[entry.ID][recommendedID]
		if !ok || !rec.Compatible {
			continue
		}
		offers = append(offers, optimizeOffer{
			CatalogID:     entry.ID,
			ModelName:     entry.Name,
			FromVariantID: installedID,
			ToVariantID:   recommendedID,
		})
	}
	return offers
}

// optimizeOffersSignature identifies an offer set independent of order.
func optimizeOffersSignature(offers []optimizeOffer) string {
	parts := make([]string, len(offers))
	for i := range offers {
		parts[i] = offers[i].CatalogID + ":" + offers[i].ToVariantID
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// currentOptimizeOffers computes the offers for this host from live state: the
// visible catalog, the installed variants, and the same recommender pass the
// catalog endpoint runs, after ensuring the OpenVINO device probe has run
// (ensureHostProbed), minus an offer that would replace a custom primary model
// (withoutCustomPrimaryOffer). It is not gated on request authentication because its
// only consumer is the bell notice, which guests cannot read (the notifications
// API shows unauthenticated callers detection notices only).
// The caller guarantees c.ModelManager is non-nil (syncOptimizeNotice checks it).
func (c *Handler) currentOptimizeOffers() []optimizeOffer {
	visible := classifier.VisibleCatalog()
	installed := make(map[string]string, len(visible))
	for i := range visible {
		if vid, ok := c.ModelManager.InstalledVariantID(visible[i].ID); ok {
			installed[visible[i].ID] = vid
		}
	}
	s := c.CurrentSettings()
	ortStatus := inference.CheckORTAvailability(s.BirdNET.ONNXRuntimePath)
	c.ensureHostProbed()
	byVariant, recommended, _ := c.rankCatalog(visible, ortStatus)
	offers := optimizeOffers(visible, installed, byVariant, recommended)
	return withoutCustomPrimaryOffer(offers, visible, s.BirdNET.ModelPath)
}

// withoutCustomPrimaryOffer drops the offer for the permanent BirdNET v2.4 entry
// when the user configured their own primary model file. The installed-model
// scan reports any configured file that is not a gallery build as the BuiltIn
// baseline, so without this the bell would tell a user running a custom model to
// "optimize" it away (the swap rewrites BirdNET.ModelPath to the gallery build).
// A configured path that points at a deleted gallery build is also skipped:
// that is a configuration the user may be troubleshooting, not one to push a
// swap at. configuredPrimary is settings.BirdNET.ModelPath, the documented
// "what did the user configure" read.
func withoutCustomPrimaryOffer(offers []optimizeOffer, entries []classifier.CatalogEntry, configuredPrimary string) []optimizeOffer {
	if configuredPrimary == "" {
		return offers
	}
	return slices.DeleteFunc(offers, func(o optimizeOffer) bool {
		for i := range entries {
			e := &entries[i]
			if e.ID != o.CatalogID || !classifier.IsPermanentEntry(e) {
				continue
			}
			for j := range e.Variants {
				if e.Variants[j].BuiltIn && e.Variants[j].ID == o.FromVariantID {
					return true
				}
			}
		}
		return false
	})
}

// noticeSvc resolves the notification service, preferring the test seam. It
// returns nil when the service is not initialized yet.
func (c *Handler) noticeSvc() noticeService {
	if c.notices != nil {
		return c.notices
	}
	// Check the concrete pointer before boxing it: returning a nil *Service as
	// noticeService would yield a non-nil interface, defeating the caller's nil
	// check and panicking on the first call.
	if svc := notification.GetService(); svc != nil {
		return svc
	}
	return nil
}

// syncOptimizeNotice evaluates the optimize offers and raises, replaces, or
// clears the persistent bell notice to match. It is idempotent and safe to call
// concurrently (optimize.mu serializes whole evaluations). When the notification
// service is not initialized nothing is latched, so the next trigger retries.
func (c *Handler) syncOptimizeNotice() {
	if c.ModelManager == nil {
		return
	}
	svc := c.noticeSvc()
	if svc == nil {
		return
	}
	c.optimize.mu.Lock()
	defer c.optimize.mu.Unlock()
	c.applyOptimizeNotice(svc, c.currentOptimizeOffers())
}

// applyOptimizeNotice reconciles the latched notice with offers. The caller holds
// c.optimize.mu.
func (c *Handler) applyOptimizeNotice(svc noticeService, offers []optimizeOffer) {
	n := &c.optimize
	sig := optimizeOffersSignature(offers)
	// id and sig are always set and cleared together, so an equal signature
	// means the latched notice already matches (or none is due).
	if sig == n.sig {
		return
	}
	if n.id != "" {
		// A user-deleted notice is fine: Delete treats a missing one as success.
		_ = svc.Delete(n.id)
		n.id = ""
		n.sig = ""
	}
	if len(offers) == 0 {
		return
	}
	notif := newOptimizeNotification(offers)
	if err := svc.CreateWithMetadata(notif); err != nil {
		c.LogWarnIfEnabled("failed to create model optimize notification",
			logger.Int("offer_count", len(offers)), logger.Error(err))
		return // not latched: the next trigger retries
	}
	n.id = notif.ID
	n.sig = sig
}

// newOptimizeNotification builds the bell notice for a non-empty offer set.
func newOptimizeNotification(offers []optimizeOffer) *notification.Notification {
	names := make([]string, len(offers))
	for i := range offers {
		names[i] = offers[i].ModelName
	}
	models := strings.Join(names, ", ")
	// Neutral wording: an offer is a build better matched to this host's hardware
	// or to its resolved region, and a regional offer is not necessarily faster.
	// The English fallbacks mirror en.json. The model list follows a fixed noun so
	// the sentence reads the same for one model or several.
	title := fmt.Sprintf("%d models have better builds for this system", len(offers))
	if len(offers) == 1 {
		title = "1 model has a better build for this system"
	}
	message := fmt.Sprintf(
		"A build better matched to this system's hardware or location is available for: %s. Open Settings > Analysis > Models and choose Optimize to switch.",
		models)
	return notification.NewNotification(
		notification.TypeInfo,
		notification.PriorityMedium,
		title,
		message,
	).
		WithComponent(optimizeNoticeComponent).
		WithTitleKey(notification.MsgModelOptimizeTitle, map[string]any{"count": len(offers)}).
		WithMessageKey(notification.MsgModelOptimizeMessage, map[string]any{"models": models}).
		WithDeliveryTarget(notification.DeliveryTargetBell)
}

// StartOptimizeNoticeSync enables optimize notice evaluations and schedules the
// first one (the startup evaluation). Until it is called, ScheduleOptimizeNoticeSync
// is a no-op, so a handler that is not serving (a controller built without
// routes, as tests do) never arms a timer from its install or uninstall paths.
// It is a no-op after StopOptimizeNoticeSync.
func (c *Handler) StartOptimizeNoticeSync() {
	if c == nil || c.ModelManager == nil {
		return
	}
	n := &c.optimize
	n.timerMu.Lock()
	defer n.timerMu.Unlock()
	if n.stopped {
		return
	}
	n.started = true
	c.armOptimizeNoticeTimerLocked()
}

// ScheduleOptimizeNoticeSync re-evaluates the optimize notice after a short
// debounce, so a burst of triggers costs one evaluation (each evaluation probes
// the host hardware and ranks the whole catalog). It is a no-op before
// StartOptimizeNoticeSync and after StopOptimizeNoticeSync.
func (c *Handler) ScheduleOptimizeNoticeSync() {
	if c == nil || c.ModelManager == nil {
		return
	}
	n := &c.optimize
	n.timerMu.Lock()
	defer n.timerMu.Unlock()
	if !n.started || n.stopped {
		return
	}
	c.armOptimizeNoticeTimerLocked()
}

// armOptimizeNoticeTimerLocked (re)starts the debounce timer. The caller holds
// c.optimize.timerMu.
func (c *Handler) armOptimizeNoticeTimerLocked() {
	n := &c.optimize
	if n.timer != nil {
		n.timer.Stop()
	}
	n.timer = time.AfterFunc(optimizeNoticeDebounce, c.fireOptimizeNoticeSync)
}

// fireOptimizeNoticeSync is the debounce timer's callback. It runs the evaluation
// as a Core-tracked goroutine so Controller.Shutdown's Core.Wait joins it, and it
// starts nothing once StopOptimizeNoticeSync has run: both the stopped check and
// the tracked start happen under timerMu, which StopOptimizeNoticeSync takes to
// set stopped, so no evaluation can be added to the WaitGroup after Stop returns.
func (c *Handler) fireOptimizeNoticeSync() {
	n := &c.optimize
	n.timerMu.Lock()
	defer n.timerMu.Unlock()
	if n.stopped {
		return
	}
	c.Go(c.syncOptimizeNotice)
}

// StopOptimizeNoticeSync stops any pending evaluation and ignores later
// schedules. Controller.Shutdown calls it before Core.Cancel and Core.Wait:
// after it returns no new evaluation starts, and one already running is joined
// by Core.Wait. That running evaluation can hold shutdown for as long as an
// OpenVINO device probe child takes, bounded by the probe timeout.
func (c *Handler) StopOptimizeNoticeSync() {
	if c == nil {
		return
	}
	n := &c.optimize
	n.timerMu.Lock()
	defer n.timerMu.Unlock()
	n.stopped = true
	if n.timer != nil {
		n.timer.Stop()
		n.timer = nil
	}
}
