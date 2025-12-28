/**
 * Advanced search query parser for BirdNET-Go
 *
 * Supports syntax like:
 * - "Robin confidence:>85"
 * - "confidence:>90 time:dawn"
 * - "Blue Jay date:today verified:true"
 */

export type FilterOperator = '>' | '<' | '>=' | '<=' | '=' | ':';

export type FilterType =
  | 'confidence'
  | 'time'
  | 'date'
  | 'hour'
  | 'daterange'
  | 'verified'
  | 'species'
  | 'location'
  | 'locked'
  | 'source';

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

// Valid time-of-day values
const TIME_OF_DAY_VALUES = ['dawn', 'day', 'dusk', 'night'];

// Date shortcuts
const DATE_SHORTCUTS = ['today', 'yesterday', 'week', 'month'];

/**
 * Parse a search query string into text and filters
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

  // Regular expression to match filter patterns like "filter:value" or "filter:>value"
  // Made safer by limiting operator repetition to prevent ReDoS
  const filterRegex = /(\w+):([><=]{0,3}[^\s]+)/g;
  const textParts: string[] = [];
  let lastIndex = 0;

  let match;
  while ((match = filterRegex.exec(query)) !== null) {
    // Add text before this filter
    const beforeFilter = query.slice(lastIndex, match.index).trim();
    if (beforeFilter) {
      textParts.push(beforeFilter);
    }

    const [fullMatch, filterType, filterValue] = match;

    // Parse the filter
    const parsedFilter = parseFilter(filterType as FilterType, filterValue, fullMatch);
    if (parsedFilter.error) {
      result.errors.push(parsedFilter.error);
    } else if (parsedFilter.filter) {
      result.filters.push(parsedFilter.filter);
    }

    lastIndex = match.index + fullMatch.length;
  }

  // Add any remaining text after the last filter
  const remainingText = query.slice(lastIndex).trim();
  if (remainingText) {
    textParts.push(remainingText);
  }

  // Join all text parts
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

    case 'locked':
      return parseLockedFilter(operator, actualValue, raw);

    case 'source':
      return parseSourceFilter(operator, actualValue, raw);

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

  // Convert to boolean
  let boolValue: boolean;
  if (['true', 'yes', '1'].includes(lowerValue)) {
    boolValue = true;
  } else if (['false', 'no', '0'].includes(lowerValue)) {
    boolValue = false;
  } else {
    return { error: 'Verified value must be true, false, yes, no, 1, 0, or human' };
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

function parseSourceFilter(
  operator: FilterOperator,
  value: string,
  raw: string
): FilterParseResult {
  // Only allow equality for source
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

/**
 * Convert parsed filters to API query parameters
 */
export function formatFiltersForAPI(filters: SearchFilter[]): Record<string, string> {
  const params: Record<string, string> = {};

  for (const filter of filters) {
    switch (filter.type) {
      case 'confidence':
        params.confidence = `${filter.operator}${filter.value}`;
        break;

      case 'time':
        params.timeOfDay = filter.value.toString();
        break;

      case 'date':
        params.date = filter.value.toString();
        break;

      case 'hour':
        if (filter.value2) {
          params.hourRange = `${filter.value}-${filter.value2}`;
        } else {
          params.hour = filter.value.toString();
        }
        break;

      case 'daterange':
        params.startDate = filter.value.toString();
        params.endDate = filter.value2 ?? '';
        break;

      case 'verified':
        params.verified = filter.value.toString();
        break;

      case 'species':
        params.species = filter.value.toString();
        break;

      case 'location':
        params.location = filter.value.toString();
        break;

      case 'locked':
        params.locked = filter.value.toString();
        break;

      case 'source':
        params.source = filter.value.toString();
        break;
    }
  }

  return params;
}

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
        TIME_OF_DAY_VALUES.forEach(time => {
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
        suggestions.push('verified:true', 'verified:false', 'verified:human');
        break;

      case 'locked':
        suggestions.push('locked:true', 'locked:false');
        break;

      case 'source':
        suggestions.push('source:');
        break;
    }
  } else {
    // Suggest filter types
    const filterTypes = [
      'confidence:',
      'time:',
      'date:',
      'hour:',
      'verified:',
      'species:',
      'location:',
      'locked:',
      'source:',
    ];
    filterTypes.forEach(type => {
      if (type.startsWith(partialInput.toLowerCase())) {
        suggestions.push(type);
      }
    });
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

    case 'locked':
      return `Locked: ${filter.value}`;

    case 'source':
      return `Source: ${filter.value}`;

    default:
      return filter.raw;
  }
}
