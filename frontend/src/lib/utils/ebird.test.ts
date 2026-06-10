import { describe, it, expect } from 'vitest';
import {
  buildEbirdSpeciesUrl,
  isValidEbirdRegionCode,
  isValidEbirdSpeciesCode,
  toEbirdSiteLanguage,
} from './ebird';

describe('isValidEbirdSpeciesCode', () => {
  it('accepts real lowercase eBird codes', () => {
    expect(isValidEbirdSpeciesCode('euptit1')).toBe(true);
    expect(isValidEbirdSpeciesCode('amerob')).toBe(true);
  });

  it('rejects placeholder codes with uppercase prefix', () => {
    // BirdNET-Go placeholder codes start with an uppercase prefix.
    expect(isValidEbirdSpeciesCode('TM1a2b3c')).toBe(false);
    expect(isValidEbirdSpeciesCode('XXabc123')).toBe(false);
  });

  it('rejects empty/nullish values', () => {
    expect(isValidEbirdSpeciesCode('')).toBe(false);
    expect(isValidEbirdSpeciesCode('   ')).toBe(false);
    expect(isValidEbirdSpeciesCode(null)).toBe(false);
    expect(isValidEbirdSpeciesCode(undefined)).toBe(false);
  });
});

describe('isValidEbirdRegionCode', () => {
  it('treats empty as valid (global)', () => {
    expect(isValidEbirdRegionCode('')).toBe(true);
    expect(isValidEbirdRegionCode('   ')).toBe(true);
    expect(isValidEbirdRegionCode(null)).toBe(true);
    expect(isValidEbirdRegionCode(undefined)).toBe(true);
  });

  it('accepts country, subnational1 and subnational2 codes', () => {
    expect(isValidEbirdRegionCode('BE')).toBe(true);
    expect(isValidEbirdRegionCode('BE-WAL')).toBe(true);
    expect(isValidEbirdRegionCode('US-NY-109')).toBe(true);
  });

  it('rejects malformed codes', () => {
    expect(isValidEbirdRegionCode('BEWAL')).toBe(false);
    expect(isValidEbirdRegionCode('B')).toBe(false);
    expect(isValidEbirdRegionCode('BE_WAL')).toBe(false);
    expect(isValidEbirdRegionCode('BE-WAL-NY-XX')).toBe(false);
  });
});

describe('toEbirdSiteLanguage', () => {
  it('passes through a base language code', () => {
    expect(toEbirdSiteLanguage('fr')).toBe('fr');
    expect(toEbirdSiteLanguage('en')).toBe('en');
  });

  it('reduces locale variants to their base language', () => {
    expect(toEbirdSiteLanguage('fr-BE')).toBe('fr');
    expect(toEbirdSiteLanguage('pt_BR')).toBe('pt');
    expect(toEbirdSiteLanguage('en-US')).toBe('en');
  });

  it('returns empty string for nullish input', () => {
    expect(toEbirdSiteLanguage('')).toBe('');
    expect(toEbirdSiteLanguage(null)).toBe('');
    expect(toEbirdSiteLanguage(undefined)).toBe('');
  });
});

describe('buildEbirdSpeciesUrl', () => {
  it('builds a global URL with species code and language', () => {
    expect(buildEbirdSpeciesUrl({ speciesCode: 'euptit1', locale: 'en' })).toBe(
      'https://ebird.org/species/euptit1?siteLanguage=en'
    );
  });

  it('builds a regional URL when a region is provided', () => {
    expect(buildEbirdSpeciesUrl({ speciesCode: 'euptit1', region: 'BE-WAL', locale: 'fr' })).toBe(
      'https://ebird.org/species/euptit1/BE-WAL?siteLanguage=fr'
    );
  });

  it('reduces locale variants for siteLanguage', () => {
    expect(buildEbirdSpeciesUrl({ speciesCode: 'euptit1', locale: 'fr-BE' })).toBe(
      'https://ebird.org/species/euptit1?siteLanguage=fr'
    );
  });

  it('omits siteLanguage when locale is missing', () => {
    expect(buildEbirdSpeciesUrl({ speciesCode: 'euptit1' })).toBe(
      'https://ebird.org/species/euptit1'
    );
  });

  it('falls back to the global page when the region is empty or invalid', () => {
    expect(buildEbirdSpeciesUrl({ speciesCode: 'euptit1', region: '', locale: 'en' })).toBe(
      'https://ebird.org/species/euptit1?siteLanguage=en'
    );
    expect(
      buildEbirdSpeciesUrl({ speciesCode: 'euptit1', region: 'not a region', locale: 'en' })
    ).toBe('https://ebird.org/species/euptit1?siteLanguage=en');
  });

  it('returns null when the species code is missing or a placeholder', () => {
    expect(buildEbirdSpeciesUrl({ speciesCode: '', locale: 'en' })).toBeNull();
    expect(buildEbirdSpeciesUrl({ speciesCode: null, locale: 'en' })).toBeNull();
    expect(buildEbirdSpeciesUrl({ speciesCode: 'TM1a2b3c', locale: 'en' })).toBeNull();
  });
});
