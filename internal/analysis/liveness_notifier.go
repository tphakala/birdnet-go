package analysis

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// Liveness notification coalescing parameters. This reuses the same burst-tracker
// mechanism as the error hook (internal/notification/error_integration.go), with a
// wider window tuned for flapping audio sources: their silence/recovery events are
// sparser and far more repetitive than distinct errors, so a longer window is
// needed to actually collapse a source that drops every few minutes. The first
// livenessBurstThreshold events in a window pass individually, the next becomes a
// single flapping summary, and the rest are suppressed until the window rolls
// over. Only the user-facing notification is affected; source restart/recovery
// is driven independently by the watchdog and is not touched here.
const (
	livenessBurstThreshold = 2
	livenessBurstWindow    = 15 * time.Minute
	// livenessBurstCategory is the burst-tracker category for liveness events.
	// The tracker keys on sourceID+category, so silence and recovery events for
	// the same source share one bucket (one flapping incident).
	livenessBurstCategory = "liveness"
	// livenessComponent labels liveness notifications for telemetry/UI filtering.
	livenessComponent = "audiocore.liveness"
)

// livenessNotifySender delivers a single audio-source notification. It is
// injected so the coalescing policy can be unit-tested without the notification
// service. The real sender calls notification.Service.CreateWithComponent.
type livenessNotifySender func(priority notification.Priority, title, body string)

// livenessNotifier coalesces repeated transient silence/recovery notifications
// for a flapping audio source into a single summary, while letting critical
// (escalated/failed) states through immediately and unbatched. It exists to stop
// a source that repeatedly drops and self-recovers (e.g. an RTSP camera that
// keeps tearing down its session) from producing a high-priority notification on
// every cycle, without changing any stream or recovery behavior.
type livenessNotifier struct {
	burst *notification.ErrorBurstTracker
	send  livenessNotifySender
}

// newLivenessNotifier builds a notifier that dispatches through send. A nil send
// is replaced with a no-op so notify never panics on a misconfigured caller.
func newLivenessNotifier(send livenessNotifySender) *livenessNotifier {
	if send == nil {
		send = func(notification.Priority, string, string) {}
	}
	return &livenessNotifier{
		burst: notification.NewErrorBurstTracker(livenessBurstThreshold, livenessBurstWindow),
		send:  send,
	}
}

// notify dispatches a watchdog state change to the user, coalescing transient
// flapping but never suppressing a genuinely-down source.
func (n *livenessNotifier) notify(sourceID string, state audiocore.LivenessState, msg string) {
	// Escalated/failed mean the source did not recover on its own; always alert
	// immediately and bypass coalescing so a real outage is never hidden. Reset the
	// burst window as well, so the "recovered" all-clear that follows a critical
	// outage is delivered fresh instead of being suppressed by the flapping burst
	// that preceded the escalation.
	if state == audiocore.StateEscalated || state == audiocore.StateFailed {
		n.burst.Reset(sourceID, livenessBurstCategory)
		n.send(notification.PriorityCritical, "Audio source "+msg, "Source "+sourceID+": "+msg)
		return
	}

	// Transient silence/recovery: collapse repeats per source.
	action, summary := n.burst.Record(sourceID, livenessBurstCategory, msg)
	switch action {
	case notification.BurstActionAllow:
		n.send(notification.PriorityHigh, "Audio source "+msg, "Source "+sourceID+": "+msg)
	case notification.BurstActionSummary:
		// One summary stands in for the rest of the window. Say the source is
		// unstable (not down) and point at the dashboard, since further transient
		// alerts are grouped away until the window rolls over. (An escalation to a
		// critical outage resets the window, so a post-critical "recovered" is still
		// delivered rather than grouped.)
		body := fmt.Sprintf("Source %s is unstable: %d silence/recovery events in %d min. "+
			"Further alerts are grouped to reduce noise; see the dashboard for live status.",
			sourceID, summary.Count, summary.WindowMin)
		n.send(notification.PriorityHigh, "Audio source flapping", body)
	case notification.BurstActionSuppress:
		// Already summarized this window; drop to avoid notification spam.
	}
}
