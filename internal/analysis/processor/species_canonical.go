// species_canonical.go provides the ingestion-time canonical-name normalization
// chokepoint. Acoustic models are trained on different, time-frozen taxonomies, so
// they emit different scientific names for one taxon (e.g. BirdNET v2.4
// "Streptopelia senegalensis" vs the eBird "Spilopelia senegalensis" the v3
// geomodel and Perch use). Collapsing every detection to the canonical scientific
// name at ingestion gives one stored identity per species so the same bird does
// not appear twice.
package processor

import (
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// taxonomyResolver resolves a species label to its scientific name, common name,
// and taxonomy code. *classifier.Orchestrator satisfies it; tests use a fake. The
// interface keeps canonicalizeSpecies unit-testable without a loaded model.
type taxonomyResolver interface {
	EnrichResultWithTaxonomy(label string) (scientific, common, code string)
}

// canonicalizeSpecies resolves a model-emitted scientific name to its canonical
// form via the OpenFauna taxonomic alias map. When an alias applies it returns the
// canonical scientific name, re-resolves the common name and taxonomy code FOR THE
// CANONICAL NAME (so the canonical identity is never paired with the legacy name's
// common name or code), and returns the legacy name as raw so nothing is lost. When
// no alias applies, the inputs pass through unchanged and raw is empty; the resolver
// is not consulted, so the hot path pays only one alias-map lookup per detection.
func canonicalizeSpecies(resolver taxonomyResolver, scientificName, commonName, speciesCode string) (canonicalSci, common, code, raw string) {
	canonical := openfauna.CanonicalName(scientificName)
	if canonical == scientificName {
		return scientificName, commonName, speciesCode, ""
	}
	// Re-resolve common name and code for the canonical name. EnrichResultWithTaxonomy
	// re-runs the same taxonomy/OpenFauna resolution path used for the original label,
	// keyed on the canonical scientific name. The orchestrator fills the common name via
	// its name-resolver chain (the bare scientific name carries no "_Common" suffix for
	// SplitSpeciesName to split), so this is normally non-empty for a genuine alias.
	_, canonicalCommon, canonicalCode := resolver.EnrichResultWithTaxonomy(canonical)
	// Defensive: if the canonical name has no resolvable common name (rare: a
	// reclassified taxon the active locale/dataset does not localize), keep the model's
	// original common name rather than emitting an empty one. For a genuine alias the
	// two share a common name, and a non-empty value avoids degrading the detection to a
	// bare scientific name downstream.
	if canonicalCommon == "" {
		canonicalCommon = commonName
	}
	return canonical, canonicalCommon, canonicalCode, scientificName
}
