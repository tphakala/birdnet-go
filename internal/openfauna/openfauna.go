// Package openfauna provides read-only, memory-frugal lookups of species common
// names (translations across many locales) and taxonomic metadata, embedded from
// a vendored copy of the compiled OpenFauna dataset
// (https://github.com/tphakala/openfauna).
//
// The dataset is large (tens of thousands of species across 40+ locales), so the
// package never materializes all of it. Build a sparse Index for just the species
// and locale you need with BuildIndex, and use Lookup/LookupMeta for the rare
// species that fall outside a pre-built Index. LookupScientificNames serves the
// reverse direction (localized common name -> scientific name) for the rare need to
// canonicalize a user-supplied common name.
//
// The embedded data is a committed, gzipped snapshot; see README.md for the
// command that regenerates it from an openfauna checkout.
package openfauna

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// loggerModule is the module name used for structured logging from this package.
const loggerModule = "openfauna"

// Column layout of the translations CSV (schema: scientific_name,locale,common_name).
// The translations schema is a stable triple, so fixed indices are used.
const (
	transColScientific = 0
	transColLocale     = 1
	transColCommon     = 2
	translationColumns = 3
)

// GetLogger returns the structured logger scoped to this package.
func GetLogger() logger.Logger {
	return logger.Global().Module(loggerModule)
}

// DataVersion returns a short description of the embedded dataset's provenance
// (the openfauna source commit and generation date it was vendored from). It is
// included in index-build logs to make "which data is shipped" answerable when
// troubleshooting name-resolution issues.
func DataVersion() string {
	return strings.TrimSpace(string(dataSource))
}

// LinkEntry is one external-link reference for a species from OpenFauna's nested
// links map: a stable id resolved against the sources registry, and an optional
// url override used verbatim when the registry template cannot address the species
// (e.g. a Wikipedia article with no confident Wikidata QID).
type LinkEntry struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// Meta holds taxonomy and the per-species external-link references for a species.
// Taxonomy comes from the GBIF backbone; Links is keyed by source id (e.g.
// "wikipedia", "inaturalist", "gbif") and resolved to URLs by the sources registry
// (see links.go). The URL fields of the old flat schema are gone: links are now
// resolved generically rather than precomputed per provider.
type Meta struct {
	Class        string
	Order        string
	Family       string
	FamilyCommon string
	Links        map[string]LinkEntry
}

// Index is a sparse, immutable lookup table for a single locale, holding only the
// species requested at BuildIndex time. It is safe for concurrent reads.
type Index struct {
	locale string
	names  map[string]string // scientific name -> common name (this locale)
	meta   map[string]Meta   // scientific name -> metadata
}

// translationRowFunc receives one decoded translations row.
type translationRowFunc func(scientific, locale, common string) error

// streamTranslations decodes the embedded translations.csv.gz row by row, calling
// fn for each data row. It never holds more than one row in memory.
func streamTranslations(fn translationRowFunc) error {
	zr, err := gzip.NewReader(bytes.NewReader(translationsGz))
	if err != nil {
		return errors.New(err).
			Component(loggerModule).
			Category(errors.CategoryFileParsing).
			Context("operation", "open_translations_gzip").
			Build()
	}
	defer func() { _ = zr.Close() }()
	return decodeTranslationRows(zr, fn)
}

// decodeTranslationRows reads the (uncompressed) translations CSV from src and
// calls fn for each data row. Split out from streamTranslations so the filtering
// behaviour can be tested with synthetic data.
func decodeTranslationRows(src io.Reader, fn translationRowFunc) error {
	r := csv.NewReader(src)
	r.ReuseRecord = true
	r.FieldsPerRecord = translationColumns
	if _, err := r.Read(); err != nil { // header
		return errors.New(err).
			Component(loggerModule).
			Category(errors.CategoryFileParsing).
			Context("operation", "read_translations_header").
			Build()
	}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errors.New(err).
				Component(loggerModule).
				Category(errors.CategoryFileParsing).
				Context("operation", "read_translations_row").
				Build()
		}
		if cbErr := fn(rec[transColScientific], rec[transColLocale], rec[transColCommon]); cbErr != nil {
			return cbErr
		}
	}
}

