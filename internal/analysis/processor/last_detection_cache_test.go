// last_detection_cache_test.go - Unit tests for the per-model last-detection cache.
package processor

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// makeResult is a test helper that builds a detection.Result with the given fields.
func makeResult(commonName, scientificName string, confidence float64, ts time.Time) detection.Result {
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

const ringTestModelID = "birdnet-v2.4"

// seedRing inserts n detections marked "sp-0".."sp-(n-1)" into modelID's ring,
// each one second apart starting at base. The species common name doubles as an
// ordered marker so tests can assert ordering without tracking timestamps.
func seedRing(t *testing.T, p *Processor, modelID string, n int, base time.Time) {
	t.Helper()
	for i := range n {
		marker := "sp-" + strconv.Itoa(i)
		res := makeResult(marker, "sci-"+marker, 0.5, base.Add(time.Duration(i)*time.Second))
		p.updateLastDetection(modelID, &res)
	}
}

// speciesMarkers extracts the ordered species common names from a newest-first
// slice so assertions can compare against an expected []string of markers.
func speciesMarkers(dets []LastDetection) []string {
	out := make([]string, len(dets))
	for i := range dets {
		out[i] = dets[i].Species
	}
	return out
}

// TestRecentDetections_OrderingAndWrap covers insertion ordering, newest-first
// output, and wrap-around past the ring size.
func TestRecentDetections_OrderingAndWrap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		inserts  int
		expected []string // newest-first species markers
	}{
		{
			name:     "single entry",
			inserts:  1,
			expected: []string{"sp-0"},
		},
		{
			name:     "three entries newest first",
			inserts:  3,
			expected: []string{"sp-2", "sp-1", "sp-0"},
		},
		{
			name:     "exactly full ring",
			inserts:  lastDetectionRingSize,
			expected: []string{"sp-9", "sp-8", "sp-7", "sp-6", "sp-5", "sp-4", "sp-3", "sp-2", "sp-1", "sp-0"},
		},
		{
			name:     "wrap-around keeps newest ten",
			inserts:  lastDetectionRingSize + 5,
			expected: []string{"sp-14", "sp-13", "sp-12", "sp-11", "sp-10", "sp-9", "sp-8", "sp-7", "sp-6", "sp-5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := minimalProcessor()
			seedRing(t, p, ringTestModelID, tt.inserts, time.Unix(1000, 0))

			recent := p.GetRecentDetections(ringTestModelID)
			require.Len(t, recent, len(tt.expected))
			assert.Equal(t, tt.expected, speciesMarkers(recent), "recent detections must be newest-first")

			// GetLastDetection returns the single most-recent entry.
			last, ok := p.GetLastDetection(ringTestModelID)
			require.True(t, ok)
			assert.Equal(t, tt.expected[0], last.Species, "GetLastDetection must return the newest entry")
		})
	}
}

// TestRecentDetections_AbsentModel verifies the nil return for an unseen model.
func TestRecentDetections_AbsentModel(t *testing.T) {
	t.Parallel()
	p := minimalProcessor()
	assert.Nil(t, p.GetRecentDetections("never-seen"), "absent model returns nil slice")
	assert.Nil(t, p.GetRecentDetections(""), "empty model returns nil slice")
}

// TestRecentDetections_IndependentCopy proves the returned slice does not alias
// the ring's backing array: mutating it, and continuing to write to the ring,
// must not corrupt a previously returned snapshot.
func TestRecentDetections_IndependentCopy(t *testing.T) {
	t.Parallel()
	p := minimalProcessor()
	seedRing(t, p, ringTestModelID, 3, time.Unix(3000, 0))

	snapshot := p.GetRecentDetections(ringTestModelID)
	require.Len(t, snapshot, 3)
	original := slices.Clone(speciesMarkers(snapshot))

	// Mutating the returned slice must not affect the ring.
	snapshot[0].Species = "MUTATED"
	snapshot[0].Confidence = 999

	// Continuing to write to the ring (enough to overwrite every slot) must not
	// mutate the earlier snapshot beyond the local change above.
	seedRing(t, p, ringTestModelID, lastDetectionRingSize, time.Unix(4000, 0))

	assert.Equal(t, original[1], snapshot[1].Species, "ring writes must not mutate a prior snapshot")
	assert.Equal(t, original[2], snapshot[2].Species, "ring writes must not mutate a prior snapshot")

	// A fresh read never carries the mutation applied to the earlier snapshot.
	fresh := p.GetRecentDetections(ringTestModelID)
	require.Len(t, fresh, lastDetectionRingSize)
	for _, d := range fresh {
		assert.NotEqual(t, "MUTATED", d.Species, "mutating a snapshot must not leak into the ring")
	}
}

// TestRecentDetections_PerModelIsolation verifies each model keeps its own ring.
func TestRecentDetections_PerModelIsolation(t *testing.T) {
	t.Parallel()
	p := minimalProcessor()
	base := time.Unix(5000, 0)

	a0 := makeResult("a0", "sci-a0", 0.5, base)
	b0 := makeResult("b0", "sci-b0", 0.5, base.Add(time.Second))
	a1 := makeResult("a1", "sci-a1", 0.5, base.Add(2*time.Second))
	p.updateLastDetection("model-a", &a0)
	p.updateLastDetection("model-b", &b0)
	p.updateLastDetection("model-a", &a1)

	assert.Equal(t, []string{"a1", "a0"}, speciesMarkers(p.GetRecentDetections("model-a")), "model-a ring is isolated")
	assert.Equal(t, []string{"b0"}, speciesMarkers(p.GetRecentDetections("model-b")), "model-b ring is isolated")
}
