import { describe, it, expect, beforeAll } from 'vitest';
import {
  buildSpeciesNameMaps,
  resolveSpeciesDisplayNames,
  resolveSpeciesQuery,
  isSpeciesInList,
  type SpeciesNameMaps,
  type SpeciesApiEntry,
} from './speciesNames';

describe('buildSpeciesNameMaps', () => {
  const sampleSpecies: SpeciesApiEntry[] = [
    { commonName: 'Tawny Owl', scientificName: 'Strix aluco', label: 'Strix aluco_Tawny Owl' },
    { commonName: 'Great Tit', scientificName: 'Parus major', label: 'Parus major_Great Tit' },
    {
      commonName: 'House Sparrow',
      scientificName: 'Passer domesticus',
      label: 'Passer domesticus_House Sparrow',
    },
  ];

  it('accepts snake_case keys as a fallback shape', () => {
    // Guards against regressions if an API response ever lands with
    // snake_case keys (e.g. a different species endpoint is reused here).
    const snakeCaseEntries: SpeciesApiEntry[] = [
      {
        common_name: 'Tawny Owl',
        scientific_name: 'Strix aluco',
        label: 'Strix aluco_Tawny Owl',
      },
      {
        common_name: 'Great Tit',
        scientific_name: 'Parus major',
        label: 'Parus major_Great Tit',
      },
    ];
    const maps = buildSpeciesNameMaps(snakeCaseEntries);
    expect(maps.commonToScientific.get('tawny owl')).toBe('Strix aluco');
    expect(maps.scientificToCommon.get('strix aluco')).toBe('Tawny Owl');
    expect(maps.allNames).toContain('Tawny Owl');
    expect(maps.allNames).toContain('Parus major');
  });

  it('prefers camelCase over snake_case when both are present', () => {
    const entries: SpeciesApiEntry[] = [
      {
        commonName: 'Tawny Owl',
        scientificName: 'Strix aluco',
        common_name: 'WRONG',
        scientific_name: 'WRONG',
        label: 'Strix aluco_Tawny Owl',
      },
    ];
    const maps = buildSpeciesNameMaps(entries);
    expect(maps.commonToScientific.get('tawny owl')).toBe('Strix aluco');
    expect(maps.scientificToCommon.get('strix aluco')).toBe('Tawny Owl');
    expect(maps.commonToScientific.has('wrong')).toBe(false);
  });

  it('builds commonToScientific map keyed by lowercase common name', () => {
    const maps = buildSpeciesNameMaps(sampleSpecies);
    expect(maps.commonToScientific.get('tawny owl')).toBe('Strix aluco');
    expect(maps.commonToScientific.get('great tit')).toBe('Parus major');
  });

  it('builds scientificToCommon map keyed by lowercase scientific name', () => {
    const maps = buildSpeciesNameMaps(sampleSpecies);
    expect(maps.scientificToCommon.get('strix aluco')).toBe('Tawny Owl');
    expect(maps.scientificToCommon.get('parus major')).toBe('Great Tit');
  });

  it('builds allNames array containing both common and scientific names', () => {
    const maps = buildSpeciesNameMaps(sampleSpecies);
    expect(maps.allNames).toContain('Tawny Owl');
    expect(maps.allNames).toContain('Strix aluco');
    expect(maps.allNames).toContain('Great Tit');
    expect(maps.allNames).toContain('Parus major');
  });

  it('deduplicates allNames', () => {
    const maps = buildSpeciesNameMaps(sampleSpecies);
    const uniqueCount = new Set(maps.allNames).size;
    expect(maps.allNames.length).toBe(uniqueCount);
  });

  it('skips map entries for missing scientificName but keeps common name in allNames', () => {
    const species: SpeciesApiEntry[] = [
      { commonName: 'Unknown Bird', scientificName: '', label: 'Unknown Bird' },
    ];
    const maps = buildSpeciesNameMaps(species);
    expect(maps.commonToScientific.size).toBe(0);
    expect(maps.scientificToCommon.size).toBe(0);
    expect(maps.allNames).toContain('Unknown Bird');
  });

  it('handles empty input', () => {
    const maps = buildSpeciesNameMaps([]);
    expect(maps.commonToScientific.size).toBe(0);
    expect(maps.scientificToCommon.size).toBe(0);
    expect(maps.allNames).toEqual([]);
  });
});

