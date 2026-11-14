import { describe, expect, it } from 'vitest';
import { barHeight, formatDecimal, formatScore } from './detailFormat';

describe('formatScore', () => {
  it('shows whole percentages from 10% up', () => {
    expect(formatScore(0.87)).toBe('87%');
    expect(formatScore(0.1)).toBe('10%');
    expect(formatScore(1)).toBe('100%');
  });

  it('keeps one decimal below 10%, so low scores still differ', () => {
    expect(formatScore(0.0049)).toBe('0.5%');
    expect(formatScore(0.0011)).toBe('0.1%');
    expect(formatScore(0.09)).toBe('9.0%');
  });

  it('shows a floor instead of 0.0% for a tiny score', () => {
    expect(formatScore(0.0004)).toBe('<0.1%');
  });

  it('shows 0% for zero and for values that are not scores', () => {
    expect(formatScore(0)).toBe('0%');
    expect(formatScore(-0.2)).toBe('0%');
    expect(formatScore(Number.NaN)).toBe('0%');
  });
});

describe('formatDecimal', () => {
  it('keeps one decimal by default and drops it for a whole number when asked', () => {
    expect(formatDecimal(4.46)).toBe('4.5');
    expect(formatDecimal(48, 0)).toBe('48');
    expect(formatDecimal(44.1, 0)).toBe('44.1');
  });

  // Regression: a minimum above one decimal threw a RangeError.
  it('shows as many decimals as the minimum asks for', () => {
    expect(formatDecimal(4.5, 2)).toBe('4.50');
  });

  it.each([
    [4.25, -1, '4.3'],
    [4.5, Number.NaN, '4.5'],
    [4.5, Number.POSITIVE_INFINITY, '4.5'],
    [4.5, 1.9, '4.5'],
  ])('takes the minimum of a value %s with minimum %s as %s', (value, minimum, expected) => {
    expect(formatDecimal(value, minimum)).toBe(expected);
  });

  it('cuts a minimum above 20 digits to 20', () => {
    expect(formatDecimal(4.5, 101)).toBe(`4.5${'0'.repeat(19)}`);
  });

  it.each([
    [-4.46, '-4.5'],
    [0, '0.0'],
    [1234.5, '1,234.5'],
    [Number.NaN, 'NaN'],
    [Number.POSITIVE_INFINITY, '∞'],
    [Number.NEGATIVE_INFINITY, '-∞'],
    [1e21, '1,000,000,000,000,000,000,000.0'],
  ])('formats the value %s without throwing', (value, expected) => {
    expect(formatDecimal(value)).toBe(expected);
  });
});

describe('barHeight', () => {
  it('scales a count to the tallest bar and keeps a sliver for a small count', () => {
    expect(barHeight(5, 10)).toBe(50);
    expect(barHeight(1, 100)).toBe(8);
    expect(barHeight(0, 10)).toBe(0);
  });

  it.each([
    ['no peak', 0, 0, 100],
    ['negative peak', 3, -1, 100],
    ['NaN peak', 3, Number.NaN, 100],
    ['infinite peak', 3, Number.POSITIVE_INFINITY, 100],
    ['NaN count', Number.NaN, 10, 100],
    ['infinite count', Number.POSITIVE_INFINITY, 10, 100],
    ['negative count', -2, 10, 100],
  ])('is 0 for %s', (_name, count, peak, scale) => {
    expect(barHeight(count, peak, scale)).toBe(0);
  });

  it.each([
    ['NaN scale', Number.NaN, 50],
    ['infinite scale', Number.POSITIVE_INFINITY, 50],
    ['zero scale', 0, 50],
    ['negative scale', -5, 50],
  ])('falls back to a scale of 100 for %s', (_name, scale, expected) => {
    expect(barHeight(5, 10, scale)).toBe(expected);
  });

  it('never goes above the scale, even for a count above the peak', () => {
    expect(barHeight(20, 10, 82)).toBe(82);
  });

  it('keeps the sliver within a scale smaller than it', () => {
    expect(barHeight(1, 1000, 4)).toBe(4);
  });
});
