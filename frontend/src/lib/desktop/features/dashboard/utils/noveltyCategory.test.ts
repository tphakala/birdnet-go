import { describe, it, expect } from 'vitest';
import {
  resolveNoveltyCategory,
  noveltyCategoryColorVar,
  noveltyCategoryTextColor,
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
    const infrequentItem: NoveltyFlags = { is_infrequent: true };

    it('claims a server-flagged returning species on today', () => {
      expect(resolveNoveltyCategory(infrequentItem, { isToday: true })).toBe('infrequent');
    });

    it('does not activate on past dates (isToday false)', () => {
      expect(resolveNoveltyCategory(infrequentItem, { isToday: false })).toBeNull();
    });

    it('does not activate without isToday', () => {
      expect(resolveNoveltyCategory(infrequentItem)).toBeNull();
    });

    it('does not activate when the server did not flag the row', () => {
      expect(resolveNoveltyCategory({}, { isToday: true })).toBeNull();
      expect(resolveNoveltyCategory({ is_infrequent: false }, { isToday: true })).toBeNull();
    });

    it('is a fallback tier: lifetime/year/season win even when flagged infrequent', () => {
      const opts = { isToday: true };
      expect(resolveNoveltyCategory({ is_new_species: true, is_infrequent: true }, opts)).toBe(
        'lifetime'
      );
      expect(resolveNoveltyCategory({ is_new_this_year: true, is_infrequent: true }, opts)).toBe(
        'year'
      );
      expect(resolveNoveltyCategory({ is_new_this_season: true, is_infrequent: true }, opts)).toBe(
        'season'
      );
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

describe('noveltyCategoryTextColor', () => {
  it('maps the status categories to their AA text tokens', () => {
    expect(noveltyCategoryTextColor('lifetime')).toBe('var(--text-warning)');
    expect(noveltyCategoryTextColor('year')).toBe('var(--text-info)');
    expect(noveltyCategoryTextColor('season')).toBe('var(--text-success)');
  });

  it('keeps the secondary accent for the infrequent category', () => {
    expect(noveltyCategoryTextColor('infrequent')).toBe('var(--color-secondary)');
  });
});
