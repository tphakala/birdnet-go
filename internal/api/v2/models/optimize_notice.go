package models

// optimize_notice.go surfaces the model gallery's within-model "optimize" offers
// in the notification bell. The gallery derives the offers client-side from the
// catalog, so a user who never opens Settings > Analysis > Model gallery never
// learns that a better build of an installed model exists for this host (#4423:
// a Raspberry Pi 5 whose stock BirdNET v2.4 build failed every inference was
// fixed by the offered build). The bell notice is computed with the same
// recommender and host profile as the catalog endpoint, so the two cannot
// disagree, and it is kept in sync on startup and on every model topology change.

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

// optimizeOfferCountMetadataKey carries the number of offers on the notice.
const optimizeOfferCountMetadataKey = "optimize_offer_count"

// optimizeOffer is one installed model whose host-recommended variant differs
// from the installed one. It mirrors the frontend OptimizeOffer
// (frontend/src/lib/utils/variantSelection.ts).
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

	// timerMu guards the debounce timer and the stopped flag.
	timerMu sync.Mutex
	timer   *time.Timer
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
// catalog endpoint runs. It is not gated on request authentication because its
// only consumer is the bell notice, which guests cannot read (the notifications
// API shows unauthenticated callers detection notices only).
func (c *Handler) currentOptimizeOffers() []optimizeOffer {
	if c.ModelManager == nil {
		return nil
	}
	visible := classifier.VisibleCatalog()
	installed := make(map[string]string, len(visible))
	for i := range visible {
		if vid, ok := c.ModelManager.InstalledVariantID(visible[i].ID); ok {
			installed[visible[i].ID] = vid
		}
	}
	ortStatus := inference.CheckORTAvailability(c.CurrentSettings().BirdNET.ONNXRuntimePath)
	byVariant, recommended, _ := c.rankCatalog(visible, ortStatus)
	return optimizeOffers(visible, installed, byVariant, recommended)
}

// noticeSvc resolves the notification service, preferring the test seam. It
// returns nil when the service is not initialized yet.
func (c *Handler) noticeSvc() noticeService {
	if c.notices != nil {
		return c.notices
	}
	if svc := notification.GetService(); svc != nil {
		return svc
	}
	return nil
}

// SyncOptimizeNotice evaluates the optimize offers and raises, replaces, or
// clears the persistent bell notice to match. It is idempotent and safe to call
// concurrently. When the notification service is not initialized nothing is
// latched, so the next call retries.
func (c *Handler) SyncOptimizeNotice() {
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
	if sig == n.sig && (n.id != "" || sig == "") {
		return // unchanged offer set: nothing to do
	}
	if n.id != "" {
		// A user-deleted notice is fine: Delete then just reports it missing.
		_ = svc.Delete(n.id)
		n.id = ""
		n.sig = ""
	}
	if len(offers) == 0 {
		return
	}
	notif := newOptimizeNotification(offers)
	if err := svc.CreateWithMetadata(notif); err != nil {
		c.LogWarnIfEnabled("failed to create model optimize notification", logger.Error(err))
		return // not latched: the next sync retries
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
	title := fmt.Sprintf("%d models have a better build for this system", len(offers))
	if len(offers) == 1 {
		title = "1 model has a better build for this system"
	}
	message := fmt.Sprintf(
		"A build of %s that better matches this system's hardware or location is available. Open Settings > Analysis > Model gallery and choose Optimize to switch.",
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
		WithMetadata(optimizeOfferCountMetadataKey, len(offers)).
		WithDeliveryTarget(notification.DeliveryTargetBell)
}

// ScheduleOptimizeNoticeSync runs SyncOptimizeNotice after a short debounce, so a
// burst of topology events costs one evaluation (each evaluation probes the host
// hardware and ranks the whole catalog). It is a no-op after StopOptimizeNotice.
func (c *Handler) ScheduleOptimizeNoticeSync() {
	if c == nil || c.ModelManager == nil {
		return
	}
	n := &c.optimize
	n.timerMu.Lock()
	defer n.timerMu.Unlock()
	if n.stopped {
		return
	}
	if n.timer != nil {
		n.timer.Stop()
	}
	n.timer = time.AfterFunc(optimizeNoticeDebounce, c.SyncOptimizeNotice)
}

// StopOptimizeNotice stops any pending evaluation and ignores later schedules. It
// is called on controller shutdown so no evaluation fires into a torn-down
// process.
func (c *Handler) StopOptimizeNotice() {
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
