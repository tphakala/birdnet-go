package speciesindex

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

func TestBuild_AmbiguousCommonNameDropped(t *testing.T) {
	t.Parallel()

	s := Build([]string{
		"Strix aluco_Owl",
		"Bubo bubo_Owl",
		"Parus major_Great Tit",
	}, nil, "")

	// sciToCommon keeps both species; scientific names are always unique.
	assert.Equal(t, "Owl", s.SciToCommon["Strix aluco"])
	assert.Equal(t, "Owl", s.SciToCommon["Bubo bubo"])

	// The ambiguous folded common name is absent from the reverse map.
	_, ok := s.CommonToSci["owl"]
	assert.False(t, ok, "ambiguous common-name key should be removed")

	// Non-ambiguous names remain.
	assert.Equal(t, "Parus major", s.CommonToSci["great tit"])
}

func TestBuild_ThirdDuplicateDoesNotRestoreKey(t *testing.T) {
	t.Parallel()

	s := Build([]string{
		"Strix aluco_Owl",
		"Bubo bubo_Owl",
		"Tyto alba_Owl",
	}, nil, "")

	_, ok := s.CommonToSci["owl"]
	assert.False(t, ok, "a third duplicate must not restore an ambiguous key")
}

func TestBuild_ScientificOnlyLabelSearchableViaResolver(t *testing.T) {
	t.Parallel()

	// A scientific-only label (no "_") becomes searchable when the resolver
	// supplies a common name.
	s := Build([]string{"Myotis myotis"},
		localResolver{m: map[string]string{"Myotis myotis": "Mustakorvayokko"}}, "")
	assert.Equal(t, "Mustakorvayokko", s.SciToCommon["Myotis myotis"])
	assert.Equal(t, "Myotis myotis", s.CommonToSci["mustakorvayokko"])

	// Without a resolver, a scientific-only label has no common name and is
	// dropped from the name maps (but is still tracked in the memo maps).
	bare := Build([]string{"Myotis myotis"}, nil, "")
	_, ok := bare.SciToCommon["Myotis myotis"]
	assert.False(t, ok)
	assert.Contains(t, bare.Labels, "Myotis myotis")
	assert.Equal(t, "Myotis myotis", bare.LabelBySci["Myotis myotis"])
}

func TestBuild_MalformedLabelsSkipped(t *testing.T) {
	t.Parallel()

	s := Build([]string{
		"Strix aluco_Tawny Owl",
		"_MissingScientific",
		"MissingCommon_",
		"NoSeparatorAtAll",
		"",
		"   _   ",
	}, nil, "")

	// Only the one well-formed label survives into the name maps.
	assert.Len(t, s.SciToCommon, 1)
	assert.Len(t, s.CommonToSci, 1)
	assert.Equal(t, "Tawny Owl", s.SciToCommon["Strix aluco"])
	assert.Equal(t, "Strix aluco", s.CommonToSci["tawny owl"])
}

func TestBuild_MemoMaps(t *testing.T) {
	t.Parallel()

	legacy, canonical := firstAliasPair(t)
	labels := []string{
		legacy + "_Legacy Common",
		canonical + "_Canonical Common",
		"Turdus merula_Blackbird",
		"Turdus merula_Blackbird", // exact duplicate
	}
	s := Build(labels, nil, "")

	// Labels is first-occurrence-deduplicated.
	assert.Equal(t, []string{
		legacy + "_Legacy Common",
		canonical + "_Canonical Common",
		"Turdus merula_Blackbird",
	}, s.Labels)

	// CanonicalByLabel covers every (deduplicated) label.
	assert.Len(t, s.CanonicalByLabel, len(s.Labels))
	for _, l := range s.Labels {
		_, ok := s.CanonicalByLabel[l]
		assert.Truef(t, ok, "CanonicalByLabel missing %q", l)
	}

	// The alias pair groups under one canonical key.
	ck := strings.ToLower(openfauna.CanonicalName(legacy))
	assert.Equal(t, ck, strings.ToLower(openfauna.CanonicalName(canonical)),
		"legacy and canonical must share a canonical key")
	assert.ElementsMatch(t,
		[]string{legacy + "_Legacy Common", canonical + "_Canonical Common"},
		s.LabelsByCanonical[ck])

	// LabelBySci maps each scientific name to its full label.
	assert.Equal(t, legacy+"_Legacy Common", s.LabelBySci[legacy])
	assert.Equal(t, canonical+"_Canonical Common", s.LabelBySci[canonical])
	assert.Equal(t, "Turdus merula_Blackbird", s.LabelBySci["Turdus merula"])
}

