/**
 * Advanced search query parser for BirdNET-Go
 *
 * Supports syntax like:
 * - "Robin confidence:>85"
 * - "confidence:>90 time:dawn"
 * - "Blue Jay date:today verified:correct"
 */

import { getLocalDateString } from '$lib/utils/date';

export type FilterOperator = '>' | '<' | '>=' | '<=' | '=' | ':';

// Single source of truth for recognized filter keys: both the FilterType union
// and the runtime FILTER_KEY_SET derive from this array, so they cannot drift
// apart. Adding a key here updates the type and the parser's recognition set
// together, preventing a new key that type-checks but is silently never parsed.
const FILTER_KEYS = [
  'confidence',
  'time',
  'date',
  'hour',
  'daterange',
  'verified',
  'species',
  'location',
  'source',
  'locked',
] as const;

export type FilterType = (typeof FILTER_KEYS)[number];

export interface SearchFilter {
  type: FilterType;
  operator: FilterOperator;
  value: string | number | boolean;
  value2?: string; // For range queries like hour:6-9
  raw: string; // Original filter text for display
}

export interface ParsedSearch {
  textQuery: string;
  filters: SearchFilter[];
  errors: string[];
}

// Valid time-of-day values. "sunrise"/"sunset" are canonical (they name real sun
// events, which is how the backend resolves them); "dawn"/"dusk" are kept as
// accepted aliases so existing saved searches and muscle memory keep working.
const TIME_OF_DAY_CANONICAL = ['day', 'night', 'sunrise', 'sunset'];
const TIME_OF_DAY_VALUES = [...TIME_OF_DAY_CANONICAL, 'dawn', 'dusk'];

// Legacy period names mapped onto the canonical ones the API and the detections
// filter panel both use.
const TIME_OF_DAY_ALIASES: Record<string, string> = {
  dawn: 'sunrise',
  dusk: 'sunset',
};

// Date shortcuts
const DATE_SHORTCUTS = ['today', 'yesterday', 'week', 'month'];

// Recognized filter keys (derived from FILTER_KEYS); bounds greedy multi-word value capture.
const FILTER_KEY_SET = new Set<string>(FILTER_KEYS);

// Free-text filters accept quoted or multi-word values; all others are single-token.
const FREE_TEXT_FILTERS = new Set<string>(['species', 'location', 'source']);

interface KeyToken {
  key: string;
  keyStart: number;
  valueStart: number;
}

// Find recognized "key:" tokens at word boundaries, in order of appearance.
function findFilterKeyTokens(query: string): KeyToken[] {
  const tokens: KeyToken[] = [];
  const re = /(?:^|\s)([A-Za-z]+):/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(query)) !== null) {
    const key = m[1].toLowerCase();
    if (!FILTER_KEY_SET.has(key)) continue;
    // Skip any leading whitespace the boundary group captured.
    const keyStart = m.index + m[0].length - (m[1].length + 1);
    tokens.push({ key, keyStart, valueStart: keyStart + m[1].length + 1 });
  }
  return tokens;
}

// Capture the value for a filter token: quoted or greedy for free-text filters,
// single whitespace-delimited token otherwise. `boundary` is the start index of
// the next recognized key token (or query.length) and bounds the greedy case.
function captureValue(
  query: string,
  tok: KeyToken,
  boundary: number
): { value: string; nextCursor: number } {
  const isFreeText = FREE_TEXT_FILTERS.has(tok.key);

  if (isFreeText && query[tok.valueStart] === '"') {
    const closing = query.indexOf('"', tok.valueStart + 1);
    if (closing >= 0) {
      return { value: query.slice(tok.valueStart + 1, closing), nextCursor: closing + 1 };
    }
    // Unmatched quote: consume to end of string.
    return { value: query.slice(tok.valueStart + 1).trim(), nextCursor: query.length };
  }

  if (isFreeText) {
    return { value: query.slice(tok.valueStart, boundary).trim(), nextCursor: boundary };
  }

  // Single-token filters: value is the next whitespace-delimited token.
  const wsMatch = /^\S+/.exec(query.slice(tok.valueStart));
  const token = wsMatch ? wsMatch[0] : '';
  return { value: token, nextCursor: tok.valueStart + token.length };
}

/**
 * Parse a search query string into text and filters.
 */
