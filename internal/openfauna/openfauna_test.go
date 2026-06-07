package openfauna

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildIndex_FiltersByLocaleAndSet_Embedded(t *testing.T) {
	t.Parallel()

	ix, err := BuildIndex([]string{"Turdus merula", "Barbastella barbastellus"}, "fi")
	require.NoError(t, err)
	assert.Equal(t, "fi", ix.Locale())

	// A bird and a bat both resolve to Finnish names (openfauna covers both).
	bird, ok := ix.CommonName("Turdus merula")
	require.True(t, ok)
	assert.NotEmpty(t, bird)
	assert.NotEqual(t, "Turdus merula", bird)

	bat, ok := ix.CommonName("Barbastella barbastellus")
	require.True(t, ok)
	assert.NotEmpty(t, bat)

	// Species NOT in the requested set must be absent (sparse).
	_, ok = ix.CommonName("Erithacus rubecula")
	assert.False(t, ok, "sparse index must not contain a species it did not request")

	// Prove the locale filter is actually applied (not just "some name resolved"):
	// a de index must yield a different common name than the fi index. This stays
	// drift-tolerant by comparing two locales rather than pinning a literal string.
	deIx, err := BuildIndex([]string{"Turdus merula"}, "de")
	require.NoError(t, err)
	deBird, ok := deIx.CommonName("Turdus merula")
	require.True(t, ok)
	assert.NotEqual(t, bird, deBird, "fi and de must resolve to different names (locale filter)")
}

func TestBuildIndex_UnknownLocale_EmptyButNoError(t *testing.T) {
	t.Parallel()

	ix, err := BuildIndex([]string{"Turdus merula"}, "zzz")
	require.NoError(t, err)
	_, ok := ix.CommonName("Turdus merula")
	assert.False(t, ok, "unknown locale must yield no translations")
}

func TestBuildIndex_EmptyInput_NoError(t *testing.T) {
	t.Parallel()

	ix, err := BuildIndex(nil, "fi")
	require.NoError(t, err)
	assert.Equal(t, "fi", ix.Locale())
	_, ok := ix.CommonName("Turdus merula")
	assert.False(t, ok)
}

func TestBuildIndex_AttachesMetadata_Embedded(t *testing.T) {
	t.Parallel()

	ix, err := BuildIndex([]string{"Turdus merula"}, "en")
	require.NoError(t, err)

	m, ok := ix.Meta("Turdus merula")
	require.True(t, ok, "expected metadata for Turdus merula")
	assert.NotEmpty(t, m.Class)
	assert.NotEmpty(t, m.Family)
	// The 7th metadata column (inaturalist_url) must flow through to the right
	// field. Contains("inaturalist") weakly pins the column identity (catching a
	// wikipedia/inaturalist column swap) while staying tolerant of URL drift.
	assert.Contains(t, m.INaturalistURL, "inaturalist", "iNaturalist column must map to INaturalistURL")
}

func TestLookup_SingleSpecies_Embedded(t *testing.T) {
	t.Parallel()

	deName, ok := Lookup("Turdus merula", "de")
	require.True(t, ok)
	assert.NotEmpty(t, deName)

	// Lookup must honor its locale argument: fi resolves to a different name.
	fiName, ok := Lookup("Turdus merula", "fi")
	require.True(t, ok)
	assert.NotEqual(t, deName, fiName, "Lookup must honor the locale argument")

	_, ok = Lookup("Definitely notaspecies", "de")
	assert.False(t, ok, "nonexistent species must not resolve")
}

func TestLookupMeta_SingleSpecies_Embedded(t *testing.T) {
	t.Parallel()

	m, ok := LookupMeta("Turdus merula")
	require.True(t, ok)
	assert.NotEmpty(t, m.Family)
	assert.NotEmpty(t, m.INaturalistURL)

	_, ok = LookupMeta("Definitely notaspecies")
	assert.False(t, ok)
}

func TestLocales_Embedded(t *testing.T) {
	t.Parallel()

	ls := Locales()
	// The dataset ships 40+ locales; 30 is a deliberately loose floor so the test
	// tolerates upstream locale removals without becoming brittle.
	assert.GreaterOrEqual(t, len(ls), 30, "expected at least 30 locales")
	for _, code := range []string{"en", "fi", "de", "en_uk", "zh_cn"} {
		assert.Contains(t, ls, code)
	}
}

func TestDataVersion_Embedded(t *testing.T) {
	t.Parallel()

	v := DataVersion()
	assert.NotEmpty(t, v)
	assert.Contains(t, v, "openfauna@", "data version should record the openfauna source commit")
}

