import { describe, it, expect } from 'vitest';

import { movingAverageTrend, onsetCount, type DawnOnsetPoint } from '../dawnOnset';

function pt(date: string, onsetRelMinutes: number | null, detectionCount = 10): DawnOnsetPoint {
  return { date, onsetRelMinutes, detectionCount };
}

describe('onsetCount', () => {
  it('counts only the days with a non-null onset', () => {
    expect(onsetCount({ points: [pt('a', 1), pt('b', null), pt('c', 3)] })).toBe(2);
  });

  it('is zero for empty and all-gap data', () => {
    expect(onsetCount({ points: [] })).toBe(0);
    expect(onsetCount({ points: [pt('a', null), pt('b', null)] })).toBe(0);
  });
});

describe('movingAverageTrend', () => {
  it('averages the non-null values in a centered window', () => {
    const points = [pt('1', 10), pt('2', 20), pt('3', 30)];
    // window 3 (half 1): i0 -> [10,20]=15, i1 -> [10,20,30]=20, i2 -> [20,30]=25.
    expect(movingAverageTrend(points, 3, 1)).toEqual([15, 20, 25]);
  });

  it('skips nulls inside the window but averages the values that are present', () => {
    const points = [pt('1', 10), pt('2', null), pt('3', 30)];
    // i1 window is [10, null, 30] -> (10 + 30) / 2 = 20.
    expect(movingAverageTrend(points, 3, 1)[1]).toBe(20);
  });

  it('returns null where fewer than minSamples non-null values fall in the window', () => {
    const points = [pt('1', 10), pt('2', null), pt('3', null), pt('4', null), pt('5', 50)];
    const trend = movingAverageTrend(points, 3, 2);
    expect(trend[2]).toBeNull(); // window [null,null,null]
    expect(trend[0]).toBeNull(); // window [10,null] -> only 1 sample
  });

  it('returns a result aligned 1:1 with the input', () => {
    expect(movingAverageTrend([pt('1', 10), pt('2', 20)], 7, 1)).toHaveLength(2);
  });

  it('returns all-null for an all-null series', () => {
    expect(movingAverageTrend([pt('1', null), pt('2', null)], 3, 1)).toEqual([null, null]);
  });
});
