import { describe, expect, it } from 'vitest';
import type { DailySpeciesSummary } from '$lib/types/detection.types';
import {
  axisTicks,
  formatClock,
  nextSort,
  peakHour,
  sortRows,
  taxonGroup,
  DEFAULT_PHONE_SORT,
} from './phoneSummary';

function row(overrides: Partial<DailySpeciesSummary>): DailySpeciesSummary {
  return {
    scientific_name: 'Turdus merula',
    common_name: 'Blackbird',
    species_code: '',
    count: 1,
    hourly_counts: Array.from({ length: 24 }, () => 0),
    high_confidence: true,
    first_heard: '',
    latest_heard: '',
    thumbnail_url: '',
    ...overrides,
  };
}

describe('taxonGroup', () => {
  it('maps label classes to groups, others to other, and an unknown class to null', () => {
    expect(taxonGroup(row({ taxonomic_class: 'Aves' }))).toBe('bird');
    expect(taxonGroup(row({ taxonomic_class: 'Chiroptera' }))).toBe('bat');
    expect(taxonGroup(row({ taxonomic_class: 'Mammalia' }))).toBe('other');
    expect(taxonGroup(row({ taxonomic_class: '' }))).toBe('other');
    expect(taxonGroup(row({}))).toBeNull();
  });
});

describe('nextSort', () => {
  it('flips the active column and starts a new one in its natural direction', () => {
    expect(nextSort(DEFAULT_PHONE_SORT, 'count')).toEqual({ key: 'count', dir: 'asc' });
    expect(nextSort(DEFAULT_PHONE_SORT, 'name')).toEqual({ key: 'name', dir: 'asc' });
    expect(nextSort(DEFAULT_PHONE_SORT, 'conf')).toEqual({ key: 'conf', dir: 'desc' });
  });
});

describe('sortRows', () => {
  const rows = [
    row({
      scientific_name: 'a',
      common_name: 'Wren',
      count: 2,
      max_confidence: 0.9,
      latest_heard: '07:00:00',
    }),
    row({
      scientific_name: 'b',
      common_name: 'Crow',
      count: 5,
      max_confidence: 0.6,
      latest_heard: '09:00:00',
    }),
    row({
      scientific_name: 'c',
      common_name: 'Owl',
      count: 2,
      max_confidence: 0.7,
      latest_heard: '05:00:00',
    }),
  ];
  const ids = (sorted: DailySpeciesSummary[]) => sorted.map(r => r.scientific_name);
  const nameOf = (r: DailySpeciesSummary) => r.common_name;

  it('sorts by each column without mutating the input', () => {
    expect(ids(sortRows(rows, { key: 'count', dir: 'desc' }, nameOf))).toEqual(['b', 'a', 'c']);
    expect(ids(sortRows(rows, { key: 'name', dir: 'asc' }, nameOf))).toEqual(['b', 'c', 'a']);
    expect(ids(sortRows(rows, { key: 'conf', dir: 'desc' }, nameOf))).toEqual(['a', 'c', 'b']);
    expect(ids(sortRows(rows, { key: 'latest', dir: 'asc' }, nameOf))).toEqual(['c', 'a', 'b']);
    expect(ids(rows)).toEqual(['a', 'b', 'c']);
  });
});

describe('peakHour', () => {
  it('returns the busiest hour up to maxHour, or null without detections', () => {
    const counts = Array.from({ length: 24 }, () => 0);
    expect(peakHour(counts, 23)).toBeNull();
    counts[6] = 3;
    counts[20] = 9;
    expect(peakHour(counts, 23)).toBe(20);
    expect(peakHour(counts, 12)).toBe(6);
  });
});

describe('axisTicks', () => {
  it('ends on maxHour and never puts a tick within 4 hours of it', () => {
    for (let maxHour = 0; maxHour <= 23; maxHour++) {
      const ticks = axisTicks(maxHour);
      expect(ticks.at(-1)).toBe(maxHour);
      for (const tick of ticks.slice(0, -1)) expect(maxHour - tick).toBeGreaterThanOrEqual(4);
    }
    expect(axisTicks(23)).toEqual([0, 6, 12, 18, 23]);
    expect(axisTicks(14)).toEqual([0, 6, 14]);
  });
});

describe('formatClock', () => {
  it('reads HH:MM from a clock time or an RFC3339 timestamp', () => {
    expect(formatClock('07:05:00')).toBe('07:05');
    expect(formatClock('2026-03-01T18:42:10+02:00')).toBe('18:42');
    expect(formatClock(undefined)).toBe('');
  });
});
