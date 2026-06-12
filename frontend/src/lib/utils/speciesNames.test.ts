import { describe, it, expect, beforeAll } from 'vitest';
import {
  buildSpeciesNameMaps,
  resolveSpeciesDisplayNames,
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

describe('NFC normalization', () => {
  // "Lehtopöllö" is the Finnish common name for Tawny Owl.
  // nfcCommon uses composed code points (U+00F6 = o-with-umlaut).
  // nfdInput is written in decomposed form (NFD): base letter 'o' followed by
  // the combining diaeresis (U+0308). The two constants look identical visually
  // but differ at the byte level so the test exercises real NFC-vs-NFD input.
  const nfcCommon = 'Lehtopöllö'; // composed (NFC)
  const nfdInput = 'Lehtopöllö'; // decomposed (NFD): o + combining diaeresis

  const species: SpeciesApiEntry[] = [
    { commonName: nfcCommon, scientificName: 'Strix aluco', label: `Strix aluco_${nfcCommon}` },
  ];

  it('resolves an NFD-form common name against the NFC-stored map', () => {
    const maps = buildSpeciesNameMaps(species);
    const resolved = resolveSpeciesDisplayNames(nfdInput, maps);
    expect(resolved.displayScientificName).toBe('Strix aluco');
    // The common-name branch returns storedValue verbatim, so the display name
    // keeps its NFD bytes; assert it preserves casing and folds to the canonical
    // NFC form. This catches a regression that returned the lowercased lookup key.
    expect(resolved.displayCommonName.normalize('NFC')).toBe(nfcCommon);
  });

  it('matches an NFD candidate against an NFC list entry in isSpeciesInList', () => {
    const maps = buildSpeciesNameMaps(species);
    expect(isSpeciesInList(nfdInput, [nfcCommon], maps)).toBe(true);
  });
});

describe('ambiguous common-name delete-on-conflict', () => {
  const species: SpeciesApiEntry[] = [
    { commonName: 'Owl', scientificName: 'Strix aluco', label: 'Strix aluco_Owl' },
    { commonName: 'Owl', scientificName: 'Bubo bubo', label: 'Bubo bubo_Owl' },
    { commonName: 'Great Tit', scientificName: 'Parus major', label: 'Parus major_Great Tit' },
  ];

  it('removes an ambiguous common-name key from commonToScientific', () => {
    const maps = buildSpeciesNameMaps(species);
    expect(maps.commonToScientific.has('owl')).toBe(false);
    // Non-ambiguous names still resolve.
    expect(maps.commonToScientific.get('great tit')).toBe('Parus major');
  });

  it('keeps every species in scientificToCommon despite common-name ambiguity', () => {
    const maps = buildSpeciesNameMaps(species);
    expect(maps.scientificToCommon.get('strix aluco')).toBe('Owl');
    expect(maps.scientificToCommon.get('bubo bubo')).toBe('Owl');
  });

  it('removes a common-name key shared by three distinct scientific names', () => {
    const threeWay: SpeciesApiEntry[] = [
      { commonName: 'Owl', scientificName: 'Strix aluco', label: 'Strix aluco_Owl' },
      { commonName: 'Owl', scientificName: 'Bubo bubo', label: 'Bubo bubo_Owl' },
      { commonName: 'Owl', scientificName: 'Asio otus', label: 'Asio otus_Owl' },
    ];
    const maps = buildSpeciesNameMaps(threeWay);
    expect(maps.commonToScientific.has('owl')).toBe(false);
    expect(maps.scientificToCommon.get('asio otus')).toBe('Owl');
  });
});
