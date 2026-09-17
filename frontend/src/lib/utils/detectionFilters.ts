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
  startDate: 'start_date',
  endDate: 'end_date',
  confidenceMin: 'confidenceMin',
  confidenceMax: 'confidenceMax',
  verified: 'verified',
  locked: 'locked',
  timeOfDay: 'timeOfDay',
  source: 'source',
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

/**
 * Read the filter set out of a URL query string.
 *
 * Note that `search` is shared with the header search box, which writes the same
 * parameter; that is deliberate, so typing in either place produces one filtered
 * view rather than two competing notions of "the current query".
 */
export function parseDetectionFilters(params: URLSearchParams): DetectionFilters {
  const min = parseConfidence(params.get(PARAM.confidenceMin), CONFIDENCE_MIN);
  const max = parseConfidence(params.get(PARAM.confidenceMax), CONFIDENCE_MAX);

  return {
    search: params.get(PARAM.search)?.trim() ?? '',
    startDate: parseDate(params.get(PARAM.startDate)),
    endDate: parseDate(params.get(PARAM.endDate)),
    // An inverted range would be rejected by the API, so normalize a bad bookmark
    // into the equivalent valid range instead of failing the whole request.
    confidenceMin: Math.min(min, max),
    confidenceMax: Math.max(min, max),
    verified: parseEnum(params.get(PARAM.verified), VERIFIED_VALUES, ''),
    locked: parseEnum(params.get(PARAM.locked), LOCKED_VALUES, ''),
    timeOfDay: parseEnum(params.get(PARAM.timeOfDay), TIME_OF_DAY_VALUES, ''),
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
  setOrDelete(PARAM.startDate, filters.startDate);
  setOrDelete(PARAM.endDate, filters.endDate);
  setOrDelete(PARAM.verified, filters.verified);
  setOrDelete(PARAM.locked, filters.locked);
  setOrDelete(PARAM.timeOfDay, filters.timeOfDay);
  setOrDelete(PARAM.source, filters.source.trim());

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
    source: value(filters.source.trim()),
  };
}

/** A fresh copy of the "nothing filtered" state. */
export function emptyDetectionFilters(): DetectionFilters {
  return { ...DEFAULT_DETECTION_FILTERS };
}
