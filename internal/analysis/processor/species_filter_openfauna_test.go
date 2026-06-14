// species_filter_openfauna_test.go: tests for resolveSpeciesFilter coverage of
// secondary-model (bat/Perch) species, both by scientific-only label and by
// localized common name reverse-resolved through OpenFauna.

package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveSpeciesFilter_SecondaryModelScientificLabel verifies that a
// scientific-only secondary-model label (a bat) in the label union matches a
// config entry given by scientific name. Before sourcing labels from the model
// union these never matched because the picker only saw primary BirdNET labels.
func TestResolveSpeciesFilter_SecondaryModelScientificLabel(t *testing.T) {
	t.Parallel()

	labels := []string{"Turdus merula_Eurasian Blackbird", "Barbastella barbastellus"}
	isAll, resolved := resolveSpeciesFilter(
		[]string{"Barbastella barbastellus"}, labels, nil, "", "test",
	)

	assert.False(t, isAll)
	assert.True(t, resolved["barbastella barbastellus"],
		"scientific-only bat label must match a scientific-name config entry")
}

// TestResolveSpeciesFilter_LocalizedCommonNameViaOpenFauna verifies that a
// localized common name for a secondary-model species reverse-resolves to its
// scientific name through OpenFauna, even though the model label is
// scientific-only (no embedded common name to key on). Uses the embedded
// OpenFauna dataset: Barbastella barbastellus,fi -> "mopsilepakko".
func TestResolveSpeciesFilter_LocalizedCommonNameViaOpenFauna(t *testing.T) {
	t.Parallel()

	labels := []string{"Barbastella barbastellus"}
	isAll, resolved := resolveSpeciesFilter(
		[]string{"mopsilepakko"}, labels, nil, "fi", "test",
	)

	assert.False(t, isAll)
	assert.True(t, resolved["barbastella barbastellus"],
		"localized bat common name must reverse-resolve to scientific via OpenFauna")
}

// TestResolveSpeciesFilter_ReverseHitNotInLabelsStaysUnresolved verifies that a
// localized common name OpenFauna reverse-resolves to a species absent from the
// loaded model labels is NOT resolved: filters must never match a species that no
// loaded model can emit, and the entry stays unresolved so it is still flagged.
func TestResolveSpeciesFilter_ReverseHitNotInLabelsStaysUnresolved(t *testing.T) {
	t.Parallel()

	// "mopsilepakko" reverse-resolves to "Barbastella barbastellus" via OpenFauna,
	// but that species is not in the loaded labels here (no bat model loaded).
	labels := []string{"Turdus merula_Eurasian Blackbird"}
	isAll, resolved := resolveSpeciesFilter(
		[]string{"mopsilepakko"}, labels, nil, "fi", "test",
	)

	assert.False(t, isAll)
	assert.Empty(t, resolved, "a reverse hit absent from the loaded labels must not resolve")
}

// TestResolveSpeciesFilter_UnknownEntryStaysUnresolved verifies that a genuinely
// unknown entry does not resolve to anything (and is not silently accepted).
func TestResolveSpeciesFilter_UnknownEntryStaysUnresolved(t *testing.T) {
	t.Parallel()

	isAll, resolved := resolveSpeciesFilter(
		[]string{"definitely-not-a-species-xyz"}, []string{"Turdus merula_Eurasian Blackbird"}, nil, "fi", "test",
	)

	assert.False(t, isAll)
	assert.Empty(t, resolved, "an unknown entry must not resolve to any species")
}