func TestSnapshot_ResolveLabel_ExactBeforeAlias(t *testing.T) {
	t.Parallel()

	legacy, canonical := firstAliasPair(t)

	// Both the legacy and canonical names are present, so their shared canonical
	// key is ambiguous (two candidates).
	s := Build([]string{
		legacy + "_Legacy Common",
		canonical + "_Canonical Common",
	}, nil, "")

	// Exact scientific-name hit wins even though the canonical group is ambiguous.
	label, common, ok := s.ResolveLabel(legacy)
	require.True(t, ok)
	assert.Equal(t, legacy+"_Legacy Common", label)
	assert.Equal(t, "Legacy Common", common)

	// A non-exact query whose canonical key is ambiguous returns ok=false rather
	// than picking arbitrarily. An upper-cased legacy name is not an exact
	// LabelBySci key, but the alias map lookup is case-insensitive so it
	// canonicalizes to the same (ambiguous) key.
	nonExact := strings.ToUpper(legacy)
	require.NotEqual(t, legacy, nonExact, "need a non-exact variant of the legacy name")
	require.NotEqual(t, canonical, nonExact)
	_, _, ok = s.ResolveLabel(nonExact)
	assert.False(t, ok, "ambiguous alias must not resolve")

	// Alias path with a single candidate resolves to that label.
	single := Build([]string{canonical + "_Canonical Common"}, nil, "")
	label, common, ok = single.ResolveLabel(legacy)
	require.True(t, ok)
	assert.Equal(t, canonical+"_Canonical Common", label)
	assert.Equal(t, "Canonical Common", common)

	// An empty query never resolves.
	_, _, ok = s.ResolveLabel("   ")
	assert.False(t, ok)
}

func TestSnapshot_CanonicalKey_MemoHitAndMiss(t *testing.T) {
	t.Parallel()

	s := Build([]string{"Turdus merula_Blackbird"}, nil, "")

	// Memo hit for a built label.
	assert.Equal(t, s.CanonicalByLabel["Turdus merula_Blackbird"],
		s.CanonicalKey("Turdus merula_Blackbird"))

	// Miss: computed on the fly, equal to the direct expression.
	want := strings.ToLower(openfauna.CanonicalName("Passer domesticus"))
	assert.Equal(t, want, s.CanonicalKey("Passer domesticus_House Sparrow"))
}

func TestSnapshot_Search_SortedAndCaseFolded(t *testing.T) {
	t.Parallel()

	s := Build([]string{
		"Strix aluco_Tawny Owl",
		"Bubo bubo_Eurasian Eagle-Owl",
		"Parus major_Great Tit",
	}, nil, "")

	// "owl" (already folded) matches both owl species, returned sorted.
	got := s.Search(Fold("Owl"))
	assert.Equal(t, []string{"Bubo bubo", "Strix aluco"}, got)

	// A non-matching query returns no results; an empty query returns nil.
	assert.Empty(t, s.Search(Fold("penguin")))
	assert.Nil(t, s.Search(""))
}

func TestEmpty_NonNilMaps(t *testing.T) {
	t.Parallel()

	e := Empty()
	require.NotNil(t, e)
	assert.NotNil(t, e.SciToCommon)
	assert.NotNil(t, e.SciToCommonFolded)
	assert.NotNil(t, e.CommonToSci)
	assert.NotNil(t, e.CanonicalByLabel)
	assert.NotNil(t, e.LabelsByCanonical)
	assert.NotNil(t, e.LabelBySci)
	assert.NotNil(t, e.Labels)
	assert.Empty(t, e.SciToCommon)
}
