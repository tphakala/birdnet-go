// Species Selector Type Definitions
//
// For current analytics types, prefer importing from:
// - `frontend/src/lib/desktop/features/analytics/registry/types.ts` (interface definitions)
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
   * `frontend/src/lib/desktop/features/analytics/registry/types.ts`
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
  icon?: string;
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
  /**
   * Whether the candidate has comparison prose. Entries with a guide render the
   * comparison sections; entries without render `external_links` instead, so every
   * selection is useful. Drives a subtle "links only" hint in the picker rail.
   */
  has_guide: boolean;
  guide_summary?: string;
  /**
   * Resource links shown for description-less entries (populated by the backend
   * only when enrichments are enabled). Localized to the request locale.
   */
  external_links?: ExternalLink[];
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

// ---------------------------------------------------------------------------
// Canonical guide-section vocabulary
//
// Wikipedia article text arrives as `## Heading` sections (see the Go
// guideprovider's convertWikiSections). These lowercase heading fragments map a
// section to a canonical comparison row. Lists include common localized
// Wikipedia section-heading fragments for ALL 16 locales BirdNET-Go ships
// (cs da de en es fi fr hu it lv nb nl pl pt sk sv); unmatched headings degrade
// gracefully (the section is simply omitted from the comparison). Matching is
// case-insensitive substring, so accented forms are kept as they appear on-wiki.
// ---------------------------------------------------------------------------

/** Heading fragments that denote a "songs & calls" / voice section. */
export const GUIDE_SONGS_HEADINGS = [
  // en / de / fr / es-pt-it (canto, voce, voz) / pl / fi / sv
  'songs and calls',
  'song',
  'calls',
  'voice',
  'vocalization',
  'stimme',
  'gesang',
  'chant et cris',
  'voix',
  'voz',
  'canto',
  'voce',
  'głos',
  'ääntelyt',
  'läte',
  // cs / sk
  'hlas',
  'zpěv',
  'spev',
  // da / nb
  'sang',
  'stemme',
  // hu
  'hangja',
  'ének',
  // lv
  'balss',
  'dziesma',
  // nl
  'geluid',
  'zang',
  'roep',
];

/** Heading fragments that denote an appearance / description section. */
export const GUIDE_APPEARANCE_HEADINGS = [
  // en / de / fr / es / fi / sv
  'description',
  'appearance',
  'identification',
  'beschreibung',
  'merkmale',
  'aussehen',
  'apparence',
  'descripción',
  'aspecto',
  'kuvaus',
  'utseende',
  // it / pt
  'descrizione',
  'aspetto',
  'descrição',
  'aparência',
  // cs / sk
  'popis',
  'vzhled',
  'vzhľad',
  // da / nb
  'beskrivelse',
  'kendetegn',
  'kjennetegn',
  // hu
  'leírás',
  'megjelenése',
  'külleme',
  // lv
  'apraksts',
  'izskats',
  // nl
  'beschrijving',
  'kenmerken',
  'uiterlijk',
  // pl
  'wygląd',
  'opis',
];

/** Heading fragments that denote a distribution / habitat / range section. */
export const GUIDE_HABITAT_HEADINGS = [
  // en / de / fr / es / fi / sv
  'distribution and habitat',
  'distribution',
  'habitat',
  'range',
  'verbreitung',
  'lebensraum',
  'répartition',
  'distribución',
  'levinneisyys',
  'utbredning',
  // it / pt
  'distribuzione',
  'areale',
  'distribuição',
  // cs / sk
  'rozšíření',
  'rozšírenie',
  'výskyt',
  'biotop',
  // da / nb
  'udbredelse',
  'levested',
  'utbredelse',
  'leveområde',
  // hu
  'elterjedése',
  'előfordulása',
  'élőhely',
  // lv
  'izplatība',
  'dzīvotne',
  // nl
  'verspreiding',
  'leefgebied',
  // pl
  'występowanie',
  'zasięg',
  'środowisko',
];

/** Heading fragments that denote a behaviour / ecology section. */
export const GUIDE_BEHAVIOUR_HEADINGS = [
  // en / de / fr / es / fi / sv
  'behaviour',
  'behavior',
  'ecology',
  'verhalten',
  'comportement',
  'comportamiento',
  'ecología',
  'käyttäytyminen',
  'elintavat',
  'ekologia',
  'beteende',
  'ekologi',
  // it / pt
  'comportamento',
  'ecologia',
  'biologia',
  // cs / sk
  'chování',
  'ekologie',
  'správanie',
  'ekológia',
  // da / nb
  'adfærd',
  'atferd',
  'økologi',
  // hu
  'életmódja',
  'viselkedése',
  // lv
  'uzvedība',
  'ekoloģija',
  // nl
  'gedrag',
  'ecologie',
  'leefwijze',
  // pl
  'zachowanie',
];

export type CanonicalSectionId = 'appearance' | 'voice' | 'habitat' | 'behaviour';

/** The canonical comparison rows extracted from a guide description. */
export interface CanonicalSections {
  appearance: string;
  voice: string;
  habitat: string;
  behaviour: string;
}

function matchesHeading(heading: string, vocab: string[]): boolean {
  const h = heading.trim().toLowerCase();
  if (h === '') return false;
  return vocab.some(token => h.includes(token));
}

/**
 * Classifies a guide section heading into a canonical comparison row, or null
 * when it matches none. An empty heading (the article lead) is not a canonical
 * row here — callers fall back to the lead for appearance when no Description
 * section exists.
 */
export function classifyCanonicalHeading(heading: string): CanonicalSectionId | null {
  if (matchesHeading(heading, GUIDE_APPEARANCE_HEADINGS)) return 'appearance';
  if (matchesHeading(heading, GUIDE_SONGS_HEADINGS)) return 'voice';
  if (matchesHeading(heading, GUIDE_HABITAT_HEADINGS)) return 'habitat';
  if (matchesHeading(heading, GUIDE_BEHAVIOUR_HEADINGS)) return 'behaviour';
  return null;
}

/**
 * Extracts the canonical comparison rows from a guide description. The first
 * matching section wins for each row. When no appearance/description section is
 * present, the article lead (text before the first `## `) is used so the
 * comparison card is never empty for a guide that has any prose.
 */
export function extractCanonicalSections(description: string): CanonicalSections {
  const out: CanonicalSections = { appearance: '', voice: '', habitat: '', behaviour: '' };
  let lead = '';
  for (const section of parseGuideDescription(description)) {
    if (section.heading === '') {
      if (!lead) lead = section.body;
      continue;
    }
    const id = classifyCanonicalHeading(section.heading);
    // eslint-disable-next-line security/detect-object-injection -- id is a CanonicalSectionId union (fixed keys), not external input
    if (id && !out[id]) out[id] = section.body;
  }
  if (!out.appearance) out.appearance = lead;
  return out;
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
