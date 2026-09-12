package classifier

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// newTestRangeFilterService returns a range-filter service whose state holds the
// given backend, for tests that inject a fake range filter without loading real
// model files. Phase 2b of the model de-privilege epic moved the range filter off
// *BirdNET into this orchestrator-owned service, so tests set the backend here
// instead of assigning bn.rangeFilter. A nil backend means "no filter loaded".
func newTestRangeFilterService(backend inference.RangeFilter) *rangeFilterService {
	rfs := newRangeFilterService(nil)
	rfs.state.Store(&rangeFilterState{backend: backend})
	return rfs
}

// swapTestBackend atomically installs backend as the current range-filter backend
// and closes the previous one, under rfs.mu, mirroring reload's swap step without
// building from disk. Test seam for the backend-lifecycle race test.
func (rfs *rangeFilterService) swapTestBackend(backend inference.RangeFilter) {
	rfs.mu.Lock()
	old := rfs.loadState()
	// Bump the generation exactly as reload does, so the occurrence cache is
	// invalidated across the swap in tests that exercise it.
	rfs.state.Store(&rangeFilterState{backend: backend, generation: old.generation + 1})
	if old.backend != nil && old.backend != backend {
		old.backend.Close()
	}
	rfs.mu.Unlock()
}

// fakeUniversalRangeFilter implements inference.RangeFilter and
// UniversalSpeciesPredictor for testing BuildRangeFilter.
type fakeUniversalRangeFilter struct {
	geoLabels []string
	scores    []SpeciesScore
	rawScores []float32
	err       error
}

func (f *fakeUniversalRangeFilter) Predict(_, _, _ float32) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]float32, len(f.rawScores))
	copy(out, f.rawScores)
	return out, nil
}

func (f *fakeUniversalRangeFilter) NumSpecies() int { return len(f.rawScores) }
func (f *fakeUniversalRangeFilter) Close()          {}

func (f *fakeUniversalRangeFilter) PredictSpeciesScores(_, _, _, _ float32) ([]SpeciesScore, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]SpeciesScore, len(f.scores))
	copy(out, f.scores)
	return out, nil
}

func (f *fakeUniversalRangeFilter) GeomodelLabels() []string {
	return f.geoLabels
}

// buildTestOrchestrator creates a minimal Orchestrator with the given range
// filter and settings suitable for testing BuildRangeFilter.
func buildTestOrchestrator(t *testing.T, settings *conf.Settings, rf interface{ Close() }) *Orchestrator {
	t.Helper()
	bn := &BirdNET{
		Settings: settings,
	}
	o := &Orchestrator{
		Settings: settings,
		primary:  bn,
	}
	o.settingsAtomic.Store(settings)
	var backend inference.RangeFilter
	if irf, ok := rf.(inference.RangeFilter); ok {
		backend = irf
	}
	o.rangeFilter = newTestRangeFilterService(backend)
	return o
}

func TestBuildRangeFilter_PassUnmappedSpecies(t *testing.T) {
	geoLabels := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Corvus corax_Northern Raven",
	}

	classifierLabels := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Ficedula hypoleuca_Pied Flycatcher", // not in geomodel
		"Regulus regulus_Goldcrest",          // not in geomodel
	}

	geoScores := []SpeciesScore{
		{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
		{Score: 0.8, Label: "Parus major_Great Tit"},
	}

	tests := []struct {
		name              string
		passUnmapped      bool
		wantMinSpecies    int
		wantUnmappedInSet []string
	}{
		{
			name:           "disabled: only geomodel species included",
			passUnmapped:   false,
			wantMinSpecies: 2,
		},
		{
			name:           "enabled: unmapped classifier species added",
			passUnmapped:   true,
			wantMinSpecies: 4,
			wantUnmappedInSet: []string{
				"Ficedula hypoleuca_Pied Flycatcher",
				"Regulus regulus_Goldcrest",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := conftest.GetTestSettings()
			settings.BirdNET.Latitude = 60.0
			settings.BirdNET.Longitude = 25.0
			settings.BirdNET.LocationConfigured = true
			settings.BirdNET.RangeFilter.Threshold = 0.01
			settings.BirdNET.RangeFilter.PassUnmappedSpecies = tt.passUnmapped
			settings.BirdNET.Labels = classifierLabels
			conftest.SetTestSettings(settings)
			t.Cleanup(func() { conftest.SetTestSettings(nil) })

			rf := &fakeUniversalRangeFilter{
				geoLabels: geoLabels,
				scores:    geoScores,
				rawScores: []float32{0.9, 0.8, 0.3},
			}

			o := buildTestOrchestrator(t, settings, rf)
			err := BuildRangeFilter(o)
			require.NoError(t, err)

			included := conf.GetSettings().GetIncludedSpecies()
			assert.GreaterOrEqual(t, len(included), tt.wantMinSpecies,
				"included species count should be at least %d", tt.wantMinSpecies)

			for _, want := range tt.wantUnmappedInSet {
				assert.Contains(t, included, want,
					"unmapped species %q should be in included set", want)
			}
		})
	}
}

