package classifier

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// Regression guard for issue #3336: potential CGO segfault on model reload.
//
// The TFLite/ONNX classifier and range-filter interpreters are not
// goroutine-safe and free their native resources in Close(). BirdNET protects
// them with bn.mu: every native inference call (Predict / PredictSpeciesScores)
// and every backend Close() must hold bn.mu for its full duration. If a path
// dropped the lock before calling into the backend, a concurrent reload/Delete
// could Close() the interpreter mid-call, a use-after-free segfault.
//
// The fakes below model that native lifecycle: Predict() reads `live` (the
// interpreter buffer) and Close() frees it. Run under `go test -race`, an
// inference call that no longer holds bn.mu races on `live` against the
// concurrent Close(), so this test fails the moment the invariant is broken.

// lifecycleStats counts inference calls that actually reached a live backend
// (nil backends short-circuit before Predict), shared across every fake instance
// installed during one subtest. The final assertions use it to confirm the test
// did not pass vacuously by only ever hitting nil-backend fast paths.
type lifecycleStats struct {
	classifierCalls  atomic.Int64
	rangeFilterCalls atomic.Int64
}

// lifecycleClassifier implements inference.Classifier and models a native
// classifier whose Close() frees a buffer that Predict() reads.
type lifecycleClassifier struct {
	numSpecies int
	stats      *lifecycleStats
	live       []float32 // native interpreter buffer; freed by Close, read by Predict
}

func newLifecycleClassifier(numSpecies int, stats *lifecycleStats) *lifecycleClassifier {
	return &lifecycleClassifier{numSpecies: numSpecies, stats: stats, live: make([]float32, numSpecies)}
}

func (c *lifecycleClassifier) Predict(_ []float32) ([]float32, error) {
	c.stats.classifierCalls.Add(1)
	out := make([]float32, c.numSpecies)
	copy(out, c.live) // touch the freed-on-Close resource
	return out, nil
}
func (c *lifecycleClassifier) NumSpecies() int { return c.numSpecies }
func (c *lifecycleClassifier) Close()          { c.live = nil }

// lifecycleRangeFilter implements inference.RangeFilter (legacy, non-universal
// path: GetProbableSpecies -> predictFilter -> rangeFilter.Predict).
type lifecycleRangeFilter struct {
	numSpecies int
	stats      *lifecycleStats
	live       []float32
}

func newLifecycleRangeFilter(numSpecies int, stats *lifecycleStats) *lifecycleRangeFilter {
	return &lifecycleRangeFilter{numSpecies: numSpecies, stats: stats, live: make([]float32, numSpecies)}
}

func (r *lifecycleRangeFilter) Predict(_, _, _ float32) ([]float32, error) {
	r.stats.rangeFilterCalls.Add(1)
	out := make([]float32, r.numSpecies)
	copy(out, r.live) // touch the freed-on-Close resource
	return out, nil
}
func (r *lifecycleRangeFilter) NumSpecies() int { return r.numSpecies }
func (r *lifecycleRangeFilter) Close()          { r.live = nil }

// lifecycleUniversalRangeFilter additionally implements UniversalSpeciesPredictor
// so GetProbableSpecies takes the universal (PredictSpeciesScores) path.
type lifecycleUniversalRangeFilter struct {
	labels []string
	stats  *lifecycleStats
	live   []float32
}

func newLifecycleUniversalRangeFilter(labels []string, stats *lifecycleStats) *lifecycleUniversalRangeFilter {
	return &lifecycleUniversalRangeFilter{labels: labels, stats: stats, live: make([]float32, len(labels))}
}

func (r *lifecycleUniversalRangeFilter) Predict(_, _, _ float32) ([]float32, error) {
	r.stats.rangeFilterCalls.Add(1)
	out := make([]float32, len(r.labels))
	copy(out, r.live)
	return out, nil
}
func (r *lifecycleUniversalRangeFilter) NumSpecies() int          { return len(r.labels) }
func (r *lifecycleUniversalRangeFilter) Close()                   { r.live = nil }
func (r *lifecycleUniversalRangeFilter) GeomodelLabels() []string { return r.labels }

func (r *lifecycleUniversalRangeFilter) PredictSpeciesScores(_, _, _, _ float32) ([]SpeciesScore, error) {
	r.stats.rangeFilterCalls.Add(1)
	probe := make([]float32, len(r.labels))
	copy(probe, r.live) // touch the freed-on-Close resource
	scores := make([]SpeciesScore, len(r.labels))
	for i, label := range r.labels {
		scores[i] = SpeciesScore{Score: 0, Label: label}
	}
	return scores, nil
}

