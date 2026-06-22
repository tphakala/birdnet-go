import { describe, it, expect } from 'vitest';
import {
  heatmapCells,
  toHourlyResolution,
  slotStartLabel,
  slotsPerDay,
  maxCellCount,
  type HeatmapData,
} from '../heatmap';

const sample: HeatmapData = {
  dates: ['2026-03-01', '2026-03-02'],
  slotResolutionMinutes: 15,
  cells: {
    dateIndex: [0, 0, 1],
    slot: [0, 1, 95],
    count: [3, 2, 7],
  },
};

describe('heatmapCells', () => {
  it('zips the parallel columnar arrays into cell objects', () => {
    expect(heatmapCells(sample)).toEqual([
      { dateIndex: 0, slot: 0, count: 3 },
      { dateIndex: 0, slot: 1, count: 2 },
      { dateIndex: 1, slot: 95, count: 7 },
    ]);
  });

  it('returns an empty array when there are no cells', () => {
    expect(
      heatmapCells({
        dates: ['2026-03-01'],
        slotResolutionMinutes: 15,
        cells: { dateIndex: [], slot: [], count: [] },
      })
    ).toEqual([]);
  });

  it('truncates to the shortest array when lengths disagree (defensive)', () => {
    const ragged: HeatmapData = {
      dates: ['2026-03-01'],
      slotResolutionMinutes: 15,
      cells: { dateIndex: [0, 0], slot: [0], count: [5, 6] },
    };
    expect(heatmapCells(ragged)).toEqual([{ dateIndex: 0, slot: 0, count: 5 }]);
  });
});

describe('toHourlyResolution', () => {
  it('re-buckets sub-hour slots into 24 hourly slots, summing counts', () => {
    const hourly = toHourlyResolution(sample);
    expect(hourly.slotResolutionMinutes).toBe(60);
    expect(hourly.dates).toEqual(sample.dates);
    // date 0: slots 0 and 1 both fall in hour 0 -> merged count 5; date 1: slot 95 -> hour 23.
    expect(heatmapCells(hourly)).toEqual([
      { dateIndex: 0, slot: 0, count: 5 },
      { dateIndex: 1, slot: 23, count: 7 },
    ]);
  });

  it('is a no-op when already hourly', () => {
    const alreadyHourly: HeatmapData = {
      dates: ['2026-03-01'],
      slotResolutionMinutes: 60,
      cells: { dateIndex: [0], slot: [13], count: [4] },
    };
    expect(toHourlyResolution(alreadyHourly)).toEqual(alreadyHourly);
  });
});

describe('slotStartLabel', () => {
  it('formats the slot start as HH:MM', () => {
    expect(slotStartLabel(0, 15)).toBe('00:00');
    expect(slotStartLabel(95, 15)).toBe('23:45');
    expect(slotStartLabel(1, 30)).toBe('00:30');
    expect(slotStartLabel(13, 60)).toBe('13:00');
  });
});

describe('slotsPerDay', () => {
  it('returns the number of slots for a resolution', () => {
    expect(slotsPerDay(15)).toBe(96);
    expect(slotsPerDay(30)).toBe(48);
    expect(slotsPerDay(60)).toBe(24);
  });
});

describe('maxCellCount', () => {
  it('returns the largest cell count', () => {
    expect(maxCellCount(heatmapCells(sample))).toBe(7);
  });

  it('returns 0 for no cells', () => {
    expect(maxCellCount([])).toBe(0);
  });
});
