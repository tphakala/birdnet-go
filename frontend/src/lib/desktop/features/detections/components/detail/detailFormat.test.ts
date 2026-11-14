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

  it('takes a minimum out of range as the nearest one that is allowed', () => {
    expect(formatDecimal(4.25, -1)).toBe('4.3');
    expect(formatDecimal(4.5, Number.NaN)).toBe('4.5');
    expect(formatDecimal(4.5, 101)).toHaveLength(102);
  });
});

describe('barHeight', () => {
  it('scales a count to the tallest bar and keeps a sliver for a small count', () => {
    expect(barHeight(5, 10)).toBe(50);
    expect(barHeight(1, 100)).toBe(8);
    expect(barHeight(0, 10)).toBe(0);
  });

  // Regression: with no detections at all (a peak of 0) the height was NaN.
  it('is 0 when there is nothing to scale to', () => {
    expect(barHeight(0, 0)).toBe(0);
    expect(barHeight(3, Number.NaN)).toBe(0);
    expect(barHeight(Number.NaN, 10)).toBe(0);
  });
});
