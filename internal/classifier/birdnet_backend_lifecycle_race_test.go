package classifier

import (
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
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

// lifecycleClassifier implements inference.Classifier and models a native
// classifier whose Close() frees a buffer that Predict() reads.
type lifecycleClassifier struct {
	numSpecies int
	live       []float32 // native interpreter buffer; freed by Close, read by Predict
}

func newLifecycleClassifier(numSpecies int) *lifecycleClassifier {
	return &lifecycleClassifier{numSpecies: numSpecies, live: make([]float32, numSpecies)}
}

func (c *lifecycleClassifier) Predict(_ []float32) ([]float32, error) {
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
	live       []float32
}

func newLifecycleRangeFilter(numSpecies int) *lifecycleRangeFilter {
	return &lifecycleRangeFilter{numSpecies: numSpecies, live: make([]float32, numSpecies)}
}

func (r *lifecycleRangeFilter) Predict(_, _, _ float32) ([]float32, error) {
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
	live   []float32
}

func newLifecycleUniversalRangeFilter(labels []string) *lifecycleUniversalRangeFilter {
	return &lifecycleUniversalRangeFilter{labels: labels, live: make([]float32, len(labels))}
}

func (r *lifecycleUniversalRangeFilter) Predict(_, _, _ float32) ([]float32, error) {
	out := make([]float32, len(r.labels))
	copy(out, r.live)
	return out, nil
}
func (r *lifecycleUniversalRangeFilter) NumSpecies() int          { return len(r.labels) }
func (r *lifecycleUniversalRangeFilter) Close()                   { r.live = nil }
func (r *lifecycleUniversalRangeFilter) GeomodelLabels() []string { return r.labels }

func (r *lifecycleUniversalRangeFilter) PredictSpeciesScores(_, _, _, _ float32) ([]SpeciesScore, error) {
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
		newFilter func() inference.RangeFilter
	}{
		{
			name:      "legacy range filter Predict path",
			newFilter: func() inference.RangeFilter { return newLifecycleRangeFilter(len(labels)) },
		},
		{
			name:      "universal range filter PredictSpeciesScores path",
			newFilter: func() inference.RangeFilter { return newLifecycleUniversalRangeFilter(labels) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build via struct literal (not NewBirdNET) so the test runs without an
			// embedded model. The explicit-settings range-filter call below uses this
			// snapshot directly, so location/labels take effect regardless of any
			// global test settings left by other tests.
			settings := conf.GetTestSettings()
			settings.BirdNET.Labels = labels
			settings.BirdNET.LocationConfigured = true
			settings.BirdNET.Latitude = 60.17
			settings.BirdNET.Longitude = 24.94
			settings.BirdNET.RangeFilter.Threshold = 0.0

			bn := &BirdNET{
				Settings:     settings,
				speciesCache: make(map[string]*speciesCacheEntry),
				ModelInfo:    ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4"},
				classifier:   newLifecycleClassifier(len(labels)),
				rangeFilter:  tt.newFilter(),
			}
			bn.settingsAtomic.Store(settings)

			const iterations = 250
			ctx := t.Context()
			sample := [][]float32{{0.1}}
			now := time.Now()

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
					bn.classifier = newLifecycleClassifier(len(labels))
					if bn.rangeFilter != nil {
						bn.rangeFilter.Close()
					}
					bn.rangeFilter = tt.newFilter()
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
		})
	}
}
