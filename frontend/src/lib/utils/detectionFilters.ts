/**
 * Conversion between the detections filter panel's state and the URL query
 * string / API parameters.
 *
 * The filter set lives in the URL so a filtered view is shareable, survives a
 * reload, and responds to back/forward navigation. Keeping the parsing and
 * serialization in one place means the panel, the page fetch, and the bulk
 * "select all matching" request cannot drift into filtering by different things.
 */

import {
  DEFAULT_DETECTION_FILTERS,
  type DetectionFilters,
  type DetectionLockedFilter,
  type DetectionTimeOfDayFilter,
  type DetectionVerifiedFilter,
} from '$lib/types/detection.types';

/** Query-parameter names, matching what GET /api/v2/detections reads. */
const PARAM = {
  search: 'search',
  species: 'species',
  startDate: 'start_date',
  endDate: 'end_date',
  confidenceMin: 'confidenceMin',
  confidenceMax: 'confidenceMax',
  verified: 'verified',
  locked: 'locked',
  timeOfDay: 'timeOfDay',
  hourRange: 'hourRange',
  source: 'source',
} as const;

/**
 * Parameters a dashboard or analytics drill-down links in with, each of which
 * narrows the list exactly like one of the panel's own filters. They are folded
 * into the corresponding field on arrival and removed once the panel submits, so
 * a drill-down can be seen and edited instead of surviving as a constraint with
 * no control anywhere in the form.
 */
const LEGACY_PARAM = {
  date: 'date',
  hour: 'hour',
  duration: 'duration',
} as const;

const VERIFIED_VALUES: readonly DetectionVerifiedFilter[] = [
  'correct',
  'false_positive',
  'unverified',
];
const LOCKED_VALUES: readonly DetectionLockedFilter[] = ['true', 'false'];
const TIME_OF_DAY_VALUES: readonly DetectionTimeOfDayFilter[] = [
  'day',
  'night',
  'sunrise',
  'sunset',
];

const CONFIDENCE_MIN = 0;
const CONFIDENCE_MAX = 100;

const HOUR_MIN = 0;
const HOUR_MAX = 23;

/** ISO date, as the date inputs and the API both expect it. */
const ISO_DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

/**
 * Coerce a query-parameter value to one of an allowed set, falling back to the
 * "filter off" empty string. Unknown values are dropped rather than passed
 * through: the API rejects them, which would turn a stale bookmark into an error
 * page instead of an unfiltered list.
 */
function parseEnum<T extends string>(
  raw: string | null,
  allowed: readonly T[],
  fallback: T
): T | typeof fallback {
  if (!raw) return fallback;
  const value = raw.trim().toLowerCase();
  return (allowed as readonly string[]).includes(value) ? (value as T) : fallback;
}

/**
 * Parse a confidence bound given as a whole percentage, clamping into [0, 100].
 * Returns the supplied default when absent or unparseable.
 */
function parseConfidence(raw: string | null, fallback: number): number {
  if (raw === null || raw.trim() === '') return fallback;
  const value = Number(raw);
  if (!Number.isFinite(value)) return fallback;
  return Math.min(CONFIDENCE_MAX, Math.max(CONFIDENCE_MIN, Math.round(value)));
}

/** Accept only well-formed ISO dates; anything else means "no bound". */
function parseDate(raw: string | null): string {
  if (!raw) return '';
  const value = raw.trim();
  return ISO_DATE_RE.test(value) ? value : '';
}

/** A whole hour in [0, 23], or '' when the value is absent or unusable. */
function parseHour(raw: string | null): string {
  if (raw === null || raw.trim() === '') return '';
  const value = Number(raw);
  if (!Number.isInteger(value) || value < HOUR_MIN || value > HOUR_MAX) return '';
  return String(value);
}

/**
 * Read the clock-hour band, preferring the panel's own `hourRange` and falling
 * back to the `hour`/`duration` pair an hourly drill-down links in with.
 *
 * `duration` counts hours inclusive of the first, so hour=7&duration=3 is 07:00
 * through 09:59. A duration that would run past midnight is clamped to 23 rather
 * than wrapped: the API rejects an inverted range, so wrapping here would produce
 * a band it silently drops.
 */
