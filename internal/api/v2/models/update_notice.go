package models

// update_notice.go surfaces installed model builds that a newer release of the
// same model replaces (an installed variant that is Legacy and names a live
// variant of a different release, classifier.VariantUpdate) in the notification
// bell, so a user who never opens Settings > Analysis > Models still learns that
// a newer build exists. It only informs: the installed build keeps working and
// nothing is downloaded until the user chooses Update on the model card. The
// notice is raised while such a build is installed and clears when it is not
// (after the swap, or after uninstalling), whatever the host hardware, which is
// why it is independent of the recommender, unlike the optimize notice. It is
// evaluated by the same triggers as the optimize notice (fireOptimizeNoticeSync).

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// English fallbacks of the notice, mirroring notifications.content.modelUpdate in
// en.json (TestUpdateNoticeFallbacksMatchEnglishLocale keeps them equal). The
// title has a singular and a plural form; the message takes the model list.
const (
	updateTitleOne        = "An installed model has a newer build"
	updateTitleManyFormat = "%d installed models have newer builds"
	updateMessageFormat   = "A newer build replaces the installed one for: %s. The installed build keeps working until you switch. Open Settings > Analysis > Models and choose Update."
)

// updateOffer is one installed model build that a newer release replaces.
type updateOffer struct {
	CatalogID   string
	ModelName   string
	ToVariantID string
	FromVersion string
	ToVersion   string
}

// updateNotice is the single persistent update bell notice per process, latched
// by the offer set's signature like optimizeNotice.
type updateNotice struct {
	// mu serializes whole evaluations (compute and apply).
	mu    sync.Mutex
	latch notification.PersistentNotice
}

// installedUpdate returns the update offer for the installed variant of entry,
// or false when there is none. An entry that a visible entry supersedes has none
// (its own notice tells the user to move to the successor), and neither does an
// installed variant that is not an older release with a live successor
// (classifier.VariantUpdate). It is the one rule behind the bell notice, the
// catalog response's update field and, through them, the card action.
func installedUpdate(entries []classifier.CatalogEntry, entry *classifier.CatalogEntry, installedVariantID string) (updateOffer, bool) {
	successor, ok := classifier.VariantUpdate(entry, installedVariantID)
	if !ok || visibleSuccessor(entries, entry.SupersededBy) != nil {
		return updateOffer{}, false
	}
	return updateOffer{
		CatalogID:   entry.ID,
		ModelName:   entry.Name,
		ToVariantID: successor.ID,
		FromVersion: classifier.VariantVersion(entry, installedVariantID),
		ToVersion:   classifier.VariantVersion(entry, successor.ID),
	}, true
}

// updateOffers derives the offers from the visible catalog and installedVariant,
// which reports the installed variant of a catalog ID (false when it is not
// installed). Offers keep catalog order.
func updateOffers(entries []classifier.CatalogEntry, installedVariant func(id string) (string, bool)) []updateOffer {
	var offers []updateOffer
	for i := range entries {
		installedID, ok := installedVariant(entries[i].ID)
		if !ok {
			continue
		}
		if offer, ok := installedUpdate(entries, &entries[i], installedID); ok {
			offers = append(offers, offer)
		}
	}
	return offers
}

// updateOffersSignature identifies an offer set independent of order.
func updateOffersSignature(offers []updateOffer) string {
	parts := make([]string, len(offers))
	for i := range offers {
		parts[i] = offers[i].CatalogID + ":" + offers[i].ToVariantID
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// syncUpdateNotice evaluates the update offers from the visible catalog and the
// installed variants and raises, replaces, or clears the persistent bell notice to
// match. It is idempotent and safe to call concurrently. When the notification
// service is not initialized nothing is latched, so the next trigger retries.
func (c *Handler) syncUpdateNotice() {
	if c.ModelManager == nil {
		return
	}
	svc := c.noticeSvc()
	if svc == nil {
		return
	}
	c.update.mu.Lock()
	defer c.update.mu.Unlock()
	c.applyUpdateNotice(svc, updateOffers(classifier.VisibleCatalog(), c.ModelManager.InstalledVariantID))
}

// applyUpdateNotice reconciles the latched notice with offers. The caller holds
// c.update.mu. A failed delete keeps the latch and a failed create is not
// latched; either way the latch re-arms a bounded retry through
// ScheduleOptimizeNoticeSync.
func (c *Handler) applyUpdateNotice(svc noticeService, offers []updateOffer) {
	sig := updateOffersSignature(offers)
	c.LogDebugIfEnabled("model update notice evaluated",
		logger.Int("offer_count", len(offers)), logger.String("signature", sig))
	c.update.latch.SetRetry(c.ScheduleOptimizeNoticeSync)
	err := c.update.latch.Reconcile(svc, func() (string, func() *notification.Notification) {
		return sig, func() *notification.Notification { return newUpdateNotification(offers) }
	})
	if err != nil {
		c.LogWarnIfEnabled("failed to update model update notification",
			logger.Int("offer_count", len(offers)), logger.Error(err))
	}
}

// newUpdateNotification builds the bell notice for a non-empty offer set. Each
// model reads "Name (from → to)", which keeps the list language-neutral; the
// English fallbacks mirror en.json.
func newUpdateNotification(offers []updateOffer) *notification.Notification {
	entries := make([]string, len(offers))
	for i := range offers {
		entries[i] = fmt.Sprintf("%s (%s → %s)", offers[i].ModelName, offers[i].FromVersion, offers[i].ToVersion)
	}
	models := strings.Join(entries, ", ")
	title := fmt.Sprintf(updateTitleManyFormat, len(offers))
	if len(offers) == 1 {
		title = updateTitleOne
	}
	return notification.NewNotification(
		notification.TypeInfo,
		notification.PriorityMedium,
		title,
		fmt.Sprintf(updateMessageFormat, models),
	).
		WithComponent(notification.ComponentClassifier).
		WithTitleKey(notification.MsgModelUpdateTitle, map[string]any{"count": len(offers)}).
		WithMessageKey(notification.MsgModelUpdateMessage, map[string]any{"models": models}).
		WithDeliveryTarget(notification.DeliveryTargetBell)
}
