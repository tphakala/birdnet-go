/**
 * Lists of species for picking one by name. Two scopes: the species the range
 * filter expects at the station's location, and every species the station's
 * models know.
 *
 * Each list is requested once, on first use, and kept for the life of the page.
 * Matching and ranking a query is left to the shared species prediction helpers.
 */
import { isPlainObject } from '$lib/utils/security';
import { buildAppUrl } from '$lib/utils/urlHelpers';

/** A species that can be picked. */
export interface CatalogSpecies {
  scientificName: string;
  commonName: string;
}

/** Which species a catalog holds: those in the range filter, or all the models know. */
export type CatalogScope = 'range' | 'all';

const CATALOG_ENDPOINTS: Record<CatalogScope, string> = {
  range: '/api/v2/range/species/list',
  all: '/api/v2/species/all',
};

const catalogs = new Map<CatalogScope, Promise<CatalogSpecies[]>>();

async function fetchCatalog(endpoint: string): Promise<CatalogSpecies[]> {
  const response = await fetch(buildAppUrl(endpoint));
  if (!response.ok) {
    throw new Error(`species list request failed: ${response.status}`);
  }
  const data: unknown = await response.json();
  const entries = isPlainObject(data) && Array.isArray(data.species) ? data.species : [];
  const seen = new Set<string>();
  const species: CatalogSpecies[] = [];
  for (const entry of entries) {
    // An entry that is not an object, or has no scientific name, is left out.
    if (!isPlainObject(entry) || typeof entry.scientificName !== 'string') continue;
    const scientificName = entry.scientificName.trim();
    if (!scientificName || seen.has(scientificName)) continue;
    seen.add(scientificName);
    // An empty common name falls back to the scientific name, like a missing one.
    let commonName = typeof entry.commonName === 'string' ? entry.commonName.trim() : '';
    if (commonName === '') commonName = scientificName;
    species.push({ scientificName, commonName });
  }
  return species;
}

/**
 * Starts loading a species list if it is not loaded yet. Rejects when the
 * request fails.
 */
export function loadSpeciesCatalog(scope: CatalogScope): Promise<CatalogSpecies[]> {
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