func TestBuildRangeFilter_PassUnmappedSpecies_RespectsExcludeList(t *testing.T) {
	geoLabels := []string{
		"Turdus merula_Common Blackbird",
	}
	classifierLabels := []string{
		"Turdus merula_Common Blackbird",
		"Ficedula hypoleuca_Pied Flycatcher", // unmapped
		"Regulus regulus_Goldcrest",          // unmapped, excluded
	}
	geoScores := []SpeciesScore{
		{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
	}

	settings := conftest.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = true
	settings.BirdNET.Labels = classifierLabels
	settings.Realtime.Species.Exclude = []string{"Goldcrest"}
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	rf := &fakeUniversalRangeFilter{
		geoLabels: geoLabels,
		scores:    geoScores,
		rawScores: []float32{0.9},
	}

	o := buildTestOrchestrator(t, settings, rf)
	err := BuildRangeFilter(o)
	require.NoError(t, err)

	included := conf.GetSettings().GetIncludedSpecies()
	assert.Contains(t, included, "Ficedula hypoleuca_Pied Flycatcher",
		"unmapped, non-excluded species should be included")
	assert.NotContains(t, included, "Regulus regulus_Goldcrest",
		"unmapped but excluded species should not be included")
}

func TestBuildRangeFilter_UpdatesUnmappedScore(t *testing.T) {
	geoLabels := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
	}
	classifierLabels := []string{
		"Turdus merula_Common Blackbird",
		"Ficedula hypoleuca_Pied Flycatcher",
	}

	inner := &fakeRangeFilter{
		scores: []float32{0.9, 0.8},
	}
	mrf := newMappedRangeFilter(inner, classifierLabels, geoLabels, 0.0)
	require.InDelta(t, 0.0, float64(mrf.unmappedScore), 0.001,
		"initial unmappedScore should be 0.0")

	settings := conftest.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = true
	settings.BirdNET.Labels = classifierLabels
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o := buildTestOrchestrator(t, settings, mrf)

	err := BuildRangeFilter(o)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, float64(mrf.unmappedScore), 0.001,
		"unmappedScore should be 1.0 after rebuild with PassUnmappedSpecies=true")

	// Toggle off and rebuild
	settings2 := conf.CloneSettings(settings)
	settings2.BirdNET.RangeFilter.PassUnmappedSpecies = false
	conftest.SetTestSettings(settings2)

	err = BuildRangeFilter(o)
	require.NoError(t, err)
	assert.InDelta(t, 0.0, float64(mrf.unmappedScore), 0.001,
		"unmappedScore should be 0.0 after rebuild with PassUnmappedSpecies=false")
}

