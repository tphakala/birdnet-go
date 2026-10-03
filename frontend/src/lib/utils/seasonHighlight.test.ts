import { describe, it, expect } from 'vitest';
import { getSeasonHighlight } from './seasonHighlight';

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
    expect(h?.i18nKey).toBe('analytics.species.guide.season.summer');
    expect(h?.icon).toBe('sun');
  });

  it('maps equatorial wet/dry tokens to their own icons', () => {
    expect(getSeasonHighlight('wet1')?.icon).toBe('cloud-rain');
    expect(getSeasonHighlight('dry2')?.icon).toBe('sun-dim');
  });

  it('normalizes casing and whitespace', () => {
    const h = getSeasonHighlight('  Winter  ');
    expect(h?.i18nKey).toBe('analytics.species.guide.season.winter');
    expect(h?.icon).toBe('snowflake');
  });

  it('returns a null icon for unknown tokens', () => {
    const h = getSeasonHighlight('monsoon');
    expect(h?.i18nKey).toBe('analytics.species.guide.season.monsoon');
    expect(h?.icon).toBeNull();
  });
});
