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
	// The inaturalist link must flow through to the Links map keyed by "inaturalist".
	assert.NotEmpty(t, m.Links["inaturalist"].ID, "iNaturalist link must be present in Links map")
}

func TestBuildIndex_CaseInsensitiveMatching_Embedded(t *testing.T) {
	t.Parallel()

	// Callers may supply varying case/whitespace; matching is case-insensitive.
	ix, err := BuildIndex([]string{"  TURDUS MERULA  "}, "fi")
	require.NoError(t, err)

	name, ok := ix.CommonName("turdus merula")
	require.True(t, ok, "a case/space-varying index entry and query must still resolve")
	assert.NotEmpty(t, name)

	m, ok := ix.Meta("Turdus Merula")
	require.True(t, ok, "metadata lookup is case-insensitive too")
	assert.NotEmpty(t, m.Family)
}

func TestLookup_CaseInsensitive_Embedded(t *testing.T) {
	t.Parallel()

	name, ok := Lookup("  turdus MERULA  ", "fi")
	require.True(t, ok, "Lookup must match case-insensitively and trim whitespace")
	assert.NotEmpty(t, name)

	m, ok := LookupMeta("TURDUS MERULA")
	require.True(t, ok, "LookupMeta must match case-insensitively")
	assert.NotEmpty(t, m.Family)
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
	assert.NotEmpty(t, m.Links["inaturalist"].ID, "inaturalist link must be present in Links map")

	_, ok = LookupMeta("Definitely notaspecies")
	assert.False(t, ok)
}

