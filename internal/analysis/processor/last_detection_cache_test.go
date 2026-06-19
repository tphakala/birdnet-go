// last_detection_cache_test.go - Unit tests for the per-model last-detection cache.
package processor

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testThrottle is the throttle most feed tests use; distinct species are never
// throttled regardless of its value, so it only matters for repeat-species tests.
const testThrottle = 9 * time.Second

// feed records one in-range detection in modelID's feed using testThrottle.
func feed(p *Processor, modelID, common, scientific string, conf float64, ts time.Time) {
	p.updateLastDetection(modelID, common, scientific, conf, ts, true, testThrottle)
}

// minimalProcessor returns a zero-value Processor suitable for testing the cache.
// The cache fields (lastDetectionCache, lastDetectionMu) are zero-value safe
// and require no constructor initialisation.
func minimalProcessor() *Processor {
	return &Processor{}
}

// TestDetectionThrottle verifies the per-model interval is the model's clip length
// snapped to the whole multiple closest to detectionThrottleTarget (9s), with a
// floor of one segment and a default for an unknown clip length.
func TestDetectionThrottle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		clip time.Duration
		want time.Duration
	}{
		{"birdnet 3s segments -> 9s", 3 * time.Second, 9 * time.Second},
		{"perch 5s segments -> 10s", 5 * time.Second, 10 * time.Second},
		{"1s segments -> 9s", 1 * time.Second, 9 * time.Second},
		{"9s segments -> 9s", 9 * time.Second, 9 * time.Second},
		{"20s segments floor at one segment", 20 * time.Second, 20 * time.Second},
		{"unknown clip length falls back to target", 0, detectionThrottleTarget},
		{"negative clip length falls back to target", -1, detectionThrottleTarget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, detectionThrottle(tt.clip))
		})
	}
}

// TestLastDetectionCache_UpdateThenGet verifies that a recorded detection is
// retrieved by GetLastDetection with its fields intact.
func TestLastDetectionCache_UpdateThenGet(t *testing.T) {
	t.Parallel()

	p := minimalProcessor()
	ts := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	feed(p, "birdnet-v2.4", "Common Blackbird", "Turdus merula", 0.91, ts)

	got, ok := p.GetLastDetection("birdnet-v2.4")
	require.True(t, ok, "expected cache hit for known modelID")
	assert.Equal(t, "Common Blackbird", got.Species)
	assert.Equal(t, "Turdus merula", got.ScientificName)
	assert.InDelta(t, 0.91, got.Confidence, 1e-9)
	assert.Equal(t, ts.Unix(), got.AtUnix)
	assert.True(t, got.InRange, "feed helper records in-range detections")
}

// TestLastDetectionCache_InRangeRecorded verifies the InRange flag is stored and
// returned as given, so the frontend can mark out-of-range / non-avian predictions.
func TestLastDetectionCache_InRangeRecorded(t *testing.T) {
	t.Parallel()

	p := minimalProcessor()
	base := time.Unix(1000, 0)
	// In-range bird and an out-of-range / non-avian prediction (distinct species).
	p.updateLastDetection("m", "European Robin", "Erithacus rubecula", 0.8, base, true, testThrottle)
	p.updateLastDetection("m", "Engine", "Engine", 0.7, base.Add(time.Second), false, testThrottle)

	recent := p.GetRecentDetections("m")
	require.Len(t, recent, 2)
	// Newest first: Engine (out of range), then Robin (in range).
	assert.Equal(t, "Engine", recent[0].Species)
	assert.False(t, recent[0].InRange, "non-avian prediction is flagged out of range")
	assert.Equal(t, "European Robin", recent[1].Species)
	assert.True(t, recent[1].InRange, "in-range bird keeps InRange true")
}

// TestLastDetectionCache_NewestAtFront verifies that a later detection becomes the
// head returned by GetLastDetection (most-recent first feed).
func TestLastDetectionCache_NewestAtFront(t *testing.T) {
	t.Parallel()

	p := minimalProcessor()
	ts1 := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	feed(p, "birdnet-v2.4", "Common Blackbird", "Turdus merula", 0.95, ts1)
	feed(p, "birdnet-v2.4", "European Robin", "Erithacus rubecula", 0.55, ts1.Add(5*time.Second))

	got, ok := p.GetLastDetection("birdnet-v2.4")
	require.True(t, ok)
	assert.Equal(t, "European Robin", got.Species, "most-recent detection must be the head")

	recent := p.GetRecentDetections("birdnet-v2.4")
	assert.Equal(t, []string{"European Robin", "Common Blackbird"}, speciesMarkers(recent), "feed is newest first")
}

