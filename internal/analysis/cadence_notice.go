package analysis

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier/cadence"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// cadenceNoticeComponent is the notification component of the cadence notice.
const cadenceNoticeComponent = "analysis"

// cadenceNoticeOverlapDecimals is how many decimals overlaps show in the notice;
// the planner works on a 100 ms grid.
const cadenceNoticeOverlapDecimals = 1

// cadenceNotice raises one bell notice per process when the first published
// cadence plan is capped or overloaded, so a user upgrading onto hardware that
// cannot sustain the configured overlap learns why the false positive filter
// can need fewer confirmations than before. Plans published later only update or
// clear it: a capped or overloaded plan replaces the notice (as a new, unread
// one) when its status or a value its text states changes, any other plan clears
// it for good. A cap that first appears after a startup plan that was not capped
// or overloaded raises no bell; the settings page shows it inline.
//
// The zero value is ready to use; service defaults to the process-wide
// notification service. It is safe for concurrent use.
type cadenceNotice struct {
	// mu serializes whole evaluations, so overlapping publishes and the latch's
	// retry callback apply their results in order. Lock order: mu, then the
	// latch's own lock.
	mu    sync.Mutex
	latch notification.PersistentNotice

	// service returns the notification service, or nil while it is not up.
	// Tests replace it.
	service func() notification.NoticeService

	seen   bool          // a plan has been observed
	active bool          // a notice is due
	plan   *cadence.Plan // the plan the due notice describes
}

// observe records a newly published plan and brings the bell notice in line.
func (n *cadenceNotice) observe(plan *cadence.Plan) {
	if plan == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	switch {
	case !n.seen:
		n.seen = true
		n.active = cadenceNoticeDue(plan)
	case n.active && !cadenceNoticeDue(plan):
		n.active = false
	}
	if n.active {
		n.plan = plan
	} else {
		n.plan = nil
	}
	n.reconcileLocked()
}

// retry re-applies the current state; the latch calls it after a failed create.
func (n *cadenceNotice) retry() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reconcileLocked()
}

// reconcileLocked applies the due state to the latch. The caller holds n.mu.
// Without a notification service nothing is applied; the next publish retries.
func (n *cadenceNotice) reconcileLocked() {
	svc := n.noticeService()
	if svc == nil {
		return
	}
	n.latch.SetRetry(n.retry)
	plan := n.plan
	if err := n.latch.Reconcile(svc, func() (string, func() *notification.Notification) {
		if plan == nil {
			return "", nil
		}
		return cadenceNoticeSignature(plan), func() *notification.Notification { return newCadenceNotification(plan) }
	}); err != nil {
		GetLogger().Warn("failed to update analysis cadence notification", logger.Error(err))
	}
}

// noticeService returns the configured service, or the process-wide one. A nil
// *notification.Service is returned as a nil interface.
func (n *cadenceNotice) noticeService() notification.NoticeService {
	if n.service != nil {
		return n.service()
	}
	if svc := notification.GetService(); svc != nil {
		return svc
	}
	return nil
}

// cadenceNoticeSignature keys the notice on its status and the values its text
// states, so a later plan that changes any of them replaces the notice and one
// that changes none leaves it alone. The overloaded text states no overlap.
func cadenceNoticeSignature(plan *cadence.Plan) string {
	if plan.Status == cadence.StatusOverloaded {
		return fmt.Sprintf("%s/%dm/%ds", plan.Status, plan.ModelCount, plan.SourceCount)
	}
	return fmt.Sprintf("%s/%dms/%dms/%dm/%ds", plan.Status,
		plan.ConfiguredBaseOverlap.Milliseconds(), plan.EffectiveBaseOverlap.Milliseconds(),
		plan.ModelCount, plan.SourceCount)
}

// cadenceNoticeDue reports whether a plan limits the analysis below the
// configured cadence.
func cadenceNoticeDue(plan *cadence.Plan) bool {
	return plan.Status == cadence.StatusCapped || plan.Status == cadence.StatusOverloaded
}

// formatNoticeOverlap formats an overlap in seconds for the notice.
func formatNoticeOverlap(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', cadenceNoticeOverlapDecimals, 64)
}

// countNoun returns "1 model" or "2 models" for the English fallback.
func countNoun(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// newCadenceNotification builds the bell notice for a capped or overloaded plan.
// Title and Message carry the English fallback; the keys and params let the UI
// translate it.
func newCadenceNotification(plan *cadence.Plan) *notification.Notification {
	models := countNoun(plan.ModelCount, "model", "models")
	sources := countNoun(plan.SourceCount, "audio source", "audio sources")
	params := map[string]any{
		"models":     plan.ModelCount,
		"sources":    plan.SourceCount,
		"configured": formatNoticeOverlap(plan.ConfiguredBaseOverlap),
		"effective":  formatNoticeOverlap(plan.EffectiveBaseOverlap),
	}

	if plan.Status == cadence.StatusOverloaded {
		return notification.NewNotification(
			notification.TypeWarning,
			notification.PriorityHigh,
			"This device cannot keep up with audio analysis",
			fmt.Sprintf("Running %s on %s exceeds the measured capacity of this device even without overlap, so analysis can fall behind and detections can be missed. Enable fewer models or audio sources.", models, sources),
		).
			WithComponent(cadenceNoticeComponent).
			WithTitleKey(notification.MsgCadenceOverloadedTitle, nil).
			WithMessageKey(notification.MsgCadenceOverloadedMessage, params).
			WithDeliveryTarget(notification.DeliveryTargetBell)
	}

	return notification.NewNotification(
		notification.TypeWarning,
		notification.PriorityMedium,
		"Analysis overlap limited for this device",
		fmt.Sprintf("Running %s on %s at the configured %s s overlap exceeds the measured capacity of this device, so audio is analyzed with %s s overlap. This can lower the number of confirmations the false positive filter requires. Your saved settings are unchanged; see Settings > Analysis.", models, sources, params["configured"], params["effective"]),
	).
		WithComponent(cadenceNoticeComponent).
		WithTitleKey(notification.MsgCadenceCappedTitle, nil).
		WithMessageKey(notification.MsgCadenceCappedMessage, params).
		WithDeliveryTarget(notification.DeliveryTargetBell)
}
