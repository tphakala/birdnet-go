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
// canonical scientific name, re-resolves the common name for the canonical name, and
// returns the legacy name as raw so nothing is lost. When no alias applies, the
// inputs pass through unchanged and raw is empty; the resolver is not consulted, so
// the hot path pays only one alias-map lookup per detection.
//
// The eBird code is deliberately NOT re-resolved: the embedded eBird taxonomy is a
// frozen 2021 snapshot keyed on the legacy name, and eBird species codes are stable
// across a pure rename (the only kind aliases.json contains), so the legacy name's
// code IS the canonical taxon's real code. Re-resolving by the canonical name would
// miss the frozen snapshot and degrade the real code to a synthetic placeholder. The
// common name, by contrast, IS re-resolved: OpenFauna localizes both the legacy and
// canonical names, so the canonical resolution is reliable (and equals the legacy
// one for a genuine alias).
func canonicalizeSpecies(resolver taxonomyResolver, scientificName, commonName, speciesCode string) (canonicalSci, common, code, raw string) {
	canonical := openfauna.CanonicalName(scientificName)
	if canonical == scientificName {
		return scientificName, commonName, speciesCode, ""
	}
	// Re-resolve the common name for the canonical name. EnrichResultWithTaxonomy
	// re-runs the taxonomy/OpenFauna resolution path keyed on the canonical scientific
	// name; the orchestrator fills the common name via its name-resolver chain (the
	// bare scientific name carries no "_Common" suffix for SplitSpeciesName to split),
	// so this is normally non-empty for a genuine alias.
	_, canonicalCommon, _ := resolver.EnrichResultWithTaxonomy(canonical)
	// Defensive: if the canonical name has no resolvable common name (rare: a
	// reclassified taxon the active locale/dataset does not localize), keep the model's
	// original common name rather than emitting an empty one. For a genuine alias the
	// two share a common name, and a non-empty value avoids degrading the detection to a
	// bare scientific name downstream.
	if canonicalCommon == "" {
		canonicalCommon = commonName
	}
	return canonical, canonicalCommon, speciesCode, scientificName
}
