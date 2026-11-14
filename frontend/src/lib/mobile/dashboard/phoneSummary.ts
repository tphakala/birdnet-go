// phoneSummary.ts - Pure helpers for the phone daily-summary table.

import type { DailySpeciesSummary } from '$lib/types/detection.types';

/** Column the phone species table can be sorted by. */
export type PhoneSortKey = 'count' | 'name' | 'conf' | 'latest';

/** Current sort column and direction. */
export interface PhoneSort {
  key: PhoneSortKey;
  dir: 'asc' | 'desc';
}

/** Default sort: busiest species first, as the desktop heatmap orders them. */
export const DEFAULT_PHONE_SORT: PhoneSort = { key: 'count', dir: 'desc' };

/** Taxon group a row belongs to, from the label's taxonomic class. */
export type TaxonGroup = 'bird' | 'bat' | 'other';

/** Taxon groups in display order. */
export const TAXON_GROUPS: readonly TaxonGroup[] = ['bird', 'bat', 'other'];

/** Taxonomic class names stored on datastore labels, mapped to their group. */
const TAXON_GROUP_BY_CLASS: Readonly<Record<string, TaxonGroup>> = {
  Aves: 'bird',
  Chiroptera: 'bat',
};

/** Pixels per hour in the hourly bar charts: a 3 px bar plus a 1 px gap. */
export const BAR_STRIDE_PX = 4;

/** Hours between hour-axis ticks. */
const TICK_STEP_HOURS = 6;

/** Closest a tick may sit to the final (current-hour) tick: 4 h x 4 px fits a two-digit label. */
const MIN_TICK_GAP_HOURS = 4;

/**
 * Group for a row; a label without a class (e.g. multi-taxa models) is "other". Returns
 * null while the class is not known yet: rows added live by SSE carry none until the
 * summary is refetched, so they are not counted in any group and pass every filter.
 */
export function taxonGroup(row: DailySpeciesSummary): TaxonGroup | null {
  if (row.taxonomic_class === undefined) return null;
  return TAXON_GROUP_BY_CLASS[row.taxonomic_class] ?? 'other';
}

/**
 * Next sort after tapping a column header: the active column flips direction, a new
 * column starts A-Z for names and highest-first for numbers.
 */
export function nextSort(current: PhoneSort, key: PhoneSortKey): PhoneSort {
  if (current.key === key) return { key, dir: current.dir === 'asc' ? 'desc' : 'asc' };
  return { key, dir: key === 'name' ? 'asc' : 'desc' };
}

/** Returns a sorted copy of rows; nameOf supplies the displayed (localized) name. */
export function sortRows(
  rows: DailySpeciesSummary[],
  sort: PhoneSort,
  nameOf: (_row: DailySpeciesSummary) => string
): DailySpeciesSummary[] {
  const sign = sort.dir === 'asc' ? 1 : -1;
  const compare = (a: DailySpeciesSummary, b: DailySpeciesSummary): number => {
    switch (sort.key) {
      case 'name':
        return nameOf(a).localeCompare(nameOf(b));
      case 'conf':
        return (a.max_confidence ?? 0) - (b.max_confidence ?? 0);
      case 'latest':
        return a.latest_heard.localeCompare(b.latest_heard);
      case 'count':
        return a.count - b.count;
    }
  };
  return [...rows].sort((a, b) => sign * compare(a, b) || b.count - a.count);
}

/** Hour (0..maxHour) with the most detections, or null when there are none. */
export function peakHour(counts: number[], maxHour: number): number | null {
  let best: number | null = null;
  let bestCount = 0;
  counts.slice(0, maxHour + 1).forEach((count, hour) => {
    if (count > bestCount) {
      best = hour;
      bestCount = count;
    }
  });
  return best;
}

/**
 * Hour-axis ticks for a chart covering hours 0..maxHour: every TICK_STEP_HOURS plus
 * maxHour itself. A step tick closer than MIN_TICK_GAP_HOURS to maxHour is dropped so
 * the two labels never overlap.
 */
export function axisTicks(maxHour: number): number[] {
  const ticks: number[] = [];
  for (let hour = 0; hour <= maxHour - MIN_TICK_GAP_HOURS; hour += TICK_STEP_HOURS) {
    ticks.push(hour);
  }
  ticks.push(maxHour);
  return ticks;
}

/** "HH:MM" from an "HH:MM:SS" or RFC3339 timestamp, or '' when there is none. */
export function formatClock(value: string | undefined): string {
  return /(?:^|T)(\d{2}:\d{2})/.exec(value ?? '')?.[1] ?? '';
}

/** "HH:00" for an hour of the day. */
export function formatHour(hour: number): string {
  return `${String(hour).padStart(2, '0')}:00`;
}
