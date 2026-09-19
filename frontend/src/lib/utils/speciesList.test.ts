/**
 * Tests for mapSpeciesListResponse, which turns the range species-list API
 * response into the stored labels and scientific-name lookup used by the
 * species list editors.
 */

import { describe, it, expect } from 'vitest';
import { mapSpeciesListResponse } from './speciesList';

describe('mapSpeciesListResponse', () => {
  it('prefers the common name and falls back to the label', () => {
    const { predictions } = mapSpeciesListResponse({
      species: [
        { label: 'Parus major_Great Tit', commonName: 'Great Tit', scientificName: 'Parus major' },
        { label: 'Turdus merula', commonName: '' },
      ],
    });

    expect(predictions).toEqual(['Great Tit', 'Turdus merula']);
  });

  it('indexes scientific names by the stored value, skipping entries without one', () => {
    const { scientificNames } = mapSpeciesListResponse({
      species: [
        { label: 'Parus major_Great Tit', commonName: 'Great Tit', scientificName: 'Parus major' },
        { label: 'Turdus merula', commonName: 'Common Blackbird' },
      ],
    });

    expect(scientificNames.size).toBe(1);
    expect([...scientificNames.values()]).toEqual(['Parus major']);
  });

  it.each([
    ['undefined', undefined],
    ['a response without a species list', {}],
    ['a non-array species list', { species: 'nope' as unknown as [] }],
  ])('returns empty results for %s', (_name, data) => {
    const result = mapSpeciesListResponse(data);

    expect(result.predictions).toEqual([]);
    expect(result.scientificNames.size).toBe(0);
  });
});
