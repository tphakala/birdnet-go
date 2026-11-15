package models

// superseded_notice.go surfaces installed models that the catalog says have been
// replaced (CatalogEntry.SupersededBy) in the notification bell, so a user who
// never opens Settings > Analysis > Models still learns that a newer model exists
// and that the old one can go. The notice is raised while a superseded model is
// installed and its successor is visible in the active catalog, whether or not
// the successor is installed yet (if it cleared on installing the successor,
// nobody would be told to remove the old model and both would keep running).
// Deleting it in the bell hides it until the next restart; uninstalling the
// superseded model clears it for good. It is evaluated by the same triggers as
// the optimize notice (fireOptimizeNoticeSync).

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// supersededOffer is one installed model whose catalog entry names a visible
// successor.
type supersededOffer struct {
	CatalogID     string
	ModelName     string
	SuccessorID   string
	SuccessorName string
}

// supersededNotice is the single persistent superseded bell notice per process,
// latched by the offer set's signature like optimizeNotice.
type supersededNotice struct {
	// mu serializes whole evaluations (compute and apply).
	mu    sync.Mutex
	latch notification.PersistentNotice
}

// supersededOffers derives the offers from the visible catalog: an entry
// qualifies when it names a successor, is installed, and the successor is among
// entries. Offers keep catalog order.
func supersededOffers(entries []classifier.CatalogEntry, installed func(id string) bool) []supersededOffer {
	var offers []supersededOffer
	for i := range entries {
		entry := &entries[i]
		successor := visibleSuccessor(entries, entry.SupersededBy)
		if successor == nil || !installed(entry.ID) {
			continue
		}
		offers = append(offers, supersededOffer{
			CatalogID:     entry.ID,
			ModelName:     entry.Name,
			SuccessorID:   successor.ID,
			SuccessorName: successor.Name,
		})
	}
	return offers
}

// supersededOffersSignature identifies an offer set independent of order.
func supersededOffersSignature(offers []supersededOffer) string {
	parts := make([]string, len(offers))
	for i := range offers {
		parts[i] = offers[i].CatalogID + ">" + offers[i].SuccessorID
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// syncSupersededNotice evaluates the superseded offers from the visible catalog
// and the installed models and raises, replaces, or clears the persistent bell
// notice to match. It is idempotent and safe to call concurrently. When the
// notification service is not initialized nothing is latched, so the next
// trigger retries.
func (c *Handler) syncSupersededNotice() {
	if c.ModelManager == nil {
		return
	}
	svc := c.noticeSvc()
	if svc == nil {
		return
	}
	c.superseded.mu.Lock()
	defer c.superseded.mu.Unlock()
	c.applySupersededNotice(svc, supersededOffers(classifier.VisibleCatalog(), c.ModelManager.IsInstalled))
}

// applySupersededNotice reconciles the latched notice with offers. The caller
// holds c.superseded.mu. A failed delete keeps the latch and a failed create is
// not latched; either way the latch re-arms a bounded retry through
// ScheduleOptimizeNoticeSync.
func (c *Handler) applySupersededNotice(svc noticeService, offers []supersededOffer) {
	sig := supersededOffersSignature(offers)
	c.LogDebugIfEnabled("model superseded notice evaluated",
		logger.Int("offer_count", len(offers)), logger.String("signature", sig))
	c.superseded.latch.SetRetry(c.ScheduleOptimizeNoticeSync)
	err := c.superseded.latch.Reconcile(svc, func() (string, func() *notification.Notification) {
		return sig, func() *notification.Notification { return newSupersededNotification(offers) }
	})
	if err != nil {
		c.LogWarnIfEnabled("failed to update model superseded notification",
			logger.Int("offer_count", len(offers)), logger.Error(err))
	}
}

// newSupersededNotification builds the bell notice for a non-empty offer set.
// Each model reads "Old name → New name", which keeps the list language-neutral;
// the English fallbacks mirror en.json.
func newSupersededNotification(offers []supersededOffer) *notification.Notification {
	pairs := make([]string, len(offers))
	for i := range offers {
		pairs[i] = offers[i].ModelName + " → " + offers[i].SuccessorName
	}
	models := strings.Join(pairs, ", ")
	title := fmt.Sprintf("%d installed models have newer releases", len(offers))
	if len(offers) == 1 {
		title = "1 installed model has a newer release"
	}
	message := fmt.Sprintf(
		"A newer release replaces: %s. Open Settings > Analysis > Models, install the newer model and remove the old one.",
		models)
	return notification.NewNotification(
		notification.TypeInfo,
		notification.PriorityMedium,
		title,
		message,
	).
		WithComponent(notification.ComponentClassifier).
		WithTitleKey(notification.MsgModelSupersededTitle, map[string]any{"count": len(offers)}).
		WithMessageKey(notification.MsgModelSupersededMessage, map[string]any{"models": models}).
		WithDeliveryTarget(notification.DeliveryTargetBell)
}
