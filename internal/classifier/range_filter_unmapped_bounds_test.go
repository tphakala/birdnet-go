package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// unmappedBoundsFixture builds a settings snapshot whose label set is SHORTER than
// the classifier->geomodel mapping a mappedRangeFilter captures at model-load time.
// The mapping is built from a 3-label classifier set whose index 2 has no geomodel
// match (geoIdx == -1) and is therefore beyond the 2-label snapshot. With
// PassUnmappedSpecies enabled, the unmapped loop indexes the snapshot labels by the
// mapping index, so index 2 is out of range - the panic this guards against. The divergence
// models a concurrent model/settings reload window (or a custom settings struct passed
// to GetProbableSpeciesWithSettings).
func unmappedBoundsFixture(t *testing.T) (*conf.Settings, *mappedRangeFilter) {
	t.Helper()
	settings := conftest.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = true
	// Snapshot carries only 2 labels - shorter than the mapping built below.
	settings.BirdNET.Labels = []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
	}

	geomodelLabels := []string{"Turdus merula_Common Blackbird"}
	modelClassifierLabels := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Ficedula hypoleuca_Pied Flycatcher", // unmapped, at index 2 (beyond the snapshot)
	}
	mrf := newMappedRangeFilter(
		&fakeRangeFilter{scores: []float32{0.9}},
		modelClassifierLabels,
		geomodelLabels,
		1.0,
	)
	return settings, mrf
}

// TestGetProbableSpecies_PassUnmapped_MappingLongerThanSnapshotLabels_NoPanic covers
// the getProbableSpecies universal path: the unmapped loop must bounds-check the
// mapping index against the live settings snapshot before indexing it.
func TestGetProbableSpecies_PassUnmapped_MappingLongerThanSnapshotLabels_NoPanic(t *testing.T) {
	settings, mrf := unmappedBoundsFixture(t)

	bn := &BirdNET{
		Settings:     settings,
		rangeFilter:  mrf,
		speciesCache: make(map[string]*speciesCacheEntry),
	}

	require.NotPanics(t, func() {
		scores, _, err := bn.getProbableSpecies(time.Now(), 0, settings)
		require.NoError(t, err)
		labels := make([]string, 0, len(scores))
		for _, ss := range scores {
			labels = append(labels, ss.Label)
		}
		// The in-range unmapped label (index 1) is still added; the out-of-range
		// index (2) is safely skipped rather than panicking.
		assert.Contains(t, labels, "Parus major_Great Tit",
			"an in-range unmapped classifier species must still be added")
	})
}

// TestBuildRangeFilter_PassUnmapped_MappingLongerThanSnapshotLabels_NoPanic covers the
// sibling unmapped loop on the BuildRangeFilter path.
func TestBuildRangeFilter_PassUnmapped_MappingLongerThanSnapshotLabels_NoPanic(t *testing.T) {
	settings, mrf := unmappedBoundsFixture(t)
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o := buildTestOrchestrator(t, settings, mrf)

	require.NotPanics(t, func() {
		require.NoError(t, BuildRangeFilter(o))
	})

	included := conf.GetSettings().GetIncludedSpecies()
	assert.Contains(t, included, "Parus major_Great Tit",
		"an in-range unmapped classifier species must still be added")
}
