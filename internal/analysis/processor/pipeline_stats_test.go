package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

func TestPipelineStats_RecordAndReset(t *testing.T) {
	t.Parallel()

	ps := NewPipelineStats(func(id string) string {
		if id == "src-1" {
			return "Backyard Mic"
		}
		return id
	})

	ps.RecordInference("src-1", "birdnet-v2.4", 10, 2, 0.85, 0.70)
	ps.RecordInference("src-1", "birdnet-v2.4", 8, 0, 0.55, 0.70)
	ps.RecordInference("src-1", "perch-v2", 5, 1, 0.42, 0.70)

	ps.mu.Lock()
	defer ps.mu.Unlock()

	birdnetKey := sourceModelKey{sourceID: "src-1", modelID: "birdnet-v2.4"}
	perchKey := sourceModelKey{sourceID: "src-1", modelID: "perch-v2"}

	require.Contains(t, ps.stats, birdnetKey)
	require.Contains(t, ps.stats, perchKey)

	s := ps.stats[birdnetKey]
	assert.Equal(t, 2, s.inferences)
	assert.Equal(t, 18, s.rawResults)
	assert.Equal(t, 2, s.passedFilter)
	assert.InDelta(t, 0.85, float64(s.maxConfidence), 0.001)
	assert.InDelta(t, 0.70, float64(s.threshold), 0.001)

	sp := ps.stats[perchKey]
	assert.Equal(t, 1, sp.inferences)
	assert.Equal(t, 5, sp.rawResults)
	assert.Equal(t, 1, sp.passedFilter)
	assert.InDelta(t, 0.42, float64(sp.maxConfidence), 0.001)
}

func TestPipelineStats_MaxConfidenceTracksHighest(t *testing.T) {
	t.Parallel()

	ps := NewPipelineStats(nil)

	ps.RecordInference("src-1", "model-a", 3, 0, 0.30, 0.50)
	ps.RecordInference("src-1", "model-a", 3, 0, 0.90, 0.50)
	ps.RecordInference("src-1", "model-a", 3, 0, 0.60, 0.50)

	ps.mu.Lock()
	defer ps.mu.Unlock()

	key := sourceModelKey{sourceID: "src-1", modelID: "model-a"}
	assert.InDelta(t, 0.90, float64(ps.stats[key].maxConfidence), 0.001)
}

func TestPipelineStats_ZeroActivitySuppressed(t *testing.T) {
	t.Parallel()

	ps := NewPipelineStats(nil)

	// Record nothing, then call logAndReset manually
	log := GetLogger()
	ps.logAndReset(log)

	ps.mu.Lock()
	defer ps.mu.Unlock()
	assert.Empty(t, ps.stats)
}

// TestPipelineStats_RecordDaylightDiscard verifies that daylight-filter discards
// are aggregated into the periodic pipeline-stats summary, and in particular that
// a window whose every detection was filtered (zero inferences, non-zero
// discards) is still reported. That is exactly the case a user who sees zero
// saved detections needs surfaced, so it must not be suppressed. Not parallel:
// logtest swaps the process-global logger.
func TestPipelineStats_RecordDaylightDiscard(t *testing.T) {
	// Each subtest builds isolated logger capture and PipelineStats state. Not
	// parallel: logtest swaps the process-global logger.
	t.Run("discard-only window is reported", func(t *testing.T) {
		buf := logtest.CaptureBuffer(t)
		ps := NewPipelineStats(nil)

		ps.RecordDaylightDiscard("src-1", "BirdNET_v2.4")
		ps.RecordDaylightDiscard("src-1", "BirdNET_v2.4")
		ps.logAndReset(GetLogger())

		out := buf.String()
		require.Contains(t, out, "pipeline stats", "a discard-only window must still emit a summary")
		assert.Contains(t, out, "daylight_discards=2")
		assert.Contains(t, out, "inferences=0")
	})

	t.Run("window is suppressed after its discards were reported", func(t *testing.T) {
		buf := logtest.CaptureBuffer(t)
		ps := NewPipelineStats(nil)

		ps.RecordDaylightDiscard("src-1", "BirdNET_v2.4")
		ps.logAndReset(GetLogger()) // consumes and resets the window
		buf.Reset()
		ps.logAndReset(GetLogger()) // now idle
		assert.NotContains(t, buf.String(), "pipeline stats", "an idle window after reset must be suppressed")
	})
}

// TestPipelineStats_InferencesAndDiscards verifies a window carrying both
// inferences and daylight discards reports both counts on one line.
func TestPipelineStats_InferencesAndDiscards(t *testing.T) {
	buf := logtest.CaptureBuffer(t)
	ps := NewPipelineStats(nil)

	ps.RecordInference("src-2", "perch_v2", 5, 1, 0.8, 0.5)
	ps.RecordDaylightDiscard("src-2", "perch_v2")
	ps.logAndReset(GetLogger())

	out := buf.String()
	require.Contains(t, out, "pipeline stats")
	assert.Contains(t, out, "inferences=1")
	assert.Contains(t, out, "daylight_discards=1")
}
