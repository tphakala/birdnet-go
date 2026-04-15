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
    const commonName = s.commonName ?? s.label;
    const scientificName = s.scientificName ?? '';

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
 * Heuristic check whether a query string looks like a scientific name
 * (e.g. "Turdus merula", "Parus major"). Used to skip common-name resolution
 * for inputs that are already scientific names or partial scientific names.
 *
 * Rules:
 *   - ASCII letters, spaces, and hyphens only (no diacritics, no digits)
 *   - One or two whitespace-separated tokens
 *   - First token starts with an uppercase letter
 */
export function looksLikeScientificName(input: string): boolean {
  const trimmed = input.trim();
  if (!trimmed) return false;

  // Reject anything with diacritics, digits, or unexpected punctuation.
  if (!/^[A-Za-z][A-Za-z\- ]*$/.test(trimmed)) return false;

  const tokens = trimmed.split(/\s+/);
  if (tokens.length < 1 || tokens.length > 2) return false;

  const firstChar = tokens[0]?.charAt(0) ?? '';
  return firstChar >= 'A' && firstChar <= 'Z';
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
 *   - If the input looks like a scientific name, passes it through unchanged.
 *   - Otherwise, looks for a case-insensitive substring match against common
 *     names; returns the first hit's scientific name.
 *   - If no match is found, returns the raw input so the backend can still
 *     attempt a partial match on scientific names.
 */
export function resolveSpeciesQuery(input: string, maps: SpeciesNameMaps | null): string {
  const trimmed = input.trim();
  if (!trimmed) return '';
  if (!maps) return trimmed;

  const lower = trimmed.toLowerCase();

  // Exact common-name hit wins.
  const exactCommon = maps.commonToScientific.get(lower);
  if (exactCommon !== undefined) return exactCommon;

  // Input that already looks like a scientific name: pass through so the
  // backend can still LIKE-match partial scientific names (e.g. "Turdus").
  if (looksLikeScientificName(trimmed)) return trimmed;

  // Also pass through if it matches a known scientific name exactly.
  if (maps.scientificToCommon.has(lower)) return trimmed;

  // Fall back to substring match across common names.
  for (const [commonLower, scientific] of maps.commonToScientific) {
    if (commonLower.includes(lower)) return scientific;
  }

  // No match; let the backend see the raw string as a last resort.
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
