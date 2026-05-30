/**
 * Pure data transforms for the Analytics page charts.
 *
 * Extracted from Analytics.svelte so the bucketing/aggregation logic can be
 * unit-tested in isolation and reused by the D3 chart components. None of these
 * functions touch the DOM or any framework state.
 */
import { parseLocalDateString } from '$lib/utils/date';

/**
 * Time-of-day period labels, in display order. The hour boundaries match the
 * previous implementation exactly:
 *   Night    hours 0-4   (>= 0  && < 5)
 *   Dawn     hours 5-8   (>= 5  && < 9)
 *   Morning  hours 9-11  (>= 9  && < 12)
 *   Afternoon hours 12-16 (>= 12 && < 17)
 *   Evening  hours 17-19 (>= 17 && < 20)
 *   Night    hours 20-23 (else)
 */
export const TIME_OF_DAY_PERIODS = [
  'Night (0-4)',
  'Dawn (5-8)',
  'Morning (9-11)',
  'Afternoon (12-16)',
  'Evening (17-19)',
  'Night (20-23)',
] as const;

export interface CategoryDatum {
  label: string;
  value: number;
}

export interface TrendPoint {
  date: Date;
  value: number;
}

export interface HourlyDatum {
  hour: number;
  count: number;
}

export interface TrendResponse {
  data: { date: string; count: number }[];
}

export interface NewSpeciesApiDatum {
  common_name: string;
  scientific_name: string;
  first_heard_date: string;
}

export interface NewSpeciesPoint {
  commonName: string;
  scientificName: string;
  firstHeard: Date;
}

/**
 * Map an hour (0-23) to its time-of-day period index. Any hour not matched by
 * the earlier ranges falls into the final Night bucket, mirroring the original
 * else branch so unexpected values never throw.
 */
function periodIndexForHour(hour: number): number {
  if (hour >= 0 && hour < 5) return 0;
  if (hour >= 5 && hour < 9) return 1;
  if (hour >= 9 && hour < 12) return 2;
  if (hour >= 12 && hour < 17) return 3;
  if (hour >= 17 && hour < 20) return 4;
  return 5;
}

/**
 * Bucket hourly detection counts into the six time-of-day periods.
 * Always returns six entries in TIME_OF_DAY_PERIODS order, zero-filled.
 */
export function bucketHourlyByPeriod(hourly: HourlyDatum[]): CategoryDatum[] {
  const counts = new Array<number>(TIME_OF_DAY_PERIODS.length).fill(0);

  if (Array.isArray(hourly)) {
    for (const entry of hourly) {
      const index = periodIndexForHour(entry.hour);
      // eslint-disable-next-line security/detect-object-injection -- index is constrained to 0-5 by periodIndexForHour
      counts[index] += entry.count;
    }
  }

  return TIME_OF_DAY_PERIODS.map((label, index) => ({
    label,
    // eslint-disable-next-line security/detect-object-injection -- index iterates a fixed-length array
    value: counts[index],
  }));
}

/**
 * Aggregate daily trend points by date (summing duplicate dates), convert the
 * date strings to Date objects, and sort ascending by date. Points whose date
 * cannot be parsed are dropped.
 */
export function aggregateTrendPoints(response: TrendResponse | null): TrendPoint[] {
  const rows = response?.data ?? [];
  if (!Array.isArray(rows) || rows.length === 0) return [];

  const totals = new Map<string, number>();
  for (const entry of rows) {
    const current = totals.get(entry.date) ?? 0;
    totals.set(entry.date, current + entry.count);
  }

  const points: TrendPoint[] = [];
  for (const [dateStr, value] of totals.entries()) {
    const date = parseLocalDateString(dateStr);
    if (!date) continue;
    points.push({ date, value });
  }

  points.sort((a, b) => a.date.getTime() - b.date.getTime());
  return points;
}

/**
 * Convert new-species API rows into chart-ready points with a parsed Date.
 * Rows with a missing or unparseable first_heard_date are dropped. The common
 * name falls back to the scientific name when empty.
 */
export function mapNewSpecies(data: NewSpeciesApiDatum[]): NewSpeciesPoint[] {
  if (!Array.isArray(data)) return [];

  const points: NewSpeciesPoint[] = [];
  for (const item of data) {
    const firstHeard = parseLocalDateString(item.first_heard_date);
    if (!firstHeard) continue;
    points.push({
      commonName: item.common_name || item.scientific_name,
      scientificName: item.scientific_name,
      firstHeard,
    });
  }
  return points;
}
