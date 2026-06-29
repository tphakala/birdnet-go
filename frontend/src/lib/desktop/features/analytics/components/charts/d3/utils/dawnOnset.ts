// Dawn-chorus onset chart data + pure helpers.
//
// The backend (GET /api/v2/analytics/time/dawn-onset) returns one entry per calendar day in the
// requested range. `onsetRelMinutes` is the day's chorus onset relative to civil dawn (negative =
// before civil dawn) or null when the day had too few detections or no civil dawn (polar day /
// night). Emitting every day - including nulls - gives the chart a continuous date axis so its
// trend line can break over gaps instead of interpolating a misleading line across them.

export interface DawnOnsetPoint {
  /** Station-local calendar day, YYYY-MM-DD. */
  date: string;
  /** Onset minute-of-day minus civil dawn's minute-of-day; null on gap days. */
  onsetRelMinutes: number | null;
  /** Day's detection count (false positives excluded), shown in the tooltip. */
  detectionCount: number;
}

export interface DawnOnsetData {
  points: DawnOnsetPoint[];
}

/** Count of plotted (non-null) onset points; drives the registry's not-enough-data check. */
export function onsetCount(data: DawnOnsetData): number {
  return data.points.reduce((n, p) => (p.onsetRelMinutes === null ? n : n + 1), 0);
}

/**
 * Centered moving average of `onsetRelMinutes` over a window of `windowDays` consecutive daily
 * points (the backend emits one point per day). Each window uses only its non-null values; the
 * result is null when fewer than `minSamples` non-null values fall in the window, so the rendered
 * trend line breaks over sustained gaps rather than interpolating across them. The returned array
 * is aligned 1:1 with `points`.
 */
export function movingAverageTrend(
  points: DawnOnsetPoint[],
  windowDays: number,
  minSamples: number
): (number | null)[] {
  const half = Math.floor(windowDays / 2);
  return points.map((_, i) => {
    const lo = Math.max(0, i - half);
    const hi = Math.min(points.length, i + half + 1);
    const values = points
      .slice(lo, hi)
      .map(p => p.onsetRelMinutes)
      .filter((v): v is number => v !== null);
    if (values.length < minSamples) return null;
    return values.reduce((sum, v) => sum + v, 0) / values.length;
  });
}