export function parseSearchQuery(query: string): ParsedSearch {
  const result: ParsedSearch = {
    textQuery: '',
    filters: [],
    errors: [],
  };

  if (!query.trim()) {
    return result;
  }

  const tokens = findFilterKeyTokens(query);
  if (tokens.length === 0) {
    result.textQuery = query.trim();
    return result;
  }

  const textParts: string[] = [];
  let cursor = 0;

  for (let idx = 0; idx < tokens.length; idx++) {
    // eslint-disable-next-line security/detect-object-injection -- idx is a bounded loop counter over KeyToken[]
    const tok = tokens[idx];
    // A key token swallowed by an earlier quoted/greedy value is skipped.
    if (tok.keyStart < cursor) continue;

    const before = query.slice(cursor, tok.keyStart).trim();
    if (before) textParts.push(before);

    // Boundary = the next recognized key token at or after this value start.
    let boundary = query.length;
    for (let j = idx + 1; j < tokens.length; j++) {
      // Safety guard: regex anchoring means a later token cannot start inside a
      // prior token's key span, so this is effectively the first j past idx.
      // eslint-disable-next-line security/detect-object-injection -- j is a bounded loop counter over KeyToken[]
      if (tokens[j].keyStart >= tok.valueStart) {
        // eslint-disable-next-line security/detect-object-injection -- j is a bounded loop counter over KeyToken[]
        boundary = tokens[j].keyStart;
        break;
      }
    }

    const { value, nextCursor } = captureValue(query, tok, boundary);
    const raw = query.slice(tok.keyStart, nextCursor).trim();

    const parsed = parseFilter(tok.key as FilterType, value, raw);
    if (parsed.error) {
      result.errors.push(parsed.error);
    } else if (parsed.filter) {
      result.filters.push(parsed.filter);
    }

    cursor = nextCursor;
  }

  const tail = query.slice(cursor).trim();
  if (tail) {
    textParts.push(tail);
  }

  result.textQuery = textParts.join(' ').trim();
  return result;
}

interface FilterParseResult {
  filter?: SearchFilter;
  error?: string;
}

/**
 * Parse a single filter like "confidence:>85" or "time:dawn"
 */
function parseFilter(type: FilterType, value: string, raw: string): FilterParseResult {
  // Extract operator if present
  const operatorMatch = value.match(/^([><=]+)(.+)$/);
  let operator: FilterOperator = ':';
  let actualValue = value;

  if (operatorMatch) {
    operator = operatorMatch[1] as FilterOperator;
    actualValue = operatorMatch[2];
  }

  // Validate and parse based on filter type
  switch (type) {
    case 'confidence':
      return parseConfidenceFilter(operator, actualValue, raw);

    case 'time':
      return parseTimeFilter(operator, actualValue, raw);

    case 'date':
      return parseDateFilter(operator, actualValue, raw);

    case 'hour':
      return parseHourFilter(operator, actualValue, raw);

    case 'daterange':
      return parseDateRangeFilter(operator, actualValue, raw);

    case 'verified':
      return parseVerifiedFilter(operator, actualValue, raw);

    case 'species':
      return parseSpeciesFilter(operator, actualValue, raw);

    case 'location':
      return parseLocationFilter(operator, actualValue, raw);

    case 'source':
      return parseSourceFilter(operator, actualValue, raw);

    case 'locked':
      return parseLockedFilter(operator, actualValue, raw);

    default:
      return { error: `Unknown filter type: ${type}` };
  }
}

function parseConfidenceFilter(
  operator: FilterOperator,
  value: string,
  raw: string
): FilterParseResult {
  // Only allow comparison operators for confidence
  if (!['>', '<', '>=', '<=', '=', ':'].includes(operator)) {
    return { error: `Invalid operator "${operator}" for confidence filter` };
  }

  const numValue = parseFloat(value);
  if (isNaN(numValue) || numValue < 0 || numValue > 100) {
    return { error: 'Confidence must be a number between 0 and 100' };
  }

  return {
    filter: {
      type: 'confidence',
      operator,
      value: numValue,
      raw,
    },
  };
}

function parseTimeFilter(operator: FilterOperator, value: string, raw: string): FilterParseResult {
  // Only allow equality for time-of-day
  if (operator !== ':' && operator !== '=') {
    return { error: `Invalid operator "${operator}" for time filter` };
  }

  const lowerValue = value.toLowerCase();
  if (!TIME_OF_DAY_VALUES.includes(lowerValue)) {
    return { error: `Invalid time value. Must be one of: ${TIME_OF_DAY_VALUES.join(', ')}` };
  }

  return {
    filter: {
      type: 'time',
      operator: ':',
      value: lowerValue,
      raw,
    },
  };
}

