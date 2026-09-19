import { normalizeForLookup } from '$lib/utils/speciesNames';

export interface SpeciesListResponse {
  species?: Array<{ label: string; commonName?: string; scientificName?: string }>;
}

export interface MappedSpeciesList {
  predictions: string[];
  scientificNames: Map<string, string>;
}

/** Maps the range species-list response to stored labels and localization metadata. */
export function mapSpeciesListResponse(data: SpeciesListResponse | undefined): MappedSpeciesList {
  if (!Array.isArray(data?.species)) {
    return { predictions: [], scientificNames: new Map() };
  }

  const scientificNames = new Map<string, string>();
  const predictions = data.species.map(species => {
    // Preserve the API consumer's established fallback semantics for empty names.
    const value = species.commonName || species.label; // eslint-disable-line @typescript-eslint/prefer-nullish-coalescing
    if (species.scientificName) {
      scientificNames.set(normalizeForLookup(value), species.scientificName);
    }
    return value;
  });

  return { predictions, scientificNames };
}
