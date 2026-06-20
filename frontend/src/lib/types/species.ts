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

// ---------------------------------------------------------------------------
// Species Guide types (authoritative API shapes; mirror internal/api/v2)
// ---------------------------------------------------------------------------

export type GuideQuality = 'full' | 'intro_only' | 'stub';
export type Expectedness = 'expected' | 'uncommon' | 'rare' | 'unexpected';

export interface ExternalLink {
  name: string;
  url: string;
}

export interface GuideFeatureFlags {
  notes: boolean;
  enrichments: boolean;
  similar_species: boolean;
}

export interface SpeciesGuideData {
  scientific_name: string;
  common_name: string;
  description: string;
  quality: GuideQuality;
  expectedness?: Expectedness;
  current_season?: string;
  external_links?: ExternalLink[];
  features: GuideFeatureFlags;
  source: { provider: string; url: string; license: string; license_url: string };
  partial: boolean;
  cached_at: string;
}

export interface SimilarSpeciesEntry {
  scientific_name: string;
  common_name: string;
  relationship: 'same_genus' | 'same_family' | 'similar';
  guide_summary?: string;
}
export interface SimilarSpeciesResponse {
  scientific_name: string;
  genus: string;
  similar: SimilarSpeciesEntry[];
}
export interface SpeciesNoteData {
  id: number;
  entry: string;
  created_at: string;
  updated_at: string;
}
export interface GuideSection {
  heading: string;
  body: string;
}

/**
 * Splits a guide description on `^## ` markdown headers into {heading, body}
 * segments. Leading text before the first header becomes a heading:'' segment.
 */
export function parseGuideDescription(description: string): GuideSection[] {
  const sections: GuideSection[] = [];
  const parts = description.split(/^## /m);
  for (const part of parts) {
    const trimmed = part.trim();
    if (!trimmed) continue;
    const newlineIdx = trimmed.indexOf('\n');
    if (newlineIdx === -1) {
      if (sections.length === 0 && !description.trimStart().startsWith('## ')) {
        sections.push({ heading: '', body: trimmed });
      } else {
        sections.push({ heading: trimmed, body: '' });
      }
    } else {
      const heading =
        sections.length === 0 && !description.trimStart().startsWith('## ')
          ? ''
          : trimmed.slice(0, newlineIdx).trim();
      const body =
        sections.length === 0 && !description.trimStart().startsWith('## ')
          ? trimmed
          : trimmed.slice(newlineIdx + 1).trim();
      sections.push({ heading, body });
    }
  }
  return sections;
}
