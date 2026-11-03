package processor

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// With deduplication disabled every analysis window writes a line, so those
// lines are Trace and absent at Debug. With deduplication enabled the lines mark
// a first sighting, a change or a heartbeat and stay at Debug.
func TestLogDetectionResults_DedupDisabledIsTrace(t *testing.T) {
	// Not parallel: logtest.CaptureBufferAt swaps the process-global logger.
	const message = "Detection processing results"

	tests := []struct {
		name         string
		dedupEnabled bool
		heartbeatDue bool   // age the first window past the default health check interval before the second call
		secondCall   [2]int // raw and filtered counts of the second call, if any
		wantReason   string // log_reason expected in the output, empty when nothing is logged
	}{
		{name: "dedup disabled is not logged at debug", dedupEnabled: false},
		{name: "first log stays at debug", dedupEnabled: true, wantReason: logReasonFirstLog},
		{name: "changed values stay at debug", dedupEnabled: true, secondCall: [2]int{4, 2}, wantReason: logReasonValuesChanged},
		{name: "health check stays at debug", dedupEnabled: true, heartbeatDue: true, secondCall: [2]int{3, 1}, wantReason: logReasonHealthCheck},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := logtest.CaptureBufferAt(t, slog.LevelDebug)
			p := &Processor{
				logDedup: NewLogDeduplicator(DeduplicationConfig{Enabled: tt.dedupEnabled}),
			}

			p.logDetectionResults("source-a", 3, 1)
			if tt.secondCall != [2]int{} {
				// Discard the first line so only the second call is judged.
				buf.Reset()
				if tt.heartbeatDue {
					p.logDedup.states["source-a"].LastLogTime = time.Now().Add(-time.Hour)
				}
				p.logDetectionResults("source-a", tt.secondCall[0], tt.secondCall[1])
			}

			if tt.wantReason == "" {
				assert.NotContains(t, buf.String(), message)
				return
			}
			assert.Contains(t, buf.String(), "level=DEBUG msg=\""+message+"\"")
			assert.Contains(t, buf.String(), "log_reason="+tt.wantReason)
		})
	}
}
