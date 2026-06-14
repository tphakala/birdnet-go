// species_all_test.go: tests for buildAllSpeciesList, the /api/v2/species/all
// include/exclude picker payload builder.

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildAllSpeciesList_CachedMapLocalizedAndSorted verifies that when the
// cached sciToCommon map is populated (the steady-state path), the picker list is
// built from it: every entry (including a scientific-only secondary-model species
// such as a bat) carries its localized common name, and the output is sorted by
// scientific name. fallbackLabels is ignored on this path.
func TestBuildAllSpeciesList_CachedMapLocalizedAndSorted(t *testing.T) {
	t.Parallel()

	sciToCommon := map[string]string{
		"Barbastella barbastellus": "mopsilepakko", // localized bat name (secondary model)
		"Turdus merula":            "Mustarastas",  // localized bird name
	}

	got := buildAllSpeciesList(sciToCommon, nil)

	require.Len(t, got, 2)

	// Sorted by scientific name: "Barbastella" < "Turdus".
	assert.Equal(t, "Barbastella barbastellus", got[0].ScientificName)
	assert.Equal(t, "mopsilepakko", got[0].CommonName,
		"scientific-only bat species must carry its localized common name")
	assert.Equal(t, "Barbastella barbastellus_mopsilepakko", got[0].Label)

	assert.Equal(t, "Turdus merula", got[1].ScientificName)
	assert.Equal(t, "Mustarastas", got[1].CommonName)
}

// TestBuildAllSpeciesList_FallbackToEmbeddedCommon verifies that when the cached
// map has no localized name (e.g. the startup window before name maps localize),
// the label's embedded common name is used.
func TestBuildAllSpeciesList_FallbackToEmbeddedCommon(t *testing.T) {
	t.Parallel()

	got := buildAllSpeciesList(map[string]string{}, []string{"Turdus merula_Eurasian Blackbird"})
	require.Len(t, got, 1)
	assert.Equal(t, "Turdus merula", got[0].ScientificName)
	assert.Equal(t, "Eurasian Blackbird", got[0].CommonName)
	assert.Equal(t, "Turdus merula_Eurasian Blackbird", got[0].Label)
}

// TestBuildAllSpeciesList_ScientificOnlyWithoutLocalization verifies that a
// scientific-only label with no localized common name does not produce a doubled
// "Sci_Sci" label: ParseSpeciesString reports CommonName == ScientificName for
// such labels, so the output label stays scientific-only.
func TestBuildAllSpeciesList_ScientificOnlyWithoutLocalization(t *testing.T) {
	t.Parallel()

	got := buildAllSpeciesList(map[string]string{}, []string{"Barbastella barbastellus"})
	require.Len(t, got, 1)
	assert.Equal(t, "Barbastella barbastellus", got[0].ScientificName)
	assert.Equal(t, "Barbastella barbastellus", got[0].Label,
		"scientific-only label must not be doubled into Sci_Sci")
}

// TestBuildAllSpeciesList_FallbackDedupesByScientificName verifies that the
// fallback path (empty cached map) dedups labels naming the same species in
// different forms ("Scientific_Common" from BirdNET vs scientific-only from a
// secondary model), which AllLabels can union together, while preserving input
// order and the first-seen label form.
func TestBuildAllSpeciesList_FallbackDedupesByScientificName(t *testing.T) {
	t.Parallel()

	got := buildAllSpeciesList(map[string]string{}, []string{
		"Turdus merula_Eurasian Blackbird", // BirdNET form
		"Turdus merula",                    // secondary-model scientific-only form, same species
		"Parus major_Great Tit",
	})

	require.Len(t, got, 2, "same species in two label forms must appear once")
	// Input order preserved: Turdus merula (first form wins), then Parus major.
	assert.Equal(t, "Turdus merula", got[0].ScientificName)
	assert.Equal(t, "Turdus merula_Eurasian Blackbird", got[0].Label,
		"first-seen label form is kept")
	assert.Equal(t, "Parus major", got[1].ScientificName)
}

// TestBuildAllSpeciesList_Empty verifies an empty label set yields an empty,
// non-nil list.
func TestBuildAllSpeciesList_Empty(t *testing.T) {
	t.Parallel()

	got := buildAllSpeciesList(map[string]string{}, nil)
	assert.Empty(t, got)
}