// TestDecodeTranslationRows_NoReuseRecordAliasing pins the decoder's contract:
// values yielded to the callback may be retained (stored in a map) and must not
// be aliased to a buffer that later rows overwrite. With encoding/csv this holds
// because each row's fields are a fresh allocation; the test exists to catch a
// future refactor that decoded into a manually reused buffer and broke the
// contract the production code (BuildIndex storing fields into maps) depends on.
func TestDecodeTranslationRows_NoReuseRecordAliasing(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	b.WriteString("scientific_name,locale,common_name\n")
	b.WriteString("First species,en,FirstCommonName\n")
	for i := range 200 {
		fmt.Fprintf(&b, "Other species %d,en,OtherCommonNameLongerToForceOverwrite%d\n", i, i)
	}

	stored := map[string]string{}
	err := decodeTranslationRows(strings.NewReader(b.String()), func(sci, loc, common string) error {
		stored[sci] = common
		return nil
	})
	require.NoError(t, err)
	require.Len(t, stored, 201, "every distinct row must be retained, not collapsed by aliasing")
	assert.Equal(t, "FirstCommonName", stored["First species"],
		"value stored from the first row must survive reading 200 later rows")
	assert.Equal(t, "OtherCommonNameLongerToForceOverwrite199", stored["Other species 199"])
}

func TestNilIndex_MethodsDoNotPanic(t *testing.T) {
	t.Parallel()

	var ix *Index // e.g. a caller that ignored a BuildIndex error
	name, ok := ix.CommonName("Turdus merula")
	assert.False(t, ok)
	assert.Empty(t, name)

	m, ok := ix.Meta("Turdus merula")
	assert.False(t, ok)
	assert.Empty(t, m.Family)

	assert.Empty(t, ix.Locale())
}

// TestDecodeMetadataRows_HeaderMappedAndExpansionTolerant proves the metadata
// decoder maps columns by header name: it tolerates a different column order and
// ignores unknown future columns (here a hypothetical thumbnail_url), which is the
// whole point of header mapping for an expanding schema.
func TestDecodeMetadataRows_HeaderMappedAndExpansionTolerant(t *testing.T) {
	t.Parallel()

	csv := "order,scientific_name,family,class,wikipedia_url,inaturalist_url,family_common,thumbnail_url\n" +
		"Passeriformes,Turdus merula,Turdidae,Aves,https://w/x,https://i/1,Thrushes,https://t/x\n"

	got := map[string]Meta{}
	err := decodeMetadataRows(strings.NewReader(csv), func(sci string, m Meta) error {
		got[sci] = m
		return nil
	})
	require.NoError(t, err)

	m, ok := got["Turdus merula"]
	require.True(t, ok)
	assert.Equal(t, "Aves", m.Class)
	assert.Equal(t, "Passeriformes", m.Order)
	assert.Equal(t, "Turdidae", m.Family)
	assert.Equal(t, "Thrushes", m.FamilyCommon)
	assert.Equal(t, "https://w/x", m.WikipediaURL)
	assert.Equal(t, "https://i/1", m.INaturalistURL)
}

func TestDecodeMetadataRows_MissingScientificNameColumn(t *testing.T) {
	t.Parallel()

	csv := "class,order\nAves,Passeriformes\n"
	err := decodeMetadataRows(strings.NewReader(csv), func(string, Meta) error { return nil })
	require.Error(t, err, "missing scientific_name column must be an error")
}

// TestDecodeMetadataRows_MissingOptionalColumn proves backward compatibility the
// other direction: a header that omits an optional known column (family_common)
// still decodes, leaving that field empty rather than erroring or misaligning.
func TestDecodeMetadataRows_MissingOptionalColumn(t *testing.T) {
	t.Parallel()

	csv := "scientific_name,class,order,family,wikipedia_url,inaturalist_url\n" +
		"Turdus merula,Aves,Passeriformes,Turdidae,https://w/x,https://i/1\n"

	got := map[string]Meta{}
	err := decodeMetadataRows(strings.NewReader(csv), func(sci string, m Meta) error {
		got[sci] = m
		return nil
	})
	require.NoError(t, err)

	m, ok := got["Turdus merula"]
	require.True(t, ok)
	assert.Equal(t, "Aves", m.Class)
	assert.Equal(t, "Turdidae", m.Family)
	assert.Equal(t, "https://i/1", m.INaturalistURL)
	assert.Empty(t, m.FamilyCommon, "absent optional column must decode to empty, not misalign")
}

// TestDecodeTranslationRows_FiltersSynthetic exercises the per-row callback over
// an in-memory translations CSV without touching the embedded data.
func TestDecodeTranslationRows_FiltersSynthetic(t *testing.T) {
	t.Parallel()

	csv := "scientific_name,locale,common_name\n" +
		"Turdus merula,fi,mustarastas\n" +
		"Turdus merula,de,Amsel\n" +
		"Erithacus rubecula,fi,punarinta\n"

	got := map[string]string{}
	err := decodeTranslationRows(strings.NewReader(csv), func(sci, loc, common string) error {
		if loc == "fi" {
			got[sci] = common
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Turdus merula": "mustarastas", "Erithacus rubecula": "punarinta"}, got)
}
