package processor

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// With deduplication disabled every analysis window writes a line, so those
// lines are Trace and absent at Debug. With deduplication enabled the lines mark
// a change or heartbeat and stay at Debug.
func TestLogDetectionResults_DedupDisabledIsTrace(t *testing.T) {
	// Not parallel: logtest.CaptureBufferAt swaps the process-global logger.
	const message = "Detection processing results"

	tests := []struct {
		name         string
		dedupEnabled bool
		wantLogged   bool
	}{
		{name: "dedup disabled is not logged at debug", dedupEnabled: false, wantLogged: false},
		{name: "dedup enabled first log is logged at debug", dedupEnabled: true, wantLogged: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := logtest.CaptureBufferAt(t, slog.LevelDebug)
			p := &Processor{
				logDedup: NewLogDeduplicator(DeduplicationConfig{Enabled: tt.dedupEnabled}),
			}

			p.logDetectionResults("source-a", 3, 1)

			if tt.wantLogged {
				assert.Contains(t, buf.String(), message)
			} else {
				assert.NotContains(t, buf.String(), message)
			}
		})
	}
}
