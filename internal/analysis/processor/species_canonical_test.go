package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shared fixtures for the canonical-name tests: the Laughing Dove is a real
// taxonomic alias (BirdNET v2.4 "Streptopelia senegalensis" -> eBird "Spilopelia
// senegalensis") present in the embedded OpenFauna alias map.
const (
	testSciCanonical = "Spilopelia senegalensis"
	testSciLegacy    = "Streptopelia senegalensis"
	testCommonDove   = "Laughing Dove"
	testCodeDove     = "laudov1"
	testCodeLegacy   = "legacycode"
)

// fakeTaxonomyResolver is a test double for the taxonomyResolver interface that
// lets a test control (and observe) the canonical re-resolution call.
type fakeTaxonomyResolver struct {
	fn func(label string) (scientific, common, code string)
}

func (f fakeTaxonomyResolver) EnrichResultWithTaxonomy(label string) (scientific, common, code string) {
	return f.fn(label)
}

// TestCanonicalizeSpecies_AliasReResolvesCommonKeepsCode verifies that when a model
// emits a legacy scientific name, canonicalizeSpecies collapses it to the canonical
// name, preserves the legacy name as raw, re-resolves the common name for the
// canonical name, and KEEPS the original eBird code. The code is stable across a pure
// rename and the frozen eBird taxonomy holds only the legacy name, so re-resolving it
// would degrade the real code to a placeholder.
func TestCanonicalizeSpecies_AliasReResolvesCommonKeepsCode(t *testing.T) {
	t.Parallel()

	resolver := fakeTaxonomyResolver{fn: func(label string) (string, string, string) {
		require.Equal(t, testSciCanonical, label,
			"re-resolution must use the canonical scientific name")
		// The resolver's code is intentionally ignored; the original code is kept.
		return testSciCanonical, testCommonDove, testCodeDove
	}}

	sci, common, code, raw := canonicalizeSpecies(resolver,
		testSciLegacy, "Legacy Common", testCodeLegacy)

	assert.Equal(t, testSciCanonical, sci, "scientific name should be canonical")
	assert.Equal(t, testCommonDove, common, "common name should be re-resolved for canonical")
	assert.Equal(t, testCodeLegacy, code, "original eBird code should be preserved (stable across the rename)")
	assert.Equal(t, testSciLegacy, raw, "raw should preserve the legacy name")
}

// TestCanonicalizeSpecies_AliasKeepsOriginalCommonWhenCanonicalUnresolved verifies
// that when the canonical name has no resolvable common name, canonicalizeSpecies
// keeps the model's original common name instead of emitting an empty one. An empty
// common name would otherwise be replaced downstream by the bare scientific name (it
// never drops the detection: parseAndValidateSpecies applies that fallback right after
// this call), so keeping the real common name is purely a fidelity improvement.
func TestCanonicalizeSpecies_AliasKeepsOriginalCommonWhenCanonicalUnresolved(t *testing.T) {
	t.Parallel()

	resolver := fakeTaxonomyResolver{fn: func(string) (string, string, string) {
		return testSciCanonical, "", testCodeDove // canonical name resolves no common name
	}}

	sci, common, code, raw := canonicalizeSpecies(resolver,
		testSciLegacy, testCommonDove, testCodeLegacy)

	assert.Equal(t, testSciCanonical, sci)
	assert.Equal(t, testCommonDove, common,
		"should keep the original common name when the canonical name resolves none")
	assert.Equal(t, testCodeLegacy, code, "original eBird code should be preserved")
	assert.Equal(t, testSciLegacy, raw)
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
