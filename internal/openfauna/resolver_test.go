package openfauna

import (
	"bytes"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// TestMapLocale_Table pins the birdnet-go -> openfauna locale mapping. Results are
// derived from Locales() (no hardcoded override table), so the cases cover exact
// passthrough, region-strip (pt-br -> pt), region-expand (zh -> zh_cn, lv -> lv_lv),
// uncovered languages (-> en), and trimming/casing.
func TestMapLocale_Table(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		// Exact / underscore swap.
		{"en-uk", "en_uk"},
		{"en-us", "en_us"},
		{"de", "de"},
		{"fi", "fi"},
		{"es", "es"},
		{"et", "et"}, // base "et" present; must NOT expand to et_ee
		{"ja", "ja"},
		{"pt", "pt"},
		{"pt-pt", "pt_pt"},
		// Region strip: pt-br has no pt_br, falls back to base pt.
		{"pt-br", "pt"},
		// Region expand: no bare zh/lv, but a single regional variant exists.
		{"zh", "zh_cn"},
		{"lv", "lv_lv"},
		// Languages present in the dataset resolve to their own code (exact, or
		// "-" -> "_"); coverage level is irrelevant to the mapping.
		{"af", "af"},
		{"ar", "ar"},
		{"he", "he"},
		{"ko", "ko"},
		{"th", "th"},
		{"id", "id"},
		{"ml", "ml"},
		{"hi-in", "hi_in"},
		{"vi-vn", "vi_vn"},
		// Only codes with no exact, base, or regional match fall back to English.
		// Use reserved/unassigned codes so this stays correct as the dataset grows.
		{"zz", "en"},
		{"qaa-x", "en"},
		// Trimming and casing.
		{"  FI  ", "fi"},
		{"EN-UK", "en_uk"},
		// Degenerate input.
		{"", "en"},
		{"not-a-locale", "en"},
	}

	available := Locales()
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := mapLocale(tc.in)
			assert.Equal(t, tc.want, got)
			// Every result must be the English fallback or a real openfauna locale.
			if got != localeFallback {
				assert.True(t, slices.Contains(available, got),
					"mapLocale must return a code present in Locales(), got %q", got)
			}
		})
	}
}

func TestResolver_ResolvesFromSparseIndex_Embedded(t *testing.T) {
	t.Parallel()

	r := NewResolver()
	require.NoError(t, r.Rebuild([]string{"Turdus merula", "Barbastella barbastellus"}, "fi"))
	assert.Equal(t, "fi", r.Locale())

	// A bird and a bat both resolve (openfauna covers Chiroptera too; this is the
	// #928 bat-localization preview).
	bird := r.Resolve("Turdus merula", "")
	assert.NotEmpty(t, bird)
	assert.NotEqual(t, "Turdus merula", bird)

	bat := r.Resolve("Barbastella barbastellus", "")
	assert.NotEmpty(t, bat)

	// Matching is case-insensitive (callers may supply any case/whitespace).
	assert.Equal(t, bird, r.Resolve("  turdus MERULA ", ""))

	// Prove the locale is actually applied: a de resolver yields a different name.
	rDe := NewResolver()
	require.NoError(t, rDe.Rebuild([]string{"Turdus merula"}, "de"))
	assert.NotEqual(t, bird, rDe.Resolve("Turdus merula", ""),
		"fi and de must resolve to different names")
}

func TestResolver_OutOfSetSpecies_OnDemandFallback_Embedded(t *testing.T) {
	t.Parallel()

	// Erithacus rubecula is NOT in the working set, so it is absent from the sparse
	// index. The on-demand Lookup fallback must still resolve it (historic detection).
	r := NewResolver()
	require.NoError(t, r.Rebuild([]string{"Turdus merula"}, "fi"))

	want, ok := Lookup("Erithacus rubecula", "fi")
	require.True(t, ok)
	assert.Equal(t, want, r.Resolve("Erithacus rubecula", ""))
}

