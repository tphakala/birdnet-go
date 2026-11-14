import { describe, expect, it } from 'vitest';
import { formatScore } from './detailFormat';

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
