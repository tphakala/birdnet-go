// Species Selector Type Definitions
//
// For current analytics types, prefer importing from:
// - `frontend/src/lib/desktop/features/analytics/pages/AdvancedAnalytics.svelte` (interface definitions)
// - Use the typed interfaces there for new analytics code instead of the legacy `count` property

/**
 * Branded type for species IDs to prevent mixing with other strings
 * Use createSpeciesId() to create instances from raw strings
 */
export type SpeciesId = string & { __brand: 'SpeciesId' };

/**
 * Creates a SpeciesId from a raw string
 * @param id - Raw string ID
 * @returns Branded SpeciesId
 */
export function createSpeciesId(id: string): SpeciesId {
  return id as SpeciesId;
}

export interface Species {
  id: SpeciesId;
  commonName: string;
  scientificName?: string;
  frequency?: SpeciesFrequency;
  category?: string;
  description?: string;
  imageUrl?: string;
  /**
   * @deprecated For backwards compatibility only
   *
   * Legacy detection count for analytics display. This property is maintained
   * for compatibility with existing components but should not be used in new code.
   *
   * For new analytics features, use the typed interfaces in:
   * `frontend/src/lib/desktop/features/analytics/pages/AdvancedAnalytics.svelte`
   * which provide proper type safety and structure for analytics data.
   */
  count?: number;
}

export type SpeciesFrequency = 'very-common' | 'common' | 'uncommon' | 'rare';

export type SpeciesSelectorSize = 'xs' | 'sm' | 'md' | 'lg' | 'xl';

export type SpeciesSelectorVariant = 'chip' | 'list' | 'compact';

export interface SpeciesSelectorConfig {
  size: SpeciesSelectorSize;
  variant: SpeciesSelectorVariant;
  maxSelections?: number;
  searchable: boolean;
  categorized: boolean;
  showFrequency: boolean;
}

export interface SpeciesGroup {
  category: string;
  items: Species[];
}

// Event types for the species selector
export interface SpeciesSelectorEvents {
  change: { selected: SpeciesId[] };
  add: { species: Species };
  remove: { species: Species };
  search: { query: string };
  clear: Record<string, never>;
}