func TestResolver_UntranslatedLocale_FallsBackToEnglish_Embedded(t *testing.T) {
	t.Parallel()

	// "it" is a sparse locale in the dataset; Erithacus rubecula has no Italian
	// translation but does have English, so Resolve must fall back to the English
	// common name rather than returning empty.
	const sci = "Erithacus rubecula"
	enName, hasEn := Lookup(sci, "en")
	require.True(t, hasEn)

	r := NewResolver()
	require.NoError(t, r.Rebuild([]string{sci}, "it"))
	assert.Equal(t, "it", r.Locale())

	got := r.Resolve(sci, "")
	if itName, hasIt := Lookup(sci, "it"); hasIt {
		// Stay correct if upstream later adds the Italian translation.
		assert.Equal(t, itName, got)
	} else {
		assert.Equal(t, enName, got,
			"a species untranslated in the active locale must fall back to the English common name")

		// The English fallback for an untranslated working-set species is pre-seeded
		// at Rebuild, so it resolves from the cache without a per-species dataset scan.
		st := r.cur.Load()
		require.NotNil(t, st)
		cached, ok := st.cache.Load(normalizeName(sci))
		assert.True(t, ok, "untranslated working-set species must be pre-seeded into the cache")
		assert.Equal(t, enName, cached)
	}
}

func TestResolver_EnglishLocale_PreseedsUntranslatedAsMiss_Embedded(t *testing.T) {
	t.Parallel()

	// A few species (some bats) exist in the dataset with no English translation.
	// When the active locale resolves to English, such working-set species are
	// pre-seeded as known misses at Rebuild so they skip the slow path entirely.
	const sci = "Eptesicus nilssonii"
	_, hasEn := Lookup(sci, "en")
	require.False(t, hasEn,
		"test premise: %q must have no English translation; pick another English-missing species if upstream added one", sci)

	r := NewResolver()
	require.NoError(t, r.Rebuild([]string{sci}, "en")) // active locale resolves to English
	assert.Equal(t, "en", r.Locale())

	st := r.cur.Load()
	require.NotNil(t, st)
	cached, ok := st.cache.Load(normalizeName(sci))
	assert.True(t, ok, "an English-missing working-set species must be pre-seeded as a known miss")
	assert.Empty(t, cached)

	assert.Empty(t, r.Resolve(sci, ""))
}

func TestResolver_NonexistentSpecies_ReturnsEmpty_Embedded(t *testing.T) {
	t.Parallel()

	r := NewResolver()
	require.NoError(t, r.Rebuild([]string{"Turdus merula"}, "fi"))

	const missSpecies = "Definitely notaspecies"
	assert.Empty(t, r.Resolve(missSpecies, ""))

	// The miss must be memoized (stored as "") so a second resolve serves from the
	// cache instead of rescanning the whole dataset again.
	st := r.cur.Load()
	require.NotNil(t, st)
	cached, ok := st.cache.Load(normalizeName(missSpecies))
	assert.True(t, ok, "a true miss must be stored in the negative-result memo")
	assert.Empty(t, cached)

	assert.Empty(t, r.Resolve(missSpecies, ""))
}

func TestResolver_AtomicSwapRebuild_HotReload_Embedded(t *testing.T) {
	t.Parallel()

	r := NewResolver()
	require.NoError(t, r.Rebuild([]string{"Turdus merula"}, "fi"))
	fiName := r.Resolve("Turdus merula", "")
	assert.NotEmpty(t, fiName)

	// Locale change (hot-reload): rebuild atomically swaps the index.
	require.NoError(t, r.Rebuild([]string{"Turdus merula"}, "de"))
	assert.Equal(t, "de", r.Locale())
	deName := r.Resolve("Turdus merula", "")
	assert.NotEmpty(t, deName)
	assert.NotEqual(t, fiName, deName, "rebuild at a new locale must change the resolved name")
}

