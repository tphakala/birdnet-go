package openfauna

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/sync/singleflight"
)

// maxLoggedUnresolvedSpecies caps how many unresolved scientific names are listed in
// the rebuild WARN log, so the line stays bounded when a model pairs a broad working
// set with a locale whose species are largely absent from OpenFauna.
const maxLoggedUnresolvedSpecies = 50

// localeFallback is the ultimate locale used when a requested birdnet-go locale
// maps to no available openfauna locale. English has the widest coverage.
const localeFallback = "en"

// mapLocale translates a birdnet-go locale code (e.g. "en-uk", "pt-br", "zh") to
// an available openfauna locale code (e.g. "en_uk", "pt", "zh_cn"). The result is
// always either localeFallback or a code present in Locales(); no hardcoded
// override table is used, so the mapping self-corrects as upstream locales change.
//
// Resolution order: exact (after "-" -> "_"), then the base language, then the
// first available regional variant of that base, then English.
func mapLocale(bngLocale string) string {
	in := strings.ToLower(strings.TrimSpace(bngLocale))
	if in == "" {
		return localeFallback
	}

	// mapLocale runs only on Rebuild (rare), so a few linear scans of the small,
	// sorted locale slice are cheaper than building a throwaway set.
	available := Locales()

	// 1. Exact match after normalizing the separator (en-uk -> en_uk).
	cand := strings.ReplaceAll(in, "-", "_")
	if slices.Contains(available, cand) {
		return cand
	}

	// 2. Base language (pt_br -> pt).
	base := cand
	if b, _, found := strings.Cut(cand, "_"); found {
		base = b
		if slices.Contains(available, base) {
			return base
		}
	}

	// 3. First available regional variant of the base (zh -> zh_cn, lv -> lv_lv).
	// Locales() is sorted, so the choice is deterministic.
	prefix := base + "_"
	for _, l := range available {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}

	// 4. English.
	return localeFallback
}

// resolverState is a snapshot swapped atomically on Rebuild. The index and locale
// are immutable once published; the cache is a concurrency-safe (sync.Map) memo for
// the on-demand lookup fallback, and sf coalesces concurrent slow-path lookups for
// the same species so only one dataset scan runs. Bundling them behind a single
// pointer guarantees a locale change replaces them together (and discards the stale
// cache and in-flight group).
type resolverState struct {
	index  *Index
	locale string             // effective openfauna locale code
	cache  *sync.Map          // normalized scientific name -> resolved common name ("" = known miss)
	sf     singleflight.Group // dedupes concurrent slow-path lookups per scientific name
}

// Resolver resolves scientific names to localized common names from the embedded
// OpenFauna dataset. It serves a sparse per-locale index for a working set and
// falls back to an on-demand (memoized) single-species Lookup for species outside
// that set. The zero value and a nil *Resolver are safe to use and resolve nothing
// until Rebuild is called. Resolve is safe to call concurrently with Rebuild.
//
// It implements the classifier NameResolver interface: the per-call locale
// argument is ignored because the resolver is built for the active BirdNET.Locale,
// matching the existing BirdNETLabelResolver/TaxonomyResolver convention.
type Resolver struct {
	cur atomic.Pointer[resolverState]
}

// NewResolver returns an empty Resolver. Resolve returns "" until the first
// successful Rebuild populates the index and effective locale.
func NewResolver() *Resolver {
	return &Resolver{}
}

// Rebuild builds a fresh sparse index for the given working set at the locale
// mapped from bngLocale, then atomically swaps it in (and resets the lookup
// cache). A failed build leaves the previous snapshot in place. Safe to call
// concurrently with Resolve; intended to be invoked on range-filter rebuilds and
// locale changes for hot-reload.
func (r *Resolver) Rebuild(scientificNames []string, bngLocale string) error {
	if r == nil {
		return nil
	}
	eff := mapLocale(bngLocale)
	idx, err := BuildIndex(scientificNames, eff)
	if err != nil {
		return err
	}

	// Build an English index for the working-set species missing from the active
	// locale, so the partition below can tell an English fallback apart from a
	// species OpenFauna cannot localize at all. Skipped when English is already the
	// active locale (there is nothing wider to fall back to).
	missing := missingFromIndex(scientificNames, idx)
	var enIdx *Index
	if eff != localeFallback && len(missing) > 0 {
		built, enErr := BuildIndex(missing, localeFallback)
		if enErr != nil {
			return enErr
		}
		enIdx = built
	}

	// Partition the missing species: enFallback species display in English; unresolved
	// species have no OpenFauna name in either locale and fall through to the model
	// labels or the scientific name downstream.
	enFallback, unresolved := classifyMissing(missing, enIdx)

	// Pre-seed both groups into the lookup cache so untranslated working-set species
	// never hit the O(dataset) on-demand Lookup: an English name when one exists, or a
	// "" known-miss otherwise. Sparse locales ship translations for as little as ~2%
	// of species, so this keeps the slow path for out-of-working-set (historic)
	// species only.
	cache := &sync.Map{}
	for sci, name := range enFallback {
		cache.Store(normalizeName(sci), name)
	}
	for _, sci := range unresolved {
		cache.Store(normalizeName(sci), "")
	}

	r.cur.Store(&resolverState{index: idx, locale: eff, cache: cache})
	GetLogger().Info("openfauna name resolver rebuilt",
		logger.String("bng_locale", bngLocale),
		logger.String("effective_locale", eff),
		logger.Int("working_set", len(scientificNames)),
		logger.Int("english_fallbacks", len(enFallback)),
		logger.Int("unresolved", len(unresolved)),
	)
	// Name the species OpenFauna could not localize at all. This runs only on the cold
	// rebuild path (startup, locale change, daily range-filter refresh), never on the
	// hot Resolve path, and is logged at WARN so it lands in INFO-level support dumps:
	// it is the diagnostic for "species X shows the wrong name" reports, which are
	// usually upstream taxonomy reclassifications (e.g. a genus rename leaving the old
	// label untranslated). Capped so the line stays bounded.
	if len(unresolved) > 0 {
		GetLogger().Warn("openfauna could not localize some working-set species; falling back to model labels or scientific name",
			logger.String("effective_locale", eff),
			logger.Int("count", len(unresolved)),
			logger.String("species", capJoinSpecies(unresolved, maxLoggedUnresolvedSpecies)),
		)
	}
	return nil
}

