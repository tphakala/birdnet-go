package processor

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// With deduplication disabled every analysis window writes a line, so those
// lines are Trace and absent at Debug. With deduplication enabled a line marks a
// first sighting, a change or a heartbeat and stays at Debug.
func TestLogDetectionResults_DedupDisabledIsTrace(t *testing.T) {
	// Not parallel: logtest.CaptureBufferAt swaps the process-global logger.
	const message = "Detection processing results"

	tests := []struct {
		name         string
		dedupEnabled bool
		wantDebug    bool
	}{
		{name: "dedup disabled is not logged at debug", dedupEnabled: false, wantDebug: false},
		{name: "dedup enabled stays at debug", dedupEnabled: true, wantDebug: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := logtest.CaptureBufferAt(t, slog.LevelDebug)
			p := &Processor{
				logDedup: NewLogDeduplicator(DeduplicationConfig{Enabled: tt.dedupEnabled}),
			}

			p.logDetectionResults("source-a", 3, 1)

			if !tt.wantDebug {
				assert.NotContains(t, buf.String(), message)
				return
			}
			assert.Contains(t, buf.String(), "level=DEBUG msg=\""+message+"\"")
			assert.Contains(t, buf.String(), "log_reason="+logReasonFirstLog)
		})
	}
}
