import { describe, it, expect } from 'vitest';
import {
  resolveNoveltyCategory,
  noveltyCategoryColorVar,
  type NoveltyFlags,
} from './noveltyCategory';

describe('resolveNoveltyCategory', () => {
  it('returns lifetime for new species (highest precedence)', () => {
    expect(resolveNoveltyCategory({ is_new_species: true })).toBe('lifetime');
  });

  it('returns year when new this year but not lifetime', () => {
    expect(resolveNoveltyCategory({ is_new_this_year: true })).toBe('year');
  });

  it('returns season when new this season only', () => {
    expect(resolveNoveltyCategory({ is_new_this_season: true })).toBe('season');
  });

  it('returns null when not new in any period and no infrequent options', () => {
    expect(resolveNoveltyCategory({})).toBeNull();
  });

  describe('infrequent tier', () => {
    const infrequentItem: NoveltyFlags = { days_since_last_seen: 30 };

    it('claims a returning species over the threshold on today', () => {
      expect(
        resolveNoveltyCategory(infrequentItem, { infrequentThresholdDays: 14, isToday: true })
      ).toBe('infrequent');
    });

    it('does not activate on past dates (isToday false)', () => {
      expect(
        resolveNoveltyCategory(infrequentItem, { infrequentThresholdDays: 14, isToday: false })
      ).toBeNull();
    });

    it('does not activate when threshold is undefined (tracking disabled)', () => {
      expect(resolveNoveltyCategory(infrequentItem, { isToday: true })).toBeNull();
    });

    it('does not activate when the gap is at or below the threshold', () => {
      expect(
        resolveNoveltyCategory(
          { days_since_last_seen: 14 },
          { infrequentThresholdDays: 14, isToday: true }
        )
      ).toBeNull();
    });

    it('treats missing days_since_last_seen as 0 (never infrequent)', () => {
      expect(resolveNoveltyCategory({}, { infrequentThresholdDays: 14, isToday: true })).toBeNull();
    });

    it('is a fallback tier: lifetime/year/season win even with a large absence gap', () => {
      const opts = { infrequentThresholdDays: 14, isToday: true };
      expect(resolveNoveltyCategory({ is_new_species: true, days_since_last_seen: 99 }, opts)).toBe(
        'lifetime'
      );
      expect(
        resolveNoveltyCategory({ is_new_this_year: true, days_since_last_seen: 99 }, opts)
      ).toBe('year');
      expect(
        resolveNoveltyCategory({ is_new_this_season: true, days_since_last_seen: 99 }, opts)
      ).toBe('season');
    });
  });
});

describe('noveltyCategoryColorVar', () => {
  it('maps every category to a CSS variable', () => {
    expect(noveltyCategoryColorVar('lifetime')).toBe('var(--color-warning)');
    expect(noveltyCategoryColorVar('year')).toBe('var(--color-info)');
    expect(noveltyCategoryColorVar('season')).toBe('var(--color-success)');
    expect(noveltyCategoryColorVar('infrequent')).toBe('var(--color-secondary)');
  });
});