// missingFromIndex returns the working-set species absent from idx, preserving input
// order. A species is "missing" when the active-locale index holds no common name for
// it, so it needs an English fallback or is unresolved. Names that normalize to the
// same key are returned at most once, so a working set with duplicates or casing
// variants does not inflate the unresolved count or repeat names in the rebuild log.
func missingFromIndex(scientificNames []string, idx *Index) []string {
	var missing []string
	seen := make(map[string]struct{}, len(scientificNames))
	for _, sci := range scientificNames {
		key := normalizeName(sci)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if _, ok := idx.CommonName(sci); !ok {
			missing = append(missing, sci)
		}
	}
	return missing
}

// classifyMissing partitions the active-locale-missing species (as returned by
// missingFromIndex) into those an English name rescues (enFallback: scientific name ->
// English common name) and those with no OpenFauna name in either locale (unresolved,
// input order preserved). en may be nil when English is the active locale, in which
// case every species is unresolved (there is nothing wider to fall back to).
func classifyMissing(missing []string, en *Index) (enFallback map[string]string, unresolved []string) {
	enFallback = make(map[string]string)
	for _, sci := range missing {
		var name string
		if en != nil {
			name, _ = en.CommonName(sci)
		}
		if name != "" {
			enFallback[sci] = name
		} else {
			unresolved = append(unresolved, sci)
		}
	}
	return enFallback, unresolved
}

// capJoinSpecies joins up to maxNames scientific names with ", ", appending a
// "(+N more)" suffix when the list is longer so the rendered log line stays bounded.
func capJoinSpecies(names []string, maxNames int) string {
	if len(names) <= maxNames {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:maxNames], ", ") + fmt.Sprintf(" (+%d more)", len(names)-maxNames)
}

// Resolve returns the localized common name for scientificName, or "" if it is not
// found in the active or English locale. The per-call locale argument is ignored
// (see Resolver). Matching is case-insensitive.
func (r *Resolver) Resolve(scientificName, _ string) string {
	if r == nil {
		return ""
	}
	st := r.cur.Load()
	if st == nil {
		return ""
	}

	// Normalize once and reuse the key for the index, cache lookup, and store.
	key := normalizeName(scientificName)

	// Fast path: the sparse working-set index. names is unexported but same-package,
	// so read it directly with the pre-normalized key to avoid re-normalizing inside
	// CommonName on every resolve.
	if st.index != nil {
		if name, ok := st.index.names[key]; ok && name != "" {
			return name
		}
	}

	// Slow path: on-demand single-species lookup, memoized (including misses).
	if v, ok := st.cache.Load(key); ok {
		if name, ok := v.(string); ok {
			return name
		}
	}

	// Coalesce concurrent lookups for the same species: Lookup streams the whole
	// dataset, so a stampede of identical misses would otherwise each scan it.
	v, _, _ := st.sf.Do(key, func() (any, error) {
		// Re-check the cache; a prior flight may have populated it while we waited.
		if cached, ok := st.cache.Load(key); ok {
			if name, ok := cached.(string); ok {
				return name, nil
			}
		}
		name, ok := Lookup(scientificName, st.locale)
		if !ok && st.locale != localeFallback {
			// Per-species fallback to English for species untranslated in the active locale.
			name, ok = Lookup(scientificName, localeFallback)
		}
		if !ok {
			name = ""
		}
		st.cache.Store(key, name)
		return name, nil
	})
	name, _ := v.(string)
	return name
}

// ResolveLocal returns the localized common name for scientificName only if it is
// already resident in memory (the working-set index or the pre-seeded/memoized
// cache), reporting ok=false instead of falling back to the O(dataset) on-demand
// Lookup. Bulk callers (e.g. rebuilding the full-model name maps) use it so that
// out-of-working-set species do not each trigger a dataset scan. Matching is
// case-insensitive.
func (r *Resolver) ResolveLocal(scientificName string) (name string, ok bool) {
	if r == nil {
		return "", false
	}
	st := r.cur.Load()
	if st == nil {
		return "", false
	}
	key := normalizeName(scientificName)
	if st.index != nil {
		if n, found := st.index.names[key]; found && n != "" {
			return n, true
		}
	}
	// The cache holds pre-seeded English fallbacks for working-set species and
	// memoized slow-path results; an empty entry is a known miss, so treat only a
	// non-empty cached name as a usable local hit.
	if v, found := st.cache.Load(key); found {
		if n, isStr := v.(string); isStr && n != "" {
			return n, true
		}
	}
	return "", false
}

// Locale reports the effective openfauna locale code of the current index, or ""
// before the first Rebuild. Intended for introspection and logging.
func (r *Resolver) Locale() string {
	if r == nil {
		return ""
	}
	if st := r.cur.Load(); st != nil {
		return st.locale
	}
	return ""
}
