import { describe, it, expect } from 'vitest';
import {
  applyDetectionFiltersToParams,
  countActiveDetectionFilters,
  detectionFiltersToResolveBody,
  emptyDetectionFilters,
  hasActiveDetectionFilters,
  parseDetectionFilters,
} from '../detectionFilters';
import type { DetectionFilters } from '$lib/types/detection.types';

/** Build a filter set from a partial override of the "nothing filtered" state. */
function filters(overrides: Partial<DetectionFilters> = {}): DetectionFilters {
  return { ...emptyDetectionFilters(), ...overrides };
}

/** Round-trip helper: serialize filters to a query string and read them back. */
function roundTrip(input: DetectionFilters): DetectionFilters {
  const params = new URLSearchParams();
  applyDetectionFiltersToParams(params, input);
  return parseDetectionFilters(params);
}

describe('parseDetectionFilters', () => {
  it('returns the unfiltered state for an empty query string', () => {
    expect(parseDetectionFilters(new URLSearchParams())).toEqual(emptyDetectionFilters());
  });

  it('reads every supported parameter', () => {
    const params = new URLSearchParams({
      search: 'Barn Owl',
      start_date: '2026-01-01',
      end_date: '2026-03-31',
      confidenceMin: '60',
      confidenceMax: '90',
      verified: 'false_positive',
      locked: 'true',
      timeOfDay: 'sunset',
      source: 'Garden mic',
    });

    expect(parseDetectionFilters(params)).toEqual({
      search: 'Barn Owl',
      startDate: '2026-01-01',
      endDate: '2026-03-31',
      confidenceMin: 60,
      confidenceMax: 90,
      verified: 'false_positive',
      locked: 'true',
      timeOfDay: 'sunset',
      source: 'Garden mic',
    });
  });

  it('trims surrounding whitespace from free-text values', () => {
    const params = new URLSearchParams({ search: '  Robin  ', source: ' Garden ' });
    const result = parseDetectionFilters(params);
    expect(result.search).toBe('Robin');
    expect(result.source).toBe('Garden');
  });

  describe('rejecting values the API would refuse', () => {
    // A stale bookmark must degrade to a wider result set, not to an error page.
    it('drops an unrecognized verdict', () => {
      expect(parseDetectionFilters(new URLSearchParams({ verified: 'maybe' })).verified).toBe('');
    });

    it('drops an unrecognized time-of-day period', () => {
      expect(parseDetectionFilters(new URLSearchParams({ timeOfDay: 'teatime' })).timeOfDay).toBe(
        ''
      );
    });

    it('drops a non-boolean lock value', () => {
      expect(parseDetectionFilters(new URLSearchParams({ locked: 'sometimes' })).locked).toBe('');
    });

    it('drops a malformed date', () => {
      const result = parseDetectionFilters(
        new URLSearchParams({ start_date: 'yesterday', end_date: '2026-1-1' })
      );
      expect(result.startDate).toBe('');
      expect(result.endDate).toBe('');
    });

    it('falls back to the default bound for a non-numeric confidence', () => {
      const result = parseDetectionFilters(
        new URLSearchParams({ confidenceMin: 'high', confidenceMax: 'low' })
      );
      expect(result.confidenceMin).toBe(0);
      expect(result.confidenceMax).toBe(100);
    });
  });

  it('accepts values case-insensitively', () => {
    const params = new URLSearchParams({ verified: 'CORRECT', timeOfDay: 'SunRise' });
    const result = parseDetectionFilters(params);
    expect(result.verified).toBe('correct');
    expect(result.timeOfDay).toBe('sunrise');
  });

  it('clamps confidence bounds into the percentage range', () => {
    const result = parseDetectionFilters(
      new URLSearchParams({ confidenceMin: '-40', confidenceMax: '400' })
    );
    expect(result.confidenceMin).toBe(0);
    expect(result.confidenceMax).toBe(100);
  });

  it('normalizes an inverted confidence range instead of passing it through', () => {
    // The API rejects min > max, so a hand-edited URL would fail the whole
    // request rather than just the one filter.
    const result = parseDetectionFilters(
      new URLSearchParams({ confidenceMin: '90', confidenceMax: '20' })
    );
    expect(result.confidenceMin).toBe(20);
    expect(result.confidenceMax).toBe(90);
  });

  it('ignores unrelated parameters', () => {
    const params = new URLSearchParams({ numResults: '50', sortBy: 'species_asc', offset: '75' });
    expect(parseDetectionFilters(params)).toEqual(emptyDetectionFilters());
  });
});

describe('applyDetectionFiltersToParams', () => {
  it('writes nothing for the unfiltered state', () => {
    const params = new URLSearchParams();
    applyDetectionFiltersToParams(params, emptyDetectionFilters());
    expect(params.toString()).toBe('');
  });

  it('omits a full-width confidence band', () => {
    const params = new URLSearchParams();
    applyDetectionFiltersToParams(params, filters({ confidenceMin: 0, confidenceMax: 100 }));
    expect(params.has('confidenceMin')).toBe(false);
    expect(params.has('confidenceMax')).toBe(false);
  });

  it('writes only the bound that narrows', () => {
    const params = new URLSearchParams();
    applyDetectionFiltersToParams(params, filters({ confidenceMin: 70, confidenceMax: 100 }));
    expect(params.get('confidenceMin')).toBe('70');
    expect(params.has('confidenceMax')).toBe(false);
  });

  it('clears parameters that are no longer active', () => {
    // Applying a narrower filter set must remove the previous one's parameters,
    // or the URL would accumulate filters the panel is no longer showing.
    const params = new URLSearchParams({
      search: 'Owl',
      verified: 'correct',
      timeOfDay: 'night',
      confidenceMin: '80',
    });
    applyDetectionFiltersToParams(params, filters({ search: 'Robin' }));

    expect(params.get('search')).toBe('Robin');
    expect(params.has('verified')).toBe(false);
    expect(params.has('timeOfDay')).toBe(false);
    expect(params.has('confidenceMin')).toBe(false);
  });

  it('preserves parameters it does not own', () => {
    const params = new URLSearchParams({ numResults: '50', sortBy: 'confidence_desc' });
    applyDetectionFiltersToParams(params, filters({ search: 'Robin' }));
    expect(params.get('numResults')).toBe('50');
    expect(params.get('sortBy')).toBe('confidence_desc');
  });

  it('trims free-text values before writing them', () => {
    const params = new URLSearchParams();
    applyDetectionFiltersToParams(params, filters({ search: '   ' }));
    expect(params.has('search')).toBe(false);
  });
});

