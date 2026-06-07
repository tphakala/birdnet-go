package openfauna

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/logger"
)

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
// the on-demand lookup fallback. Bundling all three behind a single pointer
// guarantees a locale change replaces them together (and discards the stale cache).
type resolverState struct {
	index  *Index
	locale string    // effective openfauna locale code
	cache  *sync.Map // normalized scientific name -> resolved common name ("" = known miss)
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

	// Pre-seed the English fallback for working-set species that have no
	// translation in the active locale. Sparse locales ship translations for as
	// little as ~2% of species, so without this every untranslated working-set
	// species would hit the O(dataset) on-demand Lookup on its first resolve.
	// Resolving them once here keeps the slow path for out-of-working-set
	// (historic) species only.
	cache := &sync.Map{}
	preseeded := 0
	if eff != localeFallback {
		var missing []string
		for _, sci := range scientificNames {
			if _, ok := idx.CommonName(sci); !ok {
				missing = append(missing, sci)
			}
		}
		if len(missing) > 0 {
			enIdx, enErr := BuildIndex(missing, localeFallback)
			if enErr != nil {
				return enErr
			}
			for _, sci := range missing {
				name, _ := enIdx.CommonName(sci) // "" when absent in English too
				cache.Store(normalizeName(sci), name)
				preseeded++
			}
		}
	} else {
		// Active locale is English: a working-set species missing from the index has
		// no English name at all, so pre-seed it as a known miss to keep it off the
		// slow path as well.
		for _, sci := range scientificNames {
			if _, ok := idx.CommonName(sci); !ok {
				cache.Store(normalizeName(sci), "")
				preseeded++
			}
		}
	}

	r.cur.Store(&resolverState{index: idx, locale: eff, cache: cache})
	GetLogger().Info("openfauna name resolver rebuilt",
		logger.String("bng_locale", bngLocale),
		logger.String("effective_locale", eff),
		logger.Int("working_set", len(scientificNames)),
		logger.Int("english_fallbacks", preseeded),
	)
	return nil
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

	// Fast path: the sparse working-set index. Read the (already normalized) map
	// directly to avoid re-normalizing inside CommonName on every resolve.
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

	name, ok := Lookup(scientificName, st.locale)
	if !ok && st.locale != localeFallback {
		// Per-species fallback to English for species untranslated in the active locale.
		name, ok = Lookup(scientificName, localeFallback)
	}
	if !ok {
		name = ""
	}
	st.cache.Store(key, name)
	return name
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