// TestBirdNET_ConcurrentInferenceAndBackendReload_NoRace exercises classifier and
// range-filter inference concurrently with reload-style close+swap and Delete()
// teardown, verifying (under -race) that bn.mu serializes inference against
// backend destruction so the issue #3336 use-after-free cannot occur.
func TestBirdNET_ConcurrentInferenceAndBackendReload_NoRace(t *testing.T) {
	labels := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Corvus corax_Northern Raven",
	}

	tests := []struct {
		name      string
		newFilter func(stats *lifecycleStats) inference.RangeFilter
	}{
		{
			name: "legacy range filter Predict path",
			newFilter: func(stats *lifecycleStats) inference.RangeFilter {
				return newLifecycleRangeFilter(len(labels), stats)
			},
		},
		{
			name: "universal range filter PredictSpeciesScores path",
			newFilter: func(stats *lifecycleStats) inference.RangeFilter {
				return newLifecycleUniversalRangeFilter(labels, stats)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := &lifecycleStats{}

			// Build via struct literal (not NewBirdNET) so the test runs without an
			// embedded model. The explicit-settings range-filter call below uses this
			// snapshot directly, so location/labels take effect regardless of any
			// global test settings left by other tests.
			settings := conftest.GetTestSettings()
			settings.BirdNET.Labels = labels
			settings.BirdNET.LocationConfigured = true
			settings.BirdNET.Latitude = 60.17
			settings.BirdNET.Longitude = 24.94
			settings.BirdNET.RangeFilter.Threshold = 0.0

			bn := &BirdNET{
				Settings:     settings,
				speciesCache: make(map[string]*speciesCacheEntry),
				ModelInfo:    ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4"},
				classifier:   newLifecycleClassifier(len(labels), stats),
				rangeFilter:  tt.newFilter(stats),
			}
			bn.settingsAtomic.Store(settings)

			const iterations = 250
			ctx := t.Context()
			sample := [][]float32{{0.1}}
			now := time.Now()

			// Warm up both inference paths on the initial live backends before the
			// concurrent storm. The fakes count the call as they enter the native
			// step, so this guarantees each path is exercised at least once even if
			// the Delete writer below keeps the backends nil for much of the run.
			// It makes the call-count assertions deterministic rather than relying
			// on the scheduler to land a read on a live backend.
			_, _ = bn.Predict(ctx, sample)
			_, _ = bn.GetProbableSpeciesWithSettings(now, 0, settings)

			var wg sync.WaitGroup
			start := make(chan struct{})

			// Reader: classifier inference (analyze.go Predict path).
			wg.Go(func() {
				<-start
				for range iterations {
					_, _ = bn.Predict(ctx, sample)
				}
			})

			// Reader: range-filter inference. The explicit-settings variant uses the
			// location-configured snapshot deterministically so the native Predict /
			// PredictSpeciesScores call is actually reached every iteration.
			wg.Go(func() {
				<-start
				for range iterations {
					_, _ = bn.GetProbableSpeciesWithSettings(now, 0, settings)
				}
			})

			// Writer: simulate ReloadModel / ReloadRangeFilter by closing the old
			// backends and installing fresh ones under bn.mu, exactly as the
			// production reload paths do (the Close happens while the lock is held).
			wg.Go(func() {
				<-start
				for range iterations {
					bn.mu.Lock()
					if bn.classifier != nil {
						bn.classifier.Close()
					}
					bn.classifier = newLifecycleClassifier(len(labels), stats)
					if bn.rangeFilter != nil {
						bn.rangeFilter.Close()
					}
					bn.rangeFilter = tt.newFilter(stats)
					bn.mu.Unlock()
				}
			})

			// Writer: exercise the Delete() teardown path. Delete closes and nils both
			// backends under bn.mu; the reload writer reinstalls them and the readers
			// tolerate a nil backend.
			wg.Go(func() {
				<-start
				for range iterations {
					bn.Delete()
				}
			})

			close(start)
			wg.Wait()

			// Guard against a vacuous pass: confirm both inference paths actually ran
			// on a live backend (nil backends short-circuit before the native call),
			// so the race window above was genuinely exercised.
			require.Positive(t, stats.classifierCalls.Load(),
				"classifier Predict path never ran on a live backend")
			require.Positive(t, stats.rangeFilterCalls.Load(),
				"range-filter inference path never ran on a live backend")
		})
	}
}