describe('resolveSpeciesDisplayNames', () => {
  let maps: SpeciesNameMaps;

  beforeAll(() => {
    const sampleSpecies: SpeciesApiEntry[] = [
      { commonName: 'Tawny Owl', scientificName: 'Strix aluco', label: 'Strix aluco_Tawny Owl' },
      { commonName: 'Great Tit', scientificName: 'Parus major', label: 'Parus major_Great Tit' },
    ];
    maps = buildSpeciesNameMaps(sampleSpecies);
  });

  it('resolves a common name correctly', () => {
    const result = resolveSpeciesDisplayNames('Tawny Owl', maps);
    expect(result.displayCommonName).toBe('Tawny Owl');
    expect(result.displayScientificName).toBe('Strix aluco');
  });

  it('resolves a scientific name correctly', () => {
    const result = resolveSpeciesDisplayNames('Strix aluco', maps);
    expect(result.displayCommonName).toBe('Tawny Owl');
    expect(result.displayScientificName).toBe('Strix aluco');
  });

  it('handles case-insensitive common name lookup', () => {
    const result = resolveSpeciesDisplayNames('tawny owl', maps);
    expect(result.displayCommonName).toBe('tawny owl');
    expect(result.displayScientificName).toBe('Strix aluco');
  });

  it('handles case-insensitive scientific name lookup', () => {
    const result = resolveSpeciesDisplayNames('strix aluco', maps);
    expect(result.displayCommonName).toBe('Tawny Owl');
    expect(result.displayScientificName).toBe('strix aluco');
  });

  it('falls back gracefully for unknown names', () => {
    const result = resolveSpeciesDisplayNames('Unknown Bird', maps);
    expect(result.displayCommonName).toBe('Unknown Bird');
    expect(result.displayScientificName).toBe('');
  });

  it('handles empty string', () => {
    const result = resolveSpeciesDisplayNames('', maps);
    expect(result.displayCommonName).toBe('');
    expect(result.displayScientificName).toBe('');
  });
});

describe('isSpeciesInList', () => {
  let maps: SpeciesNameMaps;

  beforeAll(() => {
    const sampleSpecies: SpeciesApiEntry[] = [
      { commonName: 'Tawny Owl', scientificName: 'Strix aluco', label: 'Strix aluco_Tawny Owl' },
      { commonName: 'Great Tit', scientificName: 'Parus major', label: 'Parus major_Great Tit' },
    ];
    maps = buildSpeciesNameMaps(sampleSpecies);
  });

  it('returns true when common name is in list and checking scientific name', () => {
    const list = ['Tawny Owl'];
    expect(isSpeciesInList('Strix aluco', list, maps)).toBe(true);
  });

  it('returns true when scientific name is in list and checking common name', () => {
    const list = ['Strix aluco'];
    expect(isSpeciesInList('Tawny Owl', list, maps)).toBe(true);
  });

  it('returns false when species is not in list at all', () => {
    const list = ['Tawny Owl'];
    expect(isSpeciesInList('Great Tit', list, maps)).toBe(false);
  });
});

describe('resolveSpeciesQuery', () => {
  let maps: SpeciesNameMaps;

  beforeAll(() => {
    const sampleSpecies: SpeciesApiEntry[] = [
      { commonName: 'Tawny Owl', scientificName: 'Strix aluco', label: 'Strix aluco_Tawny Owl' },
      { commonName: 'Great Tit', scientificName: 'Parus major', label: 'Parus major_Great Tit' },
      {
        commonName: 'Lehtopöllö',
        scientificName: 'Strix aluco',
        label: 'Strix aluco_Lehtopöllö',
      },
      {
        commonName: 'Eurasian Blue Tit',
        scientificName: 'Cyanistes caeruleus',
        label: 'Cyanistes caeruleus_Eurasian Blue Tit',
      },
    ];
    maps = buildSpeciesNameMaps(sampleSpecies);
  });

  it('returns empty string for empty input', () => {
    expect(resolveSpeciesQuery('', maps)).toBe('');
    expect(resolveSpeciesQuery('   ', maps)).toBe('');
  });

  it('falls back to raw input when maps are null', () => {
    expect(resolveSpeciesQuery('Tawny Owl', null)).toBe('Tawny Owl');
  });

  it('resolves an exact common-name hit to its scientific name', () => {
    expect(resolveSpeciesQuery('Tawny Owl', maps)).toBe('Strix aluco');
  });

  it('resolves case-insensitively', () => {
    expect(resolveSpeciesQuery('tawny owl', maps)).toBe('Strix aluco');
    expect(resolveSpeciesQuery('GREAT TIT', maps)).toBe('Parus major');
  });

  it('resolves non-ASCII common names (e.g. Finnish)', () => {
    expect(resolveSpeciesQuery('Lehtopöllö', maps)).toBe('Strix aluco');
    expect(resolveSpeciesQuery('lehtopöllö', maps)).toBe('Strix aluco');
  });

  it('passes scientific-looking input through unchanged', () => {
    expect(resolveSpeciesQuery('Strix aluco', maps)).toBe('Strix aluco');
  });

  it('passes through single-word capitalised input as raw, letting the backend partial-match', () => {
    // "Turdus" is a genus name but no common name in the map contains the
    // substring "turdus", so the resolver should return the raw input so
    // the backend LIKE clause can still match scientific names.
    expect(resolveSpeciesQuery('Turdus', maps)).toBe('Turdus');
  });

  it('passes ambiguous partial common-name fragments through verbatim', () => {
    // "owl" is a substring of "Tawny Owl" but it is also a substring of
    // every other owl in the BirdNET label set. The resolver must NOT
    // collapse it to whichever entry happens to be inserted first in the
    // map; the backend LIKE clause is responsible for partial matching.
    expect(resolveSpeciesQuery('owl', maps)).toBe('owl');
  });

  it('returns the raw input when there is no match at all', () => {
    expect(resolveSpeciesQuery('nonexistent', maps)).toBe('nonexistent');
  });

  it('trims leading and trailing whitespace', () => {
    expect(resolveSpeciesQuery('  Tawny Owl  ', maps)).toBe('Strix aluco');
  });
});