describe('round-tripping through the URL', () => {
  const cases: Array<[string, DetectionFilters]> = [
    ['unfiltered', emptyDetectionFilters()],
    ['free text only', filters({ search: 'Great Tit' })],
    ['date range', filters({ startDate: '2026-04-01', endDate: '2026-04-30' })],
    ['confidence band', filters({ confidenceMin: 55, confidenceMax: 80 })],
    ['verdict', filters({ verified: 'unverified' })],
    ['lock state', filters({ locked: 'false' })],
    ['time of day', filters({ timeOfDay: 'sunrise' })],
    ['audio source', filters({ source: 'Balcony' })],
    [
      'everything at once',
      filters({
        search: 'Owl',
        startDate: '2026-01-01',
        endDate: '2026-12-31',
        confidenceMin: 25,
        confidenceMax: 75,
        verified: 'correct',
        locked: 'true',
        timeOfDay: 'night',
        source: 'Garden',
      }),
    ],
  ];

  it.each(cases)('survives a round trip: %s', (_name, input) => {
    expect(roundTrip(input)).toEqual(input);
  });
});

describe('hasActiveDetectionFilters', () => {
  it('is false for the unfiltered state', () => {
    expect(hasActiveDetectionFilters(emptyDetectionFilters())).toBe(false);
  });

  it('is false for a whitespace-only query', () => {
    expect(hasActiveDetectionFilters(filters({ search: '   ' }))).toBe(false);
  });

  it.each([
    ['free text', filters({ search: 'Owl' })],
    ['start date', filters({ startDate: '2026-01-01' })],
    ['end date', filters({ endDate: '2026-01-01' })],
    ['confidence minimum', filters({ confidenceMin: 1 })],
    ['confidence maximum', filters({ confidenceMax: 99 })],
    ['verdict', filters({ verified: 'correct' })],
    ['lock state', filters({ locked: 'true' })],
    ['time of day', filters({ timeOfDay: 'day' })],
    ['source', filters({ source: 'Garden' })],
  ])('is true when %s narrows the results', (_name, input) => {
    expect(hasActiveDetectionFilters(input)).toBe(true);
  });
});

describe('countActiveDetectionFilters', () => {
  it('counts nothing when unfiltered', () => {
    expect(countActiveDetectionFilters(emptyDetectionFilters())).toBe(0);
  });

  it('counts a date range as one filter even with both ends set', () => {
    expect(
      countActiveDetectionFilters(filters({ startDate: '2026-01-01', endDate: '2026-02-01' }))
    ).toBe(1);
  });

  it('counts a confidence band as one filter even with both bounds set', () => {
    expect(countActiveDetectionFilters(filters({ confidenceMin: 10, confidenceMax: 90 }))).toBe(1);
  });

  it('counts each independent filter', () => {
    expect(
      countActiveDetectionFilters(
        filters({ search: 'Owl', verified: 'correct', timeOfDay: 'night', source: 'Garden' })
      )
    ).toBe(4);
  });
});

describe('detectionFiltersToResolveBody', () => {
  it('omits every inactive filter', () => {
    const body = detectionFiltersToResolveBody(emptyDetectionFilters());
    expect(Object.values(body).every(value => value === undefined)).toBe(true);
  });

  it('sends the active filters so a bulk action targets the visible set', () => {
    const body = detectionFiltersToResolveBody(
      filters({
        startDate: '2026-05-01',
        endDate: '2026-05-31',
        confidenceMin: 40,
        confidenceMax: 70,
        verified: 'false_positive',
        locked: 'false',
        timeOfDay: 'night',
        source: 'Garden',
      })
    );

    expect(body).toEqual({
      startDate: '2026-05-01',
      endDate: '2026-05-31',
      confidenceMin: '40',
      confidenceMax: '70',
      verified: 'false_positive',
      locked: 'false',
      timeOfDay: 'night',
      source: 'Garden',
    });
  });

  it('omits confidence bounds that do not narrow', () => {
    const body = detectionFiltersToResolveBody(filters({ confidenceMin: 0, confidenceMax: 100 }));
    expect(body.confidenceMin).toBeUndefined();
    expect(body.confidenceMax).toBeUndefined();
  });

  it('sends "false" for the unlocked filter rather than dropping it', () => {
    // 'false' is a real selection ("unlocked only"), distinct from "no filter".
    const body = detectionFiltersToResolveBody(filters({ locked: 'false' }));
    expect(body.locked).toBe('false');
  });
});

describe('emptyDetectionFilters', () => {
  it('returns a fresh object each call so callers cannot share state', () => {
    const a = emptyDetectionFilters();
    const b = emptyDetectionFilters();
    expect(a).not.toBe(b);
    a.search = 'mutated';
    expect(b.search).toBe('');
  });
});