func TestLookupMeta_MemoizedResultsAreConsistent(t *testing.T) {
	t.Parallel()

	// The embedded dataset is immutable, so a present name must return identical
	// metadata on the first (uncached) and second (memoized) lookup, and an absent
	// name must stay absent. This exercises the LookupMeta memo cache path.
	first, ok1 := LookupMeta("Turdus merula")
	second, ok2 := LookupMeta("turdus merula") // different casing → same normalized key
	require.True(t, ok1)
	require.True(t, ok2)
	assert.Equal(t, first, second, "memoized metadata must match the first lookup")

	_, missA := LookupMeta("Notarealbird memoized")
	_, missB := LookupMeta("Notarealbird memoized")
	assert.False(t, missA)
	assert.False(t, missB, "an absent name stays absent on the memoized lookup")
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

// TestDecodeMetadataRows_JSONLAndExpansionTolerant proves the metadata decoder
// parses JSONL correctly and silently ignores unknown top-level fields (e.g. a
// future "media" array) because json.Unmarshal drops fields not present in the
// target struct.
func TestDecodeMetadataRows_JSONLAndExpansionTolerant(t *testing.T) {
	t.Parallel()

	// Include an unknown top-level field ("media") to confirm it is ignored.
	input := `{"scientific_name":"Turdus merula","taxonomy":{"class":"Aves","order":"Passeriformes","family":"Turdidae","family_common":"Thrushes"},"links":{"wikipedia":{"id":"Q25334"},"inaturalist":{"id":"12345"}},"media":[{"url":"https://example.com/img.jpg"}]}
`

	got := map[string]Meta{}
	err := decodeMetadataRows(strings.NewReader(input), func(sci string, m Meta) error {
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
	assert.Equal(t, "Q25334", m.Links["wikipedia"].ID)
	assert.Equal(t, "12345", m.Links["inaturalist"].ID)
}

// TestDecodeMetadataRows_EmptyScientificNameSkipped confirms that JSONL records
// with an empty or absent "scientific_name" field are silently skipped rather than
// surfaced as errors (the old CSV decoder rejected a missing header column; the
// JSONL decoder treats empty scientific_name as a skip signal).
func TestDecodeMetadataRows_EmptyScientificNameSkipped(t *testing.T) {
	t.Parallel()

	// One record with no scientific_name, one valid record. Only the valid one
	// should reach the callback.
	input := `{"taxonomy":{"class":"Aves","order":"Passeriformes","family":"Turdidae","family_common":""}}
{"scientific_name":"Turdus merula","taxonomy":{"class":"Aves","order":"Passeriformes","family":"Turdidae","family_common":""},"links":{}}
`
	got := map[string]Meta{}
	err := decodeMetadataRows(strings.NewReader(input), func(sci string, m Meta) error {
		got[sci] = m
		return nil
	})
	require.NoError(t, err)
	require.Len(t, got, 1, "record without scientific_name must be skipped, not added")
	_, ok := got["Turdus merula"]
	assert.True(t, ok, "the valid record must still be decoded")
}

// TestDecodeMetadataRows_MissingOptionalLinksKey proves forward/backward
// compatibility: a JSONL record that omits the "family_common" taxonomy field or
// an entire links key still decodes without error, leaving the corresponding
// struct field or map entry at its zero value.
func TestDecodeMetadataRows_MissingOptionalLinksKey(t *testing.T) {
	t.Parallel()

	// family_common absent, inaturalist key absent from links.
	input := `{"scientific_name":"Turdus merula","taxonomy":{"class":"Aves","order":"Passeriformes","family":"Turdidae"},"links":{"wikipedia":{"id":"Q25334"}}}
`

	got := map[string]Meta{}
	err := decodeMetadataRows(strings.NewReader(input), func(sci string, m Meta) error {
		got[sci] = m
		return nil
	})
	require.NoError(t, err)

	m, ok := got["Turdus merula"]
	require.True(t, ok)
	assert.Equal(t, "Aves", m.Class)
	assert.Equal(t, "Turdidae", m.Family)
	assert.Equal(t, "Q25334", m.Links["wikipedia"].ID)
	assert.Empty(t, m.FamilyCommon, "absent family_common must decode to empty, not error")
	assert.Empty(t, m.Links["inaturalist"].ID, "absent links key must decode to zero LinkEntry, not error")
}

// TestDecodeMetadataRows_MalformedLineSkipped proves a single corrupt JSONL line
// is skipped rather than aborting the whole stream, so one bad record cannot wipe
// out every other species' taxonomy and links. The final line also omits a trailing
// newline to confirm it is still decoded.
func TestDecodeMetadataRows_MalformedLineSkipped(t *testing.T) {
	t.Parallel()

	input := `{"scientific_name":"Turdus merula","taxonomy":{"class":"Aves"}}
{ this is not valid json
{"scientific_name":"Erithacus rubecula","taxonomy":{"class":"Aves"}}`

	got := map[string]Meta{}
	err := decodeMetadataRows(strings.NewReader(input), func(sci string, m Meta) error {
		got[sci] = m
		return nil
	})
	require.NoError(t, err, "a malformed line must be skipped, not surfaced as an error")
	require.Len(t, got, 2, "valid records on either side of a malformed line must still decode")
	assert.Contains(t, got, "Turdus merula")
	assert.Contains(t, got, "Erithacus rubecula", "the final newline-less record must still decode")
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

// TestLookupScientificNames_ReverseResolvesLocalizedCommon_Embedded covers the reverse
// direction (localized common name -> scientific name) the openfauna package gained
// for the non-primary-model case: a user adding a species (a bat or mammal whose
// model label is scientific-only) by its localized common name must resolve back to
// the scientific name. All three entries are unique in the embedded fi data, and all
// three resolve from a single batched call.
func TestLookupScientificNames_ReverseResolvesLocalizedCommon_Embedded(t *testing.T) {
	t.Parallel()

	got := LookupScientificNames([]string{"Kettu", "Ilves", "mopsilepakko"}, "fi")
	assert.Contains(t, got["Kettu"], "Vulpes vulpes", "fox")
	assert.Contains(t, got["Ilves"], "Lynx lynx", "lynx")
	assert.Contains(t, got["mopsilepakko"], "Barbastella barbastellus", "bat")
}

func TestLookupScientificNames_CaseInsensitiveAndTrimmed_Embedded(t *testing.T) {
	t.Parallel()

	// Users supply varying case/whitespace; matching mirrors the forward Lookup. The
	// result is keyed by the caller's exact input string.
	got := LookupScientificNames([]string{"  kETTu  "}, "fi")
	assert.Contains(t, got["  kETTu  "], "Vulpes vulpes", "reverse lookup must trim and match case-insensitively")
}

func TestLookupScientificNames_Miss_OmitsName_Embedded(t *testing.T) {
	t.Parallel()

	got := LookupScientificNames([]string{"drone", "", "   "}, "fi")
	assert.Empty(t, got["drone"], "a non-fauna string must not reverse-resolve")
	assert.Empty(t, got[""], "empty input must not resolve")
	assert.NotContains(t, got, "drone", "an unmatched name must be absent from the result, not an empty entry")
}

func TestLookupScientificNames_HonorsLocale_Embedded(t *testing.T) {
	t.Parallel()

	// "Kettu" is the Finnish word for fox; it is meaningless as a German common name,
	// so a de lookup (with English fallback) must not resolve it to Vulpes vulpes.
	assert.Contains(t, LookupScientificNames([]string{"Kettu"}, "fi")["Kettu"], "Vulpes vulpes")
	assert.NotContains(t, LookupScientificNames([]string{"Kettu"}, "de")["Kettu"], "Vulpes vulpes",
		"the reverse lookup must honor the active locale")
}

func TestReverseResolveToScientificNames_LowerCasesPerEntry_Embedded(t *testing.T) {
	t.Parallel()

	got := ReverseResolveToScientificNames([]string{"mopsilepakko", "Kettu", "drone"}, "fi")
	// Scientific names are lower-cased and keyed by the caller's original entry string.
	assert.Contains(t, got["mopsilepakko"], "barbastella barbastellus", "bat, lower-cased")
	assert.Contains(t, got["Kettu"], "vulpes vulpes", "fox, lower-cased")
	// A non-fauna string must be absent from the result, not present as an empty entry.
	assert.NotContains(t, got, "drone", "an unmatched name must be absent from the result")
}

func TestReverseResolveToScientificNames_EmptyInput_Embedded(t *testing.T) {
	t.Parallel()

	got := ReverseResolveToScientificNames(nil, "fi")
	assert.NotNil(t, got, "must return an empty, non-nil map for empty input")
	assert.Empty(t, got)
}

func TestReverseResolveToScientificSet_FlattensAndLowerCases_Embedded(t *testing.T) {
	t.Parallel()

	got := ReverseResolveToScientificSet([]string{"mopsilepakko", "Kettu", "Ilves"}, "fi")
	// The per-entry results are flattened into one lower-cased membership set.
	assert.Contains(t, got, "barbastella barbastellus", "bat")
	assert.Contains(t, got, "vulpes vulpes", "fox")
	assert.Contains(t, got, "lynx lynx", "lynx")
}

func TestReverseResolveToScientificSet_EmptyInput_Embedded(t *testing.T) {
	t.Parallel()

	got := ReverseResolveToScientificSet(nil, "fi")
	assert.NotNil(t, got, "must return an empty, non-nil set for empty input")
	assert.Empty(t, got)
}

func TestReverseResolveToScientificNames_MultiResolutionAllLowerCased_Embedded(t *testing.T) {
	t.Parallel()

	// "Hairy Woodpecker" carries two scientific names in the dataset (a genus split),
	// so this exercises the per-element lower-casing loop on a multi-element slice:
	// every name must be lower-cased, not just the first.
	got := ReverseResolveToScientificNames([]string{"Hairy Woodpecker"}, "en")
	names := got["Hairy Woodpecker"]
	require.Len(t, names, 2, "this common name must resolve to two scientific names in the dataset")
	assert.Contains(t, names, "dryobates villosus")
	assert.Contains(t, names, "leuconotopicus villosus")
	for _, n := range names {
		assert.Equal(t, strings.ToLower(n), n, "every resolved scientific name must be lower-cased, not just the first")
	}
}

func TestReverseResolveToScientificSet_FlattensMultiResolutionEntry_Embedded(t *testing.T) {
	t.Parallel()

	// A single entry resolving to multiple scientific names must contribute ALL of them
	// to the flat set, not just the first, so the inner flatten loop is exercised.
	got := ReverseResolveToScientificSet([]string{"Hairy Woodpecker"}, "en")
	assert.Contains(t, got, "dryobates villosus")
	assert.Contains(t, got, "leuconotopicus villosus")
}

func TestDecodeMetadataJSONL(t *testing.T) {
	const sample = `{"scientific_name":"Aquila chrysaetos","taxonomy":{"class":"Aves","order":"Accipitriformes","family":"Accipitridae","family_common":"Hawks"},"links":{"inaturalist":{"id":"5074"},"wikipedia":{"id":"Q41181"}}}
{"scientific_name":"Turdus merula","taxonomy":{"class":"Aves","order":"Passeriformes","family":"Turdidae","family_common":""},"links":{"wikipedia":{"id":"Q25334","url":"https://en.wikipedia.org/wiki/Common_blackbird"}}}
`
	got := map[string]Meta{}
	err := decodeMetadataRows(strings.NewReader(sample), func(sci string, m Meta) error {
		got[sci] = m
		return nil
	})
	if err != nil {
		t.Fatalf("decodeMetadataRows: %v", err)
	}
	eagle, ok := got["Aquila chrysaetos"]
	if !ok {
		t.Fatal("missing Aquila chrysaetos")
	}
	if eagle.Family != "Accipitridae" || eagle.FamilyCommon != "Hawks" || eagle.Class != "Aves" {
		t.Fatalf("eagle taxonomy wrong: %+v", eagle)
	}
	if eagle.Links["inaturalist"].ID != "5074" || eagle.Links["wikipedia"].ID != "Q41181" {
		t.Fatalf("eagle links wrong: %+v", eagle.Links)
	}
	blackbird := got["Turdus merula"]
	if blackbird.Links["wikipedia"].URL != "https://en.wikipedia.org/wiki/Common_blackbird" {
		t.Fatalf("blackbird wikipedia url override missing: %+v", blackbird.Links["wikipedia"])
	}
}
