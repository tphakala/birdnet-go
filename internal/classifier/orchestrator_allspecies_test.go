package classifier

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// scoreForLabel returns the SpeciesScore matching label, or false if absent.
func scoreForLabel(scores []SpeciesScore, label string) (SpeciesScore, bool) {
	for _, s := range scores {
		if s.Label == label {
			return s, true
		}
	}
	return SpeciesScore{}, false
}

// hasScientificName reports whether any score's scientific name matches sci
// (case-insensitive). Used to assert deduplication by scientific name across
// the geomodel ("ScientificName_CommonName") and Perch (scientific-name-only)
// label conventions.
func hasScientificName(scores []SpeciesScore, sci string) bool {
	for _, s := range scores {
		// Compare case-insensitively to match the production dedup, which keys
		// on the lowercased scientific name.
		if strings.EqualFold(detection.ExtractScientificName(s.Label), sci) {
			return true
		}
	}
	return false
}

// buildAllSpeciesOrchestrator wires a primary BirdNET whose range filter is the
// supplied universal predictor plus a single non-primary model returning the
// given labels. The primary is excluded from the non-primary loop by its ID.
func buildAllSpeciesOrchestrator(t *testing.T, settings *conf.Settings, rf *fakeUniversalRangeFilter, nonPrimaryID string, nonPrimaryLabels []string) *Orchestrator {
	t.Helper()

	const primaryID = "BirdNET_V3"

	bn := &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
	}
	bn.ModelInfo.ID = primaryID
	bn.rangeFilter = rf

	nonPrimary := &mockModelInstance{
		id:     nonPrimaryID,
		labels: nonPrimaryLabels,
	}

	return &Orchestrator{
		Settings: settings,
		primary:  bn,
		models: map[string]*modelEntry{
			primaryID:    {instance: bn},
			nonPrimaryID: {instance: nonPrimary},
		},
	}
}

// universalSettings returns settings configured so the primary routes through
// the universal geomodel path in getProbableSpecies.
func universalSettings(t *testing.T) *conf.Settings {
	t.Helper()
	settings := conf.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	return settings
}

