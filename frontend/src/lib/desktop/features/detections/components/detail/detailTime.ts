/**
 * Wall-clock helpers for the detection detail page.
 *
 * The API sends RFC3339 timestamps carrying the station's UTC offset. The page
 * shows station time, so these helpers read the clock fields straight from the
 * string instead of converting through the browser's time zone.
 */
import { getLocale } from '$lib/i18n';
import { addDays } from '$lib/utils/date';
import { PERCENT } from './detailFormat';
import type { DetectionDetailData, DetectionWeather, SunTimes } from './detailTypes';

export const MINUTES_PER_DAY = 1440;
export const MINUTES_PER_HOUR = 60;
const SECONDS_PER_MINUTE = 60;
const MS_PER_SECOND = 1000;

const CLOCK_PATTERN = /T(\d\d):(\d\d)/;
const SECONDS_PATTERN = /^:(\d\d)/;

interface ClockFields {
  hours: string;
  minutes: string;
  /** Empty when the timestamp carries no seconds. */
  seconds: string;
}

/** The clock fields of an RFC3339 timestamp as written, or null when it has no clock part. */
function clockFields(timestamp: string | undefined | null): ClockFields | null {
  if (!timestamp) return null;
  const match = CLOCK_PATTERN.exec(timestamp);
  if (!match) return null;
  const rest = timestamp.slice(match.index + match[0].length);
  return { hours: match[1], minutes: match[2], seconds: SECONDS_PATTERN.exec(rest)?.[1] ?? '' };
}
const DATE_PATTERN = /^(\d{4})-(\d{2})-(\d{2})/;

/** Minutes after local midnight of an RFC3339 timestamp, or null when it has no clock part. */
export function wallClockMinutes(timestamp: string | undefined | null): number | null {
  const clock = clockFields(timestamp);
  if (!clock) return null;
  return (
    Number(clock.hours) * MINUTES_PER_HOUR +
    Number(clock.minutes) +
    Number(clock.seconds || 0) / SECONDS_PER_MINUTE
  );
}

/** "HH:MM" (or "HH:MM:SS") of an RFC3339 timestamp in station time; empty when unparsable. */
export function wallClockTime(timestamp: string | undefined | null, withSeconds = false): string {
  const clock = clockFields(timestamp);
  if (!clock) return '';
  const base = `${clock.hours}:${clock.minutes}`;
  return withSeconds && clock.seconds ? `${base}:${clock.seconds}` : base;
}

/** "YYYY-MM-DD" of an RFC3339 timestamp in station time; empty when unparsable. */
export function wallClockDate(timestamp: string | undefined | null): string {
  if (!timestamp) return '';
  const match = DATE_PATTERN.exec(timestamp);
  return match ? match[0] : '';
}

const LONG_DATE: Intl.DateTimeFormatOptions = {
  weekday: 'short',
  day: 'numeric',
  month: 'short',
  year: 'numeric',
};
const SHORT_DATE: Intl.DateTimeFormatOptions = { day: 'numeric', month: 'short' };

/**
 * A calendar date ("YYYY-MM-DD", or the date part of an RFC3339 timestamp in
 * station time) in the UI language ("Sat, Nov 14, 2026" in English); with
 * `short`, the day and month only. The date is built from its fields, so the
 * browser's zone cannot shift the day.
 */
export function formatCalendarDate(date: string | undefined | null, short = false): string {
  if (!date) return '';
  const match = DATE_PATTERN.exec(date);
  if (!match) return date;
  const value = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
  return value.toLocaleDateString(getLocale(), short ? SHORT_DATE : LONG_DATE);
}

/**
 * The date and the station clock ("HH:MM") of an RFC3339 timestamp, the date in the
 * UI language. Empty when the timestamp has neither.
 */
export function formatCalendarDateTime(timestamp: string | undefined | null): string {
  const date = formatCalendarDate(wallClockDate(timestamp));
  const time = wallClockTime(timestamp);
  return [date, time].filter(Boolean).join(' ');
}

/** Seconds between two RFC3339 timestamps, or null when either is unparsable. */
export function secondsBetween(
  start: string | undefined | null,
  end: string | undefined | null
): number | null {
  if (!start || !end) return null;
  const from = Date.parse(start);
  const to = Date.parse(end);
  if (Number.isNaN(from) || Number.isNaN(to)) return null;
  return Math.round((to - from) / MS_PER_SECOND);
}

/** Position of a minute of the day on a 0 to 100 percent day axis. */
export function dayPercent(minutes: number): number {
  return (Math.min(Math.max(minutes, 0), MINUTES_PER_DAY) / MINUTES_PER_DAY) * PERCENT;
}

/** First day of a window of `days` calendar days that ends on `date` ("YYYY-MM-DD"). */
export function dailyWindowStart(date: string, days: number): string {
  return addDays(date, -(days - 1));
}

/** Minute of the station's day at which a detection was made, or null when unknown. */
export function detectionMinute(
  detection: Pick<DetectionDetailData, 'timestamp' | 'time'>
): number | null {
  return wallClockMinutes(detection.timestamp ?? `T${detection.time}`);
}

