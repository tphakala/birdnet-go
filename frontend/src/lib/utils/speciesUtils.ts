/**
 * Parsed species name components
 */
export interface ParsedSpeciesName {
  scientific: string;
  common: string;
}

/**
 * Parse species list from various input formats
 */
export function parseSpeciesList(input: string): string[] {
  if (!input || typeof input !== 'string') return [];

  // Split by newlines or commas
  const items = input
    .split(/[\n,]+/)
    .map(item => item.trim())
    .filter(item => item.length > 0);

  // Remove duplicates while preserving order
  return [...new Set(items)];
}

/**
 * Format species name for display
 */
export function formatSpeciesName(name: string): string {
  if (!name) return '';

  // Handle "Scientific_Common" format
  if (name.includes('_')) {
    const lastUnderscoreIndex = name.lastIndexOf('_');
    const scientific = name.substring(0, lastUnderscoreIndex);
    const common = name.substring(lastUnderscoreIndex + 1);
    return `${common} (${scientific})`;
  }

  return name;
}

/**
 * Extract scientific and common names from species string
 */
export function parseSpeciesName(species: string): ParsedSpeciesName {
  if (!species) return { scientific: '', common: '' };

  // Handle "Scientific_Common" format (scientific name may contain underscores)
  if (species.includes('_')) {
    const lastUnderscoreIndex = species.lastIndexOf('_');
    const scientific = species.substring(0, lastUnderscoreIndex);
    const common = species.substring(lastUnderscoreIndex + 1);
    return { scientific: scientific || '', common: common || '' };
  }

  // Handle "Common (Scientific)" format
  const match = species.match(/^(.+?)\s*\((.+?)\)$/);
  if (match) {
    return { common: match[1].trim(), scientific: match[2].trim() };
  }

  // Assume it's just a common name
  return { scientific: '', common: species };
}

/**
 * Validate species against allowed list
 */
export function validateSpecies(
  species: string,
  allowedList: string[] | null | undefined
): boolean {
  if (!species) return true;
  if (!allowedList || allowedList.length === 0) return true;

  const normalizedSpecies = species.toLowerCase().trim();

  return allowedList.some(allowed => {
    const normalizedAllowed = allowed.toLowerCase().trim();

    // Exact match
    if (normalizedSpecies === normalizedAllowed) return true;

    // Check if species contains the allowed name
    if (normalizedSpecies.includes(normalizedAllowed)) return true;
    if (normalizedAllowed.includes(normalizedSpecies)) return true;

    // Parse and check individual parts
    const speciesParts = parseSpeciesName(species);
    const allowedParts = parseSpeciesName(allowed);

    return (
      (speciesParts.scientific !== '' &&
        speciesParts.scientific.toLowerCase() === allowedParts.scientific.toLowerCase()) ||
      (speciesParts.common !== '' &&
        speciesParts.common.toLowerCase() === allowedParts.common.toLowerCase())
    );
  });
}

/**
 * Filter species list for autocomplete predictions
 */
export function filterSpeciesForAutocomplete(
  input: string,
  sourceList: string[],
  excludeList: string[] = [],
  maxResults: number = 5
): string[] {
  if (!input || sourceList.length === 0) return [];

  const normalizedInput = input.toLowerCase().trim();
  const normalizedExclude = excludeList.map(s => s.toLowerCase().trim());

  const filtered = sourceList.filter(species => {
    const normalizedSpecies = species.toLowerCase();

    // Skip if already in exclude list
    if (normalizedExclude.includes(normalizedSpecies)) return false;

    // Check if species contains the input
    if (normalizedSpecies.includes(normalizedInput)) return true;

    // Check parsed parts
    const parts = parseSpeciesName(species);
    return (
      parts.scientific.toLowerCase().includes(normalizedInput) ||
      parts.common.toLowerCase().includes(normalizedInput)
    );
  });

  // Sort by relevance (exact matches first, then starts with, then contains)
  filtered.sort((a, b) => {
    const aLower = a.toLowerCase();
    const bLower = b.toLowerCase();

    // Exact match
    if (aLower === normalizedInput) return -1;
    if (bLower === normalizedInput) return 1;

    // Starts with
    if (aLower.startsWith(normalizedInput) && !bLower.startsWith(normalizedInput)) return -1;
    if (!aLower.startsWith(normalizedInput) && bLower.startsWith(normalizedInput)) return 1;

    // Default alphabetical
    return a.localeCompare(b);
  });

  return filtered.slice(0, maxResults);
}

/**
 * Sort species list alphabetically
 */
export function sortSpecies(species: string[]): string[] {
  if (!Array.isArray(species)) return [];

  return [...species].sort((a, b) => {
    // Extract common names for sorting
    const aName = parseSpeciesName(a).common || a;
    const bName = parseSpeciesName(b).common || b;

    return aName.localeCompare(bName);
  });
}