// TestLastDetectionCache_Throttle is the core feed guarantee: the same species is
// recorded at most once per throttle interval, so a continuously singing bird does
// not flood the feed, while detections spaced beyond the interval are all kept.
func TestLastDetectionCache_Throttle(t *testing.T) {
	t.Parallel()

	p := minimalProcessor()
	base := time.Unix(1000, 0)
	robin := func(offset time.Duration) {
		feed(p, "m", "European Robin", "Erithacus rubecula", 0.7, base.Add(offset))
	}

	// Throttle is 9s. Detections at 0, 5, 9, 12, 18 seconds.
	robin(0)
	robin(5 * time.Second)
	robin(9 * time.Second)
	robin(12 * time.Second)
	robin(18 * time.Second)

	recent := p.GetRecentDetections("m")
	atUnix := make([]int64, len(recent))
	for i := range recent {
		atUnix[i] = recent[i].AtUnix
	}
	// Recorded: t=0 (first), t=9 (9-0 >= 9), t=18 (18-9 >= 9). Dropped: t=5, t=12.
	assert.Equal(t, []int64{
		base.Add(18 * time.Second).Unix(),
		base.Add(9 * time.Second).Unix(),
		base.Unix(),
	}, atUnix, "only detections spaced >= throttle apart are kept, newest first")
}

// TestLastDetectionCache_DifferentSpeciesNotThrottled verifies the throttle is
// per species: distinct species detected within the interval are all recorded.
func TestLastDetectionCache_DifferentSpeciesNotThrottled(t *testing.T) {
	t.Parallel()

	p := minimalProcessor()
	base := time.Unix(2000, 0)
	feed(p, "m", "European Robin", "Erithacus rubecula", 0.7, base)
	feed(p, "m", "Common Blackbird", "Turdus merula", 0.7, base.Add(time.Second))
	feed(p, "m", "Great Tit", "Parus major", 0.7, base.Add(2*time.Second))

	recent := p.GetRecentDetections("m")
	assert.Equal(t, []string{"Great Tit", "Common Blackbird", "European Robin"}, speciesMarkers(recent),
		"distinct species within the interval are all recorded, newest first")
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
	feed(p, "", "Common Blackbird", "Turdus merula", 0.80, time.Now())

	_, ok := p.GetLastDetection("")
	assert.False(t, ok, "empty modelID must be skipped; no cache entry must be created")
}

// TestLastDetectionCache_IndependentModels verifies that separate modelIDs maintain
// independent cache entries.
func TestLastDetectionCache_IndependentModels(t *testing.T) {
	t.Parallel()

	p := minimalProcessor()
	ts := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	feed(p, "model-a", "Common Blackbird", "Turdus merula", 0.90, ts)
	feed(p, "model-b", "European Robin", "Erithacus rubecula", 0.80, ts.Add(time.Second))

	gotA, okA := p.GetLastDetection("model-a")
	require.True(t, okA)
	assert.Equal(t, "Common Blackbird", gotA.Species)

	gotB, okB := p.GetLastDetection("model-b")
	require.True(t, okB)
	assert.Equal(t, "European Robin", gotB.Species)
}

const lastDetTestModelID = "birdnet-v2.4"

// seedDistinctSpecies inserts n detections of DISTINCT species marked
// "sp-0".."sp-(n-1)" into modelID's feed, each one second apart starting at base.
// Distinct species are never throttled, so this exercises pure feed ordering and
// capping. The species common name doubles as an ordered marker.
func seedDistinctSpecies(t *testing.T, p *Processor, modelID string, n int, base time.Time) {
	t.Helper()
	for i := range n {
		marker := "sp-" + strconv.Itoa(i)
		feed(p, modelID, marker, "sci-"+marker, 0.5, base.Add(time.Duration(i)*time.Second))
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

// TestRecentDetections_FeedOrderingAndCap covers insertion ordering, newest-first
// output, and capping past lastDetectionCap.
func TestRecentDetections_FeedOrderingAndCap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		inserts int
	}{
		{"single entry", 1},
		{"three entries", 3},
		{"exactly full feed", lastDetectionCap},
		{"over cap keeps newest", lastDetectionCap + 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := minimalProcessor()
			seedDistinctSpecies(t, p, lastDetTestModelID, tt.inserts, time.Unix(1000, 0))

			// Distinct species are never throttled, so the feed keeps the newest
			// min(inserts, cap) markers, newest first.
			kept := min(tt.inserts, lastDetectionCap)
			expected := make([]string, kept)
			for i := range kept {
				expected[i] = "sp-" + strconv.Itoa(tt.inserts-1-i)
			}

			recent := p.GetRecentDetections(lastDetTestModelID)
			require.Len(t, recent, kept)
			assert.Equal(t, expected, speciesMarkers(recent), "recent detections must be newest-first, capped")

			last, ok := p.GetLastDetection(lastDetTestModelID)
			require.True(t, ok)
			assert.Equal(t, expected[0], last.Species, "GetLastDetection must return the newest entry")
		})
	}
}

