// species_resolve.go holds the shared species-name resolution and exclude-list
// canonicalization helpers. They are pure functions over a common->scientific
// lookup map (the caller supplies its own snapshot via the name-map accessors),
// so they live on the shared substrate without depending on the facade-owned name
// maps. Shared by the detections search resolver, the analytics species filter,
// and the settings/detections exclude-list handling.
package apicore

import (
	"slices"
	"strings"
)

// ResolveCommonName looks up the common name for a scientific name in the cached
// scientific->common map, returning the scientific name itself as the fallback.
// The map is already localized at build time (the name-map builder applies the
// resolver override), so this is a plain lookup. It differs from the datastore's
// resolveCommonName method, which consults the live resolver per call. Shared by
// the insights endpoints and the detections response builder.
func ResolveCommonName(nameMap map[string]string, scientificName string) string {
	if cn, ok := nameMap[scientificName]; ok {
		return cn
	}
	return scientificName
}

// ResolveSpeciesToScientific resolves a (possibly localized common) name to a
// scientific name using the supplied common->scientific lookup map. Returns
// (resolved, true) on a hit, or (trimmedInput, false) when the input is empty or
// does not map to a known name. The map keys are NFC-normalized lowercase common
// names (see NormalizeForLookup), matching how the name maps are built.
func ResolveSpeciesToScientific(lookup map[string]string, input string) (resolved string, hit bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", false
	}
	if scientific, ok := lookup[NormalizeForLookup(trimmed)]; ok {
		return scientific, true
	}
	return trimmed, false
}

// ResolveExcludeName resolves an exclude-list entry to its scientific name using
// the lookup map, or returns the input unchanged when it does not resolve.
func ResolveExcludeName(lookup map[string]string, name string) string {
	if resolved, hit := ResolveSpeciesToScientific(lookup, name); hit {
		return resolved
	}
	return name
}

// ExcludeEntryMatches reports whether an exclude-list entry matches target,
// comparing case-insensitively both directly and after resolving the entry to its
// scientific name via the lookup map.
func ExcludeEntryMatches(lookup map[string]string, entry, target string) bool {
	if strings.EqualFold(entry, target) {
		return true
	}
	if resolved, hit := ResolveSpeciesToScientific(lookup, entry); hit {
		return strings.EqualFold(resolved, target)
	}
	return false
}

// CanonicalizeExcludeList canonicalizes a species exclude list: it trims entries,
// resolves each to its scientific name (so the per-detection filter and the
// detection-card toggle match), drops blanks, and de-duplicates case-insensitively.
// Returns nil for an empty/all-blank input so the result compares equal to a nil
// stored list under reflect.DeepEqual. Idempotent for an already-canonical list.
func CanonicalizeExcludeList(lookup map[string]string, exclude []string) []string {
	if len(exclude) == 0 {
		return nil
	}
	canonical := make([]string, 0, len(exclude))
	for _, entry := range exclude {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		resolved := ResolveExcludeName(lookup, trimmed)
		// Dedup with EqualFold for parity with ExcludeEntryMatches (both operands are
		// already resolved here, so no second resolution is needed). Uses the same
		// slices.ContainsFunc idiom the exclude-list toggle/add paths use; lists are
		// small, so the linear scan is fine.
		if slices.ContainsFunc(canonical, func(existing string) bool {
			return strings.EqualFold(existing, resolved)
		}) {
			continue
		}
		canonical = append(canonical, resolved)
	}
	if len(canonical) == 0 {
		return nil
	}
	return canonical
}
