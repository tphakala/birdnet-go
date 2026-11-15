package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
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
// lets a test control (and observe) the canonical re-resolution call and the
// taxonomy code lookup. A nil codeFn reports every label as not found.
type fakeTaxonomyResolver struct {
	fn     func(label string) (scientific, common, code string)
	codeFn func(label string) (code string, found bool)
}

func (f fakeTaxonomyResolver) EnrichResultWithTaxonomy(label string) (scientific, common, code string) {
	return f.fn(label)
}

func (f fakeTaxonomyResolver) GetSpeciesCode(label string) (code string, found bool) {
	if f.codeFn == nil {
		return "", false
	}
	return f.codeFn(label)
}

// TestCanonicalizeSpecies_AliasReResolvesCommonKeepsCode verifies that when a model
// emits a legacy scientific name, canonicalizeSpecies collapses it to the canonical
// name, preserves the legacy name as raw, re-resolves the common name for the
// canonical name, and KEEPS the original eBird code, because the frozen eBird
// taxonomy holds the legacy name and its code is the taxon's real code.
func TestCanonicalizeSpecies_AliasReResolvesCommonKeepsCode(t *testing.T) {
	t.Parallel()

	resolver := fakeTaxonomyResolver{
		fn: func(label string) (string, string, string) {
			require.Equal(t, testSciCanonical, label,
				"re-resolution must use the canonical scientific name")
			// The resolver's code is intentionally ignored; the original code is kept.
			return testSciCanonical, testCommonDove, testCodeDove
		},
		codeFn: func(label string) (string, bool) { return testCodeLegacy, label == testSciLegacy },
	}

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

	resolver := fakeTaxonomyResolver{
		fn: func(string) (string, string, string) {
			return testSciCanonical, "", testCodeDove // canonical name resolves no common name
		},
		codeFn: func(label string) (string, bool) { return testCodeLegacy, label == testSciLegacy },
	}

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
	resolver := fakeTaxonomyResolver{
		fn: func(label string) (string, string, string) {
			called = true
			return "", "", ""
		},
		codeFn: func(label string) (string, bool) {
			called = true
			return "", false
		},
	}

	sci, common, code, raw := canonicalizeSpecies(resolver,
		"Turdus merula", "Eurasian Blackbird", "eurbla1")

	assert.Equal(t, "Turdus merula", sci)
	assert.Equal(t, "Eurasian Blackbird", common)
	assert.Equal(t, "eurbla1", code)
	assert.Empty(t, raw, "non-aliased name must store an empty raw")
	assert.False(t, called, "resolver must not be called for a non-aliased name")
}

// TestCanonicalizeSpecies_AliasCode pins which eBird code an aliased detection
// carries. The raw name wins when the taxonomy holds it (a legacy name present in
// the eBird snapshot); the canonical name's code is used only when the raw name is
// absent (a newer model label) and the canonical name is present; when neither is
// present the incoming placeholder is kept.
func TestCanonicalizeSpecies_AliasCode(t *testing.T) {
	t.Parallel()

	const placeholder = "t-placeholder"
	tests := []struct {
		name      string
		raw       string
		canonical string
		known     map[string]string // taxonomy: scientific name -> real code
		incoming  string
		want      string
	}{
		{
			name: "legacy key in snapshot keeps raw code",
			raw:  "Streptopelia senegalensis", canonical: "Spilopelia senegalensis",
			known:    map[string]string{"Streptopelia senegalensis": "laudov1"},
			incoming: "laudov1", want: "laudov1",
		},
		{
			name: "new model label takes canonical code",
			raw:  "Nannus troglodytes", canonical: "Troglodytes troglodytes",
			known:    map[string]string{"Troglodytes troglodytes": "winwre4"},
			incoming: placeholder, want: "winwre4",
		},
		{
			name: "neither in taxonomy keeps placeholder",
			raw:  "Eptesicus nilssonii", canonical: "Cnephaeus nilssonii",
			known:    map[string]string{},
			incoming: placeholder, want: placeholder,
		},
		{
			name: "both in taxonomy keeps raw code",
			raw:  "Dicrurus divaricatus", canonical: "Dicrurus adsimilis",
			known:    map[string]string{"Dicrurus divaricatus": "fotdro4", "Dicrurus adsimilis": "fotdro5"},
			incoming: "fotdro4", want: "fotdro4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolver := fakeTaxonomyResolver{
				fn: func(string) (string, string, string) { return tt.canonical, "Common", "" },
				codeFn: func(label string) (string, bool) {
					code, ok := tt.known[label]
					return code, ok
				},
			}
			sci, _, code, raw := canonicalizeSpecies(resolver, tt.raw, "Common", tt.incoming)
			assert.Equal(t, tt.canonical, sci)
			assert.Equal(t, tt.raw, raw)
			assert.Equal(t, tt.want, code)
		})
	}
}

// TestCanonicalizeSpecies_EmbeddedSnapshot runs the alias code rule against the
// real embedded eBird snapshot and alias map: renamed model labels the snapshot does
// not hold get the canonical taxon's real code, a legacy key the snapshot holds
// keeps its own.
func TestCanonicalizeSpecies_EmbeddedSnapshot(t *testing.T) {
	t.Parallel()

	taxonomy, index, err := classifier.LoadTaxonomyData("")
	require.NoError(t, err)
	// The same lookup the orchestrator runs, so the real found semantics apply.
	lookup := func(label string) (string, bool) {
		return classifier.GetSpeciesCodeFromName(taxonomy, index, label)
	}
	resolver := fakeTaxonomyResolver{
		fn:     func(label string) (string, string, string) { return label, "Common", "" },
		codeFn: lookup,
	}

	tests := []struct {
		raw, canonical, want string
	}{
		{"Nannus troglodytes", "Troglodytes troglodytes", "winwre4"},
		{"Schoeniclus schoeniclus", "Emberiza schoeniclus", "reebun"},
		{testSciLegacy, testSciCanonical, testCodeDove},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()
			incoming, _ := lookup(tt.raw)
			sci, _, code, rawName := canonicalizeSpecies(resolver, tt.raw, "Common", incoming)
			assert.Equal(t, tt.canonical, sci)
			assert.Equal(t, tt.raw, rawName)
			assert.Equal(t, tt.want, code)
		})
	}
}