// metaRowFunc receives one decoded metadata row.
type metaRowFunc func(scientific string, m Meta) error

// metadataRecord mirrors one metadata.jsonl object.
type metadataRecord struct {
	ScientificName string `json:"scientific_name"`
	Taxonomy       struct {
		Class        string `json:"class"`
		Order        string `json:"order"`
		Family       string `json:"family"`
		FamilyCommon string `json:"family_common"`
	} `json:"taxonomy"`
	Links map[string]LinkEntry `json:"links"`
}

// streamMetadata decodes the embedded metadata.jsonl.gz one object per line. It
// never holds more than one record in memory.
func streamMetadata(fn metaRowFunc) error {
	zr, err := gzip.NewReader(bytes.NewReader(metadataGz))
	if err != nil {
		return errors.New(err).
			Component(loggerModule).
			Category(errors.CategoryFileParsing).
			Context("operation", "open_metadata_gzip").
			Build()
	}
	defer func() { _ = zr.Close() }()
	return decodeMetadataRows(zr, fn)
}

// decodeMetadataRows reads newline-delimited JSON metadata from src and calls fn
// for each record. Split out so the decoding can be tested with synthetic data.
func decodeMetadataRows(src io.Reader, fn metaRowFunc) error {
	// Read line by line with a bufio.Reader rather than bufio.Scanner: ReadBytes
	// grows to fit an arbitrarily long record (no fixed token cap) and lets us skip
	// a single malformed line instead of aborting the whole stream. Losing one bad
	// record must not wipe out every species' taxonomy and links.
	br := bufio.NewReader(src)
	for {
		raw, readErr := br.ReadBytes('\n')
		// Process whatever was read before handling readErr, so a final line without
		// a trailing newline is not dropped.
		if line := bytes.TrimSpace(raw); len(line) > 0 {
			var rec metadataRecord
			switch err := json.Unmarshal(line, &rec); {
			case err != nil:
				// The schema unit test guards the vendored data, so a parse failure
				// here means a corrupt or unexpected record at runtime: skip it and
				// keep going so the rest of the dataset still resolves.
				GetLogger().Error("skipping malformed openfauna metadata record", logger.Error(err))
			case rec.ScientificName == "":
				// No name to key on; skip silently (mirrors the empty-name contract).
			default:
				m := Meta{
					Class:        rec.Taxonomy.Class,
					Order:        rec.Taxonomy.Order,
					Family:       rec.Taxonomy.Family,
					FamilyCommon: rec.Taxonomy.FamilyCommon,
					Links:        rec.Links,
				}
				if cbErr := fn(rec.ScientificName, m); cbErr != nil {
					return cbErr
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return errors.New(readErr).
				Component(loggerModule).
				Category(errors.CategoryFileParsing).
				Context("operation", "scan_metadata_jsonl").
				Build()
		}
	}
}

// normalizeName canonicalizes a scientific name for case-insensitive matching.
// The dataset stores canonical binomials, but callers (model labels, the
// datastore, search input) may supply varying case or surrounding whitespace, so
// index keys and lookup queries are trimmed and lowercased consistently. This
// matches the convention of the project's other species name resolvers.
func normalizeName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

var schemaWarnOnce sync.Once

// warnOnSchemaMismatch logs (at most once per process) when the embedded data's
// schema major differs from what this package parses. The embedded data is fixed
// at build time, so the result never changes within a run; the sync.Once keeps a
// repeatedly-called BuildIndex from flooding the log.
func warnOnSchemaMismatch() {
	schemaWarnOnce.Do(func() {
		if major, ok := embeddedSchemaMajor(); !ok || major != expectedSchemaMajor {
			GetLogger().Error("embedded openfauna schema version mismatch; external links may be unavailable",
				logger.Int("expected_major", expectedSchemaMajor),
				logger.String("data_version", DataVersion()),
			)
		}
	})
}

// BuildIndex streams the embedded dataset once and returns a sparse Index holding
// only the requested scientific names, with common names for the given locale and
// metadata for those species. Names not present in the dataset are simply absent.
// An unrecognized locale yields an Index with no translations (still valid).
//
// Memory: only matching rows are retained; the full dataset is never held at once.
func BuildIndex(scientificNames []string, locale string) (*Index, error) {
	// Fail loud if the embedded data regressed to a non-2.x schema: the parser and
	// the sources registry assume the 2.x shape, so a mismatch means links/taxonomy
	// may silently not resolve. The hard gate is the schema unit test; this is the
	// runtime signal for an operator reading logs. The embedded schema is fixed for
	// the process lifetime, so warn at most once however many times BuildIndex runs.
	warnOnSchemaMismatch()

	want := make(map[string]struct{}, len(scientificNames))
	for _, n := range scientificNames {
		want[normalizeName(n)] = struct{}{}
	}
	ix := &Index{
		locale: locale,
		names:  make(map[string]string, len(want)),
		meta:   make(map[string]Meta, len(want)),
	}
	if len(want) == 0 {
		return ix, nil
	}

	if err := streamTranslations(func(sci, loc, common string) error {
		if loc != locale {
			return nil
		}
		key := normalizeName(sci)
		if _, ok := want[key]; ok {
			ix.names[key] = common
		}
		return nil
	}); err != nil {
		GetLogger().Error("failed to read embedded translations",
			logger.String("locale", locale),
			logger.Error(err),
		)
		return nil, err
	}

	if err := streamMetadata(func(sci string, m Meta) error {
		key := normalizeName(sci)
		if _, ok := want[key]; ok {
			ix.meta[key] = m
		}
		return nil
	}); err != nil {
		GetLogger().Error("failed to read embedded metadata",
			logger.String("locale", locale),
			logger.Error(err),
		)
		return nil, err
	}

	GetLogger().Info("built openfauna species index",
		logger.String("locale", locale),
		logger.String("data_version", DataVersion()),
		logger.Int("requested", len(want)),
		logger.Int("resolved_names", len(ix.names)),
		logger.Int("with_metadata", len(ix.meta)),
	)
	return ix, nil
}

// CommonName returns the common name for a scientific name in the Index's locale.
// A nil Index reports not-found so a caller that ignored a BuildIndex error
// degrades to scientific names instead of panicking.
func (ix *Index) CommonName(scientific string) (string, bool) {
	if ix == nil {
		return "", false
	}
	v, ok := ix.names[normalizeName(scientific)]
	return v, ok
}

// Meta returns taxonomy/link metadata for a scientific name, if present. A nil
// Index reports not-found rather than panicking.
func (ix *Index) Meta(scientific string) (Meta, bool) {
	if ix == nil {
		return Meta{}, false
	}
	v, ok := ix.meta[normalizeName(scientific)]
	return v, ok
}

// Locale returns the locale this Index was built for, or "" for a nil Index.
func (ix *Index) Locale() string {
	if ix == nil {
		return ""
	}
	return ix.locale
}

// errStop is returned by a streaming callback to halt iteration early once the
// single target row has been found. It is an internal control-flow sentinel,
// never surfaced to callers, so it is a plain error with no telemetry context.
var errStop = errors.NewStd("openfauna: stop iteration")

// Lookup returns the common name for one scientific name in one locale by scanning
// the embedded dataset. It is O(dataset) per call and is intended only for the
// occasional species outside a pre-built Index (for example a historic detection
// of an out-of-range species); callers should cache the result.
func Lookup(scientific, locale string) (string, bool) {
	target := normalizeName(scientific)
	var found string
	var ok bool
	if err := streamTranslations(func(sci, loc, common string) error {
		if loc == locale && normalizeName(sci) == target {
			found, ok = common, true
			return errStop
		}
		return nil
	}); err != nil && !errors.Is(err, errStop) {
		GetLogger().Error("openfauna translation lookup failed",
			logger.String("scientific", target),
			logger.String("locale", locale),
			logger.Error(err),
		)
		return "", false
	}
	GetLogger().Debug("openfauna single-species translation lookup (index-miss fallback)",
		logger.String("scientific", target),
		logger.String("locale", locale),
		logger.Bool("found", ok),
	)
	return found, ok
}

// LookupScientificNames is the reverse of Lookup: for each requested localized common
// name it returns the scientific name(s) carrying that name in the locale mapped from
// bngLocale, resolving every request in a single pass over the embedded dataset. The
// result is keyed by the caller's exact input strings (matching is case-insensitive
// and whitespace-trimmed); a requested name with no match is absent from the result.
// Each name's scientific list is de-duplicated and sorted.
//
// It exists for the rare cold-path need to canonicalize user-supplied localized common
// names (for example a non-primary model's bat or mammal, whose model label is
// scientific-only so the forward, scientific-keyed resolvers cannot reverse it). The
// scan is O(dataset) once regardless of how many names are requested, so callers batch
// all of a rebuild's unresolved overrides into one call rather than looping; it must
// not be used on a hot path.
//
// Resolution mirrors Resolver.Resolve: bngLocale is mapped to an openfauna locale
// (mapLocale) and matches there take precedence; the English fallback is consulted
// only for names the active locale did not resolve, so an English common name still
// resolves on a sparsely-translated locale.
func LookupScientificNames(commonNames []string, bngLocale string) map[string][]string {
	// Map each distinct normalized name to the caller's original input strings, so the
	// result can be keyed by exactly what the caller passed.
	inputs := make(map[string][]string) // normalized name -> original inputs
	for _, in := range commonNames {
		norm := normalizeName(in)
		if norm == "" {
			continue
		}
		inputs[norm] = append(inputs[norm], in)
	}
	if len(inputs) == 0 {
		return map[string][]string{}
	}
	eff := mapLocale(bngLocale)

	// One pass collects both the active locale and (when distinct) the English fallback;
	// the active locale wins per name, English rescues only the names it missed.
	inLocale := make(map[string]map[string]struct{})  // normalized name -> set of scientific
	inEnglish := make(map[string]map[string]struct{}) // normalized name -> set of scientific
	collect := func(dst map[string]map[string]struct{}, norm, sci string) {
		set := dst[norm]
		if set == nil {
			set = make(map[string]struct{})
			dst[norm] = set
		}
		set[sci] = struct{}{}
	}
	if err := streamTranslations(func(sci, loc, common string) error {
		isLocale := loc == eff
		isEnglish := eff != localeFallback && loc == localeFallback
		if !isLocale && !isEnglish {
			return nil
		}
		norm := normalizeName(common)
		if _, want := inputs[norm]; !want {
			return nil
		}
		if isLocale {
			collect(inLocale, norm, sci)
		} else {
			collect(inEnglish, norm, sci)
		}
		return nil
	}); err != nil {
		GetLogger().Error("openfauna reverse common-name lookup failed",
			logger.String("locale", eff),
			logger.Int("requested", len(inputs)),
			logger.Error(err),
		)
		return map[string][]string{}
	}

	out := make(map[string][]string, len(inputs))
	for norm, origins := range inputs {
		set := inLocale[norm]
		if len(set) == 0 {
			set = inEnglish[norm]
		}
		if len(set) == 0 {
			continue
		}
		sciNames := slices.Collect(maps.Keys(set))
		slices.Sort(sciNames)
		for _, in := range origins {
			out[in] = sciNames
		}
	}
	GetLogger().Debug("openfauna reverse common-name lookup",
		logger.String("locale", eff),
		logger.Int("requested", len(inputs)),
		logger.Int("resolved", len(out)),
	)
	return out
}

// ReverseResolveToScientificNames reverse-resolves localized common-name entries to their
// lower-cased scientific name(s) for the given BirdNET locale. It is a thin convenience over
// LookupScientificNames that applies the lower-casing callers need when matching against
// lower-cased scientific names, so the lower-casing and locale handling live in one place
// instead of being duplicated (and able to drift) across call sites.
//
// The result is keyed by the caller's original entry strings; entries that resolve to no
// scientific name are absent. Each entry's scientific names keep the sorted, de-duplicated
// order from LookupScientificNames and are lower-cased. Returns an empty (non-nil) map when
// entries is empty or nothing resolves. Like LookupScientificNames this is O(dataset) per
// call and must not be used on a hot path.
func ReverseResolveToScientificNames(entries []string, bngLocale string) map[string][]string {
	resolved := LookupScientificNames(entries, bngLocale)
	out := make(map[string][]string, len(resolved))
	for entry, sciNames := range resolved {
		lowered := make([]string, len(sciNames))
		for i, sci := range sciNames {
			lowered[i] = strings.ToLower(sci)
		}
		out[entry] = lowered
	}
	return out
}

// ReverseResolveToScientificSet reverse-resolves localized common-name entries to a single
// flat set of lower-cased scientific names for the given BirdNET locale, flattening the
// per-entry result of ReverseResolveToScientificNames. It serves callers that only need to
// test membership of a label's scientific name (e.g. the range-filter exclude matcher) and
// do not care which entry produced which name. Returns an empty (non-nil) set when entries
// is empty or nothing resolves. O(dataset) per call; not for hot paths.
func ReverseResolveToScientificSet(entries []string, bngLocale string) map[string]struct{} {
	resolved := ReverseResolveToScientificNames(entries, bngLocale)
	set := make(map[string]struct{}, len(resolved))
	for _, sciNames := range resolved {
		for _, sci := range sciNames {
			set[sci] = struct{}{}
		}
	}
	return set
}

// LookupCommonNames is the forward, batched companion to LookupScientificNames:
// for each requested scientific name it returns the localized common name in the
// locale mapped from bngLocale, resolving every request in a single pass over the
// embedded dataset. The result is keyed by the caller's exact input strings
// (matching is case-insensitive and whitespace-trimmed); a requested name with no
// translation in either the active locale or English is absent from the result.
//
// It exists for the cold-path need to give the reverse search maps a localized
// common name for every model label, including the scientific-only labels emitted
// by non-primary models (bats, Perch-unique species) that the forward,
// scientific-keyed working-set resolver (ResolveLocal) does not cover. The scan is
// O(dataset) once regardless of how many names are requested, so callers batch all
// of a name-map rebuild's unresolved labels into one call; it must not be used on a
// hot path.
//
// Resolution mirrors Resolver.Resolve: bngLocale is mapped via mapLocale and matches
// there take precedence; the English fallback is consulted only for names the active
// locale did not resolve.
func LookupCommonNames(scientificNames []string, bngLocale string) map[string]string {
	return lookupCommonNamesEffective(scientificNames, mapLocale(bngLocale))
}

// lookupCommonNamesEffective is the locale-already-mapped core of LookupCommonNames,
// shared with (*Resolver).ResolveLocalizedBatch which holds an effective locale.
func lookupCommonNamesEffective(scientificNames []string, eff string) map[string]string {
	inputs := make(map[string][]string) // normalized sci -> original inputs
	for _, in := range scientificNames {
		norm := normalizeName(in)
		if norm == "" {
			continue
		}
		inputs[norm] = append(inputs[norm], in)
	}
	if len(inputs) == 0 {
		return map[string]string{}
	}

	inLocale := make(map[string]string)  // normalized sci -> common (active locale)
	inEnglish := make(map[string]string) // normalized sci -> common (English fallback)
	if err := streamTranslations(func(sci, loc, common string) error {
		norm := normalizeName(sci)
		if _, want := inputs[norm]; !want {
			return nil
		}
		if common == "" {
			return nil // an empty translation cannot satisfy a lookup; skip so it cannot block a real name
		}
		switch {
		case loc == eff:
			if _, exists := inLocale[norm]; !exists {
				inLocale[norm] = common
			}
		case eff != localeFallback && loc == localeFallback:
			if _, exists := inEnglish[norm]; !exists {
				inEnglish[norm] = common
			}
		}
		return nil
	}); err != nil {
		GetLogger().Error("openfauna forward common-name batch lookup failed",
			logger.String("locale", eff),
			logger.Int("requested", len(inputs)),
			logger.Error(err),
		)
		return map[string]string{}
	}

	out := make(map[string]string, len(inputs))
	for norm, origins := range inputs {
		name := inLocale[norm]
		if name == "" {
			name = inEnglish[norm]
		}
		if name == "" {
			continue
		}
		for _, in := range origins {
			out[in] = name
		}
	}
	GetLogger().Debug("openfauna forward common-name batch lookup",
		logger.String("locale", eff),
		logger.Int("requested", len(inputs)),
		logger.Int("resolved", len(out)),
	)
	return out
}

// metaCacheMaxEntries bounds the LookupMeta memo. The embedded metadata covers
// ~15k species; the cap sits above that so every real species can be memoized
// while a flood of distinct never-present names cannot grow the memo without limit.
const metaCacheMaxEntries = 20000

// metaCacheEntry is a memoized LookupMeta result. found distinguishes a cached
// "present" entry from a cached "absent" one so negative lookups are memoized too.
type metaCacheEntry struct {
	meta  Meta
	found bool
}

var (
	// metaCache memoizes LookupMeta. The embedded dataset is immutable, so a result
	// (present or absent) for a scientific name never changes; caching it avoids the
	// O(dataset) metadata scan on repeat lookups (e.g. the per-request external links
	// built for a guide, and the guide provider's enrichment fetches).
	metaCache      sync.Map     // normalized scientific name -> metaCacheEntry
	metaCacheCount atomic.Int64 // approximate entry count guarding the soft cap
)

// storeMetaCache records a LookupMeta result under the soft cap. A new key is only
// added while under metaCacheMaxEntries: a slot is reserved up front and rolled back
// on overflow or when a concurrent writer created the key first, so the memo stays
// bounded and accurate under concurrent distinct-key lookups. Mirrors the bounded
// in-memory pattern used by the guide cache.
func storeMetaCache(key string, e *metaCacheEntry) {
	if _, loaded := metaCache.Load(key); loaded {
		return
	}
	if metaCacheCount.Add(1) > metaCacheMaxEntries {
		metaCacheCount.Add(-1)
		return
	}
	if _, loaded := metaCache.LoadOrStore(key, *e); loaded {
		metaCacheCount.Add(-1)
	}
}

// LookupMeta returns taxonomy/link metadata for one scientific name. The first
// lookup for a name scans the embedded dataset; the immutable result is then
// memoized so repeat lookups are O(1). The dataset-scan cost (see Lookup) is paid
// only on the first, uncached lookup of each name.
func LookupMeta(scientific string) (Meta, bool) {
	target := normalizeName(scientific)
	if v, ok := metaCache.Load(target); ok {
		if e, ok := v.(metaCacheEntry); ok {
			return e.meta, e.found
		}
	}
	var found Meta
	var ok bool
	if err := streamMetadata(func(sci string, m Meta) error {
		if normalizeName(sci) == target {
			found, ok = m, true
			return errStop
		}
		return nil
	}); err != nil && !errors.Is(err, errStop) {
		GetLogger().Error("openfauna metadata lookup failed",
			logger.String("scientific", target),
			logger.Error(err),
		)
		// Do not memoize on a scan error so a transient failure isn't cached.
		return Meta{}, false
	}
	storeMetaCache(target, &metaCacheEntry{meta: found, found: ok})
	GetLogger().Debug("openfauna single-species metadata lookup (index-miss fallback)",
		logger.String("scientific", target),
		logger.Bool("found", ok),
	)
	return found, ok
}

// Locales returns the sorted list of locale codes available in the dataset
// (e.g. "en", "fi", "de", "en_uk", "zh_cn"). The codes use underscores and may
// include regional variants; consumers map their own locale codes onto these.
func Locales() []string {
	lines := strings.Split(string(localesList), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if l := strings.TrimSpace(line); l != "" {
			out = append(out, l)
		}
	}
	return out
}