// The UTC offset at the end of an RFC3339 timestamp: "Z", "+02:00" or "-0530".
const OFFSET_PATTERN = /(Z|([+-])(\d\d):?(\d\d))$/;
const MS_PER_MINUTE = SECONDS_PER_MINUTE * MS_PER_SECOND;
// Length of "YYYY-MM-DDTHH:MM:SS" in an ISO string.
const ISO_SECONDS_LENGTH = 19;

/**
 * An RFC3339 timestamp written in the UTC offset of another one (`reference`), the same
 * instant, so the clock fields of the two can be compared. The sun endpoint writes its
 * times in the zone of the station's coordinates, which can differ from the zone a
 * detection's timestamp carries. Returned as given when either has no usable offset.
 */
export function inOffsetOf(
  timestamp: string | undefined,
  reference: string | undefined | null
): string | undefined {
  if (!timestamp || !reference) return timestamp;
  const offset = OFFSET_PATTERN.exec(reference);
  const instant = Date.parse(timestamp);
  if (!offset || Number.isNaN(instant) || !OFFSET_PATTERN.test(timestamp)) return timestamp;
  const sign = offset[2] === '-' ? -1 : 1;
  const minutes =
    offset[1] === 'Z' ? 0 : sign * (Number(offset[3]) * MINUTES_PER_HOUR + Number(offset[4]));
  const local = new Date(instant + minutes * MS_PER_MINUTE).toISOString();
  return local.slice(0, ISO_SECONDS_LENGTH) + offset[1];
}

/**
 * Sunrise and sunset of a detection's day: the sun endpoint first, the weather provider's
 * as a fallback, each written in the UTC offset of `reference` (the detection's timestamp)
 * when it is given.
 */
export function sunEventTimes(
  sun: SunTimes | null | undefined,
  weather: DetectionWeather | null | undefined,
  reference?: string | null
): { sunrise?: string; sunset?: string } {
  return {
    sunrise: inOffsetOf(sun?.sunrise ?? weather?.daily?.sunrise, reference),
    sunset: inOffsetOf(sun?.sunset ?? weather?.daily?.sunset, reference),
  };
}

type SunEvent = 'sunrise' | 'sunset';

/** How far a moment is from the nearest sunrise or sunset. */
export interface SunOffset {
  event: SunEvent;
  /** True when the moment is before the event. */
  before: boolean;
  hours: number;
  minutes: number;
}

/**
 * The nearest sunrise or sunset to a moment, looking one day either side of each
 * (approximated by the same day's times), so a detection after midnight is
 * measured against the previous evening's sunset or the coming sunrise, and
 * with only one of the two known, against the nearer day's one.
 */
export function nearestSunEvent(
  moment: number | null,
  sunrise: number | null,
  sunset: number | null
): SunOffset | null {
  if (moment === null) return null;
  const candidates: { event: SunEvent; at: number }[] = [];
  for (const [event, at] of [
    ['sunrise', sunrise],
    ['sunset', sunset],
  ] as const) {
    if (at === null) continue;
    candidates.push(
      { event, at: at - MINUTES_PER_DAY },
      { event, at },
      { event, at: at + MINUTES_PER_DAY }
    );
  }
  let best: { event: SunEvent; delta: number } | null = null;
  for (const candidate of candidates) {
    const delta = moment - candidate.at;
    if (best === null || Math.abs(delta) < Math.abs(best.delta)) {
      best = { event: candidate.event, delta };
    }
  }
  if (best === null) return null;
  const total = Math.round(Math.abs(best.delta));
  return {
    event: best.event,
    before: best.delta < 0,
    hours: Math.floor(total / MINUTES_PER_HOUR),
    minutes: total % MINUTES_PER_HOUR,
  };
}

const COMPASS_POINTS = [
  'n',
  'nne',
  'ne',
  'ene',
  'e',
  'ese',
  'se',
  'sse',
  's',
  'ssw',
  'sw',
  'wsw',
  'w',
  'wnw',
  'nw',
  'nnw',
] as const;
const FULL_CIRCLE_DEGREES = 360;
const NORTH_POINT = COMPASS_POINTS[0];

/**
 * The 16-point compass name of a bearing in degrees, as the lowercase key
 * ("n", "wnw") of its `detections.detail.compass.*` translation. A bearing that
 * is not a finite number reads as north.
 */
export function compassPointKey(degrees: number): (typeof COMPASS_POINTS)[number] {
  if (!Number.isFinite(degrees)) return NORTH_POINT;
  const step = FULL_CIRCLE_DEGREES / COMPASS_POINTS.length;
  const normalized = ((degrees % FULL_CIRCLE_DEGREES) + FULL_CIRCLE_DEGREES) % FULL_CIRCLE_DEGREES;
  const index = Math.round(normalized / step) % COMPASS_POINTS.length;
  return COMPASS_POINTS.at(index) ?? NORTH_POINT;
}
