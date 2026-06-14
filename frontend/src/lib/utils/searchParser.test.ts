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

  it('should parse source filter', () => {
    const result = parseSearchQuery('Robin source:rtsp_87b89761');

    expect(result.textQuery).toBe('Robin');
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      type: 'source',
      operator: ':',
      value: 'rtsp_87b89761',
      raw: 'source:rtsp_87b89761',
    });
    expect(result.errors).toHaveLength(0);
  });

  it('should trim whitespace from source filter value', () => {
    const result = parseSearchQuery('source:rtsp_front  ');

    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toMatchObject({
      type: 'source',
      value: 'rtsp_front',
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

  // location and source are aliases for the same backend dimension (source_node),
  // exposed by GET /api/v2/detections as the single `location` query param.
  it('should map source filter to the location API param (shared source_node dimension)', () => {
    const filters = [
      {
        type: 'source' as const,
        operator: ':' as const,
        value: 'rtsp_87b89761',
        raw: 'source:rtsp_87b89761',
      },
    ];

    const result = formatFiltersForAPI(filters);
    expect(result).toEqual({ location: 'rtsp_87b89761' });
  });

  it('should map location filter to the location API param', () => {
    const filters = [
      {
        type: 'location' as const,
        operator: ':' as const,
        value: 'Back Yard',
        raw: 'location:Back Yard',
      },
    ];

    const result = formatFiltersForAPI(filters);
    expect(result).toEqual({ location: 'Back Yard' });
  });

  it('should map daterange filter to snake_case start_date/end_date API params', () => {
    const filters = [
      {
        type: 'daterange' as const,
        operator: ':' as const,
        value: '2024-01-01',
        value2: '2024-01-31',
        raw: 'daterange:2024-01-01:2024-01-31',
      },
    ];

    const result = formatFiltersForAPI(filters);
    expect(result).toEqual({ start_date: '2024-01-01', end_date: '2024-01-31' });
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

  it('should include source: in filter-type suggestions', () => {
    const result = getFilterSuggestions('sou');
    expect(result).toContain('source:');
  });

  it('should include daterange: in filter-type suggestions', () => {
    const result = getFilterSuggestions('dater');
    expect(result).toContain('daterange:');
  });

  it('should suggest both date: and daterange: for the "date" prefix', () => {
    const result = getFilterSuggestions('date');
    expect(result).toEqual(expect.arrayContaining(['date:', 'daterange:']));
  });
});

describe('parseSearchQuery multi-word species filter', () => {
  it('captures an unquoted multi-word species value', () => {
    const result = parseSearchQuery('species:Great Tit');
    expect(result.errors).toHaveLength(0);
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0].type).toBe('species');
    expect(result.filters[0].value).toBe('Great Tit');
  });

  it('captures a quoted multi-word species value and strips the quotes', () => {
    const result = parseSearchQuery('species:"Great Tit"');
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0].value).toBe('Great Tit');
  });

  it('stops a multi-word value at the next recognized filter key', () => {
    const result = parseSearchQuery('species:Great Tit confidence:>90');
    expect(result.errors).toHaveLength(0);
    const species = result.filters.find(f => f.type === 'species');
    const confidence = result.filters.find(f => f.type === 'confidence');
    expect(species?.value).toBe('Great Tit');
    expect(confidence?.value).toBe(90);
    expect(confidence?.operator).toBe('>');
  });

  it('preserves free text before a multi-word filter', () => {
    const result = parseSearchQuery('flying species:Great Tit');
    expect(result.textQuery).toBe('flying');
    expect(result.filters.find(f => f.type === 'species')?.value).toBe('Great Tit');
  });

  it('captures trailing free text after a quoted value', () => {
    const result = parseSearchQuery('species:"Great Tit" flying');
    expect(result.filters.find(f => f.type === 'species')?.value).toBe('Great Tit');
    expect(result.textQuery).toBe('flying');
  });

  it('greedily swallows trailing free text after an unquoted multi-word value (documented limitation)', () => {
    const result = parseSearchQuery('species:Great Tit flying');
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0].value).toBe('Great Tit flying');
    expect(result.textQuery).toBe('');
  });

  it('handles an unmatched trailing quote without throwing', () => {
    const result = parseSearchQuery('species:"Great Tit');
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0].value).toBe('Great Tit');
  });

  it('captures a multi-word location value', () => {
    const result = parseSearchQuery('location:Back Yard');
    expect(result.filters.find(f => f.type === 'location')?.value).toBe('Back Yard');
  });

  it('pushes an error for a single-token filter with no value', () => {
    const result = parseSearchQuery('confidence:');
    expect(result.filters).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.textQuery).toBe('');
  });
});
