import { describe, expect, it } from 'vitest';
import { indexSpecies, searchSpecies } from './speciesCatalog';

const catalog = [
  { scientificName: 'Tachyphonus surinamus', commonName: 'amazonianharjatangara' },
  { scientificName: 'Parus major', commonName: 'talitiainen' },
  { scientificName: 'Anas crecca', commonName: 'tavi' },
  { scientificName: 'Pinicola enucleator', commonName: 'taviokuurna' },
  { scientificName: 'Scolopax rusticola', commonName: 'lehtokurppa' },
  { scientificName: 'Turdus pilaris', commonName: 'räkättirastas' },
  // Two made-up entries, one per word-start rank: a common name whose second word
  // starts with "ta", and a scientific name whose second word does.
  { scientificName: 'Exemplum primum', commonName: 'pohjan tavi' },
  { scientificName: 'Exemplum tardum', commonName: 'esimerkkilaji' },
].map(indexSpecies);

const names = (query: string, limit = 10) =>
  searchSpecies(catalog, query, limit).map(item => item.commonName);

describe('searchSpecies', () => {
  it('orders matches by rank, then by common name', () => {
    expect(names('ta')).toEqual([
      // common name starts with the query
      'talitiainen',
      'tavi',
      'taviokuurna',
      // scientific name starts with it: "Tachyphonus" is not what a person typing "ta" is after
      'amazonianharjatangara',
      // a later word of the common name starts with it
      'pohjan tavi',
      // a later word of the scientific name starts with it ("tardum")
      'esimerkkilaji',
      // the query appears somewhere inside the name ("rastas")
      'räkättirastas',
    ]);
  });

  it('keeps the best matches when the limit cuts the list', () => {
    expect(names('ta', 2)).toEqual(['talitiainen', 'tavi']);
  });

  it('finds a species by the start of its scientific name', () => {
    expect(names('scolo')).toEqual(['lehtokurppa']);
  });

  it('finds a species by a later word of its scientific name', () => {
    expect(names('rusti')).toEqual(['lehtokurppa']);
  });

  it('finds a species by text inside its common name', () => {
    expect(names('kurppa')).toEqual(['lehtokurppa']);
  });

  it('ignores case and surrounding spaces', () => {
    expect(names('  TAVI ')).toEqual(['tavi', 'taviokuurna', 'pohjan tavi']);
  });

  it('matches a name typed with decomposed accents', () => {
    // "ä" typed as "a" followed by a combining diaeresis, as a composing keyboard sends it.
    expect(names('räkä')).toEqual(['räkättirastas']);
  });

  it('returns nothing for an empty query or one that matches nothing', () => {
    expect(names('')).toEqual([]);
    expect(names('   ')).toEqual([]);
    expect(names('zzqqxx')).toEqual([]);
  });

  it('returns only the two names of each species', () => {
    expect(searchSpecies(catalog, 'tavi', 1)).toEqual([
      { scientificName: 'Anas crecca', commonName: 'tavi' },
    ]);
  });
});
