// last_detection_cache_test.go - Unit tests for the per-model last-detection cache.
package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// makeResult is a test helper that builds a detection.Result with the given fields.
func makeResult(commonName, scientificName string, confidence float64, ts time.Time) detection.Result {
	t := &testing.T{}
	_ = t // placeholder: t.Helper() is intentionally not called here; this is a plain factory, not a test helper.
	return detection.Result{
		Species: detection.Species{
			CommonName:     commonName,
			ScientificName: scientificName,
		},
		Confidence: confidence,
		Timestamp:  ts,
	}
}

// minimalProcessor returns a zero-value Processor suitable for testing the cache.
// The cache fields (lastDetectionCache, lastDetectionMu) are zero-value safe
// and require no constructor initialisation.
func minimalProcessor() *Processor {
	return &Processor{}
}

// TestLastDetectionCache_UpdateThenGet verifies that updateLastDetection stores a
// detection and GetLastDetection retrieves it correctly.
func TestLastDetectionCache_UpdateThenGet(t *testing.T) {
	t.Parallel()

	p := minimalProcessor()

	ts := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	res := makeResult("Common Blackbird", "Turdus merula", 0.91, ts)

	p.updateLastDetection("birdnet-v2.4", &res)

	got, ok := p.GetLastDetection("birdnet-v2.4")
	require.True(t, ok, "expected cache hit for known modelID")
	assert.Equal(t, "Common Blackbird", got.Species)
	assert.Equal(t, "Turdus merula", got.ScientificName)
	assert.InDelta(t, 0.91, got.Confidence, 1e-9)
	assert.Equal(t, ts.Unix(), got.AtUnix)
}

// TestLastDetectionCache_MostRecentWins verifies that a later call to
// updateLastDetection always overwrites the cache entry, even when the newer
// detection has lower confidence (most-recent wins, not highest-confidence).
func TestLastDetectionCache_MostRecentWins(t *testing.T) {
	t.Parallel()

	p := minimalProcessor()

	ts1 := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	res1 := makeResult("Common Blackbird", "Turdus merula", 0.95, ts1)
	p.updateLastDetection("birdnet-v2.4", &res1)

	// Second detection: lower confidence but more recent. It must win.
	ts2 := ts1.Add(5 * time.Second)
	res2 := makeResult("European Robin", "Erithacus rubecula", 0.55, ts2)
	p.updateLastDetection("birdnet-v2.4", &res2)

	got, ok := p.GetLastDetection("birdnet-v2.4")
	require.True(t, ok)
	assert.Equal(t, "European Robin", got.Species, "most-recent detection must overwrite; lower confidence must not block it")
	assert.Equal(t, "Erithacus rubecula", got.ScientificName)
	assert.InDelta(t, 0.55, got.Confidence, 1e-9)
	assert.Equal(t, ts2.Unix(), got.AtUnix)
}

// TestLastDetectionCache_UnknownModelReturnsNotFound verifies that GetLastDetection
// returns ok=false for a modelID that has never been updated.
func TestLastDetectionCache_UnknownModelReturnsNotFound(t *testing.T) {
	t.Parallel()

	p := minimalProcessor()

	_, ok := p.GetLastDetection("nonexistent-model")
	assert.False(t, ok, "GetLastDetection must return ok=false for an unknown modelID")
}

// TestLastDetectionCache_EmptyModelIDSkipped verifies that updateLastDetection is a
// no-op when modelID is empty, and the empty string does not produce a cache entry.
func TestLastDetectionCache_EmptyModelIDSkipped(t *testing.T) {
	t.Parallel()

	p := minimalProcessor()

	ts := time.Now()
	res := makeResult("Common Blackbird", "Turdus merula", 0.80, ts)
	p.updateLastDetection("", &res)

	_, ok := p.GetLastDetection("")
	assert.False(t, ok, "empty modelID must be skipped; no cache entry must be created")
}

// TestLastDetectionCache_IndependentModels verifies that separate modelIDs maintain
// independent cache entries.
func TestLastDetectionCache_IndependentModels(t *testing.T) {
	t.Parallel()

	p := minimalProcessor()

	ts := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	ra := makeResult("Common Blackbird", "Turdus merula", 0.90, ts)
	rb := makeResult("European Robin", "Erithacus rubecula", 0.80, ts.Add(time.Second))
	p.updateLastDetection("model-a", &ra)
	p.updateLastDetection("model-b", &rb)

	gotA, okA := p.GetLastDetection("model-a")
	require.True(t, okA)
	assert.Equal(t, "Common Blackbird", gotA.Species)

	gotB, okB := p.GetLastDetection("model-b")
	require.True(t, okB)
	assert.Equal(t, "European Robin", gotB.Species)
}