func TestGetProbableSpecies_PassUnmappedSpecies(t *testing.T) {
	geoLabels := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
	}
	classifierLabels := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Ficedula hypoleuca_Pied Flycatcher",
	}

	tests := []struct {
		name           string
		passUnmapped   bool
		wantUnmappedIn bool
		wantMinSpecies int
	}{
		{
			name:           "disabled: only geomodel species returned",
			passUnmapped:   false,
			wantUnmappedIn: false,
			wantMinSpecies: 2,
		},
		{
			name:           "enabled: unmapped classifier species included with score 0",
			passUnmapped:   true,
			wantUnmappedIn: true,
			wantMinSpecies: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := conftest.GetTestSettings()
			settings.BirdNET.Latitude = 60.0
			settings.BirdNET.Longitude = 25.0
			settings.BirdNET.LocationConfigured = true
			settings.BirdNET.RangeFilter.Threshold = 0.01
			settings.BirdNET.RangeFilter.PassUnmappedSpecies = tt.passUnmapped
			settings.BirdNET.Labels = classifierLabels

			rf := &fakeUniversalRangeFilter{
				geoLabels: geoLabels,
				scores: []SpeciesScore{
					{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
					{Score: 0.8, Label: "Parus major_Great Tit"},
				},
				rawScores: []float32{0.9, 0.8},
			}

			rfs := newTestRangeFilterService(rf)

			scores, _, _, err := rfs.probableSpecies(time.Now(), 0, settings)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(scores), tt.wantMinSpecies)

			hasUnmapped := false
			for _, ss := range scores {
				if ss.Label == "Ficedula hypoleuca_Pied Flycatcher" {
					hasUnmapped = true
					assert.InDelta(t, 0.0, ss.Score, 0.001,
						"unmapped species should have score 0.0")
				}
			}
			assert.Equal(t, tt.wantUnmappedIn, hasUnmapped,
				"unmapped species presence should match expectation")
		})
	}
}

// assertAllOccurrenceValues asserts every value in an occurrence map equals want.
// buildOccurrenceIndex may store both a raw and a canonical key for one species, so
// this checks all entries rather than assuming a specific key spelling.
func assertAllOccurrenceValues(t *testing.T, scores map[string]float64, want float64) {
	t.Helper()
	require.NotEmpty(t, scores)
	for k, v := range scores {
		assert.InDelta(t, want, v, 0.001, "occurrence score for %q", k)
	}
}

// TestRangeFilterService_CacheInvalidatedOnBackendSwap verifies the occurrence cache
// is invalidated across a backend swap by the generation tag alone.
// swapTestBackend bumps the generation exactly as reload does but, unlike reload, does
// NOT call clearSpeciesCache, so a stale entry survives in the map. If the generation
// gate in getCachedSpeciesScores did not reject it, the post-swap read would return the
// pre-swap backend's scores.
func TestRangeFilterService_CacheInvalidatedOnBackendSwap(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.Labels = []string{"Turdus merula_Common Blackbird"}

	geoLabels := []string{"Turdus merula_Common Blackbird"}
	backendA := &fakeUniversalRangeFilter{
		geoLabels: geoLabels,
		scores:    []SpeciesScore{{Score: 0.9, Label: "Turdus merula_Common Blackbird"}},
		rawScores: []float32{0.9},
	}
	backendB := &fakeUniversalRangeFilter{
		geoLabels: geoLabels,
		scores:    []SpeciesScore{{Score: 0.1, Label: "Turdus merula_Common Blackbird"}},
		rawScores: []float32{0.1},
	}

	rfs := newTestRangeFilterService(backendA)
	date := time.Now()

	// Populate the cache against backend A, then confirm the second read is a hit.
	got, err := rfs.getCachedSpeciesScores(date, settings)
	require.NoError(t, err)
	assertAllOccurrenceValues(t, got, 0.9)
	assert.Equal(t, 1, rfs.speciesCacheLen(), "first read should populate the cache")

	hit, err := rfs.getCachedSpeciesScores(date, settings)
	require.NoError(t, err)
	assert.Equal(t, got, hit, "second read should be a cache hit with identical scores")

	// Swap in backend B: bumps the generation but leaves the stale gen-0 entry in the
	// map, so only the generation tag can prevent serving backend A's scores.
	rfs.swapTestBackend(backendB)

	after, err := rfs.getCachedSpeciesScores(date, settings)
	require.NoError(t, err)
	assertAllOccurrenceValues(t, after, 0.1)

	// A subsequent read is a cache hit at the new generation.
	afterHit, err := rfs.getCachedSpeciesScores(date, settings)
	require.NoError(t, err)
	assert.Equal(t, after, afterHit, "read after swap should re-cache at the new generation")
}

