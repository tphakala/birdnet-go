/**
 * Species name resolution utilities for bidirectional lookup
 * between common names and scientific names.
 *
 * Mirrors the backend pattern in internal/datastore/v2only/datastore.go
 * (nameMaps struct with common and species maps).
 */

/**
 * Shape of a species entry from /api/v2/species/all.
 *
 * The canonical endpoint (`RangeFilterSpecies` in internal/api/v2/range.go)
 * serialises as camelCase. A few other species-related Go structs
 * (e.g. `SpeciesInfo`) use snake_case tags, so this interface accepts both
 * key shapes defensively. Callers should not rely on snake_case from
 * `/api/v2/species/all` specifically, but the fallback keeps us safe if
 * a different species endpoint is ever plumbed through this helper.
 */
export interface SpeciesApiEntry {
  commonName?: string;
  scientificName?: string;
  // Fallback snake_case keys for endpoints that serialise in that style.
  common_name?: string;
  scientific_name?: string;
  label: string;
}

/** Bidirectional species name lookup maps */
export interface SpeciesNameMaps {
  /** commonName (lowercase) → scientificName */
  commonToScientific: Map<string, string>;
  /** scientificName (lowercase) → commonName */
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
 * Build bidirectional species name lookup maps from API response.
 * Skips entries missing a scientific name.
 */
export function buildSpeciesNameMaps(species: SpeciesApiEntry[]): SpeciesNameMaps {
  const commonToScientific = new Map<string, string>();
  const scientificToCommon = new Map<string, string>();
  const namesSet = new Set<string>();

  for (const s of species) {
    // Prefer camelCase (the canonical shape for /api/v2/species/all); fall
    // back to snake_case, then to the combined `label` for common name only.
    const commonName = s.commonName ?? s.common_name ?? s.label;
    const scientificName = s.scientificName ?? s.scientific_name ?? '';

    // Always add common name to allNames for autocomplete, even without scientific name
    if (commonName) {
      namesSet.add(commonName);
    }

    // Only build bidirectional maps when both names are present
    if (!scientificName || !commonName) continue;

    commonToScientific.set(commonName.toLowerCase(), scientificName);
    scientificToCommon.set(scientificName.toLowerCase(), commonName);
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

  const lower = storedValue.toLowerCase();

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

  // Unknown — show as common name with empty scientific
  return {
    displayCommonName: storedValue,
    displayScientificName: '',
  };
}

/**
 * Resolve a free-text species query (common name or scientific name) to a
 * scientific name suitable for the backend search filter, using a prebuilt
 * species name map (derived from /api/v2/species/all, which follows the
 * BirdNET label locale).
 *
 * Behavior:
 *   - Empty input returns empty string.
 *   - If a common name exactly matches (case-insensitive), returns the matching
 *     scientific name.
 *   - Otherwise the input passes through unchanged so the backend can run its
 *     own partial scientific-name match (e.g. "Turdus" matching multiple
 *     species). We deliberately do not collapse ambiguous partial common-name
 *     fragments like "owl" to a single arbitrary scientific name; that would
 *     hide other matches behind whichever entry happened to be inserted first
 *     in the underlying Map.
 */
export function resolveSpeciesQuery(input: string, maps: SpeciesNameMaps | null): string {
  const trimmed = input.trim();
  if (!trimmed) return '';
  if (!maps) return trimmed;

  const lower = trimmed.toLowerCase();

  // Exact common-name hit wins.
  const exactCommon = maps.commonToScientific.get(lower);
  if (exactCommon !== undefined) return exactCommon;

  // Anything else (looks-like-scientific input, exact scientific match,
  // ambiguous partial fragment, unknown text) passes through verbatim so
  // the backend's LIKE match on scientific_name keeps working.
  return trimmed;
}

/**
 * Check if a species (by any name alias) is already in a list.
 * Prevents adding "Strix aluco" when "Tawny Owl" is already present (and vice versa).
 */
export function isSpeciesInList(candidate: string, list: string[], maps: SpeciesNameMaps): boolean {
  const candidateLower = candidate.toLowerCase();
  const listLowerSet = new Set(list.map(s => s.toLowerCase()));

  // Direct match
  if (listLowerSet.has(candidateLower)) return true;

  // Check if candidate's alias is in the list
  const aliasFromScientific = maps.scientificToCommon.get(candidateLower);
  if (aliasFromScientific !== undefined && listLowerSet.has(aliasFromScientific.toLowerCase()))
    return true;

  const aliasFromCommon = maps.commonToScientific.get(candidateLower);
  if (aliasFromCommon !== undefined && listLowerSet.has(aliasFromCommon.toLowerCase())) return true;

  return false;
}
