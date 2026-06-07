// Package openfauna provides read-only, memory-frugal lookups of species common
// names (translations across many locales) and taxonomic metadata, embedded from
// a vendored copy of the compiled OpenFauna dataset
// (https://github.com/tphakala/openfauna).
//
// The dataset is large (tens of thousands of species across 40+ locales), so the
// package never materializes all of it. Build a sparse Index for just the species
// and locale you need with BuildIndex, and use Lookup/LookupMeta for the rare
// species that fall outside a pre-built Index.
//
// The embedded data is a committed, gzipped snapshot; see README.md for the
// command that regenerates it from an openfauna checkout.
package openfauna

import (
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"io"
	"strings"

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

// Column header names of the metadata CSV. Metadata is decoded by header name
// rather than fixed position so that future columns added upstream do not break
// this decoder or an already-shipped consumer.
const (
	metaColScientific   = "scientific_name"
	metaColClass        = "class"
	metaColOrder        = "order"
	metaColFamily       = "family"
	metaColFamilyCommon = "family_common"
	metaColWikipedia    = "wikipedia_url"
	metaColINaturalist  = "inaturalist_url"
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

// Meta holds taxonomy and external-link metadata for a species, sourced from the
// GBIF backbone taxonomy, Wikipedia, and the iNaturalist Open Data taxonomy.
//
// The upstream metadata schema is designed to grow over time (for example with
// thumbnails or conservation status). New columns are added to the embedded CSV
// without breaking this package because metadata is decoded by column header
// name; consuming a new column simply means adding a field here in a later update.
type Meta struct {
	Class          string
	Order          string
	Family         string
	FamilyCommon   string
	WikipediaURL   string
	INaturalistURL string
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

// streamMetadata decodes the embedded metadata.csv.gz row by row. Columns are
// resolved by header name so additional metadata columns can be appended to the
// dataset without breaking this decoder or pinned consumers. It never holds more
// than one row in memory.
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

// decodeMetadataRows reads the (uncompressed) metadata CSV from src and calls fn
// for each data row. Columns are resolved by header name so additional metadata
// columns can be appended upstream without breaking this decoder. Split out from
// streamMetadata so the header-mapping behaviour can be tested with synthetic data.
func decodeMetadataRows(src io.Reader, fn metaRowFunc) error {
	r := csv.NewReader(src)
	r.ReuseRecord = true
	// FieldsPerRecord stays at its zero default: the reader infers the column
	// count from the header and enforces it for every row, validating the schema
	// width without hardcoding it (the metadata schema grows over time).

	header, err := r.Read()
	if err != nil {
		return errors.New(err).
			Component(loggerModule).
			Category(errors.CategoryFileParsing).
			Context("operation", "read_metadata_header").
			Build()
	}
	col := make(map[string]int, len(header))
	for i, name := range header {
		col[strings.TrimSpace(name)] = i
	}
	sciIdx, ok := col[metaColScientific]
	if !ok {
		return errors.Newf("openfauna: metadata header missing %q column", metaColScientific).
			Component(loggerModule).
			Category(errors.CategoryFileParsing).
			Context("operation", "validate_metadata_header").
			Build()
	}
	// get returns the value for a named column, or "" if the column is absent or
	// the row is short. Copying the string out of the (reused) record is safe.
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return rec[i]
		}
		return ""
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
				Context("operation", "read_metadata_row").
				Build()
		}
		if sciIdx >= len(rec) {
			continue
		}
		m := Meta{
			Class:          get(rec, metaColClass),
			Order:          get(rec, metaColOrder),
			Family:         get(rec, metaColFamily),
			FamilyCommon:   get(rec, metaColFamilyCommon),
			WikipediaURL:   get(rec, metaColWikipedia),
			INaturalistURL: get(rec, metaColINaturalist),
		}
		if cbErr := fn(rec[sciIdx], m); cbErr != nil {
			return cbErr
		}
	}
}

// BuildIndex streams the embedded dataset once and returns a sparse Index holding
// only the requested scientific names, with common names for the given locale and
// metadata for those species. Names not present in the dataset are simply absent.
// An unrecognized locale yields an Index with no translations (still valid).
//
// Memory: only matching rows are retained; the full dataset is never held at once.
func BuildIndex(scientificNames []string, locale string) (*Index, error) {
	want := make(map[string]struct{}, len(scientificNames))
	for _, n := range scientificNames {
		want[strings.TrimSpace(n)] = struct{}{}
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
		if _, ok := want[sci]; ok {
			ix.names[sci] = common
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
		if _, ok := want[sci]; ok {
			ix.meta[sci] = m
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
	v, ok := ix.names[strings.TrimSpace(scientific)]
	return v, ok
}

// Meta returns taxonomy/link metadata for a scientific name, if present. A nil
// Index reports not-found rather than panicking.
func (ix *Index) Meta(scientific string) (Meta, bool) {
	if ix == nil {
		return Meta{}, false
	}
	v, ok := ix.meta[strings.TrimSpace(scientific)]
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
	target := strings.TrimSpace(scientific)
	var found string
	var ok bool
	if err := streamTranslations(func(sci, loc, common string) error {
		if loc == locale && sci == target {
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

// LookupMeta returns taxonomy/link metadata for one scientific name by scanning
// the embedded dataset. Same performance caveat as Lookup.
func LookupMeta(scientific string) (Meta, bool) {
	target := strings.TrimSpace(scientific)
	var found Meta
	var ok bool
	if err := streamMetadata(func(sci string, m Meta) error {
		if sci == target {
			found, ok = m, true
			return errStop
		}
		return nil
	}); err != nil && !errors.Is(err, errStop) {
		GetLogger().Error("openfauna metadata lookup failed",
			logger.String("scientific", target),
			logger.Error(err),
		)
		return Meta{}, false
	}
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
