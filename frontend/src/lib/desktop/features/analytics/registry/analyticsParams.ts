/**
 * Pure (de)serialisation of analytics hub state to/from the URL query string,
 * plus date-range resolution. Kept free of Svelte and DOM dependencies so it is
 * directly unit-testable and reusable by the control bar, the hub page, and the
 * registry fetchers.
 *
 * URL contract (all in the `?query`):
 *   tab     active tab group (omitted when it equals the default tab)
 *   range   week|month|quarter|year|custom (omitted when `month`, the default)
 *   start   YYYY-MM-DD, only meaningful (and serialised) when range=custom
 *   end     YYYY-MM-DD, only meaningful (and serialised) when range=custom
 *   species comma-separated scientific names (omitted when none selected)
 *   source  audio source/mic id (omitted when all sources)
 */
import { getLocalDateString, parseLocalDateString } from '$lib/utils/date';

import type { AnalyticsParams, ChartGroup, DateRangePreset } from './types';

/** Tab groups in display order. */
export const GROUP_ORDER: readonly ChartGroup[] = [
  'overview',
  'patterns',
  'trends',
  'biodiversity',
  'quality',
] as const;

const VALID_GROUPS = new Set<string>(GROUP_ORDER);
const VALID_RANGES = new Set<string>(['week', 'month', 'quarter', 'year', 'custom']);

/** Default range when the URL omits `range`. Matches the legacy page default. */
export const DEFAULT_RANGE: DateRangePreset = 'month';

/**
 * Subtracts whole calendar days from a date in local time. Using setDate (rather
 * than millisecond arithmetic) keeps preset windows DST-safe: a "7 days ago"
 * boundary lands on the correct local date even across a DST transition.
 */
function subtractLocalDays(base: Date, days: number): Date {
  const d = new Date(base);
  d.setDate(d.getDate() - days);
  return d;
}

/** Rolling-window lengths in days for the preset ranges. */
const RANGE_DAYS: Record<Exclude<DateRangePreset, 'custom'>, number> = {
  week: 7,
  month: 30,
  quarter: 90,
  year: 365,
};

/**
 * Local YYYY-MM-DD formatter for API calls. Aliases the project's canonical
 * `getLocalDateString` util under a domain name used throughout the analytics
 * hub (avoids the UTC shift that `toISOString()` would introduce).
 */
export const formatDateForAPI = getLocalDateString;

/**
 * Resolves a range preset (and optional custom start/end) into a concrete
 * `[start, end]` Date pair. Mirrors the legacy AdvancedAnalytics behavior:
 * presets are rolling windows ending at `now`; custom uses the provided dates
 * with a one-month fallback when missing.
 *
 * `now` is injectable so callers (and tests) get deterministic ranges.
 */
export function resolveDateRange(
  range: DateRangePreset,
  start: string,
  end: string,
  now: Date = new Date()
): [Date, Date] {
  if (range === 'custom') {
    const monthAgo = subtractLocalDays(now, RANGE_DAYS.month);
    const startDate = start ? (parseLocalDateString(start) ?? monthAgo) : monthAgo;
    const endDate = end ? (parseLocalDateString(end) ?? now) : now;
    return [startDate, endDate];
  }

  // eslint-disable-next-line security/detect-object-injection -- range is a typed preset enum, not user input
  const days = RANGE_DAYS[range];
  return [subtractLocalDays(now, days), now];
}

interface ParseOptions {
  /** Tab used when `?tab=` is absent or invalid. */
  defaultTab?: ChartGroup;
  /** Injectable clock for deterministic range resolution. */
  now?: Date;
}

/**
 * Parses an analytics hub state from a URL query string (e.g. the value of
 * `window.location.search`, with or without a leading `?`).
 *
 * Invalid `tab`/`range` values fall back to defaults so a hand-edited or stale
 * URL never throws or renders a broken view.
 */
export function parseAnalyticsParams(search: string, opts: ParseOptions = {}): AnalyticsParams {
  // URLSearchParams strips a single leading '?' itself, so pass the value as-is.
  const sp = new URLSearchParams(search);
  const now = opts.now ?? new Date();
  const defaultTab = opts.defaultTab ?? 'overview';

  const rawTab = sp.get('tab');
  const tab: ChartGroup = rawTab && VALID_GROUPS.has(rawTab) ? (rawTab as ChartGroup) : defaultTab;

  const rawRange = sp.get('range');
  const range: DateRangePreset =
    rawRange && VALID_RANGES.has(rawRange) ? (rawRange as DateRangePreset) : DEFAULT_RANGE;

  const start = sp.get('start') ?? '';
  const end = sp.get('end') ?? '';

  const speciesRaw = sp.get('species') ?? '';
  const species = speciesRaw
    ? // Dedupe so a hand-edited URL with repeats (?species=A,A) does not produce
      // duplicate selections / duplicate D3 series keys.
      [
        ...new Set(
          speciesRaw
            .split(',')
            .map(s => s.trim())
            .filter(Boolean)
        ),
      ]
    : [];

  const source = sp.get('source') ?? '';

  const [startDate, endDate] = resolveDateRange(range, start, end, now);

  return { tab, range, start, end, species, source, startDate, endDate };
}

interface SerializeOptions {
  /** Tab that is omitted from the URL (the default landing tab). */
  defaultTab?: ChartGroup;
}

/**
 * Serialises analytics hub state into a query string (no leading `?`), omitting
 * defaults and empties so URLs stay clean and deep-linkable. The output is the
 * exact inverse of {@link parseAnalyticsParams} given matching options.
 */
export function serializeAnalyticsParams(
  params: AnalyticsParams,
  opts: SerializeOptions = {}
): string {
  const sp = new URLSearchParams();
  const defaultTab = opts.defaultTab ?? 'overview';

  if (params.tab !== defaultTab) {
    sp.set('tab', params.tab);
  }
  if (params.range !== DEFAULT_RANGE) {
    sp.set('range', params.range);
  }
  // start/end are only meaningful for custom ranges; presets are self-describing.
  if (params.range === 'custom') {
    if (params.start) sp.set('start', params.start);
    if (params.end) sp.set('end', params.end);
  }
  if (params.species.length > 0) {
    sp.set('species', params.species.join(','));
  }
  if (params.source) {
    sp.set('source', params.source);
  }

  return sp.toString();
}