function parseDateFilter(operator: FilterOperator, value: string, raw: string): FilterParseResult {
  // Only allow equality for dates
  if (operator !== ':' && operator !== '=') {
    return { error: `Invalid operator "${operator}" for date filter` };
  }

  const lowerValue = value.toLowerCase();

  // Check if it's a shortcut
  if (DATE_SHORTCUTS.includes(lowerValue)) {
    return {
      filter: {
        type: 'date',
        operator: ':',
        value: lowerValue,
        raw,
      },
    };
  }

  // Validate date format (YYYY-MM-DD)
  const dateRegex = /^\d{4}-\d{2}-\d{2}$/;
  if (!dateRegex.test(value)) {
    return {
      error: 'Date must be in YYYY-MM-DD format or use shortcuts: today, yesterday, week, month',
    };
  }

  // Try to parse the date to validate it's real
  const date = new Date(value);
  if (isNaN(date.getTime())) {
    return { error: 'Invalid date value' };
  }

  return {
    filter: {
      type: 'date',
      operator: ':',
      value: value,
      raw,
    },
  };
}

function parseHourFilter(operator: FilterOperator, value: string, raw: string): FilterParseResult {
  // Only allow equality or range for hours
  if (operator !== ':' && operator !== '=') {
    return { error: `Invalid operator "${operator}" for hour filter` };
  }

  // Check for range syntax (e.g., "6-9")
  if (value.includes('-')) {
    const [start, end] = value.split('-');
    const startHour = parseInt(start);
    const endHour = parseInt(end);

    if (
      isNaN(startHour) ||
      isNaN(endHour) ||
      startHour < 0 ||
      startHour > 23 ||
      endHour < 0 ||
      endHour > 23
    ) {
      return { error: 'Hour range values must be between 0 and 23' };
    }

    return {
      filter: {
        type: 'hour',
        operator: ':',
        value: startHour,
        value2: endHour.toString(),
        raw,
      },
    };
  }

  // Single hour
  const hour = parseInt(value);
  if (isNaN(hour) || hour < 0 || hour > 23) {
    return { error: 'Hour must be between 0 and 23' };
  }

  return {
    filter: {
      type: 'hour',
      operator: ':',
      value: hour,
      raw,
    },
  };
}

function parseDateRangeFilter(
  operator: FilterOperator,
  value: string,
  raw: string
): FilterParseResult {
  // Only allow equality for date ranges
  if (operator !== ':' && operator !== '=') {
    return { error: `Invalid operator "${operator}" for daterange filter` };
  }

  // Expect format like "2024-01-01:2024-01-31"
  if (!value.includes(':')) {
    return { error: 'Date range must be in format YYYY-MM-DD:YYYY-MM-DD' };
  }

  const [startDate, endDate] = value.split(':');
  const dateRegex = /^\d{4}-\d{2}-\d{2}$/;

  if (!dateRegex.test(startDate) || !dateRegex.test(endDate)) {
    return { error: 'Date range values must be in YYYY-MM-DD format' };
  }

  // Validate dates
  const start = new Date(startDate);
  const end = new Date(endDate);
  if (isNaN(start.getTime()) || isNaN(end.getTime())) {
    return { error: 'Invalid date values in range' };
  }

  if (start > end) {
    return { error: 'Start date must be before or equal to end date' };
  }

  return {
    filter: {
      type: 'daterange',
      operator: ':',
      value: startDate,
      value2: endDate,
      raw,
    },
  };
}

function parseVerifiedFilter(
  operator: FilterOperator,
  value: string,
  raw: string
): FilterParseResult {
  // Only allow equality for verified status
  if (operator !== ':' && operator !== '=') {
    return { error: `Invalid operator "${operator}" for verified filter` };
  }

  const lowerValue = value.toLowerCase();

  // The three review verdicts, matching the detections filter panel's options.
  if (['correct', 'false_positive', 'unverified'].includes(lowerValue)) {
    return {
      filter: {
        type: 'verified',
        operator: ':',
        value: lowerValue,
        raw,
      },
    };
  }

  // Handle special case for "human" verification
  if (lowerValue === 'human') {
    return {
      filter: {
        type: 'verified',
        operator: ':',
        value: 'human',
        raw,
      },
    };
  }

  // Legacy boolean spellings, kept so existing saved searches keep parsing.
  // formatFiltersForAPI maps them onto a verdict.
  let boolValue: boolean;
  if (['true', 'yes', '1'].includes(lowerValue)) {
    boolValue = true;
  } else if (['false', 'no', '0'].includes(lowerValue)) {
    boolValue = false;
  } else {
    return {
      error:
        'Verified value must be correct, false_positive, unverified, or true, false, yes, no, 1, 0, human',
    };
  }

  return {
    filter: {
      type: 'verified',
      operator: ':',
      value: boolValue,
      raw,
    },
  };
}