function parseHourBand(params: URLSearchParams): { hourStart: string; hourEnd: string } {
  const raw = params.get(PARAM.hourRange)?.trim() ?? '';
  if (raw !== '') {
    const [start, end] = raw.includes('-') ? raw.split('-', 2) : [raw, raw];
    const hourStart = parseHour(start);
    const hourEnd = parseHour(end);
    // A half-parsed range would filter by something the user never asked for, so
    // an unusable end drops the whole band rather than half of it.
    if (hourStart === '' || hourEnd === '') return { hourStart: '', hourEnd: '' };
    return orderHourBand(hourStart, hourEnd);
  }

  const hour = parseHour(params.get(LEGACY_PARAM.hour));
  if (hour === '') return { hourStart: '', hourEnd: '' };

  const duration = Number(params.get(LEGACY_PARAM.duration));
  if (!Number.isInteger(duration) || duration <= 1) return { hourStart: hour, hourEnd: hour };
  return { hourStart: hour, hourEnd: String(Math.min(HOUR_MAX, Number(hour) + duration - 1)) };
}

/** Put the band's ends in order, so a backwards bookmark still describes a range. */
function orderHourBand(hourStart: string, hourEnd: string): { hourStart: string; hourEnd: string } {
  return Number(hourStart) <= Number(hourEnd)
    ? { hourStart, hourEnd }
    : { hourStart: hourEnd, hourEnd: hourStart };
}

/**
 * Serialize the band the way the API spells it: a bare hour for a single hour,
 * `start-end` for a range, and '' when the band is unbounded. A band with only
 * one end set is completed with the opposite extreme, which is what the open end
 * means and what the API needs to accept it.
 */
export function hourBandToParam(filters: DetectionFilters): string {
  const { hourStart, hourEnd } = filters;
  if (hourStart === '' && hourEnd === '') return '';
  const start = hourStart === '' ? String(HOUR_MIN) : hourStart;
  const end = hourEnd === '' ? String(HOUR_MAX) : hourEnd;
  return start === end ? start : `${start}-${end}`;
}

/**
 * Read the filter set out of a URL query string.
 *
 * The panel offers a single free-text species field, but two parameters can fill
 * it: `search`, which the panel itself writes, and `species`, an exact match
 * emitted by the dashboard drill-downs and the species analytics page. Folding
 * `species` in means an incoming link shows what it is filtering by instead of
 * presenting an empty form next to an already-narrowed list.
 */
export function parseDetectionFilters(params: URLSearchParams): DetectionFilters {
  const min = parseConfidence(params.get(PARAM.confidenceMin), CONFIDENCE_MIN);
  const max = parseConfidence(params.get(PARAM.confidenceMax), CONFIDENCE_MAX);

  const freeText = params.get(PARAM.search)?.trim() ?? '';
  const species = params.get(PARAM.species)?.trim() ?? '';

  // A drill-down's single `date` is the same constraint as a one-day range, so it
  // fills both ends of the panel's range rather than leaving the fields empty
  // beside a list that is in fact pinned to that day.
  const singleDate = parseDate(params.get(LEGACY_PARAM.date));
  const startDate = parseDate(params.get(PARAM.startDate)) || singleDate;
  const endDate = parseDate(params.get(PARAM.endDate)) || singleDate;

  return {
    search: freeText === '' ? species : freeText,
    startDate,
    endDate,
    // An inverted range would be rejected by the API, so normalize a bad bookmark
    // into the equivalent valid range instead of failing the whole request.
    confidenceMin: Math.min(min, max),
    confidenceMax: Math.max(min, max),
    verified: parseEnum(params.get(PARAM.verified), VERIFIED_VALUES, ''),
    locked: parseEnum(params.get(PARAM.locked), LOCKED_VALUES, ''),
    timeOfDay: parseEnum(params.get(PARAM.timeOfDay), TIME_OF_DAY_VALUES, ''),
    ...parseHourBand(params),
    source: params.get(PARAM.source)?.trim() ?? '',
  };
}

/**
 * Write the filter set into a URL query string, removing parameters that are at
 * their default. Omitting inactive filters keeps shared links short and keeps the
 * unfiltered view on the plain `/ui/detections` URL.
 */
