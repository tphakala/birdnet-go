package openfauna

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureStream replays an in-memory translations CSV through the real decoder so
// the dictionary builder is exercised end to end (header handling, column order).
func fixtureStream(t *testing.T, csv string) func(translationRowFunc) error {
	t.Helper()
	return func(fn translationRowFunc) error {
		return decodeTranslationRows(strings.NewReader(csv), fn)
	}
}

// Column order mirrors the dataset schema: scientific_name,locale,common_name.
const dictFixtureCSV = `scientific_name,locale,common_name
Barbastella barbastellus,en,Western Barbastelle
Strix aluco,en,Tawny Owl
Turdus merula,en,Common Blackbird
Perca fluviatilis,en,European Perch
Barbastella barbastellus,fi,mopsilepakko
Strix aluco,fi,lehtopöllö
Myotis daubentonii,de,Wasserfledermaus
`

func TestBuildLocaleDictionaryFromStream_SparseLocaleEnglishFallback(t *testing.T) {
	t.Parallel()

	got, err := buildLocaleDictionaryFromStream(fixtureStream(t, dictFixtureCSV), "fi")
	require.NoError(t, err)

	want := map[string]string{
		"Barbastella barbastellus": "mopsilepakko",       // target locale wins
		"Strix aluco":              "lehtopöllö",          // target locale wins
		"Turdus merula":            "Common Blackbird",    // English fallback (no fi)
		"Perca fluviatilis":        "European Perch",      // English fallback (secondary-model/Perch label)
	}
	assert.Equal(t, want, got)

	// A label present only in a non-target, non-English locale (a scientific-only
	// label as far as fi+en are concerned) must NOT leak into the dictionary.
	_, present := got["Myotis daubentonii"]
	assert.False(t, present, "species without a fi or en name must be absent")
}

func TestBuildLocaleDictionaryFromStream_EnglishLocaleIsSelfContained(t *testing.T) {
	t.Parallel()

	got, err := buildLocaleDictionaryFromStream(fixtureStream(t, dictFixtureCSV), "en")
	require.NoError(t, err)

	want := map[string]string{
		"Barbastella barbastellus": "Western Barbastelle",
		"Strix aluco":              "Tawny Owl",
		"Turdus merula":            "Common Blackbird",
		"Perca fluviatilis":        "European Perch",
	}
	assert.Equal(t, want, got)
}

func TestBuildLocaleDictionaryFromStream_SkipsEmptyCommonNames(t *testing.T) {
	t.Parallel()

	csv := `scientific_name,locale,common_name
Strix aluco,en,Tawny Owl
Strix aluco,fi,
Turdus merula,fi,Mustarastas
`
	got, err := buildLocaleDictionaryFromStream(fixtureStream(t, csv), "fi")
	require.NoError(t, err)

	// Empty fi translation must not block the English fallback.
	assert.Equal(t, "Tawny Owl", got["Strix aluco"])
	assert.Equal(t, "Mustarastas", got["Turdus merula"])
}

// TestBuildLocaleDictionary_RealDataset exercises the public entry against the
// embedded dataset and confirms the locale-mapping reuse (fi resolves to fi, nb
// resolves to the "no" macrolanguage dataset).
func TestBuildLocaleDictionary_RealDataset(t *testing.T) {
	t.Parallel()

	fi, err := BuildLocaleDictionary("fi")
	require.NoError(t, err)
	assert.Greater(t, len(fi), 1000, "Finnish dictionary should cover the bulk of the dataset")
	assert.Equal(t, "mopsilepakko", fi["Barbastella barbastellus"],
		"the canonical bat example must resolve to its Finnish name")

	// nb must resolve through the nb->no alias to the Norwegian dataset, not English.
	nb, err := BuildLocaleDictionary("nb")
	require.NoError(t, err)
	assert.Greater(t, len(nb), 1000)
	en, err := BuildLocaleDictionary("en")
	require.NoError(t, err)
	// At least one species should carry a Norwegian name distinct from English,
	// proving nb did not silently fall back to the English dataset.
	differs := false
	for sci, nbName := range nb {
		if enName, ok := en[sci]; ok && enName != nbName {
			differs = true
			break
		}
	}
	assert.True(t, differs, "nb dictionary must contain Norwegian names, not English")
}