// TestRangeFilterService_CacheGenerationRace exercises concurrent occurrence-cache
// reads against a stream of backend swaps under the race detector, proving the
// generation reads (atomic loadState) and the cache access (speciesCacheMu) are
// race-free and that a reader never serves an out-of-range (torn) score.
func TestRangeFilterService_CacheGenerationRace(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.Labels = []string{"Turdus merula_Common Blackbird"}

	newBackend := func(score float64) *fakeUniversalRangeFilter {
		return &fakeUniversalRangeFilter{
			geoLabels: []string{"Turdus merula_Common Blackbird"},
			scores:    []SpeciesScore{{Score: score, Label: "Turdus merula_Common Blackbird"}},
			rawScores: []float32{float32(score)},
		}
	}

	rfs := newTestRangeFilterService(newBackend(0.5))
	date := time.Now()

	var wg sync.WaitGroup
	const readers = 8
	for range readers {
		wg.Go(func() {
			for range 200 {
				got, err := rfs.getCachedSpeciesScores(date, settings)
				if err != nil {
					t.Errorf("getCachedSpeciesScores: %v", err)
					return
				}
				for _, v := range got {
					if v < 0 || v > 1 {
						t.Errorf("out-of-range occurrence score %v", v)
						return
					}
				}
			}
		})
	}
	wg.Go(func() {
		for i := range 100 {
			rfs.swapTestBackend(newBackend(float64(i%10) / 10.0))
		}
	})
	wg.Wait()

	// Positive control: the swapper actually ran and advanced the generation, so the
	// readers were exercised against a moving backend rather than an all-cache-hit run.
	require.Positive(t, rfs.loadState().generation, "concurrent swaps should have advanced the generation")
}

// speciesCacheLen returns the occurrence-cache size under the cache lock, so tests can
// assert on caching behavior without racing the map (the -race detector flags an
// unsynchronized len() otherwise).
func (rfs *rangeFilterService) speciesCacheLen() int {
	rfs.speciesCacheMu.RLock()
	defer rfs.speciesCacheMu.RUnlock()
	return len(rfs.speciesCache)
}

// fakeReloadingRangeFilter simulates a reload that commits a new backend generation WHILE
// a prediction is in flight, to deterministically exercise the write-path skip-caching
// guard (genBefore != genNow) in getCachedSpeciesScores. probableSpecies calls
// PredictSpeciesScores while holding rfs.mu, which is the same lock that guards generation
// writes, so this bumps the published state directly here; calling swapTestBackend instead
// would self-deadlock on rfs.mu. The bump fires once, so a later uncontended read caches
// normally.
type fakeReloadingRangeFilter struct {
	fakeUniversalRangeFilter
	rfs  *rangeFilterService
	once sync.Once
}

func (f *fakeReloadingRangeFilter) PredictSpeciesScores(lat, lon, week, threshold float32) ([]SpeciesScore, error) {
	f.once.Do(func() {
		// Publish a new generation carrying the same backend, mimicking a concurrent
		// reload that swapped the range filter during this prediction. Safe under the
		// held rfs.mu; the counter only ever increases.
		old := f.rfs.loadState()
		f.rfs.state.Store(&rangeFilterState{backend: old.backend, fellBack: old.fellBack, generation: old.generation + 1})
	})
	return f.fakeUniversalRangeFilter.PredictSpeciesScores(lat, lon, week, threshold)
}

