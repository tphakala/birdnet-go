// taxonomy_synonyms.go: Provides scientific name synonym mappings for taxonomy changes.
// BirdNET V2.4 uses 2021E taxonomy, but image providers (Wikipedia, Avicommons) may use
// updated taxonomy where species have been reclassified to different genera.
// This map allows fallback lookups when the primary name fails.
package imageprovider

import (
	"strings"
	"sync"
)

// builtInTaxonomySynonyms maps BirdNET scientific names (2021E taxonomy) to their updated
// equivalents in newer taxonomy. When an image lookup fails with the BirdNET name,
// the provider retries with the synonym.
//
// Format: "old name" -> "new name"
// Names are stored in their canonical form (Title Case) and compared case-insensitively.
var builtInTaxonomySynonyms = map[string]string{
	// Accipiter → Astur reclassification (IOC 2022+)
	"Accipiter cooperii":      "Astur cooperii",
	"Accipiter gentilis":      "Astur gentilis",
	"Accipiter bicolor":       "Astur bicolor",
	"Accipiter melanoleucus":  "Astur melanoleucus",
	"Accipiter striatus":      "Astur striatus",
	"Accipiter superciliosus": "Astur superciliosus",

	// Corvus → Coloeus reclassification
	"Corvus monedula":  "Coloeus monedula",
	"Corvus dauuricus": "Coloeus dauuricus",

	// Streptopelia → Spilopelia reclassification
	"Streptopelia senegalensis": "Spilopelia senegalensis",
	"Streptopelia chinensis":    "Spilopelia chinensis",

	// Bubulcus → Ardea reclassification
	"Bubulcus ibis": "Ardea coromanda",
}

// synonymEntry stores a canonical old→updated pair for building lookup maps.
type synonymEntry struct {
	old     string // canonical old name (Title Case for built-ins, as-provided for overrides)
	updated string // canonical updated name
}

// Cached forward/reverse lookup maps. Protected by synonymMu.
var (
	synonymMu     sync.RWMutex
	cachedForward map[string]string
	cachedReverse map[string]string
)

func init() {
	// Build initial indexes from built-ins only (no overrides).
	cachedForward, cachedReverse = buildSynonymIndexes(nil)
}

// SetCustomSynonyms replaces the current config-based synonym overrides and
// rebuilds the cached lookup maps. Safe for concurrent use.
// Call this when settings are loaded or hot-reloaded.
func SetCustomSynonyms(overrides map[string]string) {
	synonymMu.Lock()
	defer synonymMu.Unlock()
	cachedForward, cachedReverse = buildSynonymIndexes(overrides)
}

// buildSynonymIndexes builds normalized forward and reverse lookup maps using
// built-in defaults plus optional config overrides.
//
// Viper lowercases map keys during YAML deserialization, so overrides arrive
// with lowercase keys (e.g., "bubulcus ibis") while built-ins use Title Case.
// We use lowercased keys in the merge map to ensure overrides properly replace
// built-ins regardless of casing.
func buildSynonymIndexes(overrides map[string]string) (forward, reverse map[string]string) {
	// Merge built-ins and overrides with case-insensitive deduplication.
	// Key: lowercased old name (for dedup). Value: canonical entry.
	merged := make(map[string]synonymEntry, len(builtInTaxonomySynonyms)+len(overrides))

	for old, updated := range builtInTaxonomySynonyms {
		merged[strings.ToLower(old)] = synonymEntry{old, updated}
	}

	for old, updated := range overrides {
		oldTrimmed := strings.TrimSpace(old)
		updatedTrimmed := strings.TrimSpace(updated)
		if oldTrimmed == "" || updatedTrimmed == "" {
			continue
		}
		merged[strings.ToLower(oldTrimmed)] = synonymEntry{oldTrimmed, updatedTrimmed}
	}

	// Build forward map first (fully populated before reverse).
	forward = make(map[string]string, len(merged))
	for lowerOld, e := range merged {
		forward[lowerOld] = e.updated
	}

	// Build reverse map: only add entries whose updated name is not itself
	// a forward key, preventing cycles in chained synonyms (A→B, B→C).
	reverse = make(map[string]string, len(merged))
	for _, e := range merged {
		lowerUpdated := strings.ToLower(e.updated)
		if _, isForwardKey := forward[lowerUpdated]; !isForwardKey {
			reverse[lowerUpdated] = e.old
		}
	}

	return forward, reverse
}

// GetTaxonomySynonym returns the alternate scientific name for a given name, if one exists.
// It checks both directions: BirdNET name → updated name, and updated name → BirdNET name.
// Returns the synonym and true if found, or empty string and false otherwise.
func GetTaxonomySynonym(scientificName string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(scientificName))
	if normalized == "" {
		return "", false
	}

	synonymMu.RLock()
	forward := cachedForward
	reverse := cachedReverse
	synonymMu.RUnlock()

	// Check forward: BirdNET name → updated name
	if updated, found := forward[normalized]; found {
		return updated, true
	}

	// Check reverse: updated name → BirdNET name
	if original, found := reverse[normalized]; found {
		return original, true
	}

	return "", false
}
