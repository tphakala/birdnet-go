import { describe, it, expect } from 'vitest';

import {
  maxDensity,
  ridgelineLayout,
  peakIndex,
  RIDGE_OVERLAP,
  type RidgelineSeries,
} from '../ridgeline';

function series(scientificName: string, density: number[]): RidgelineSeries {
  return { scientificName, commonName: scientificName, density };
}

describe('maxDensity', () => {
  it('returns the largest value across all series', () => {
    expect(
      maxDensity([series('A', [0.1, 0.6, 0.3]), series('B', [0.2, 0.2, 0.2]), series('C', [0.9])])
    ).toBe(0.9);
  });

  it('returns 0 for no series or all-empty densities', () => {
    expect(maxDensity([])).toBe(0);
    expect(maxDensity([series('A', []), series('B', [])])).toBe(0);
  });
});

describe('ridgelineLayout', () => {
  it('returns an empty layout for no rows or non-positive height', () => {
    expect(ridgelineLayout([], 400).rows).toHaveLength(0);
    expect(ridgelineLayout([series('A', [1])], 0).rows).toHaveLength(0);
  });

  it('places a single row so its baseline is at the bottom and its peak touches the top', () => {
    const innerHeight = 300;
    const { rows, amplitude } = ridgelineLayout([series('A', [1])], innerHeight);
    expect(rows).toHaveLength(1);
    // n=1: rowStep = H / overlap, amplitude = overlap * rowStep = H, baseline = amplitude = H.
    expect(amplitude).toBeCloseTo(innerHeight, 9);
    expect(rows[0].baseline).toBeCloseTo(innerHeight, 9);
    // Full-amplitude peak (baseline - amplitude) sits exactly at the plot top.
    expect(rows[0].baseline - amplitude).toBeCloseTo(0, 9);
  });

  it('spaces multiple rows so the top peak touches y=0 and the bottom baseline is at innerHeight', () => {
    const innerHeight = 300;
    const input = [series('A', [1]), series('B', [1]), series('C', [1])];
    const { rows, amplitude } = ridgelineLayout(input, innerHeight, RIDGE_OVERLAP);
    expect(rows).toHaveLength(3);

    // Baselines strictly increase down the stack.
    expect(rows[0].baseline).toBeLessThan(rows[1].baseline);
    expect(rows[1].baseline).toBeLessThan(rows[2].baseline);

    // Top row's full-amplitude peak reaches the plot top; bottom baseline reaches the bottom.
    expect(rows[0].baseline - amplitude).toBeCloseTo(0, 9);
    expect(rows[rows.length - 1].baseline).toBeCloseTo(innerHeight, 9);

    // amplitude exceeds the row step (so ridges overlap) for overlap > 1.
    const rowStep = rows[1].baseline - rows[0].baseline;
    expect(amplitude).toBeGreaterThan(rowStep);
  });

  it('preserves input order and indices', () => {
    const input = [series('First', [1]), series('Second', [1])];
    const { rows } = ridgelineLayout(input, 200);
    expect(rows.map(r => r.series.scientificName)).toEqual(['First', 'Second']);
    expect(rows.map(r => r.index)).toEqual([0, 1]);
  });
});

describe('peakIndex', () => {
  it('returns the index of the largest bucket', () => {
    expect(peakIndex([0.1, 0.2, 0.7, 0.0])).toBe(2);
  });

  it('returns -1 for empty or all-zero densities', () => {
    expect(peakIndex([])).toBe(-1);
    expect(peakIndex([0, 0, 0])).toBe(-1);
  });
});