export function applyDetectionFiltersToParams(
  params: URLSearchParams,
  filters: DetectionFilters
): void {
  const setOrDelete = (key: string, value: string) => {
    if (value) {
      params.set(key, value);
    } else {
      params.delete(key);
    }
  };

  setOrDelete(PARAM.search, filters.search.trim());
  // `species` is the exact-match form of the field the panel writes as free-text
  // `search`. Once the panel submits, the exact-match parameter has to go, or an
  // incoming drill-down would keep intersecting with the query just typed.
  params.delete(PARAM.species);
  setOrDelete(PARAM.startDate, filters.startDate);
  setOrDelete(PARAM.endDate, filters.endDate);
  setOrDelete(PARAM.verified, filters.verified);
  setOrDelete(PARAM.locked, filters.locked);
  setOrDelete(PARAM.timeOfDay, filters.timeOfDay);
  setOrDelete(PARAM.hourRange, hourBandToParam(filters));
  setOrDelete(PARAM.source, filters.source.trim());

  // The drill-down spellings have been read into the fields above, so they are
  // dropped here. Left in place they would keep narrowing the list by a day and
  // an hour that no control in the panel shows or can clear.
  params.delete(LEGACY_PARAM.date);
  params.delete(LEGACY_PARAM.hour);
  params.delete(LEGACY_PARAM.duration);

  // A bound only narrows when it is off its extreme, so a full-width band is
  // omitted entirely rather than sent as "0 to 100".
  setOrDelete(
    PARAM.confidenceMin,
    filters.confidenceMin > CONFIDENCE_MIN ? String(filters.confidenceMin) : ''
  );
  setOrDelete(
    PARAM.confidenceMax,
    filters.confidenceMax < CONFIDENCE_MAX ? String(filters.confidenceMax) : ''
  );
}

/**
 * Report whether any filter narrows the result set.
 *
 * The detections page pins the unfiltered view to a single day; a filtered view
 * must not inherit that, or a search for a species last heard in spring would
 * return nothing. Callers use this to decide whether to send a `date`.
 */
export function hasActiveDetectionFilters(filters: DetectionFilters): boolean {
  return (
    filters.search.trim() !== '' ||
    filters.startDate !== '' ||
    filters.endDate !== '' ||
    filters.confidenceMin > CONFIDENCE_MIN ||
    filters.confidenceMax < CONFIDENCE_MAX ||
    filters.verified !== '' ||
    filters.locked !== '' ||
    filters.timeOfDay !== '' ||
    filters.hourStart !== '' ||
    filters.hourEnd !== '' ||
    filters.source.trim() !== ''
  );
}

/** Count the filters that are narrowing, for the panel's summary badge. */
export function countActiveDetectionFilters(filters: DetectionFilters): number {
  let count = 0;
  if (filters.search.trim() !== '') count++;
  // A date range reads as one filter even when both ends are set.
  if (filters.startDate !== '' || filters.endDate !== '') count++;
  if (filters.confidenceMin > CONFIDENCE_MIN || filters.confidenceMax < CONFIDENCE_MAX) count++;
  if (filters.verified !== '') count++;
  if (filters.locked !== '') count++;
  if (filters.timeOfDay !== '') count++;
  // An hour band reads as one filter even when both ends are set, like the dates.
  if (filters.hourStart !== '' || filters.hourEnd !== '') count++;
  if (filters.source.trim() !== '') count++;
  return count;
}

/**
 * Build the advanced-filter half of the POST /detections/batch/resolve body.
 *
 * Bulk actions apply to whatever this resolves, so every filter that narrows the
 * visible list has to narrow this too. Keys match BatchResolveRequest.
 */
export function detectionFiltersToResolveBody(
  filters: DetectionFilters
): Record<string, string | undefined> {
  const value = (v: string) => (v === '' ? undefined : v);

  return {
    startDate: value(filters.startDate),
    endDate: value(filters.endDate),
    confidenceMin:
      filters.confidenceMin > CONFIDENCE_MIN ? String(filters.confidenceMin) : undefined,
    confidenceMax:
      filters.confidenceMax < CONFIDENCE_MAX ? String(filters.confidenceMax) : undefined,
    verified: value(filters.verified),
    locked: value(filters.locked),
    timeOfDay: value(filters.timeOfDay),
    hourRange: value(hourBandToParam(filters)),
    source: value(filters.source.trim()),
  };
}

/** A fresh copy of the "nothing filtered" state. */
export function emptyDetectionFilters(): DetectionFilters {
  return { ...DEFAULT_DETECTION_FILTERS };
}
