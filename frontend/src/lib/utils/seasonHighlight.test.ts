import { describe, it, expect } from 'vitest';
import { getSeasonHighlight, hemisphereForLatitude } from './seasonHighlight';

describe('hemisphereForLatitude', () => {
  it('classifies northern, southern, and equatorial bands', () => {
    expect(hemisphereForLatitude(52)).toBe('northern');
    expect(hemisphereForLatitude(-33)).toBe('southern');
    expect(hemisphereForLatitude(0)).toBe('equatorial');
    expect(hemisphereForLatitude(5)).toBe('equatorial');
    expect(hemisphereForLatitude(-9)).toBe('equatorial');
    expect(hemisphereForLatitude(11)).toBe('northern');
    expect(hemisphereForLatitude(-11)).toBe('southern');
  });
});

describe('getSeasonHighlight', () => {
  it('returns null for empty input', () => {
    expect(getSeasonHighlight(undefined)).toBeNull();
    expect(getSeasonHighlight(null)).toBeNull();
    expect(getSeasonHighlight('')).toBeNull();
    expect(getSeasonHighlight('   ')).toBeNull();
  });

  it('maps a standard season token to highlight metadata', () => {
    const h = getSeasonHighlight('summer');
    expect(h).not.toBeNull();
    expect(h?.token).toBe('summer');
    expect(h?.i18nKey).toBe('analytics.species.guide.season.summer');
    expect(h?.emoji).toBe('☀️');
    expect(h?.isEquatorial).toBe(false);
  });

  it('flags equatorial wet/dry tokens', () => {
    expect(getSeasonHighlight('wet1')?.isEquatorial).toBe(true);
    expect(getSeasonHighlight('dry2')?.isEquatorial).toBe(true);
  });

  it('normalizes casing and whitespace', () => {
    const h = getSeasonHighlight('  Winter  ');
    expect(h?.token).toBe('winter');
    expect(h?.i18nKey).toBe('analytics.species.guide.season.winter');
  });

  it('returns empty emoji for unknown tokens', () => {
    const h = getSeasonHighlight('monsoon');
    expect(h?.token).toBe('monsoon');
    expect(h?.emoji).toBe('');
  });
});
