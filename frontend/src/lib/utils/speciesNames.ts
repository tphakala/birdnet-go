/**
 * Species name resolution utilities for bidirectional lookup
 * between common names and scientific names.
 *
 * Mirrors the backend pattern in internal/datastore/v2only/datastore.go
 * (nameMaps struct with common and species maps).
 */

/** Shape of a species entry from /api/v2/species/all */
export interface SpeciesApiEntry {
  commonName?: string;
  scientificName?: string;
  label: string;
}

/** Bidirectional species name lookup maps */
export interface SpeciesNameMaps {
  /** commonName (lowercase) -> scientificName */
  commonToScientific: Map<string, string>;
  /** scientificName (lowercase) -> commonName */
  scientificToCommon: Map<string, string>;
  /** All searchable names (both common and scientific, deduplicated) */
  allNames: string[];
}

/** Resolved display names for a species list entry */
export interface ResolvedDisplayNames {
  displayCommonName: string;
  displayScientificName: string;
}

/**
 * Normalize a name for case- and Unicode-form-insensitive lookup. Mirrors the
 * backend normalizeForLookup (strings.ToLower(norm.NFC.String(s))): labels ship
 * as NFC, but composing keyboards (macOS) submit NFD bytes for diacritics, so both
 * sides are normalized to NFC before lowercasing to prevent silent misses.
 */
export function normalizeForLookup(s: string): string {
  return s.normalize('NFC').toLowerCase();
}

/**
 * Build bidirectional species name lookup maps from API response.
 * Skips entries missing a scientific name.
 * Mirrors the backend delete-on-conflict: when two distinct scientific names
 * share a normalized common name, that key is removed from commonToScientific
 * so an ambiguous common name passes through unresolved instead of resolving
 * to an arbitrary species.
 */
export function buildSpeciesNameMaps(species: SpeciesApiEntry[]): SpeciesNameMaps {
  const commonToScientific = new Map<string, string>();
  const scientificToCommon = new Map<string, string>();
  const namesSet = new Set<string>();
  const ambiguousCommon = new Set<string>();

  for (const s of species) {
    const commonName = s.commonName ?? s.label;
    const scientificName = s.scientificName ?? '';

    // Always add common name to allNames for autocomplete, even without scientific name
    if (commonName) {
      namesSet.add(commonName);
    }

    // Only build bidirectional maps when both names are present
    if (!scientificName || !commonName) continue;

    const commonKey = normalizeForLookup(commonName);
    // Mirror the backend delete-on-conflict: when two distinct scientific names
    // share a normalized common name, drop that key so an ambiguous common name
    // passes through unresolved instead of resolving to an arbitrary species.
    if (!ambiguousCommon.has(commonKey)) {
      const existing = commonToScientific.get(commonKey);
      if (existing !== undefined && existing !== scientificName) {
        ambiguousCommon.add(commonKey);
        commonToScientific.delete(commonKey);
      } else {
        commonToScientific.set(commonKey, scientificName);
      }
    }

    // scientificToCommon keeps every species (scientific names are unique).
    scientificToCommon.set(normalizeForLookup(scientificName), commonName);
    namesSet.add(scientificName);
  }

  return {
    commonToScientific,
    scientificToCommon,
    allNames: [...namesSet],
  };
}

/**
 * Resolve display names for a species list entry.
 * Determines whether the stored value is a common name or scientific name,
 * then returns the correct pair for table display.
 *
 * Priority: check scientificToCommon first (detects scientific names),
 * then check commonToScientific (detects common names),
 * then fall back to showing the raw value as common name.
 */
export function resolveSpeciesDisplayNames(
  storedValue: string,
  maps: SpeciesNameMaps
): ResolvedDisplayNames {
  if (!storedValue) {
    return { displayCommonName: '', displayScientificName: '' };
  }

  const lower = normalizeForLookup(storedValue);

  // Check if it's a scientific name
  const resolvedCommon = maps.scientificToCommon.get(lower);
  if (resolvedCommon !== undefined) {
    return {
      displayCommonName: resolvedCommon,
      displayScientificName: storedValue,
    };
  }

  // Check if it's a common name
  const resolvedScientific = maps.commonToScientific.get(lower);
  if (resolvedScientific !== undefined) {
    return {
      displayCommonName: storedValue,
      displayScientificName: resolvedScientific,
    };
  }

  // Unknown: show as common name with empty scientific
  return {
    displayCommonName: storedValue,
    displayScientificName: '',
  };
}

/**
 * Check if a species (by any name alias) is already in a list.
 * Prevents adding "Strix aluco" when "Tawny Owl" is already present (and vice versa).
 */
export function isSpeciesInList(candidate: string, list: string[], maps: SpeciesNameMaps): boolean {
  const candidateLower = normalizeForLookup(candidate);
  const listLowerSet = new Set(list.map(s => normalizeForLookup(s)));

  // Direct match
  if (listLowerSet.has(candidateLower)) return true;

  // Check if candidate's alias is in the list
  const aliasFromScientific = maps.scientificToCommon.get(candidateLower);
  if (
    aliasFromScientific !== undefined &&
    listLowerSet.has(normalizeForLookup(aliasFromScientific))
  )
    return true;

  const aliasFromCommon = maps.commonToScientific.get(candidateLower);
  if (aliasFromCommon !== undefined && listLowerSet.has(normalizeForLookup(aliasFromCommon)))
    return true;

  return false;
}