function parseSpeciesFilter(
  operator: FilterOperator,
  value: string,
  raw: string
): FilterParseResult {
  // Only allow equality for species
  if (operator !== ':' && operator !== '=') {
    return { error: `Invalid operator "${operator}" for species filter` };
  }

  if (!value.trim()) {
    return { error: 'Species value cannot be empty' };
  }

  return {
    filter: {
      type: 'species',
      operator: ':',
      value: value.trim(),
      raw,
    },
  };
}

function parseLocationFilter(
  operator: FilterOperator,
  value: string,
  raw: string
): FilterParseResult {
  // Only allow equality for location
  if (operator !== ':' && operator !== '=') {
    return { error: `Invalid operator "${operator}" for location filter` };
  }

  if (!value.trim()) {
    return { error: 'Location value cannot be empty' };
  }

  return {
    filter: {
      type: 'location',
      operator: ':',
      value: value.trim(),
      raw,
    },
  };
}

function parseSourceFilter(
  operator: FilterOperator,
  value: string,
  raw: string
): FilterParseResult {
  if (operator !== ':' && operator !== '=') {
    return { error: `Invalid operator "${operator}" for source filter` };
  }

  if (!value.trim()) {
    return { error: 'Source value cannot be empty' };
  }

  return {
    filter: {
      type: 'source',
      operator: ':',
      value: value.trim(),
      raw,
    },
  };
}

function parseLockedFilter(
  operator: FilterOperator,
  value: string,
  raw: string
): FilterParseResult {
  // Only allow equality for locked status
  if (operator !== ':' && operator !== '=') {
    return { error: `Invalid operator "${operator}" for locked filter` };
  }

  const lowerValue = value.toLowerCase();

  // Convert to boolean
  let boolValue: boolean;
  if (['true', 'yes', '1'].includes(lowerValue)) {
    boolValue = true;
  } else if (['false', 'no', '0'].includes(lowerValue)) {
    boolValue = false;
  } else {
    return { error: 'Locked value must be true, false, yes, no, 1, or 0' };
  }

  return {
    filter: {
      type: 'locked',
      operator: ':',
      value: boolValue,
      raw,
    },
  };
}

/**
 * Convert parsed filters to API query parameters
 */
export function formatFiltersForAPI(filters: SearchFilter[]): Record<string, string> {
  const params: Record<string, string> = {};

  for (const filter of filters) {
    switch (filter.type) {
      // A comparison is expressed as one end of a confidence band rather than as
      // an operator string. The band is what the detections filter panel shows, so
      // "confidence:>85" typed in the search box appears there as a 85-100% range
      // instead of as an invisible filter the panel cannot represent.
      case 'confidence': {
        const value = String(filter.value);
        switch (filter.operator) {
          case '>':
          case '>=':
            params.confidenceMin = value;
            break;
          case '<':
          case '<=':
            params.confidenceMax = value;
            break;
          case '=':
          case ':':
            // Equality: a band with both ends on the same value.
            params.confidenceMin = value;
            params.confidenceMax = value;
            break;
        }
        break;
      }

      case 'time': {
        const period = filter.value.toString();
        // eslint-disable-next-line security/detect-object-injection -- fixed local map, key already validated against TIME_OF_DAY_VALUES by parseTimeFilter
        params.timeOfDay = TIME_OF_DAY_ALIASES[period] ?? period;
        break;
      }

      // Shortcuts are resolved to concrete dates here. The API accepts only
      // YYYY-MM-DD, so "date:today" used to be rejected outright; resolving it
      // also lets the filter panel display the resulting range.
      case 'date': {
        const range = resolveDateShortcut(filter.value.toString());
        if (range) {
          params.start_date = range.start;
          params.end_date = range.end;
        }
        break;
      }

      case 'hour':
        if (filter.value2) {
          params.hourRange = `${filter.value}-${filter.value2}`;
        } else {
          params.hour = filter.value.toString();
        }
        break;

      case 'daterange':
        // Backend GET /api/v2/detections reads snake_case start_date/end_date
        // (see parseDetectionQueryParams). camelCase keys were silently ignored.
        params.start_date = filter.value.toString();
        params.end_date = filter.value2 ?? '';
        break;

      // Emitted as a review verdict rather than a boolean. The boolean form meant
      // "carries any verdict", which the filter panel has no control for, so the
      // panel would have shown "Any status" while the list was filtered.
      case 'verified':
        params.verified = normalizeVerifiedValue(filter.value);
        break;

      case 'species':
        params.species = filter.value.toString();
        break;

      // location: filters by node name (the `location` query param, notes.source_node).
      case 'location':
        params.location = filter.value.toString();
        break;

      // source: filters by audio source (the `source` query param), which the server
      // resolves by numeric id, display name, node name or source URI. When both are
      // given the server intersects them.
      case 'source':
        params.source = filter.value.toString();
        break;

      case 'locked':
        params.locked = filter.value.toString();
        break;
    }
  }

  return params;
}

