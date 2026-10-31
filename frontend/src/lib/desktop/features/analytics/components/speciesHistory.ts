// speciesHistory.ts - Daily count series for SpeciesHistoryModal.

import { addDays, parseLocalDateString } from '$lib/utils/date';

/** One day's detection count. */
export interface DailyCountPoint {
  date: Date;
  value: number;
}

/**
 * Every day from startDate to endDate (YYYY-MM-DD, inclusive) with its count. The API
 * only returns days with detections, so missing days are filled with zero; otherwise a
 * line chart would bridge the days the species was absent.
 */
export function fillDailyCounts(
  counts: { date: string; count: number }[],
  startDate: string,
  endDate: string
): DailyCountPoint[] {
  const countByDay = new Map(counts.map(d => [d.date, d.count]));
  const points: DailyCountPoint[] = [];
  for (let day = startDate; day <= endDate; day = addDays(day, 1)) {
    const date = parseLocalDateString(day);
    if (date) points.push({ date, value: countByDay.get(day) ?? 0 });
  }
  return points;
}