// TestRecentDetections_SameSpeciesRepeatsWhenSpaced verifies the feed shows the
// same species more than once when detections are spaced beyond the throttle, so
// the detection cadence over time is visible (not collapsed into one entry).
func TestRecentDetections_SameSpeciesRepeatsWhenSpaced(t *testing.T) {
	t.Parallel()

	p := minimalProcessor()
	base := time.Unix(3000, 0)
	for i := range 3 {
		// 10s apart, beyond the 9s throttle, so each is recorded.
		feed(p, "m", "European Robin", "Erithacus rubecula", 0.7, base.Add(time.Duration(i)*10*time.Second))
	}
	recent := p.GetRecentDetections("m")
	require.Len(t, recent, 3, "spaced repeats of one species are kept as separate feed entries")
	for _, d := range recent {
		assert.Equal(t, "European Robin", d.Species)
	}
}

// TestRecentDetections_AbsentModel verifies the nil return for an unseen model.
func TestRecentDetections_AbsentModel(t *testing.T) {
	t.Parallel()
	p := minimalProcessor()
	assert.Nil(t, p.GetRecentDetections("never-seen"), "absent model returns nil slice")
	assert.Nil(t, p.GetRecentDetections(""), "empty model returns nil slice")
}

// TestRecentDetections_SpeciesKeyThrottleFallback verifies the throttle identity
// falls back to the common name when the scientific name is empty, and unidentified
// detections (both names empty) throttle as a single bucket.
func TestRecentDetections_SpeciesKeyThrottleFallback(t *testing.T) {
	t.Parallel()

	t.Run("empty scientific throttles by common name", func(t *testing.T) {
		t.Parallel()
		p := minimalProcessor()
		base := time.Unix(9000, 0)
		feed(p, "m", "Mystery Bird", "", 0.6, base)
		feed(p, "m", "Mystery Bird", "", 0.7, base.Add(time.Second)) // within throttle
		assert.Len(t, p.GetRecentDetections("m"), 1, "same common name with empty scientific is throttled")
	})

	t.Run("both names empty throttle as one bucket", func(t *testing.T) {
		t.Parallel()
		p := minimalProcessor()
		base := time.Unix(9100, 0)
		for i := range 3 {
			feed(p, "m", "", "", 0.5, base.Add(time.Duration(i)*time.Second))
		}
		assert.Len(t, p.GetRecentDetections("m"), 1, "unidentified detections throttle as a single bucket")
	})
}

// TestRecentDetections_IndependentCopy proves the returned slice does not alias
// the feed's backing array: mutating it, and continuing to write to the feed,
// must not corrupt a previously returned snapshot.
func TestRecentDetections_IndependentCopy(t *testing.T) {
	t.Parallel()
	p := minimalProcessor()
	seedDistinctSpecies(t, p, lastDetTestModelID, 3, time.Unix(3000, 0))

	snapshot := p.GetRecentDetections(lastDetTestModelID)
	require.Len(t, snapshot, 3)
	original := slices.Clone(speciesMarkers(snapshot))

	// Mutating the returned slice must not affect the feed.
	snapshot[0].Species = "MUTATED"

	// Continuing to write to the feed (enough distinct species to fill it) must not
	// mutate the earlier snapshot beyond the local change above.
	seedDistinctSpecies(t, p, lastDetTestModelID, lastDetectionCap, time.Unix(4000, 0))

	assert.Equal(t, original[1], snapshot[1].Species, "feed writes must not mutate a prior snapshot")
	assert.Equal(t, original[2], snapshot[2].Species, "feed writes must not mutate a prior snapshot")

	fresh := p.GetRecentDetections(lastDetTestModelID)
	require.Len(t, fresh, lastDetectionCap)
	for _, d := range fresh {
		assert.NotEqual(t, "MUTATED", d.Species, "mutating a snapshot must not leak into the feed")
	}
}

// TestRecentDetections_PerModelIsolation verifies each model keeps its own feed.
func TestRecentDetections_PerModelIsolation(t *testing.T) {
	t.Parallel()
	p := minimalProcessor()
	base := time.Unix(5000, 0)

	feed(p, "model-a", "a0", "sci-a0", 0.5, base)
	feed(p, "model-b", "b0", "sci-b0", 0.5, base.Add(time.Second))
	feed(p, "model-a", "a1", "sci-a1", 0.5, base.Add(2*time.Second))

	assert.Equal(t, []string{"a1", "a0"}, speciesMarkers(p.GetRecentDetections("model-a")), "model-a feed is isolated")
	assert.Equal(t, []string{"b0"}, speciesMarkers(p.GetRecentDetections("model-b")), "model-b feed is isolated")
}
