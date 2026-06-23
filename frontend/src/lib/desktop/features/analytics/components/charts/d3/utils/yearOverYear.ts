// Year-over-year tracker data + pure helpers.
//
// The backend (GET /api/v2/analytics/time/year-over-year) returns the current year-to-date cumulative
// detection count versus the same calendar span one year earlier: one point per current-year calendar
// day from Jan 1 through the requested date. `thisYear`/`lastYear` are the running cumulative counts
// (monotonic non-decreasing by construction); `delta` is thisYear - lastYear (positive = the current
// year is running ahead). Points are aligned by calendar (month, day) so seasonality lines up across a
// leap boundary; `monthDay` is that year-independent alignment key.

export interface YearOverYearPoint {
  /** Current-year station-local calendar day, YYYY-MM-DD (the x position). */
  date: string;
  /** Year-independent calendar key, MM-DD (shared alignment key, shown in the tooltip). */
  monthDay: string;
  /** Cumulative current-year detections through this calendar day. */
  thisYear: number;
  /** Cumulative previous-year detections through the same calendar day. */
  lastYear: number;
  /** thisYear - lastYear (positive = current year ahead of last). */
  delta: number;
}

export interface YearOverYearData {
  /** The current calendar year being charted. */
  currentYear: number;
  /** The previous calendar year being compared against. */
  previousYear: number;
  points: YearOverYearPoint[];
}

/**
 * Peak cumulative across BOTH series (current and previous year). Drives the registry's
 * not-enough-data check: a full-year payload always carries many points, so the gate must key on how
 * many detections actually accumulated in either year rather than the point count. Keying on both
 * years (not just the current one) keeps an early-in-the-year "we are far behind last year" chart
 * meaningful when the current series is still near zero. Both series are monotonic non-decreasing by
 * construction, but this scans for the maximum to stay robust against a malformed payload. An empty
 * series is 0.
 */
export function peakCumulative(data: YearOverYearData): number {
  let max = 0;
  for (const p of data.points) {
    if (p.thisYear > max) max = p.thisYear;
    if (p.lastYear > max) max = p.lastYear;
  }
  return max;
}
