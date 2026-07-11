package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

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
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
	}
	if irf, ok := rf.(interface {
		Predict(float32, float32, float32) ([]float32, error)
		NumSpecies() int
		Close()
	}); ok {
		bn.rangeFilter = irf
	}
	return &Orchestrator{
		Settings: settings,
		primary:  bn,
	}
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

	bn := &BirdNET{
		Settings:     settings,
		rangeFilter:  mrf,
		speciesCache: make(map[string]*speciesCacheEntry),
	}
	o := &Orchestrator{
		Settings: settings,
		primary:  bn,
	}

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

			bn := &BirdNET{
				Settings:     settings,
				rangeFilter:  rf,
				speciesCache: make(map[string]*speciesCacheEntry),
			}

			scores, _, err := bn.getProbableSpecies(time.Now(), 0, settings)
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
