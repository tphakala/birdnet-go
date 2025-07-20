import { describe, it, expect } from 'vitest';
import { parseSearchQuery, formatFiltersForAPI, getFilterSuggestions } from './searchParser';

describe('parseSearchQuery', () => {
  it('should parse simple text query without filters', () => {
    const result = parseSearchQuery('Robin');

    expect(result.textQuery).toBe('Robin');
    expect(result.filters).toHaveLength(0);
    expect(result.errors).toHaveLength(0);
  });

  it('should parse confidence filter', () => {
    const result = parseSearchQuery('Robin confidence:>85');

    expect(result.textQuery).toBe('Robin');
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      type: 'confidence',
      operator: '>',
      value: 85,
      raw: 'confidence:>85',
    });
    expect(result.errors).toHaveLength(0);
  });

  it('should parse time filter', () => {
    const result = parseSearchQuery('Birds time:dawn');

    expect(result.textQuery).toBe('Birds');
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      type: 'time',
      operator: ':',
      value: 'dawn',
      raw: 'time:dawn',
    });
    expect(result.errors).toHaveLength(0);
  });

  it('should parse multiple filters', () => {
    const result = parseSearchQuery('Robin confidence:>90 time:dawn verified:true');

    expect(result.textQuery).toBe('Robin');
    expect(result.filters).toHaveLength(3);
    expect(result.errors).toHaveLength(0);
  });

  it('should handle invalid confidence values', () => {
    const result = parseSearchQuery('Robin confidence:>150');

    expect(result.textQuery).toBe('Robin');
    expect(result.filters).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0]).toContain('Confidence must be a number between 0 and 100');
  });

  it('should handle invalid time values', () => {
    const result = parseSearchQuery('Birds time:midnight');

    expect(result.textQuery).toBe('Birds');
    expect(result.filters).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0]).toContain('Invalid time value');
  });

  it('should parse date shortcuts', () => {
    const result = parseSearchQuery('Robin date:today');

    expect(result.textQuery).toBe('Robin');
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      type: 'date',
      operator: ':',
      value: 'today',
      raw: 'date:today',
    });
  });

  it('should parse hour range', () => {
    const result = parseSearchQuery('Birds hour:6-9');

    expect(result.textQuery).toBe('Birds');
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      type: 'hour',
      operator: ':',
      value: 6,
      value2: '9',
      raw: 'hour:6-9',
    });
  });

  it('should parse verified status', () => {
    const result = parseSearchQuery('Robin verified:human');

    expect(result.textQuery).toBe('Robin');
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      type: 'verified',
      operator: ':',
      value: 'human',
      raw: 'verified:human',
    });
  });

  it('should handle complex queries with text and multiple filters', () => {
    const result = parseSearchQuery('American Robin confidence:>=85 time:dawn date:today');

    expect(result.textQuery).toBe('American Robin');
    expect(result.filters).toHaveLength(3);
    expect(result.errors).toHaveLength(0);
  });
});

describe('formatFiltersForAPI', () => {
  it('should format confidence filter', () => {
    const filters = [
      {
        type: 'confidence' as const,
        operator: '>' as const,
        value: 85,
        raw: 'confidence:>85',
      },
    ];

    const result = formatFiltersForAPI(filters);
    expect(result).toEqual({ confidence: '>85' });
  });

  it('should format time filter', () => {
    const filters = [
      {
        type: 'time' as const,
        operator: ':' as const,
        value: 'dawn',
        raw: 'time:dawn',
      },
    ];

    const result = formatFiltersForAPI(filters);
    expect(result).toEqual({ timeOfDay: 'dawn' });
  });

  it('should format multiple filters', () => {
    const filters = [
      {
        type: 'confidence' as const,
        operator: '>=' as const,
        value: 85,
        raw: 'confidence:>=85',
      },
      {
        type: 'verified' as const,
        operator: ':' as const,
        value: true,
        raw: 'verified:true',
      },
    ];

    const result = formatFiltersForAPI(filters);
    expect(result).toEqual({
      confidence: '>=85',
      verified: 'true',
    });
  });
});

describe('getFilterSuggestions', () => {
  it('should suggest filter types when typing partial names', () => {
    const result = getFilterSuggestions('conf');
    expect(result).toContain('confidence:');
  });

  it('should suggest confidence operators', () => {
    const result = getFilterSuggestions('confidence:');
    expect(result).toEqual(
      expect.arrayContaining(['confidence:>90', 'confidence:>=85', 'confidence:<50'])
    );
  });

  it('should suggest time values', () => {
    const result = getFilterSuggestions('time:');
    expect(result).toEqual(
      expect.arrayContaining(['time:dawn', 'time:day', 'time:dusk', 'time:night'])
    );
  });

  it('should suggest date shortcuts', () => {
    const result = getFilterSuggestions('date:');
    expect(result).toEqual(
      expect.arrayContaining(['date:today', 'date:yesterday', 'date:week', 'date:month'])
    );
  });

  it('should suggest verified values', () => {
    const result = getFilterSuggestions('verified:');
    expect(result).toEqual(
      expect.arrayContaining(['verified:true', 'verified:false', 'verified:human'])
    );
  });
});
