package guideprovider

import (
	"context"
	"strings"

	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// openFaunaLookup is the subset of the embedded OpenFauna dataset the provider
// needs. Declaring it as an interface keeps the provider unit-testable with
// synthetic data instead of depending on specific rows in the embedded snapshot.
type openFaunaLookup interface {
	// Meta returns taxonomy/link metadata for a scientific name, if present.
	Meta(scientificName string) (openfauna.Meta, bool)
	// CommonName returns the localized common name for a scientific name in the
	// locale mapped from bngLocale (with the dataset's English fallback), if any.
	CommonName(scientificName, bngLocale string) (string, bool)
}

// embeddedOpenFauna is the production openFaunaLookup, backed by the package-level
// helpers over the vendored, embedded dataset. Both calls scan the dataset, so this
// is only used on the cache-miss path (the same place eBird made a network call).
type embeddedOpenFauna struct{}

func (embeddedOpenFauna) Meta(scientificName string) (openfauna.Meta, bool) {
	return openfauna.LookupMeta(scientificName)
}

func (embeddedOpenFauna) CommonName(scientificName, bngLocale string) (string, bool) {
	names := openfauna.LookupCommonNames([]string{scientificName}, bngLocale)
	name, ok := names[scientificName]
	return name, ok
}

// OpenFaunaGuideProvider enriches guides with offline taxonomy (genus/family) and a
// locale-aware common name sourced from the embedded OpenFauna dataset. Like the
// eBird provider it carries no prose description of its own; in the fallback="all"
// merge it fills the taxonomy gaps left by Wikipedia — without any network call,
// API key, or rate limit. It replaces the eBird taxonomy enrichment provider.
type OpenFaunaGuideProvider struct {
	lookup openFaunaLookup
}

// NewOpenFaunaGuideProviderWithMetrics constructs an OpenFauna provider. It needs no
// credentials and cannot fail to build. The metrics sink is recorded by the cache
// around Fetch, so it is accepted for signature compatibility but not retained.
func NewOpenFaunaGuideProviderWithMetrics(_ GuideCacheMetrics) *OpenFaunaGuideProvider {
	return &OpenFaunaGuideProvider{lookup: embeddedOpenFauna{}}
}

// Name returns the provider's registration name.
func (p *OpenFaunaGuideProvider) Name() string { return OpenFaunaProviderName }

// Fetch returns offline taxonomy enrichment for a species. Genus is derived from the
// binomial's first token; family comes from the dataset metadata; the common name is
// resolved for the requested locale (with the dataset's English fallback). A species
// absent from the dataset (no metadata and no common name) yields ErrGuideNotFound so
// it never downgrades an otherwise-complete primary (Wikipedia) guide.
func (p *OpenFaunaGuideProvider) Fetch(_ context.Context, scientificName string, opts FetchOptions) (*SpeciesGuide, error) {
	meta, hasMeta := p.lookup.Meta(scientificName)
	commonName, hasCommon := p.lookup.CommonName(scientificName, opts.Locale)
	if !hasMeta && !hasCommon {
		return nil, ErrGuideNotFound
	}

	// Genus is the first whitespace-delimited token of the binomial (e.g. "Turdus"
	// from "Turdus merula"). Original casing is preserved for display.
	genus, _, _ := strings.Cut(strings.TrimSpace(scientificName), " ")

	return &SpeciesGuide{
		ScientificName: scientificName,
		CommonName:     commonName,
		Genus:          genus,
		Family:         meta.Family,
		SourceProvider: OpenFaunaProviderName,
	}, nil
}
