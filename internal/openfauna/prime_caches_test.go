package openfauna

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The memos PrimeCaches writes are package-global and append-only, so these tests use
// species that no other test in the package primes, and none of them run in parallel
// with each other where they share a name.

// TestPrimeCaches_MatchesPerNameLookups pins that priming a batch leaves the memos in
// the same state the one-at-a-time path would have: the whole point of PrimeCaches is
// to be a cheaper route to an identical result, so any divergence here is a bug that
// would surface as the guide showing different data depending on whether the species
// arrived via a warm, a similar-species fan-out, or a direct lookup.
func TestPrimeCaches_MatchesPerNameLookups(t *testing.T) {
	// Not parallel: writes the package-global memos.
	const locale = "fi"
	present := []string{"Turdus merula", "Parus major"}
	absent := "Notarealbird primecaches"

	PrimeCaches(append(slices.Clone(present), absent), locale)

	for _, sci := range present {
		name, found := LookupCommonName(sci, locale)
		require.True(t, found, "primed species %q must resolve a common name", sci)
		require.NotEmpty(t, name)
		assert.NotEqual(t, sci, name, "the common name must not be the scientific name")

		meta, ok := LookupMeta(sci)
		require.True(t, ok, "primed species %q must resolve metadata", sci)
		assert.NotEmpty(t, meta.Family, "primed metadata must carry taxonomy")
		assert.NotEmpty(t, meta.Links, "primed metadata must carry the links map")
	}

	// An absent name is memoized as absent, not left unprimed: that is what stops it
	// from re-scanning both blobs on every later request for the same name.
	_, cachedCommon := commonNameCache.Load(mapLocale(locale) + "\x00" + normalizeName(absent))
	assert.True(t, cachedCommon, "an absent name must be memoized as absent (common name)")
	_, cachedMeta := metaCache.Load(normalizeName(absent))
	assert.True(t, cachedMeta, "an absent name must be memoized as absent (metadata)")

	nameA, foundA := LookupCommonName(absent, locale)
	assert.False(t, foundA)
	assert.Empty(t, nameA)
	_, okA := LookupMeta(absent)
	assert.False(t, okA)
}

// TestPrimeCaches_LinksAreNotSharedAcrossCallers pins that a primed Meta hands each
// caller its own Links map. Priming writes the memo directly rather than going through
// LookupMeta, so it has to honour the same contract: the memoized map is published to
// every concurrent reader, and returning it unguarded is the "concurrent map read and
// map write" fatal that Meta.clone exists to prevent.
func TestPrimeCaches_LinksAreNotSharedAcrossCallers(t *testing.T) {
	// Not parallel: writes the package-global memos.
	const sci = "Erithacus rubecula"
	PrimeCaches([]string{sci}, "en")

	first, ok := LookupMeta(sci)
	require.True(t, ok)
	require.NotEmpty(t, first.Links)

	// Mutating one caller's copy must not reach the next caller's.
	clear(first.Links)
	second, ok := LookupMeta(sci)
	require.True(t, ok)
	assert.NotEmpty(t, second.Links, "a primed Meta must not share its Links map between callers")
}

// TestUnprimedCommonNames_SkipsMemoizedBlankAndDuplicate pins the filter that makes
// PrimeCaches safe to call on the request path: a batch whose names are all memoized
// must produce no work at all, because the caller (the similar-species fan-out) runs
// it on every request and a non-empty work set means a blob decompress + scan.
func TestUnprimedCommonNames_SkipsMemoizedBlankAndDuplicate(t *testing.T) {
	// Not parallel: reads and writes the package-global memo.
	const eff = "fi"
	memoized := "Unprimedtest memoized"
	storeCommonNameCache(eff+"\x00"+normalizeName(memoized), commonNameCacheEntry{name: "x", found: true})

	got := unprimedCommonNames([]string{
		memoized,
		"  ",
		"Unprimedtest fresh",
		"UNPRIMEDTEST FRESH", // same normalized name as the previous entry
	}, eff)

	assert.Equal(t, []string{"Unprimedtest fresh"}, got,
		"only the not-yet-memoized, non-blank, first-seen name may reach the scan")

	// The memo is keyed by the normalized name, so a casing variant of a memoized
	// name is also already primed and must not reopen the blob.
	assert.Empty(t, unprimedCommonNames([]string{memoized, "UNPRIMEDTEST MEMOIZED"}, eff),
		"a fully memoized batch must produce no common-name work")

	// A different locale is a different memo key, so the same name is unprimed there.
	assert.Equal(t, []string{memoized}, unprimedCommonNames([]string{memoized}, "de"))
}

// TestUnprimedMetaNames_SkipsMemoizedAndBlank pins the metadata half of the same
// filter. It keys by normalized name, so casing variants collapse to one work item.
func TestUnprimedMetaNames_SkipsMemoizedAndBlank(t *testing.T) {
	// Not parallel: reads and writes the package-global memo.
	memoized := "Unprimedmeta memoized"
	storeMetaCache(normalizeName(memoized), &metaCacheEntry{found: true})

	got := unprimedMetaNames([]string{memoized, "", "  ", "Unprimedmeta fresh", "unprimedmeta FRESH"})

	require.Len(t, got, 1, "casing variants of one name are a single work item")
	_, ok := got[normalizeName("Unprimedmeta fresh")]
	assert.True(t, ok)

	assert.Empty(t, unprimedMetaNames([]string{memoized}),
		"a fully memoized batch must produce no metadata work")
}
