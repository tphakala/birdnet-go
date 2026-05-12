// taxonomy_resolver.go provides multilingual common name resolution using
// the BirdNET v3.0 geomodel taxonomy.csv, covering ~13,361 species in 29
// languages plus English.
package classifier

import (
	"encoding/csv"
	"os"
	"strings"
)

// taxonomyLocaleColumns maps BirdNET-Go locale codes to taxonomy.csv column
// names. Locales not listed here fall back to the English "com_name" column.
var taxonomyLocaleColumns = map[string]string{
	"bg":    "common_name_bg",
	"ca":    "common_name_ca",
	"cs":    "common_name_cs",
	"da":    "common_name_da",
	"de":    "common_name_de",
	"es":    "common_name_es",
	"et":    "common_name_et",
	"fi":    "common_name_fi",
	"fr":    "common_name_fr",
	"hr":    "common_name_hr",
	"ja":    "common_name_ja",
	"lt":    "common_name_lt",
	"nl":    "common_name_nl",
	"no":    "common_name_no",
	"pl":    "common_name_pl",
	"pt":    "common_name_pt",
	"pt-br": "common_name_pt",
	"pt-pt": "common_name_pt_PT",
	"ru":    "common_name_ru",
	"sk":    "common_name_sk",
	"sr":    "common_name_sr",
	"sv":    "common_name_sv",
	"tr":    "common_name_tr",
	"uk":    "common_name_uk",
	"zh":    "common_name_zh-CN",
}

// TaxonomyResolver resolves scientific names to common names using the
// BirdNET v3.0 geomodel taxonomy.csv. The index is built at construction
// time for the configured locale, with fallback to English common names.
//
// Implements NameResolver.
type TaxonomyResolver struct {
	index map[string]string
}

// NewTaxonomyResolver creates a resolver by parsing taxonomyPath for the
// given locale. If the locale has no dedicated column in taxonomy.csv,
// English common names (com_name column) are used.
func NewTaxonomyResolver(taxonomyPath, locale string) (*TaxonomyResolver, error) {
	f, err := os.Open(taxonomyPath) //nolint:gosec // G304: path from catalog metadata
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	reader := csv.NewReader(f)
	reader.LazyQuotes = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	if len(records) < 2 {
		return &TaxonomyResolver{index: make(map[string]string)}, nil
	}

	header := records[0]

	sciCol := findColumn(header, "sci_name")
	if sciCol < 0 {
		return &TaxonomyResolver{index: make(map[string]string)}, nil
	}

	nameCol := resolveLocaleColumn(header, locale)

	index := make(map[string]string, len(records)-1)
	for _, row := range records[1:] {
		if sciCol >= len(row) || nameCol >= len(row) {
			continue
		}
		sci := strings.ToLower(strings.TrimSpace(row[sciCol]))
		name := strings.TrimSpace(row[nameCol])
		if sci != "" && name != "" {
			index[sci] = name
		}
	}

	return &TaxonomyResolver{index: index}, nil
}

// Resolve returns the common name for a scientific name.
// The locale parameter is accepted for interface compliance but unused;
// the locale is selected at construction time.
func (r *TaxonomyResolver) Resolve(scientificName, _ string) string {
	if r.index == nil {
		return ""
	}
	return r.index[strings.ToLower(scientificName)]
}

// resolveLocaleColumn finds the best column index for the given locale.
// Falls back to the English "com_name" column if no locale-specific column
// exists.
func resolveLocaleColumn(header []string, locale string) int {
	locale = strings.ToLower(locale)

	if colName, ok := taxonomyLocaleColumns[locale]; ok {
		if idx := findColumn(header, colName); idx >= 0 {
			return idx
		}
	}

	if idx := findColumn(header, "com_name"); idx >= 0 {
		return idx
	}

	return 0
}

// findColumn returns the index of the named column in header, or -1.
func findColumn(header []string, name string) int {
	for i, h := range header {
		if strings.EqualFold(strings.TrimSpace(h), name) {
			return i
		}
	}
	return -1
}
