package openfauna

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupCommonNames_ResolvesSecondaryModelScientificLabel(t *testing.T) {
	t.Parallel()

	// Barbastella barbastellus is a bat: scientific-only label, localized in Finnish.
	got := LookupCommonNames([]string{"Barbastella barbastellus"}, "fi")

	require.Contains(t, got, "Barbastella barbastellus")
	assert.Equal(t, "mopsilepakko", got["Barbastella barbastellus"])
}

func TestLookupCommonNames_EmptyInputReturnsEmptyMap(t *testing.T) {
	t.Parallel()

	got := LookupCommonNames(nil, "fi")
	assert.Empty(t, got)
}

func TestLookupCommonNames_FinnishTranslationForKnownSpecies(t *testing.T) {
	t.Parallel()

	// Turdus merula has a genuine Finnish translation; this test pins the exact value.
	got := LookupCommonNames([]string{"Turdus merula"}, "fi")
	require.Contains(t, got, "Turdus merula")
	assert.Equal(t, "mustarastas", got["Turdus merula"])
}

func TestLookupCommonNames_EnglishFallbackForUntranslatedLocale(t *testing.T) {
	t.Parallel()

	// Puffinus newelli has an English common name but no Finnish translation.
	// The function must fall back to the English name rather than returning empty.
	// This exercises the eff != localeFallback && loc == localeFallback branch in
	// lookupCommonNamesEffective.
	en := LookupCommonNames([]string{"Puffinus newelli"}, "en")
	require.Contains(t, en, "Puffinus newelli", "English name must exist for this species")
	require.NotEmpty(t, en["Puffinus newelli"], "English name must be non-empty")

	got := LookupCommonNames([]string{"Puffinus newelli"}, "fi")
	require.Contains(t, got, "Puffinus newelli")
	assert.Equal(t, en["Puffinus newelli"], got["Puffinus newelli"],
		"species with no Finnish translation must resolve to its English common name")
}

func TestResolver_ResolveLocalizedBatch_UsesBuiltLocale(t *testing.T) {
	t.Parallel()

	r := NewResolver()
	require.NoError(t, r.Rebuild([]string{"Turdus merula"}, "fi"))

	// A scientific-only secondary-model species NOT in the working set must still
	// resolve through the cold-path batch (one dataset pass at the built locale).
	got := r.ResolveLocalizedBatch([]string{"Barbastella barbastellus"})
	require.Contains(t, got, "Barbastella barbastellus")
	assert.Equal(t, "mopsilepakko", got["Barbastella barbastellus"])
}

func TestResolver_ResolveLocalizedBatch_NilResolverSafe(t *testing.T) {
	t.Parallel()

	var r *Resolver
	assert.Empty(t, r.ResolveLocalizedBatch([]string{"Turdus merula"}))
}
