/**
 * Lists of species for picking one by name. Two scopes: the species the range
 * filter expects at the station's location, and every species the station's
 * models know.
 *
 * Each list is requested once, on first use, and kept for the life of the page.
 */
import { normalizeForLookup, type SpeciesApiEntry } from '$lib/utils/speciesNames';
import { buildAppUrl } from '$lib/utils/urlHelpers';

/** A species that can be picked. */
export interface CatalogSpecies {
  scientificName: string;
  commonName: string;
}

/** A species with its names prepared for matching. */
export interface IndexedSpecies extends CatalogSpecies {
  commonKey: string;
  scientificKey: string;
}

/** Prepares a species for searchSpecies. */
export function indexSpecies(species: CatalogSpecies): IndexedSpecies {
  return {
    ...species,
    commonKey: normalizeForLookup(species.commonName),
    scientificKey: normalizeForLookup(species.scientificName),
  };
}

// Match ranks, best first. A common name is what people type, so its matches
// come before matches on the scientific name at the same position.
const RANK_COMMON_PREFIX = 0;
const RANK_SCIENTIFIC_PREFIX = 1;
const RANK_COMMON_WORD = 2;
const RANK_SCIENTIFIC_WORD = 3;
const RANK_CONTAINS = 4;
const RANK_NO_MATCH = -1;

/** How well a species matches a normalized query; RANK_NO_MATCH when it does not. */
function matchRank(item: IndexedSpecies, needle: string): number {
  const wordStart = ` ${needle}`;
  if (item.commonKey.startsWith(needle)) return RANK_COMMON_PREFIX;
  if (item.scientificKey.startsWith(needle)) return RANK_SCIENTIFIC_PREFIX;
  if (item.commonKey.includes(wordStart)) return RANK_COMMON_WORD;
  if (item.scientificKey.includes(wordStart)) return RANK_SCIENTIFIC_WORD;
  if (item.commonKey.includes(needle) || item.scientificKey.includes(needle)) return RANK_CONTAINS;
  return RANK_NO_MATCH;
}

/** Which species a catalog holds: those in the range filter, or all the models know. */
export type CatalogScope = 'range' | 'all';

const CATALOG_ENDPOINTS: Record<CatalogScope, string> = {
  range: '/api/v2/range/species/list',
  all: '/api/v2/species/all',
};

const catalogs = new Map<CatalogScope, Promise<IndexedSpecies[]>>();

async function fetchCatalog(endpoint: string): Promise<IndexedSpecies[]> {
  const response = await fetch(buildAppUrl(endpoint));
  if (!response.ok) {
    throw new Error(`species list request failed: ${response.status}`);
  }
  const data = (await response.json()) as { species?: SpeciesApiEntry[] };
  const seen = new Set<string>();
  const species: IndexedSpecies[] = [];
  for (const entry of data.species ?? []) {
    const scientificName = entry.scientificName?.trim();
    if (!scientificName || seen.has(scientificName)) continue;
    seen.add(scientificName);
    // An empty common name falls back to the scientific name, like a missing one.
    let commonName = entry.commonName?.trim() ?? '';
    if (commonName === '') commonName = scientificName;
    species.push(indexSpecies({ scientificName, commonName }));
  }
  return species;
}

/**
 * Starts loading a species list if it is not loaded yet. Rejects when the
 * request fails.
 */
export function loadSpeciesCatalog(scope: CatalogScope): Promise<IndexedSpecies[]> {
  const loading = catalogs.get(scope);
  if (loading) return loading;
  // eslint-disable-next-line security/detect-object-injection -- scope is a typed union
  const request = fetchCatalog(CATALOG_ENDPOINTS[scope]).catch((error: unknown) => {
    // A failed load is not kept, so opening the picker again retries.
    catalogs.delete(scope);
    throw error;
  });
  catalogs.set(scope, request);
  return request;
}

/**
 * Species whose common or scientific name contains the query, best first: a
 * common name that starts with the query, a scientific name that starts with
 * it, a later word of the common name and then of the scientific name that
 * starts with it, then any other match. Equal matches are in common-name order.
 * An empty query matches nothing.
 */
export function searchSpecies(
  species: IndexedSpecies[],
  query: string,
  limit: number
): CatalogSpecies[] {
  const needle = normalizeForLookup(query.trim());
  if (!needle) return [];
  const ranked: { rank: number; item: IndexedSpecies }[] = [];
  for (const item of species) {
    const rank = matchRank(item, needle);
    if (rank !== RANK_NO_MATCH) ranked.push({ rank, item });
  }
  ranked.sort((a, b) => a.rank - b.rank || a.item.commonName.localeCompare(b.item.commonName));
  return ranked.slice(0, limit).map(({ item }) => ({
    scientificName: item.scientificName,
    commonName: item.commonName,
  }));
}
