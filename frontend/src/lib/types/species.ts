// Species Selector Type Definitions

export interface Species {
  id: string;
  commonName: string;
  scientificName?: string;
  frequency?: SpeciesFrequency;
  category?: string;
  description?: string;
  imageUrl?: string;
  // Backwards compatibility for analytics
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
  change: { selected: string[] };
  add: { species: Species };
  remove: { species: Species };
  search: { query: string };
  clear: Record<string, never>;
}