// TestRangeFilterService_CacheSkippedWhenReloadRacesPrediction verifies the write-path
// anti-poisoning guard: when the backend generation changes DURING probableSpecies (a
// reload racing the prediction), getCachedSpeciesScores must return the fresh scores
// WITHOUT caching them, because they may reflect a superseded backend. Without the guard
// they would be cached tagged with the new generation and later served as if current.
func TestRangeFilterService_CacheSkippedWhenReloadRacesPrediction(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.Labels = []string{"Turdus merula_Common Blackbird"}

	inner := fakeUniversalRangeFilter{
		geoLabels: []string{"Turdus merula_Common Blackbird"},
		scores:    []SpeciesScore{{Score: 0.7, Label: "Turdus merula_Common Blackbird"}},
		rawScores: []float32{0.7},
	}
	backend := &fakeReloadingRangeFilter{fakeUniversalRangeFilter: inner}
	rfs := newTestRangeFilterService(backend)
	backend.rfs = rfs
	date := time.Now()

	// First read: the fake bumps the generation mid-prediction, so genBefore != genNow.
	// The caller still gets the fresh scores, but they must NOT be cached.
	got, err := rfs.getCachedSpeciesScores(date, settings)
	require.NoError(t, err)
	assertAllOccurrenceValues(t, got, 0.7)
	assert.Zero(t, rfs.speciesCacheLen(), "scores computed across a mid-prediction reload must not be cached")

	// Second read: the fake no longer bumps (once fired), so genBefore == genNow and the
	// entry caches normally, proving the skip was specific to the raced generation.
	_, err = rfs.getCachedSpeciesScores(date, settings)
	require.NoError(t, err)
	assert.Equal(t, 1, rfs.speciesCacheLen(), "an uncontended read should cache normally")
}

func TestBuildRangeFilter_UnmappedSpeciesInIsSpeciesIncluded(t *testing.T) {
	geoLabels := []string{
		"Turdus merula_Common Blackbird",
	}
	classifierLabels := []string{
		"Turdus merula_Common Blackbird",
		"Ficedula hypoleuca_Pied Flycatcher",
	}
	geoScores := []SpeciesScore{
		{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
	}

	settings := conftest.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = true
	settings.BirdNET.Labels = classifierLabels
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	rf := &fakeUniversalRangeFilter{
		geoLabels: geoLabels,
		scores:    geoScores,
		rawScores: []float32{0.9},
	}

	o := buildTestOrchestrator(t, settings, rf)
	err := BuildRangeFilter(o)
	require.NoError(t, err)

	updated := conf.GetSettings()
	assert.True(t, updated.IsSpeciesIncluded("Turdus merula_Common Blackbird"),
		"mapped species should be included")
	assert.True(t, updated.IsSpeciesIncluded("Ficedula hypoleuca_Pied Flycatcher"),
		"unmapped species should be included when PassUnmappedSpecies is true")
}

// TestGetWeekForFilter_MonthEndClamp verifies the derived BirdNET week stays in
// [1, 48] for every calendar day, clamping the 5th partial week at the end of a
// 29-31 day month to week 4. Regression guard for the Dec 29-31 -> week 49 bug
// (mirrors the fix in internal/api/v2/range.calculateWeek and the canonical
// onnx.CalculateWeek).
func TestGetWeekForFilter_MonthEndClamp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		dateStr string
		want    float32
	}{
		{"Jan 1", "2023-01-01", 1},
		{"Jan 28", "2023-01-28", 4},
		{"Jan 29 clamps to week 4", "2023-01-29", 4},
		{"Jan 31 clamps to week 4", "2023-01-31", 4},
		{"Feb 28", "2023-02-28", 8},
		{"Feb 29 leap clamps to week 8", "2024-02-29", 8},
		{"Dec 28", "2023-12-28", 48},
		{"Dec 29 would be 49 without clamp", "2023-12-29", 48},
		{"Dec 30 would be 49 without clamp", "2023-12-30", 48},
		{"Dec 31 would be 49 without clamp", "2023-12-31", 48},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			date, err := time.Parse(time.DateOnly, tc.dateStr)
			require.NoError(t, err)
			assert.InDelta(t, tc.want, getWeekForFilter(date), 0.0001)
		})
	}

	// Invariant: the derived week must stay within [1, 48] for every day of a
	// full leap year, covering every month-end partial week (including Feb 29).
	for date := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC); date.Year() == 2024; date = date.AddDate(0, 0, 1) {
		week := getWeekForFilter(date)
		assert.GreaterOrEqual(t, week, float32(1), "week below 1 for %s", date.Format(time.DateOnly))
		assert.LessOrEqual(t, week, float32(48), "week above 48 for %s", date.Format(time.DateOnly))
	}
}
