// Nocturnal activity clock chart data + pure helpers (design spec section 6.4).
//
// The chart overlays two data sources: hourly detection counts from the (unchanged) endpoint
// GET /api/v2/analytics/time/distribution/hourly, and sun times from GET /api/v2/analytics/sun.
// Sun event fields are minute-of-day (0..1439) in the server's local timezone, the same frame the
// hourly counts are bucketed in, so the day/night shading aligns with the hourly bars.

export const HOURS_PER_DAY = 24;
export const MINUTES_PER_DAY = 1440;
export const MINUTES_PER_HOUR = 60;

/** Sun times for the representative date, used to shade the clock's day/night arcs. */
export interface SunTimes {
  /** Representative date the sun times are for (YYYY-MM-DD, server-local). */
  date: string;
  /** Minute-of-day (0..1439) in server-local time; null if the event does not occur. */
  sunrise: number | null;
  sunset: number | null;
  /** Civil dawn/dusk; null unless a genuine civil twilight occurs (omitted at high latitudes). */
  civilDawn: number | null;
  civilDusk: number | null;
  /** false when SunCalc is unconfigured or the sun never rises/sets (polar day/night). */
  available: boolean;
}

/** The chart's combined data: hourly counts plus optional sun times for shading. */
export interface NocturnalClockData {
  /** Detection counts by hour of day (index 0..23), server-local time. */
  hourly: number[];
  /** Sun times for the day arc, or null when unavailable (chart renders without shading). */
  sun: SunTimes | null;
}

/** Total detections across all hours; drives the registry's not-enough-data check. */
export function hourlyTotal(data: NocturnalClockData): number {
  return data.hourly.reduce((sum, c) => sum + (Number.isFinite(c) ? c : 0), 0);
}

/** Largest hourly count (0 when there is no data); sizes the radial/linear value scale. */
export function maxHourly(data: NocturnalClockData): number {
  let max = 0;
  for (let h = 0; h < HOURS_PER_DAY && h < data.hourly.length; h++) {
    // eslint-disable-next-line security/detect-object-injection -- h is a bounded loop index
    const c = data.hourly[h];
    if (Number.isFinite(c) && c > max) max = c;
  }
  return max;
}

/** Hour (0..23) with the most detections, or null when there are none. Ties pick the earliest. */
export function peakHour(data: NocturnalClockData): number | null {
  let best = 0;
  let bestHour: number | null = null;
  for (let h = 0; h < HOURS_PER_DAY && h < data.hourly.length; h++) {
    // eslint-disable-next-line security/detect-object-injection -- h is a bounded loop index
    const c = data.hourly[h];
    if (Number.isFinite(c) && c > best) {
      best = c;
      bestHour = h;
    }
  }
  return bestHour;
}

/**
 * Angle (radians) for a minute-of-day on the 24h clock dial: midnight (minute 0) at the top
 * (12 o'clock), advancing clockwise. This matches d3-shape `arc` angle convention (0 = top,
 * positive = clockwise), so it can be passed directly as startAngle/endAngle.
 */
export function minuteToAngle(minute: number): number {
  return (minute / MINUTES_PER_DAY) * 2 * Math.PI;
}

/**
 * Cartesian point on a circle for a clock angle (midnight at top, clockwise) in SVG coordinates
 * where y grows downward: x = cx + r*sin(theta), y = cy - r*cos(theta). Used to place hour ticks.
 */
export function polarToCartesian(
  cx: number,
  cy: number,
  radius: number,
  angle: number
): { x: number; y: number } {
  return { x: cx + radius * Math.sin(angle), y: cy - radius * Math.cos(angle) };
}

/** Format a minute-of-day as a 24-hour "HH:MM" label (e.g. 372 -> "06:12"). */
export function formatMinuteOfDay(minute: number): string {
  const m = ((Math.round(minute) % MINUTES_PER_DAY) + MINUTES_PER_DAY) % MINUTES_PER_DAY;
  const hh = Math.floor(m / MINUTES_PER_HOUR);
  const mm = m % MINUTES_PER_HOUR;
  return `${String(hh).padStart(2, '0')}:${String(mm).padStart(2, '0')}`;
}

/**
 * Start/end angles (radians) for a clock arc spanning [startMinute, endMinute], wrapping forward
 * over midnight when the end is numerically at or before the start. That wrap happens when an
 * event's minute-of-day in the server timezone lands on the far side of midnight (a station far
 * from the server's timezone, e.g. a far-east location on a UTC server). d3-shape `arc` draws the
 * sweep from startAngle to endAngle, so the wrap is expressed by adding one full turn to endAngle
 * rather than letting it draw backwards (which would shade the night side instead of the day side).
 */
export function arcAngles(
  startMinute: number,
  endMinute: number
): { startAngle: number; endAngle: number } {
  const startAngle = minuteToAngle(startMinute);
  let endAngle = minuteToAngle(endMinute);
  if (endAngle <= startAngle) endAngle += 2 * Math.PI;
  return { startAngle, endAngle };
}

/**
 * X-position segments for shading the daytime span [startMinute, endMinute] on a linear 24h axis of
 * the given pixel width. Normally one segment; when the span wraps past midnight (end before start)
 * it splits into two: [0, end] and [start, width], so the daytime band never collapses to nothing.
 */
export function dayRegionSegments(
  startMinute: number,
  endMinute: number,
  width: number
): { x: number; width: number }[] {
  const toX = (m: number) => (m / MINUTES_PER_DAY) * width;
  const startX = toX(startMinute);
  const endX = toX(endMinute);
  if (endX >= startX) {
    return [{ x: startX, width: endX - startX }];
  }
  return [
    { x: 0, width: endX },
    { x: startX, width: width - startX },
  ];
}
