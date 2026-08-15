import { describe, it, expect } from 'vitest';
import { localizedCountryNames } from './countryNames';

describe('localizedCountryNames', () => {
  it('localizes ISO codes to display names in the given locale', () => {
    expect(localizedCountryNames(['FI', 'SE'], 'en')).toEqual(['Finland', 'Sweden']);
  });

  it('localizes to a non-English locale', () => {
    expect(localizedCountryNames(['DE'], 'de')).toEqual(['Deutschland']);
  });

  it('preserves the input order', () => {
    expect(localizedCountryNames(['SE', 'FI'], 'en')).toEqual(['Sweden', 'Finland']);
  });

  it('returns a localized placeholder (not the raw code) for a well-formed but unassigned region', () => {
    // ZZ is a valid shape; ICU resolves it to a placeholder name ("Unknown
    // Region") rather than throwing, so the util passes ICU's result through and
    // does NOT take the raw-code fallback branch.
    const [name] = localizedCountryNames(['ZZ'], 'en');
    expect(typeof name).toBe('string');
    expect(name.length).toBeGreaterThan(0);
    expect(name).not.toBe('ZZ');
  });

  it('falls back to the raw code for a malformed code', () => {
    expect(localizedCountryNames(['XYZ1'], 'en')).toEqual(['XYZ1']);
  });

  it('returns an empty array for a nullish list', () => {
    expect(localizedCountryNames(null, 'en')).toEqual([]);
    expect(localizedCountryNames(undefined, 'en')).toEqual([]);
  });

  it('falls back to the raw codes for an invalid locale', () => {
    expect(localizedCountryNames(['FI'], '!!!')).toEqual(['FI']);
  });

  it('falls back to the raw codes when Intl.DisplayNames is unavailable', () => {
    // Simulate an older runtime by removing Intl.DisplayNames (typed read-only,
    // so reach it through a mutable view of the Intl object).
    const intl = Intl as unknown as { DisplayNames?: typeof Intl.DisplayNames };
    const original = intl.DisplayNames;
    intl.DisplayNames = undefined;
    try {
      expect(localizedCountryNames(['FI'], 'en')).toEqual(['FI']);
    } finally {
      intl.DisplayNames = original;
    }
  });
});
