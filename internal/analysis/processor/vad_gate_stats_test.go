package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/inference/vad"
)

func TestVADSourceLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cacheKey string
		want     string
	}{
		{"embedded", "embedded", "embedded"},
		{"absolute path", "path:/models/silero_vad.onnx", "path"},
		{"relative path", "path:relative.onnx", "path"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, vadSourceLabel(tt.cacheKey))
		})
	}
}

func TestVADGate_StatusZeroValue(t *testing.T) {
	t.Parallel()
	g := &vadGate{}
	st := g.status()
	assert.False(t, st.Loaded, "a fresh gate is not loaded")
	assert.Zero(t, st.Invocations)
	assert.Zero(t, st.AvgMs, "avg must be 0 with no invocations, not NaN")
	assert.Zero(t, st.MaxMs)
	assert.Zero(t, st.SpeechHits)
	assert.Empty(t, st.Strategy)
	assert.Empty(t, st.Source)
}

func TestVADGate_RecordInferenceAggregates(t *testing.T) {
	t.Parallel()
	g := &vadGate{}
	g.recordInference(2 * time.Millisecond)
	g.recordInference(4 * time.Millisecond)
	g.recordInference(3 * time.Millisecond)

	st := g.status()
	assert.Equal(t, int64(3), st.Invocations)
	assert.InDelta(t, 3.0, st.AvgMs, 0.001, "avg of 2,4,3 ms")
	assert.InDelta(t, 4.0, st.MaxMs, 0.001, "max is the lifetime peak")
	// recordInference does not touch the probability; it is hit-only.
	assert.Zero(t, st.LastSpeechProbability, "probability is recorded on speech hits, not per inference")
}

func TestVADGate_RecordSpeechHit(t *testing.T) {
	t.Parallel()
	g := &vadGate{}
	at := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	g.recordSpeechHit(at, 0.6, "Front Yard")
	g.recordSpeechHit(at.Add(time.Minute), 0.85, "Back Yard")

	st := g.status()
	assert.Equal(t, int64(2), st.SpeechHits)
	assert.Equal(t, at.Add(time.Minute).Unix(), st.LastSpeechUnix, "newest speech time wins")
	assert.InDelta(t, 0.85, st.LastSpeechProbability, 0.0001, "probability of the most recent hit")

	// History is newest-first with source + probability.
	require.Len(t, st.RecentHits, 2)
	assert.Equal(t, at.Add(time.Minute).Unix(), st.RecentHits[0].AtUnix, "newest hit first")
	assert.Equal(t, "Back Yard", st.RecentHits[0].Source)
	assert.InDelta(t, 0.85, st.RecentHits[0].Probability, 0.0001)
	assert.Equal(t, "Front Yard", st.RecentHits[1].Source)
}

func TestVADGate_RecentHitsCappedNewestFirst(t *testing.T) {
	t.Parallel()
	g := &vadGate{}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	// Push more than the cap; the oldest must be evicted, newest kept first.
	for i := range vadHitCap + 5 {
		g.recordSpeechHit(base.Add(time.Duration(i)*time.Second), 0.5, "s1")
	}
	st := g.status()
	require.Len(t, st.RecentHits, vadHitCap, "history is capped at vadHitCap")
	// Newest push (i = vadHitCap+4) is first.
	assert.Equal(t, base.Add(time.Duration(vadHitCap+4)*time.Second).Unix(), st.RecentHits[0].AtUnix)
	// Oldest retained is the (vadHitCap)th from the newest.
	assert.Equal(t, base.Add(time.Duration(5)*time.Second).Unix(), st.RecentHits[vadHitCap-1].AtUnix)
}

func TestVADGate_StatusReflectsSnapshot(t *testing.T) {
	t.Parallel()
	g := &vadGate{}
	g.snapshot.Store(&vadConfigSnapshot{strategy: "sequence", source: "embedded", sampleRate: vad.SampleRate})

	st := g.status()
	assert.True(t, st.Loaded)
	assert.Equal(t, "sequence", st.Strategy)
	assert.Equal(t, "embedded", st.Source)
	assert.Equal(t, 16000, st.SampleRate)

	// Unload clears the snapshot but lifetime counters survive.
	g.recordInference(time.Millisecond)
	g.snapshot.Store(nil)
	st = g.status()
	assert.False(t, st.Loaded)
	assert.Equal(t, int64(1), st.Invocations, "counters are lifetime totals, not reset on unload")
}
