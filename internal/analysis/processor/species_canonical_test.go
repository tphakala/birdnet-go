package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTaxonomyResolver is a test double for the taxonomyResolver interface that
// lets a test control (and observe) the canonical re-resolution call.
type fakeTaxonomyResolver struct {
	fn func(label string) (scientific, common, code string)
}

func (f fakeTaxonomyResolver) EnrichResultWithTaxonomy(label string) (scientific, common, code string) {
	return f.fn(label)
}

// TestCanonicalizeSpecies_AliasReResolvesCommonAndCode verifies that when a model
// emits a legacy scientific name, canonicalizeSpecies collapses it to the canonical
// name, preserves the legacy name as raw, and re-resolves the common name and
// taxonomy code for the canonical name (never pairing the canonical name with the
// legacy name's common/code).
func TestCanonicalizeSpecies_AliasReResolvesCommonAndCode(t *testing.T) {
	t.Parallel()

	resolver := fakeTaxonomyResolver{fn: func(label string) (string, string, string) {
		require.Equal(t, "Spilopelia senegalensis", label,
			"re-resolution must use the canonical scientific name")
		return "Spilopelia senegalensis", "Laughing Dove", "laudov1"
	}}

	sci, common, code, raw := canonicalizeSpecies(resolver,
		"Streptopelia senegalensis", "Legacy Common", "legacycode")

	assert.Equal(t, "Spilopelia senegalensis", sci, "scientific name should be canonical")
	assert.Equal(t, "Laughing Dove", common, "common name should be re-resolved for canonical")
	assert.Equal(t, "laudov1", code, "code should be re-resolved for canonical")
	assert.Equal(t, "Streptopelia senegalensis", raw, "raw should preserve the legacy name")
}

// TestCanonicalizeSpecies_NonAliasedUnchanged verifies that a non-aliased name
// passes through unchanged, returns an empty raw, and never triggers a (costly)
// re-resolution call.
func TestCanonicalizeSpecies_NonAliasedUnchanged(t *testing.T) {
	t.Parallel()

	called := false
	resolver := fakeTaxonomyResolver{fn: func(label string) (string, string, string) {
		called = true
		return "", "", ""
	}}

	sci, common, code, raw := canonicalizeSpecies(resolver,
		"Turdus merula", "Eurasian Blackbird", "eurbla1")

	assert.Equal(t, "Turdus merula", sci)
	assert.Equal(t, "Eurasian Blackbird", common)
	assert.Equal(t, "eurbla1", code)
	assert.Empty(t, raw, "non-aliased name must store an empty raw")
	assert.False(t, called, "resolver must not be called for a non-aliased name")
}
