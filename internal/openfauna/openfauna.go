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

// maxMalformedMetadataLogs caps how many individual malformed-record failures are
// logged per scan. A structurally wrong dataset makes EVERY line fail, and this
// function runs at startup and on request-path lookups, so an uncapped per-record
// Error log turns one problem into a ~15k-event log and telemetry storm. Past the
// cap the failures are still counted and reported once, in the summary.
const maxMalformedMetadataLogs = 10

// metaNameFunc receives one metadata record's scientific name together with the raw
// line it came from, so a caller that only needs to FIND a record pays for decoding
// the rest of exactly one.
type metaNameFunc func(scientific string, line []byte) error

// metadataNameRecord decodes only the key field. Unmarshalling into it skips the
// nested taxonomy object and, crucially, never allocates the per-record links map.
type metadataNameRecord struct {
	ScientificName string `json:"scientific_name"`
}

// streamMetadataNames walks the embedded metadata decoding only each record's
// scientific name. The line passed to fn is only valid for the duration of the
// call; a caller that keeps it must copy it.
func streamMetadataNames(fn metaNameFunc) error {
	zr, err := gzip.NewReader(bytes.NewReader(metadataGz))
	if err != nil {
		return errors.New(err).
			Component(loggerModule).
			Category(errors.CategoryFileParsing).
			Context("operation", "open_metadata_gzip").
			Build()
	}
	defer func() { _ = zr.Close() }()

	br := bufio.NewReader(zr)
	for {
		raw, readErr := br.ReadBytes('\n')
		if line := bytes.TrimSpace(raw); len(line) > 0 {
			var rec metadataNameRecord
			// A malformed line is skipped here without logging: this path is a
			// lookup, and decodeMetadataRows already reports dataset-level problems
			// (with a cap) on the paths that read the whole stream.
			if err := json.Unmarshal(line, &rec); err == nil && rec.ScientificName != "" {
				if cbErr := fn(rec.ScientificName, line); cbErr != nil {
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

// decodeMetadataRows reads newline-delimited JSON metadata from src and calls fn
// for each record. Split out so the decoding can be tested with synthetic data.
//
// A single unusable record is skipped so one bad line cannot wipe out every
// species' taxonomy and links. A dataset in which NOTHING is usable is a different
// class of problem and is returned as an error: that means the schema moved (a
// renamed or re-nested scientific_name key, an array instead of NDJSON), and
// silently yielding an empty index would strip taxonomy and every external link
// from the whole application with no diagnostic at all. The manifest's schema gate
// does not catch it, because a key rename need not bump the major version.
func decodeMetadataRows(src io.Reader, fn metaRowFunc) error {
	// Read line by line with a bufio.Reader rather than bufio.Scanner: ReadBytes
	// grows to fit an arbitrarily long record (no fixed token cap) and lets us skip
	// a single malformed line instead of aborting the whole stream.
	br := bufio.NewReader(src)
	var (
		lines     int // non-empty lines seen
		usable    int // records that yielded a scientific name
		malformed int // lines that failed to parse
		nameless  int // lines that parsed but carried no scientific name
	)
	for {
		raw, readErr := br.ReadBytes('\n')
		// Process whatever was read before handling readErr, so a final line without
		// a trailing newline is not dropped.
		if line := bytes.TrimSpace(raw); len(line) > 0 {
			lines++
			var rec metadataRecord
			switch err := json.Unmarshal(line, &rec); {
			case err != nil:
				malformed++
				if malformed <= maxMalformedMetadataLogs {
					GetLogger().Error("skipping malformed openfauna metadata record",
						logger.Error(err))
				}
			case rec.ScientificName == "":
				nameless++
			default:
				usable++
				m := Meta{
					Class:        rec.Taxonomy.Class,
					Order:        rec.Taxonomy.Order,
					Family:       rec.Taxonomy.Family,
					FamilyCommon: rec.Taxonomy.FamilyCommon,
					Links:        rec.Links,
				}
				if cbErr := fn(rec.ScientificName, m); cbErr != nil {
					// A callback sentinel (errStop) is control flow, not a data
					// problem: return it untouched and skip the usability check,
					// which has not seen the whole stream.
					return cbErr
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return metadataUsabilityError(lines, usable, malformed, nameless)
			}
			return errors.New(readErr).
				Component(loggerModule).
				Category(errors.CategoryFileParsing).
				Context("operation", "scan_metadata_jsonl").
				Build()
		}
	}
}

// metadataUsabilityError reports a structurally unusable metadata stream, and logs a
// one-line summary when records were dropped but the dataset is still usable.
func metadataUsabilityError(lines, usable, malformed, nameless int) error {
	if lines > 0 && usable == 0 {
		return errors.Newf(
			"openfauna: metadata unusable — %d records read, none carried a scientific name (%d malformed, %d nameless); the dataset schema has probably changed",
			lines, malformed, nameless).
			Component(loggerModule).
			Category(errors.CategoryFileParsing).
			Context("operation", "decode_metadata").
			Build()
	}
	if malformed > 0 || nameless > 0 {
		GetLogger().Warn("openfauna metadata decoded with skipped records",
			logger.Int("usable", usable),
			logger.Int("malformed", malformed),
			logger.Int("nameless", nameless),
			logger.Int("logged_malformed", min(malformed, maxMalformedMetadataLogs)),
		)
	}
	return nil
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
		// The metaCache/commonNameCache soft caps assume the dataset fits under them so
		// every real species memoizes. If a data refresh grows the dataset past a cap,
		// some genuine species silently stop being memoized; the manifest carries the
		// count that detects it, so warn loud rather than fail quietly.
		if count, ok := embeddedSpeciesCount(); ok && count > metaCacheMaxEntries {
			GetLogger().Warn("embedded openfauna species_count exceeds metaCache cap; some species will not be memoized",
				logger.Int("species_count", count),
				logger.Int("meta_cache_cap", metaCacheMaxEntries),
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
	return v.clone(), ok
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
	// Halt the scan once every requested name holds an active-locale hit. That is the
	// best answer any later row could supply (English is consulted only as a fallback
	// for names the active locale did not resolve), so continuing is pure waste.
	//
	// This matters most for the single-name callers the guide cache uses on its
	// miss, warm, and refresh paths: without the early exit each one inflates and
	// parses the entire ~20 MB / 460k-row translations blob to extract one string,
	// so a default 50-species warm decompressed well over a gigabyte. Lookup above
	// already used errStop for exactly this; this path did not.
	//
	// A name with no translation in the active locale never satisfies the condition,
	// so the scan correctly runs to completion to pick up its English fallback.
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
				if len(inLocale) == len(inputs) {
					return errStop
				}
			}
		case eff != localeFallback && loc == localeFallback:
			if _, exists := inEnglish[norm]; !exists {
				inEnglish[norm] = common
			}
		}
		return nil
	}); err != nil && !errors.Is(err, errStop) {
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

// clone returns a copy safe to hand to a caller. Copying a Meta by value does NOT
// copy its Links map, so returning the memoized value directly published one shared,
// mutable map to every concurrent caller: a caller adding or deleting a key — the
// natural reading of a by-value return — would race the ranges ExternalLinks performs
// on the request path and trigger an unrecoverable "concurrent map read and map
// write" fatal error. Sources() already clones for exactly this reason.
func (m Meta) clone() Meta {
	if m.Links == nil {
		return m
	}
	m.Links = maps.Clone(m.Links)
	return m
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
// bounded and accurate under concurrent distinct-key lookups.
//
// This lock-free reserve-then-LoadOrStore is exact WITHOUT a mutex because metaCache
// is append-only: the immutable dataset means an entry is never updated in place or
// deleted (unlike the guide cache, whose updates + invalidation/eviction race stores
// and require a write lock). With only insert-if-absent, among N goroutines racing
// the same new key exactly one wins the LoadOrStore and keeps its reservation (which
// matches the one stored entry); every loser rolls back. So metaCacheCount always
// equals the number of stored entries — a "winner also decrements" change would
// UNDERcount. See TestStoreMetaCache_CountMatchesEntriesUnderConcurrency.
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

// Taxonomy is the links-free subset of Meta: the scalar taxonomy fields only.
// Being all strings it needs no defensive copy, which is the point — see
// LookupTaxonomy.
type Taxonomy struct {
	Class        string
	Order        string
	Family       string
	FamilyCommon string
}

// LookupTaxonomy returns only the scalar taxonomy fields for one scientific name,
// sharing LookupMeta's memo and dataset scan. Prefer it over LookupMeta wherever
// the caller never reads Links: LookupMeta must clone the memoized Links map on
// every call (including memo hits) to keep the shared entry immutable, and that
// allocation is pure waste for a caller that only wants Family.
func LookupTaxonomy(scientific string) (Taxonomy, bool) {
	m, ok := lookupMetaShared(scientific)
	return Taxonomy{
		Class:        m.Class,
		Order:        m.Order,
		Family:       m.Family,
		FamilyCommon: m.FamilyCommon,
	}, ok
}

// LookupMeta returns taxonomy/link metadata for one scientific name. The first
// lookup for a name scans the embedded dataset; the immutable result is then
// memoized so repeat lookups are O(1). The dataset-scan cost (see Lookup) is paid
// only on the first, uncached lookup of each name.
func LookupMeta(scientific string) (Meta, bool) {
	m, ok := lookupMetaShared(scientific)
	// m.Links is the memoized map, shared with the cache and every other caller,
	// so hand out a clone.
	return m.clone(), ok
}

// lookupMetaShared is the shared body of LookupMeta and LookupTaxonomy. It returns
// the memoized Meta AS STORED: the Links map is the shared one, so callers must
// clone it before returning it outside this package or reading it after any
// mutation. Only LookupMeta exposes Links, and it clones.
func lookupMetaShared(scientific string) (Meta, bool) {
	// The guide metadata path can be reached without BuildIndex, so run the
	// schema gate here too (sync.Once-guarded; logs at most once per run).
	warnOnSchemaMismatch()
	target := normalizeName(scientific)
	if v, ok := metaCache.Load(target); ok {
		if e, ok := v.(metaCacheEntry); ok {
			return e.meta, e.found
		}
	}
	var found Meta
	var ok bool
	// Decode the name first and the rest only on a match. A full decode of every
	// record allocates a Links map per species (~15k of them) just to discard all
	// but one; naming-first skips that entirely for non-matching lines.
	if err := streamMetadataNames(func(sci string, line []byte) error {
		if normalizeName(sci) != target {
			return nil
		}
		var rec metadataRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return err
		}
		found = Meta{
			Class:        rec.Taxonomy.Class,
			Order:        rec.Taxonomy.Order,
			Family:       rec.Taxonomy.Family,
			FamilyCommon: rec.Taxonomy.FamilyCommon,
			Links:        rec.Links,
		}
		ok = true
		return errStop
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

// commonNameCacheMaxEntries bounds the LookupCommonName memo. It sits above the
// ~15k-species dataset multiplied by a handful of actively-used locales, so every
// real (species, locale) pair can be memoized while a flood of distinct never-present
// names cannot grow the memo without limit.
const commonNameCacheMaxEntries = 60000

// commonNameCacheEntry is a memoized LookupCommonName result. found distinguishes a
// cached "resolved" entry from a cached "no translation" one so negative lookups are
// memoized too.
type commonNameCacheEntry struct {
	name  string
	found bool
}

var (
	// commonNameCache memoizes LookupCommonName keyed by effective-locale + normalized
	// scientific name. Like metaCache it is correct lock-free BECAUSE the embedded
	// dataset is immutable (append-only, no update/evict), so a (name, locale) result
	// never changes. This avoids the full translations-dataset decompress+scan on the
	// guide provider's per-name cache-miss/warm/refresh path.
	commonNameCache      sync.Map     // "eff\x00norm" -> commonNameCacheEntry
	commonNameCacheCount atomic.Int64 // approximate entry count guarding the soft cap
)

// storeCommonNameCache records a LookupCommonName result under the soft cap using the
// same lock-free reserve-then-LoadOrStore pattern as storeMetaCache (exact because the
// memo is append-only). See TestStoreMetaCache_CountMatchesEntriesUnderConcurrency for
// the reasoning that applies identically here.
func storeCommonNameCache(key string, e commonNameCacheEntry) {
	if _, loaded := commonNameCache.Load(key); loaded {
		return
	}
	if commonNameCacheCount.Add(1) > commonNameCacheMaxEntries {
		commonNameCacheCount.Add(-1)
		return
	}
	if _, loaded := commonNameCache.LoadOrStore(key, e); loaded {
		commonNameCacheCount.Add(-1)
	}
}

// LookupCommonName returns the localized common name for a SINGLE scientific name in
// the locale mapped from bngLocale (with the dataset's English fallback), memoizing
// the result (present or absent) so repeat lookups are O(1). Unlike the batched
// LookupCommonNames — which scans the whole translations dataset on every call and is
// documented as cold-path only — this memoized form is safe on the guide cache-miss,
// startup-warm, and periodic-refresh paths, which resolve one name at a time.
func LookupCommonName(scientificName, bngLocale string) (string, bool) {
	norm := normalizeName(scientificName)
	if norm == "" {
		return "", false
	}
	eff := mapLocale(bngLocale)
	key := eff + "\x00" + norm
	if v, ok := commonNameCache.Load(key); ok {
		if e, ok := v.(commonNameCacheEntry); ok {
			return e.name, e.found
		}
	}
	names := lookupCommonNamesEffective([]string{scientificName}, eff)
	name, found := names[scientificName]
	storeCommonNameCache(key, commonNameCacheEntry{name: name, found: found})
	return name, found
}

// PrimeCaches resolves a batch of scientific names in one pass over each embedded
// dataset and populates the LookupCommonName and LookupMeta memos with the results,
// so subsequent single-name lookups for those species are served from memory.
//
// It exists for the startup warm and any other caller that already knows its whole
// working set. Resolving N species one at a time costs N passes: LookupCommonName
// and LookupMeta each decompress and scan an embedded blob on a memo miss, and the
// translations blob alone inflates to ~20 MB. Priming 50 species — the default warm
// target — therefore drops from roughly a hundred scans to two, which on the
// Raspberry-Pi-class hardware this project targets is the difference between tens of
// seconds of contended CPU at startup and a fraction of one.
//
// Names already memoized are excluded from both passes, and a pass whose work set
// comes out empty does not scan at all — so a fully-primed batch costs one map
// lookup per name and nothing else. That is what makes this safe to call
// unconditionally on a request path (see the similar-species fan-out), where the
// same species are asked for over and over.
//
// Absent species are memoized as absent, so a name the dataset does not carry is not
// re-scanned later either. Safe to call concurrently; the memos are append-only.
func PrimeCaches(scientificNames []string, bngLocale string) {
	if len(scientificNames) == 0 {
		return
	}
	eff := mapLocale(bngLocale)
	primeCommonNames(scientificNames, eff)
	primeMeta(scientificNames)
}

// unprimedCommonNames returns the members of scientificNames whose common name is
// not already memoized for eff, in input order and without blanks or duplicates.
// The ORIGINAL input strings are returned, not their normalized forms, because
// lookupCommonNamesEffective keys its result map by what it was given.
func unprimedCommonNames(scientificNames []string, eff string) []string {
	out := make([]string, 0, len(scientificNames))
	seen := make(map[string]struct{}, len(scientificNames))
	for _, raw := range scientificNames {
		norm := normalizeName(raw)
		if norm == "" {
			continue
		}
		if _, dup := seen[norm]; dup {
			continue
		}
		seen[norm] = struct{}{}
		if _, cached := commonNameCache.Load(eff + "\x00" + norm); cached {
			continue
		}
		out = append(out, raw)
	}
	return out
}

// unprimedMetaNames returns the normalized names from scientificNames whose metadata
// is not already memoized, dropping blanks.
func unprimedMetaNames(scientificNames []string) map[string]struct{} {
	want := make(map[string]struct{}, len(scientificNames))
	for _, raw := range scientificNames {
		norm := normalizeName(raw)
		if norm == "" {
			continue
		}
		if _, cached := metaCache.Load(norm); cached {
			continue
		}
		want[norm] = struct{}{}
	}
	return want
}

// primeCommonNames memoizes the common name of every not-yet-memoized name in one
// pass over the translations blob.
func primeCommonNames(scientificNames []string, eff string) {
	todo := unprimedCommonNames(scientificNames, eff)
	if len(todo) == 0 {
		return
	}
	// Scanning for the whole batch is never worse than scanning per name: the scan
	// stops as soon as the LAST outstanding name resolves, where N separate lookups
	// each pay their own prefix of the same blob.
	resolved := lookupCommonNamesEffective(todo, eff)
	for _, raw := range todo {
		name, found := resolved[raw]
		// normalizeName is non-empty here by construction (blanks were dropped above).
		storeCommonNameCache(eff+"\x00"+normalizeName(raw), commonNameCacheEntry{name: name, found: found})
	}
}

// primeMeta memoizes the metadata of every not-yet-memoized name in one pass over
// the metadata blob.
func primeMeta(scientificNames []string) {
	want := unprimedMetaNames(scientificNames)
	if len(want) == 0 {
		return
	}
	// This path substitutes for LookupMeta, so it owes the same schema gate
	// (sync.Once-guarded; logs at most once per run).
	warnOnSchemaMismatch()
	found := make(map[string]Meta, len(want))
	// Decode names first and the full record only on a match, exactly as LookupMeta
	// does: a full decode of every record allocates a Links map per species (~15k of
	// them) to keep a handful. With the early exit below, priming a batch is then
	// bounded by what the single-name path would have paid for its slowest member.
	if err := streamMetadataNames(func(sci string, line []byte) error {
		norm := normalizeName(sci)
		if _, ok := want[norm]; !ok {
			return nil
		}
		if _, dup := found[norm]; dup {
			return nil
		}
		var rec metadataRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return err
		}
		found[norm] = Meta{
			Class:        rec.Taxonomy.Class,
			Order:        rec.Taxonomy.Order,
			Family:       rec.Taxonomy.Family,
			FamilyCommon: rec.Taxonomy.FamilyCommon,
			Links:        rec.Links,
		}
		if len(found) == len(want) {
			return errStop
		}
		return nil
	}); err != nil && !errors.Is(err, errStop) {
		// Leave the memo untouched so a transient scan failure is not cached as
		// "absent"; the per-name path will retry.
		GetLogger().Warn("openfauna cache priming failed", logger.Error(err))
		return
	}
	for norm := range want {
		m, ok := found[norm]
		storeMetaCache(norm, &metaCacheEntry{meta: m, found: ok})
	}
}

// localesShared parses the embedded locale list exactly once and returns the shared
// slice. Callers must treat it as read-only. The embedded blob is immutable, so the
// per-call split + TrimSpace this replaces was pure repetition — and it was on the
// request path: mapLocale consults it on every LookupCommonName, including memo
// hits, which the species guide made a per-species-per-request call rather than the
// Rebuild-only one the original comment assumed.
var localesShared = sync.OnceValue(func() []string {
	lines := strings.Split(string(localesList), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if l := strings.TrimSpace(line); l != "" {
			out = append(out, l)
		}
	}
	return out
})

// Locales returns the sorted list of locale codes available in the dataset
// (e.g. "en", "fi", "de", "en_uk", "zh_cn"). The codes use underscores and may
// include regional variants; consumers map their own locale codes onto these.
//
// The returned slice is a copy, so callers may sort or filter it in place; in-package
// request-path callers use localesShared instead.
func Locales() []string {
	return slices.Clone(localesShared())
}