/**
 * Map a parsed `verified:` value onto the three review verdicts.
 *
 * `verified:true` historically selected "has been reviewed at all", which reads
 * as "verified correct" to most people and cannot be shown in the filter panel.
 * It is mapped to the verdict a user most likely meant; `verified:false` becomes
 * "unverified", which is what it already selected.
 */
function normalizeVerifiedValue(value: string | number | boolean): string {
  if (value === true || value === 'human') return 'correct';
  if (value === false) return 'unverified';
  return String(value);
}

/**
 * Resolve a date shortcut or literal date into an inclusive [start, end] range.
 *
 * "today" and "yesterday" are single days; "week" and "month" are the trailing 7
 * and 30 days ending today, matching the backend's ParseDateShortcut. Returns
 * null for anything unrecognized.
 */
function resolveDateShortcut(value: string): { start: string; end: string } | null {
  const today = new Date();
  const daysAgo = (days: number) => {
    const date = new Date(today);
    date.setDate(date.getDate() - days);
    return getLocalDateString(date);
  };

  switch (value.toLowerCase()) {
    case 'today':
      return { start: getLocalDateString(today), end: getLocalDateString(today) };
    case 'yesterday':
      return { start: daysAgo(1), end: daysAgo(1) };
    case 'week':
      return { start: daysAgo(7), end: getLocalDateString(today) };
    case 'month':
      return { start: daysAgo(30), end: getLocalDateString(today) };
    default:
      // Already a literal YYYY-MM-DD date (the parser validated the format), which
      // is a one-day range.
      return ISO_DATE_ONLY_RE.test(value) ? { start: value, end: value } : null;
  }
}

/** Literal calendar date, as `date:` accepts it. */
const ISO_DATE_ONLY_RE = /^\d{4}-\d{2}-\d{2}$/;

/**
 * Get filter suggestions based on partial input
 */
export function getFilterSuggestions(partialInput: string): string[] {
  const suggestions: string[] = [];

  // Check if user is typing a filter
  if (partialInput.includes(':')) {
    const [filterType] = partialInput.split(':', 2);

    switch (filterType.toLowerCase()) {
      case 'confidence':
        suggestions.push('confidence:>90', 'confidence:>=85', 'confidence:<50');
        break;

      case 'time':
        // Suggest only the canonical names; the aliases still parse.
        TIME_OF_DAY_CANONICAL.forEach(time => {
          suggestions.push(`time:${time}`);
        });
        break;

      case 'date':
        DATE_SHORTCUTS.forEach(shortcut => {
          suggestions.push(`date:${shortcut}`);
        });
        suggestions.push('date:2024-01-20');
        break;

      case 'verified':
        suggestions.push('verified:correct', 'verified:false_positive', 'verified:unverified');
        break;

      case 'locked':
        suggestions.push('locked:true', 'locked:false');
        break;
    }
  } else {
    // Suggest filter types. Derived from FILTER_KEYS so every recognized key
    // (including daterange:) is offered and the list can never drift from the
    // parser's accepted keys.
    const lowerInput = partialInput.toLowerCase();
    for (const key of FILTER_KEYS) {
      const suggestion = `${key}:`;
      if (suggestion.startsWith(lowerInput)) {
        suggestions.push(suggestion);
      }
    }
  }

  return suggestions;
}

/**
 * Format a filter for display as a chip
 */
export function formatFilterForDisplay(filter: SearchFilter): string {
  switch (filter.type) {
    case 'confidence':
      return `Confidence ${filter.operator}${filter.value}%`;

    case 'time':
      return `Time: ${filter.value}`;

    case 'date':
      return `Date: ${filter.value}`;

    case 'hour':
      if (filter.value2) {
        return `Hour: ${filter.value}-${filter.value2}`;
      }
      return `Hour: ${filter.value}`;

    case 'daterange':
      return `Date: ${filter.value} to ${filter.value2}`;

    case 'verified':
      return `Verified: ${filter.value}`;

    case 'species':
      return `Species: ${filter.value}`;

    case 'location':
      return `Location: ${filter.value}`;

    case 'source':
      return `Source: ${filter.value}`;

    case 'locked':
      return `Locked: ${filter.value}`;

    default:
      return filter.raw;
  }
}
