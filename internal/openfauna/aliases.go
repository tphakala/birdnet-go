package openfauna

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// Taxonomic aliasing
//
// Acoustic models are trained on different taxonomies and time-frozen label sets,
// so they emit different scientific names for the same species: a legacy BirdNET
// v2.4 label like "Streptopelia senegalensis" versus the current eBird/Clements
// name "Spilopelia senegalensis" that the v3 geomodel (and Perch) use. OpenFauna
// is the authoritative source for these reclassifications; its compiled dataset
// ships an alias map (legacy scientific name -> canonical scientific name) that
// this package exposes so consumers can collapse the variants to one canonical
// name (range-filter species matching, detection de-duplication).
//
// The map is small (a few hundred entries), so it is parsed once on first use and
// kept in memory, unlike the large streaming translation/metadata artifacts.

var (
	aliasOnce sync.Once
	aliasMap  map[string]string // normalized legacy scientific name -> canonical scientific name (dataset case)
)

// loadAliases parses the embedded alias map once. Keys are normalized for
// case-insensitive matching; values keep the dataset's canonical casing. A parse
// failure logs and leaves an empty map so callers degrade to identity resolution
// rather than panicking on a corrupt artifact.
func loadAliases() {
	var raw map[string]string
	if err := json.Unmarshal(aliasesJSON, &raw); err != nil {
		GetLogger().Error("failed to parse embedded openfauna aliases.json; taxonomic aliasing disabled",
			logger.Error(err),
		)
		aliasMap = map[string]string{}
		return
	}
	m := make(map[string]string, len(raw))
	for legacy, canonical := range raw {
		// Normalize both sides before validating. Using the normalized key for the
		// guards means a whitespace-only legacy key (which normalizes to "") cannot
		// map "" to a canonical name (which would make CanonicalName("") wrongly
		// return that name), and a case- or whitespace-only "self alias" is skipped
		// as the no-op it is. The stored value is trimmed so CanonicalName always
		// returns a stable key form.
		normLegacy := normalizeName(legacy)
		canonical = strings.TrimSpace(canonical)
		if normLegacy == "" || canonical == "" || normLegacy == normalizeName(canonical) {
			continue
		}
		m[normLegacy] = canonical
	}
	aliasMap = m
}

// CanonicalName resolves a possibly-reclassified scientific name to its canonical
// (current) form using the OpenFauna taxonomic alias map. A name that is not a
// known alias is returned unchanged, so callers can apply it unconditionally.
// Matching is case-insensitive (whitespace-trimmed); a matched name is returned
// in the dataset's canonical casing.
//
// This is the single point where legacy and modern names for one taxon are
// collapsed, so model label sets, range-filter mappings, and stored detections
// can agree on one scientific name per species.
func CanonicalName(scientific string) string {
	aliasOnce.Do(loadAliases)
	if canonical, ok := aliasMap[normalizeName(scientific)]; ok {
		return canonical
	}
	// Trim the identity path too, so an unaliased name returns the same stable key
	// form as an aliased one (callers lowercasing the result for lookup keys must
	// not have "  Spilopelia senegalensis  " miss the species it equals).
	return strings.TrimSpace(scientific)
}

// AliasCount returns the number of taxonomic aliases loaded from the embedded
// dataset. It is intended for startup logging and tests, not a hot path.
func AliasCount() int {
	aliasOnce.Do(loadAliases)
	return len(aliasMap)
}
