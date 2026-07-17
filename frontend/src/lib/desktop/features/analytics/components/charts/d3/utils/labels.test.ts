import { describe, it, expect, vi } from 'vitest';

import { truncateToWidth } from './labels';

/**
 * Fake proportional font: 10px for a wide glyph ('W'/'M'), 4px for a narrow one ('i'/'l'), 6px
 * otherwise. Mirrors the property that makes character-count truncation wrong — two strings of
 * equal length can differ in rendered width.
 */
function measureProportional(text: string): number {
  let total = 0;
  for (const ch of text) {
    if ('WM'.includes(ch)) total += 10;
    else if ('il'.includes(ch)) total += 4;
    else total += 6;
  }
  return total;
}

describe('truncateToWidth', () => {
  it('returns the text unchanged when it already fits', () => {
    expect(truncateToWidth('Robin', 100, measureProportional)).toBe('Robin');
  });

  it('returns the text unchanged when it exactly fills the budget', () => {
    // 'Robin' = 5 narrow-ish glyphs: R,o,b,n = 6 each, i = 4 -> 28px.
    expect(measureProportional('Robin')).toBe(28);
    expect(truncateToWidth('Robin', 28, measureProportional)).toBe('Robin');
  });

  it('truncates with an ellipsis and stays within the budget', () => {
    const result = truncateToWidth('Black-crowned Night-Heron', 60, measureProportional);

    expect(result).not.toBe('Black-crowned Night-Heron');
    expect(result.endsWith('…')).toBe(true);
    expect(measureProportional(result)).toBeLessThanOrEqual(60);
  });

  it('keeps the widest prefix that fits — one more character would overflow', () => {
    const result = truncateToWidth('Black-crowned Night-Heron', 60, measureProportional);
    const oneMore = 'Black-crowned Night-Heron'.slice(0, result.length) + '…';

    expect(measureProportional(oneMore)).toBeGreaterThan(60);
  });

  /**
   * The regression this whole module exists for: equal-length names truncated by character count
   * would produce equal-length output, but a wide-glyph name must lose more characters to fit the
   * same pixel budget.
   */
  it('drops more characters from a wide-glyph name than a narrow one of equal length', () => {
    const wide = 'WWWWWWWWWWWW';
    const narrow = 'iiiiiiiiiiii';
    expect(wide).toHaveLength(narrow.length);

    const wideFitted = truncateToWidth(wide, 50, measureProportional);
    const narrowFitted = truncateToWidth(narrow, 50, measureProportional);

    expect(measureProportional(wideFitted)).toBeLessThanOrEqual(50);
    expect(measureProportional(narrowFitted)).toBeLessThanOrEqual(50);
    expect(wideFitted.length).toBeLessThan(narrowFitted.length);
  });

  it('returns an empty string when not even an ellipsis fits', () => {
    expect(truncateToWidth('Robin', 3, measureProportional)).toBe('');
  });

  it('returns an empty string for a non-positive budget', () => {
    expect(truncateToWidth('Robin', 0, measureProportional)).toBe('');
    expect(truncateToWidth('Robin', -10, measureProportional)).toBe('');
  });

  it('handles an empty string without measuring', () => {
    const measure = vi.fn(measureProportional);
    expect(truncateToWidth('', 100, measure)).toBe('');
    expect(measure).not.toHaveBeenCalled();
  });

  it('bisects rather than walking every prefix', () => {
    const measure = vi.fn(measureProportional);
    const long = 'A'.repeat(500);

    truncateToWidth(long, 100, measure);

    // Linear scanning would measure ~500 times; log2(500) ≈ 9, plus the two upfront checks.
    expect(measure.mock.calls.length).toBeLessThan(20);
  });
});
