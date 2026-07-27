package analysis

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// countTitle returns how many recorded sends have the exact given title.
func countTitle(sent []recordedNotif, title string) int {
	n := 0
	for i := range sent {
		if sent[i].title == title {
			n++
		}
	}
	return n
}

// countPriority returns how many recorded sends have the given priority.
func countPriority(sent []recordedNotif, priority notification.Priority) int {
	n := 0
	for i := range sent {
		if sent[i].priority == priority {
			n++
		}
	}
	return n
}

// recordedNotif captures a single send from the livenessNotifier under test.
type recordedNotif struct {
	priority notification.Priority
	title    string
	body     string
}

// newRecordingNotifier returns a livenessNotifier whose sends are appended to
// the returned slice pointer. notify is driven sequentially by the watchdog, so
// the recorder needs no synchronization.
func newRecordingNotifier() (*livenessNotifier, *[]recordedNotif) {
	var sent []recordedNotif
	n := newLivenessNotifier(func(priority notification.Priority, title, body string) {
		sent = append(sent, recordedNotif{priority: priority, title: title, body: body})
	})
	return n, &sent
}

// TestLivenessNotifier_CriticalBypassesCoalescing verifies that escalated and
// failed states always produce an immediate critical notification, never
// coalesced, no matter how many arrive in the window.
func TestLivenessNotifier_CriticalBypassesCoalescing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		state audiocore.LivenessState
	}{
		{"escalated", audiocore.StateEscalated},
		{"failed", audiocore.StateFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n, sent := newRecordingNotifier()

			const calls = 6
			for range calls {
				n.notify("rtsp_camera", tc.state, "retries exhausted")
			}

			require.Len(t, *sent, calls, "every critical event must be sent, none coalesced")
			for _, s := range *sent {
				assert.Equal(t, notification.PriorityCritical, s.priority)
			}
		})
	}
}

// TestLivenessNotifier_TransientCoalesced verifies that a source repeatedly
// dropping and self-recovering produces individual notifications up to the
// threshold, then exactly one flapping summary, then silence.
func TestLivenessNotifier_TransientCoalesced(t *testing.T) {
	t.Parallel()
	n, sent := newRecordingNotifier()

	// Alternate silence/recovery events for one flapping source. Derive the event
	// count from the threshold so the suppress phase stays exercised if the
	// threshold constant changes.
	states := []audiocore.LivenessState{audiocore.StateAlarmed, audiocore.StateHealthy}
	msgs := []string{"silence detected", "recovered"}
	events := livenessBurstThreshold + 8
	for i := range events {
		n.notify("rtsp_camera", states[i%2], msgs[i%2])
	}

	// First `threshold` events pass individually, the (threshold+1)th becomes a
	// single summary, the rest are suppressed.
	require.Len(t, *sent, livenessBurstThreshold+1)

	for i := range livenessBurstThreshold {
		assert.Equal(t, notification.PriorityHigh, (*sent)[i].priority)
		assert.Equal(t, "Audio source "+msgs[i%2], (*sent)[i].title)
	}

	summary := (*sent)[livenessBurstThreshold]
	assert.Equal(t, notification.PriorityHigh, summary.priority)
	assert.Contains(t, summary.title, "flapping")
	assert.Contains(t, summary.body, "rtsp_camera")
	// The summary must report the coalesced count and window, not just a label.
	assert.Contains(t, summary.body, fmt.Sprintf("%d", livenessBurstThreshold+1))
	assert.Contains(t, summary.body, fmt.Sprintf("%d min", int(livenessBurstWindow.Minutes())))
}

// TestLivenessNotifier_IndependentPerSource verifies that each source has its
// own coalescing budget: one noisy source must not silence another.
func TestLivenessNotifier_IndependentPerSource(t *testing.T) {
	t.Parallel()
	n, sent := newRecordingNotifier()

	// Exhaust source A's budget entirely.
	for range livenessBurstThreshold + 8 {
		n.notify("source_a", audiocore.StateAlarmed, "silence detected")
	}
	countAfterA := len(*sent)

	// Source B's first alarm must still come through individually.
	n.notify("source_b", audiocore.StateAlarmed, "silence detected")

	require.Len(t, *sent, countAfterA+1)
	last := (*sent)[len(*sent)-1]
	assert.Equal(t, notification.PriorityHigh, last.priority)
	assert.Contains(t, last.body, "source_b")
	assert.Equal(t, "Audio source silence detected", last.title)
}

// TestLivenessNotifier_EscalationResetsCoalescing verifies that after a source
// escalates to a critical outage, the following "recovered" all-clear is
// delivered rather than being suppressed by the flapping burst that preceded the
// escalation. Regression test for the swallowed post-critical recovery.
func TestLivenessNotifier_EscalationResetsCoalescing(t *testing.T) {
	t.Parallel()
	n, sent := newRecordingNotifier()

	// Flap enough to exhaust the silence budget so transient events are suppressed.
	for range livenessBurstThreshold + 5 {
		n.notify("rtsp_camera", audiocore.StateAlarmed, "silence detected")
	}
	// A recovery now would be swallowed: the burst window is still open.
	n.notify("rtsp_camera", audiocore.StateHealthy, "recovered")
	require.Zero(t, countTitle(*sent, "Audio source recovered"),
		"precondition: recovery is suppressed while the burst window is open")

	// The source escalates to a critical outage, then recovers.
	n.notify("rtsp_camera", audiocore.StateFailed, "escalation timeout elapsed")
	n.notify("rtsp_camera", audiocore.StateHealthy, "recovered")

	// The post-critical all-clear must be delivered (Reset cleared the window).
	assert.Equal(t, 1, countTitle(*sent, "Audio source recovered"),
		"recovery after a critical escalation must notify the all-clear")
	assert.GreaterOrEqual(t, countPriority(*sent, notification.PriorityCritical), 1,
		"the critical escalation itself must be delivered")
}
