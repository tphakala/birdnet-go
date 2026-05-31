import { describe, it, expect } from 'vitest';
import {
  TIME_OF_DAY_PERIODS,
  bucketHourlyByPeriod,
  aggregateTrendPoints,
  mapNewSpecies,
} from './analyticsTransforms';

describe('bucketHourlyByPeriod', () => {
  it('exposes six period labels in order', () => {
    expect(TIME_OF_DAY_PERIODS).toEqual([
      'Night (0-4)',
      'Dawn (5-8)',
      'Morning (9-11)',
      'Afternoon (12-16)',
      'Evening (17-19)',
      'Night (20-23)',
    ]);
  });

  it('returns a zero-filled bucket for empty input', () => {
    const result = bucketHourlyByPeriod([]);
    expect(result).toHaveLength(6);
    expect(result.every(b => b.value === 0)).toBe(true);
    expect(result.map(b => b.label)).toEqual([...TIME_OF_DAY_PERIODS]);
  });

  it('places each boundary hour in the correct period', () => {
    // One detection in each of the 24 hours
    const hourly = Array.from({ length: 24 }, (_, hour) => ({ hour, count: 1 }));
    const result = bucketHourlyByPeriod(hourly);

    // Night (0-4) -> hours 0,1,2,3,4 = 5
    expect(result[0].value).toBe(5);
    // Dawn (5-8) -> hours 5,6,7,8 = 4
    expect(result[1].value).toBe(4);
    // Morning (9-11) -> hours 9,10,11 = 3
    expect(result[2].value).toBe(3);
    // Afternoon (12-16) -> hours 12,13,14,15,16 = 5
    expect(result[3].value).toBe(5);
    // Evening (17-19) -> hours 17,18,19 = 3
    expect(result[4].value).toBe(3);
    // Night (20-23) -> hours 20,21,22,23 = 4
    expect(result[5].value).toBe(4);
  });

  it('sums counts that fall into the same period', () => {
    const result = bucketHourlyByPeriod([
      { hour: 9, count: 2 },
      { hour: 10, count: 3 },
      { hour: 11, count: 5 },
    ]);
    expect(result[2].value).toBe(10); // Morning bucket
  });

  it('ignores out-of-range hours by treating them as the final Night bucket', () => {
    // The original code uses an else branch for any hour >= 20, so an
    // unexpected hour value falls into the last bucket rather than throwing.
    const result = bucketHourlyByPeriod([{ hour: 99, count: 4 }]);
    expect(result[5].value).toBe(4);
  });
});

describe('aggregateTrendPoints', () => {
  it('returns an empty array for null input', () => {
    expect(aggregateTrendPoints(null)).toEqual([]);
  });

  it('returns an empty array when there are no data points', () => {
    expect(aggregateTrendPoints({ data: [] })).toEqual([]);
  });

  it('aggregates counts per date and sorts ascending by date', () => {
    const result = aggregateTrendPoints({
      data: [
        { date: '2024-01-03', count: 2 },
        { date: '2024-01-01', count: 1 },
        { date: '2024-01-03', count: 5 },
        { date: '2024-01-02', count: 4 },
      ],
    });

    expect(result).toHaveLength(3);
    expect(result.map(p => p.value)).toEqual([1, 4, 7]);
    expect(result.every(p => p.date instanceof Date)).toBe(true);
    expect(result[0].date.getTime()).toBeLessThan(result[1].date.getTime());
    expect(result[1].date.getTime()).toBeLessThan(result[2].date.getTime());
  });

  it('drops points with unparseable dates', () => {
    const result = aggregateTrendPoints({
      data: [
        { date: 'not-a-date', count: 9 },
        { date: '2024-02-01', count: 3 },
      ],
    });
    expect(result).toHaveLength(1);
    expect(result[0].value).toBe(3);
  });
});

describe('mapNewSpecies', () => {
  it('returns an empty array for empty input', () => {
    expect(mapNewSpecies([])).toEqual([]);
  });

  it('maps snake_case API fields to a Date-bearing shape', () => {
    const result = mapNewSpecies([
      {
        common_name: 'European Robin',
        scientific_name: 'Erithacus rubecula',
        first_heard_date: '2024-03-15',
      },
    ]);

    expect(result).toHaveLength(1);
    expect(result[0].commonName).toBe('European Robin');
    expect(result[0].scientificName).toBe('Erithacus rubecula');
    expect(result[0].firstHeard).toBeInstanceOf(Date);
    // parseLocalDateString yields local midnight for the given calendar day
    expect(result[0].firstHeard.getFullYear()).toBe(2024);
    expect(result[0].firstHeard.getMonth()).toBe(2); // March (0-based)
    expect(result[0].firstHeard.getDate()).toBe(15);
  });

  it('falls back to the scientific name when common name is missing', () => {
    const result = mapNewSpecies([
      {
        common_name: '',
        scientific_name: 'Turdus merula',
        first_heard_date: '2024-04-01',
      },
    ]);
    expect(result[0].commonName).toBe('Turdus merula');
  });

  it('drops entries with missing or unparseable first_heard_date', () => {
    const result = mapNewSpecies([
      { common_name: 'A', scientific_name: 'a', first_heard_date: '' },
      { common_name: 'B', scientific_name: 'b', first_heard_date: 'bogus' },
      { common_name: 'C', scientific_name: 'c', first_heard_date: '2024-05-05' },
    ]);
    expect(result).toHaveLength(1);
    expect(result[0].commonName).toBe('C');
  });
});