// TestGetAllProbableSpecies_GeomodelCoveredBelowThreshold is the regression
// guard for issue #3250: a non-primary species the geomodel knows about but
// that is below the range-filter threshold must NOT be re-added at 1.0. This
// is the bug that ballooned the active species count.
func TestGetAllProbableSpecies_GeomodelCoveredBelowThreshold(t *testing.T) {
	settings := universalSettings(t)
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = true

	// Geomodel knows both species (geoCovered), but only Turdus merula is above
	// the threshold (returned by PredictSpeciesScores).
	rf := &fakeUniversalRangeFilter{
		geoLabels: []string{
			"Turdus merula_Common Blackbird",
			"Parus major_Great Tit",
		},
		scores: []SpeciesScore{
			{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
		},
		rawScores: []float32{0.9, 0.0},
	}

	// Perch (non-primary) emits scientific-name-only labels. Parus major is
	// geomodel-covered-but-below-threshold and must be skipped.
	o := buildAllSpeciesOrchestrator(t, settings, rf, "Perch_V2",
		[]string{"Turdus merula", "Parus major"})

	scores, err := o.GetAllProbableSpeciesWithSettings(time.Now(), 0, settings)
	require.NoError(t, err)

	// Turdus merula is present once (via the primary, ScientificName_CommonName).
	assert.True(t, hasScientificName(scores, "Turdus merula"),
		"above-threshold species should be present from primary")
	// Parus major is geomodel-covered but below threshold: range filter excludes
	// it, so it must NOT appear from the non-primary at all.
	assert.False(t, hasScientificName(scores, "Parus major"),
		"geomodel-covered, below-threshold species must not be re-added (regression #3250)")
}

// TestGetAllProbableSpecies_NoDuplicateByScientificName verifies that a
// non-primary species already present via the primary (as
// ScientificName_CommonName) is not duplicated when the non-primary emits the
// scientific name only.
func TestGetAllProbableSpecies_NoDuplicateByScientificName(t *testing.T) {
	settings := universalSettings(t)
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = true

	rf := &fakeUniversalRangeFilter{
		geoLabels: []string{"Turdus merula_Common Blackbird"},
		scores: []SpeciesScore{
			{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
		},
		rawScores: []float32{0.9},
	}

	o := buildAllSpeciesOrchestrator(t, settings, rf, "Perch_V2",
		[]string{"Turdus merula"})

	scores, err := o.GetAllProbableSpeciesWithSettings(time.Now(), 0, settings)
	require.NoError(t, err)

	count := 0
	for _, s := range scores {
		if detection.ExtractScientificName(s.Label) == "Turdus merula" {
			count++
		}
	}
	assert.Equal(t, 1, count, "species present via primary must not be duplicated by the non-primary")
}

// TestGetAllProbableSpecies_UnmappedPassThrough verifies that a non-primary
// species the geomodel does not know about is passed through at score 1.0 when
// PassUnmappedSpecies is enabled.
func TestGetAllProbableSpecies_UnmappedPassThrough(t *testing.T) {
	settings := universalSettings(t)
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = true

	rf := &fakeUniversalRangeFilter{
		geoLabels: []string{"Turdus merula_Common Blackbird"},
		scores: []SpeciesScore{
			{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
		},
		rawScores: []float32{0.9},
	}

	// Aratinga is not in the geomodel at all (unmapped).
	o := buildAllSpeciesOrchestrator(t, settings, rf, "Perch_V2",
		[]string{"Aratinga solstitialis"})

	scores, err := o.GetAllProbableSpeciesWithSettings(time.Now(), 0, settings)
	require.NoError(t, err)

	got, ok := scoreForLabel(scores, "Aratinga solstitialis")
	require.True(t, ok, "geomodel-unmapped species should be added when PassUnmappedSpecies is true")
	assert.InDelta(t, 1.0, got.Score, 0.0001, "pass-through species must be scored 1.0")
}

// TestGetAllProbableSpecies_UnmappedBlockedWhenDisabled verifies that a
// geomodel-unmapped non-primary species is NOT added when PassUnmappedSpecies
// is disabled.
func TestGetAllProbableSpecies_UnmappedBlockedWhenDisabled(t *testing.T) {
	settings := universalSettings(t)
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = false

	rf := &fakeUniversalRangeFilter{
		geoLabels: []string{"Turdus merula_Common Blackbird"},
		scores: []SpeciesScore{
			{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
		},
		rawScores: []float32{0.9},
	}

	o := buildAllSpeciesOrchestrator(t, settings, rf, "Perch_V2",
		[]string{"Aratinga solstitialis"})

	scores, err := o.GetAllProbableSpeciesWithSettings(time.Now(), 0, settings)
	require.NoError(t, err)

	_, ok := scoreForLabel(scores, "Aratinga solstitialis")
	assert.False(t, ok, "geomodel-unmapped species must be blocked when PassUnmappedSpecies is false")
}

// TestGetAllProbableSpecies_ExcludeHonored verifies that an excluded
// non-primary species is never added regardless of mapping state.
func TestGetAllProbableSpecies_ExcludeHonored(t *testing.T) {
	settings := universalSettings(t)
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = true
	settings.Realtime.Species.Exclude = []string{"Aratinga solstitialis"}

	rf := &fakeUniversalRangeFilter{
		geoLabels: []string{"Turdus merula_Common Blackbird"},
		scores: []SpeciesScore{
			{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
		},
		rawScores: []float32{0.9},
	}

	o := buildAllSpeciesOrchestrator(t, settings, rf, "Perch_V2",
		[]string{"Aratinga solstitialis"})

	scores, err := o.GetAllProbableSpeciesWithSettings(time.Now(), 0, settings)
	require.NoError(t, err)

	_, ok := scoreForLabel(scores, "Aratinga solstitialis")
	assert.False(t, ok, "excluded species must never be added even when unmapped and pass-through is enabled")
}

// TestGetAllProbableSpecies_NonUniversalPrimary verifies that when the primary
// has no geomodel (legacy TFLite range filter, not universal), non-primary
// labels are added at 1.0, deduped by scientific name, and exclude is honored
// without gating on PassUnmappedSpecies. This preserves prior behavior for
// multi-classifier setups that have no geomodel.
func TestGetAllProbableSpecies_NonUniversalPrimary(t *testing.T) {
	settings := universalSettings(t)
	// PassUnmappedSpecies is irrelevant on the non-universal path; set false to
	// prove the non-primary labels are still added.
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = false
	settings.Realtime.Species.Exclude = []string{"Corvus corax"}
	settings.BirdNET.Labels = []string{"Turdus merula_Common Blackbird"}

	// A non-universal range filter: implements inference.RangeFilter only.
	primaryRF := &fakeRangeFilter{scores: []float32{0.9}}

	bn := &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
	}
	bn.ModelInfo.ID = "BirdNET_V2.4"
	bn.rangeFilter = primaryRF

	nonPrimary := &mockModelInstance{
		id: "Perch_V2",
		labels: []string{
			"Turdus merula", // already present via primary (deduped by sci)
			"Parus major",   // new, should be added at 1.0
			"Corvus corax",  // excluded, must not be added
		},
	}

	o := &Orchestrator{
		Settings: settings,
		primary:  bn,
		models: map[string]*modelEntry{
			"BirdNET_V2.4": {instance: bn},
			"Perch_V2":     {instance: nonPrimary},
		},
	}

	scores, err := o.GetAllProbableSpeciesWithSettings(time.Now(), 0, settings)
	require.NoError(t, err)

	// Parus major is new and added at 1.0.
	got, ok := scoreForLabel(scores, "Parus major")
	require.True(t, ok, "non-universal primary: new non-primary label should be added")
	assert.InDelta(t, 1.0, got.Score, 0.0001, "non-universal primary: added label scored 1.0")

	// Turdus merula present exactly once (deduped by scientific name).
	count := 0
	for _, s := range scores {
		if detection.ExtractScientificName(s.Label) == "Turdus merula" {
			count++
		}
	}
	assert.Equal(t, 1, count, "non-universal primary: species present via primary must not be duplicated")

	// Corvus corax excluded.
	assert.False(t, hasScientificName(scores, "Corvus corax"),
		"non-universal primary: excluded species must not be added")
}

// TestGetAllProbableSpecies_BatModelSkipped verifies the bat model is never
// iterated when collecting non-primary species.
func TestGetAllProbableSpecies_BatModelSkipped(t *testing.T) {
	settings := universalSettings(t)
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = true

	rf := &fakeUniversalRangeFilter{
		geoLabels: []string{"Turdus merula_Common Blackbird"},
		scores: []SpeciesScore{
			{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
		},
		rawScores: []float32{0.9},
	}

	const primaryID = "BirdNET_V3"
	bn := &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
	}
	bn.ModelInfo.ID = primaryID
	bn.rangeFilter = rf

	batModel := &mockModelInstance{
		id:     RegistryIDBat,
		labels: []string{"Myotis daubentonii"},
	}

	o := &Orchestrator{
		Settings: settings,
		primary:  bn,
		models: map[string]*modelEntry{
			primaryID:     {instance: bn},
			RegistryIDBat: {instance: batModel},
		},
	}

	scores, err := o.GetAllProbableSpeciesWithSettings(time.Now(), 0, settings)
	require.NoError(t, err)

	assert.False(t, hasScientificName(scores, "Myotis daubentonii"),
		"bat model species must never be collected")
}

// TestGetAllProbableSpecies_DeterministicDedupByModelID verifies that when two
// non-primary models emit different labels for the same scientific name, the
// surviving label is deterministic: refs are sorted by model ID, so the
// lower-ID model is processed first and its label wins the scientific-name
// dedup (independent of Go's randomized map iteration order).
func TestGetAllProbableSpecies_DeterministicDedupByModelID(t *testing.T) {
	settings := universalSettings(t)
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = true

	rf := &fakeUniversalRangeFilter{
		geoLabels: []string{"Turdus merula_Common Blackbird"},
		scores: []SpeciesScore{
			{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
		},
		rawScores: []float32{0.9},
	}

	const primaryID = "BirdNET_V3"
	bn := &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
	}
	bn.ModelInfo.ID = primaryID
	bn.rangeFilter = rf

	// Both secondary models emit the same scientific name (geomodel-unmapped, so
	// it passes through) but with different label strings. The lower model ID
	// ("aaa_model") is processed first and its label survives.
	lower := &mockModelInstance{id: "aaa_model", labels: []string{"Aratinga solstitialis"}}
	higher := &mockModelInstance{id: "zzz_model", labels: []string{"Aratinga solstitialis_Sun Parakeet"}}

	o := &Orchestrator{
		Settings: settings,
		primary:  bn,
		models: map[string]*modelEntry{
			primaryID:   {instance: bn},
			"aaa_model": {instance: lower},
			"zzz_model": {instance: higher},
		},
	}

	scores, err := o.GetAllProbableSpeciesWithSettings(time.Now(), 0, settings)
	require.NoError(t, err)

	count := 0
	for _, s := range scores {
		if strings.EqualFold(detection.ExtractScientificName(s.Label), "Aratinga solstitialis") {
			count++
		}
	}
	assert.Equal(t, 1, count, "shared scientific name must appear exactly once")

	_, lowerPresent := scoreForLabel(scores, "Aratinga solstitialis")
	assert.True(t, lowerPresent, "lower model ID label must survive the scientific-name dedup")
	_, higherPresent := scoreForLabel(scores, "Aratinga solstitialis_Sun Parakeet")
	assert.False(t, higherPresent, "higher model ID label must be deduped away")
}