func TestResolver_EmptyWorkingSet_OnDemandStillResolves_Embedded(t *testing.T) {
	t.Parallel()

	// An empty working set yields an empty index, but on-demand Lookup still resolves.
	r := NewResolver()
	require.NoError(t, r.Rebuild(nil, "fi"))
	assert.Equal(t, "fi", r.Locale())

	want, ok := Lookup("Turdus merula", "fi")
	require.True(t, ok)
	assert.Equal(t, want, r.Resolve("Turdus merula", ""))
}

func TestResolver_NilAndZeroValue_NoPanic(t *testing.T) {
	t.Parallel()

	var nilR *Resolver
	assert.Empty(t, nilR.Resolve("Turdus merula", ""))
	assert.Empty(t, nilR.Locale())

	// A freshly constructed resolver (no Rebuild yet) resolves nothing.
	r := NewResolver()
	assert.Empty(t, r.Resolve("Turdus merula", ""))
	assert.Empty(t, r.Locale())
}

func TestResolver_ConcurrentResolveDuringRebuild_NoRace(t *testing.T) {
	t.Parallel()

	r := NewResolver()
	require.NoError(t, r.Rebuild([]string{"Turdus merula"}, "fi"))

	// Keep iteration counts modest: every Rebuild and every on-demand Lookup
	// (Erithacus is out of the working set) streams the whole embedded dataset,
	// which is slow under -race. This is plenty to trip the detector if the
	// atomic swap or the per-state cache were unsafe.
	locales := []string{"fi", "de", "en", "fr"}
	set := []string{"Turdus merula", "Erithacus rubecula"}

	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	wg.Go(func() {
		for i := range 10 {
			if err := r.Rebuild(set, locales[i%len(locales)]); err != nil {
				errCh <- err
				return
			}
		}
	})
	for range 4 {
		wg.Go(func() {
			for range 50 {
				_ = r.Resolve("Turdus merula", "")      // index hit (no scan)
				_ = r.Resolve("Erithacus rubecula", "") // on-demand, memoized per state
			}
		})
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err, "concurrent Rebuild must not fail")
	}
}

// TestResolveLocal_NoSlowPath verifies ResolveLocal serves only in-memory state
// (working-set index + cache) and never triggers the O(dataset) on-demand Lookup,
// which is what makes it safe for bulk name-map rebuilds over the full label set.
func TestResolveLocal_NoSlowPath(t *testing.T) {
	t.Parallel()
	r := NewResolver()
	require.NoError(t, r.Rebuild([]string{"Turdus merula"}, "en"))

	// Working-set species: present in the in-memory index.
	name, ok := r.ResolveLocal("Turdus merula")
	assert.True(t, ok, "working-set species should resolve from memory")
	assert.NotEmpty(t, name)

	// A species NOT in the working set must report a local miss WITHOUT consulting
	// the dataset (Resolve would slow-path it; ResolveLocal must not). "Zzz zzz" is
	// not a real species, so any non-miss would imply an unexpected lookup.
	_, ok = r.ResolveLocal("Zzz zzz")
	assert.False(t, ok, "out-of-working-set species must be a local miss")

	// Nil and pre-Rebuild resolvers are safe.
	var nilR *Resolver
	_, ok = nilR.ResolveLocal("Turdus merula")
	assert.False(t, ok)
	_, ok = NewResolver().ResolveLocal("Turdus merula")
	assert.False(t, ok)
}

// TestMissingFromIndex checks that only species absent from the active-locale index
// are reported missing, in input order, and that names normalizing to the same key
// (duplicates, casing variants) are returned at most once so the unresolved count and
// rebuild WARN log are not inflated.
func TestMissingFromIndex(t *testing.T) {
	t.Parallel()
	// Index keys are stored normalized (lowercased/trimmed) by BuildIndex.
	active := &Index{locale: "fi", names: map[string]string{"parus major": "talitiainen"}}
	missing := missingFromIndex(
		[]string{"Parus major", "Corvus corax", "Apus apus", "Corvus corax", "corvus corax"}, active)
	assert.Equal(t, []string{"Corvus corax", "Apus apus"}, missing)
}

