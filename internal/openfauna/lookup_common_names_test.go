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

func TestLookupCommonNames_EnglishFallbackForUntranslatedLocale(t *testing.T) {
	t.Parallel()

	// A species translated in English but not in the target locale must still
	// resolve via the English fallback, mirroring Resolver.Resolve.
	got := LookupCommonNames([]string{"Turdus merula"}, "fi")
	require.Contains(t, got, "Turdus merula")
	assert.NotEmpty(t, got["Turdus merula"])
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
