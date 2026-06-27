// canonical.go centralizes the canonical-name normalization used by the species
// tracker so one taxon has a single identity across the historical database load
// and the live detection path.
package species

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// canonicalSpeciesName collapses taxonomic aliases to the canonical scientific name
// via the OpenFauna alias map. Acoustic models emit different scientific names for
// one taxon (e.g. legacy "Streptopelia senegalensis" vs canonical "Spilopelia
// senegalensis"); keying every tracking map through this prevents a species whose
// history is recorded under a legacy name from firing a spurious "new species"
// alert when the same taxon is later detected under its current name. CanonicalName
// returns the (trimmed) input unchanged for non-aliased names, so species without a
// reclassification are unaffected.
func canonicalSpeciesName(scientificName string) string {
	return openfauna.CanonicalName(scientificName)
}

// keepEarliest stores ts under key, keeping the earliest time when several entries
// map to the same key. Canonicalization can collapse aliased scientific names
// (legacy + canonical) onto one key; keeping the earliest first-seen preserves the
// true first observation across the merged history.
func keepEarliest(m map[string]time.Time, key string, ts time.Time) {
	if existing, ok := m[key]; !ok || ts.Before(existing) {
		m[key] = ts
	}
}

// keepLatest stores ts under key, keeping the latest time when several entries map
// to the same key (e.g. the latest last-seen or notification time across collapsed aliases).
func keepLatest(m map[string]time.Time, key string, ts time.Time) {
	if existing, ok := m[key]; !ok || ts.After(existing) {
		m[key] = ts
	}
}