// TestClassifyMissing separates English-rescued species from species OpenFauna cannot
// localize at all. The input is the active-locale-missing set (see missingFromIndex);
// the unresolved group is the diagnostic the rebuild WARN log names.
func TestClassifyMissing(t *testing.T) {
	t.Parallel()
	// English has Corvus corax but not Accipiter gentilis (a taxonomy-reclassified
	// miss: OpenFauna carries fi/en only under the new name Astur gentilis).
	en := &Index{locale: "en", names: map[string]string{"corvus corax": "Northern Raven"}}

	enFallback, unresolved := classifyMissing([]string{"Corvus corax", "Accipiter gentilis"}, en)

	require.Len(t, enFallback, 1)
	assert.Equal(t, "Northern Raven", enFallback["Corvus corax"])
	assert.Equal(t, []string{"Accipiter gentilis"}, unresolved)
}

// TestClassifyMissing_EnglishActiveLocale treats every missing species as unresolved
// when English is already the active locale (en == nil, nothing wider to fall back to).
func TestClassifyMissing_EnglishActiveLocale(t *testing.T) {
	t.Parallel()
	enFallback, unresolved := classifyMissing([]string{"Corvus corax", "Gibberish nonexistus"}, nil)
	assert.Empty(t, enFallback)
	assert.Equal(t, []string{"Corvus corax", "Gibberish nonexistus"}, unresolved)
}

// TestCapJoinSpecies bounds the rendered unresolved-species log line.
func TestCapJoinSpecies(t *testing.T) {
	t.Parallel()
	assert.Empty(t, capJoinSpecies(nil, 3))
	assert.Equal(t, "a, b", capJoinSpecies([]string{"a", "b"}, 5))
	assert.Equal(t, "a, b", capJoinSpecies([]string{"a", "b"}, 2))
	assert.Equal(t, "a, b (+1 more)", capJoinSpecies([]string{"a", "b", "c"}, 2))
}

// TestRebuild_LogsUnresolvedSpecies locks the diagnostic wiring (not just the helpers):
// Rebuild must emit the INFO summary and a WARN naming each working-set species
// OpenFauna cannot localize, so an INFO-level support dump can pinpoint a "wrong name"
// report. Uses the embedded dataset: "Turdus merula" resolves in fi, while the synthetic
// binomial "Gibberish nonexistus" is absent from every locale including the English
// fallback, so it stays unresolved regardless of dataset refreshes (a real species can be
// backfilled upstream and resolve later). Not parallel: mutates the global logger.
func TestRebuild_LogsUnresolvedSpecies(t *testing.T) {
	var buf bytes.Buffer
	capture := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cl, err := logger.NewCentralLogger(
		&logger.LoggingConfig{
			Console:      &logger.ConsoleOutput{Enabled: false},
			FileOutput:   &logger.FileOutput{Enabled: false},
			DefaultLevel: "debug",
		},
		capture,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })

	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(prev) })

	r := NewResolver()
	require.NoError(t, r.Rebuild([]string{"Turdus merula", "Gibberish nonexistus"}, "fi"))

	out := buf.String()
	assert.Contains(t, out, "openfauna name resolver rebuilt", "INFO rebuild summary must be logged")
	assert.Contains(t, out, "unresolved=1", "INFO summary must count the unresolvable species")
	assert.Contains(t, out, "openfauna could not localize", "WARN diagnostic must fire for unresolved species")
	assert.Contains(t, out, "Gibberish nonexistus", "WARN must name the unresolvable species")
	assert.NotContains(t, out, "Turdus merula", "a species resolved in the active locale must not be flagged")
}
