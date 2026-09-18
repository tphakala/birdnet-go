import { describe, it, expect } from 'vitest';
import {
  applyDetectionFiltersToParams,
  countActiveDetectionFilters,
  detectionFiltersToResolveBody,
  emptyDetectionFilters,
  hasActiveDetectionFilters,
  hourBandToParam,
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
      hourRange: '6-9',
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
      hourStart: '6',
      hourEnd: '9',
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

  describe('the hour band', () => {
    it('reads a single hour as a band of one', () => {
      const result = parseDetectionFilters(new URLSearchParams({ hourRange: '7' }));
      expect(result.hourStart).toBe('7');
      expect(result.hourEnd).toBe('7');
    });

    it('orders a backwards range rather than passing one the API rejects', () => {
      const result = parseDetectionFilters(new URLSearchParams({ hourRange: '21-4' }));
      expect(result.hourStart).toBe('4');
      expect(result.hourEnd).toBe('21');
    });

    it('drops the whole band when either end is unusable', () => {
      // Half a range would filter by something the user never asked for.
      for (const hourRange of ['6-99', 'dawn-9', '-', '6-']) {
        const result = parseDetectionFilters(new URLSearchParams({ hourRange }));
        expect({ hourRange, ...result }).toMatchObject({ hourStart: '', hourEnd: '' });
      }
    });

    // An hourly drill-down links in with hour + duration. Folding it in is what
    // makes the hour it applies visible in the panel instead of narrowing the
    // list from a parameter no control shows.
    it('folds an hourly drill-down into the band', () => {
      const result = parseDetectionFilters(new URLSearchParams({ hour: '7', duration: '3' }));
      expect(result.hourStart).toBe('7');
      expect(result.hourEnd).toBe('9');
    });

    it('treats a drill-down without a duration as a single hour', () => {
      const result = parseDetectionFilters(new URLSearchParams({ hour: '7' }));
      expect(result.hourStart).toBe('7');
      expect(result.hourEnd).toBe('7');
    });

    it('clamps a duration that would run past midnight', () => {
      // The API rejects an inverted range, so wrapping here would produce a band
      // it silently drops.
      const result = parseDetectionFilters(new URLSearchParams({ hour: '22', duration: '6' }));
      expect(result.hourStart).toBe('22');
      expect(result.hourEnd).toBe('23');
    });

    it('prefers its own parameter over a drill-down pair', () => {
      const params = new URLSearchParams({ hourRange: '6-9', hour: '18', duration: '2' });
      const result = parseDetectionFilters(params);
      expect(result.hourStart).toBe('6');
      expect(result.hourEnd).toBe('9');
    });

    it('counts as an active filter, so the view is not pinned to today', () => {
      expect(hasActiveDetectionFilters(filters({ hourStart: '7', hourEnd: '7' }))).toBe(true);
    });

    it('counts as one filter even with both ends set', () => {
      expect(countActiveDetectionFilters(filters({ hourStart: '6', hourEnd: '9' }))).toBe(1);
    });
  });

  describe('the date parameter', () => {
    // A drill-down pins the list to one day. Reading it into the range fields
    // shows the user what is narrowing the list, and lets them widen it.
    it('fills both ends of the date range', () => {
      const result = parseDetectionFilters(new URLSearchParams({ date: '2026-09-10' }));
      expect(result.startDate).toBe('2026-09-10');
      expect(result.endDate).toBe('2026-09-10');
    });

    it('yields to an explicit range', () => {
      const params = new URLSearchParams({ date: '2026-09-10', start_date: '2026-01-01' });
      const result = parseDetectionFilters(params);
      expect(result.startDate).toBe('2026-01-01');
      // The unset end still falls back to the pinned day rather than being lost.
      expect(result.endDate).toBe('2026-09-10');
    });

    it('is ignored when malformed', () => {
      expect(parseDetectionFilters(new URLSearchParams({ date: 'today' }))).toEqual(
        emptyDetectionFilters()
      );
    });
  });

  describe('the species parameter', () => {
    // Dashboard drill-downs and the species analytics page link in with an exact
    // `species` match rather than the panel's free-text `search`. Both have to
    // land in the panel's one species field, or an incoming link would show an
    // empty form beside an already-narrowed list.
    it('fills the species field when no free-text query is present', () => {
      expect(parseDetectionFilters(new URLSearchParams({ species: 'Turdus merula' })).search).toBe(
        'Turdus merula'
      );
    });

    it('counts as an active filter, so the view is not pinned to today', () => {
      expect(
        hasActiveDetectionFilters(parseDetectionFilters(new URLSearchParams({ species: 'Robin' })))
      ).toBe(true);
    });

    it('yields to an explicit free-text query', () => {
      const params = new URLSearchParams({ search: 'Owl', species: 'Robin' });
      expect(parseDetectionFilters(params).search).toBe('Owl');
    });

    it('is trimmed, and blank means unfiltered', () => {
      expect(parseDetectionFilters(new URLSearchParams({ species: '  Robin  ' })).search).toBe(
        'Robin'
      );
      expect(parseDetectionFilters(new URLSearchParams({ species: '   ' }))).toEqual(
        emptyDetectionFilters()
      );
    });
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

  it('drops the exact-match species parameter it has folded into search', () => {
    // Otherwise a drill-down's `species` would keep intersecting with whatever
    // the user has since typed into the panel.
    const params = new URLSearchParams({ species: 'Robin', queryType: 'search' });
    applyDetectionFiltersToParams(params, filters({ search: 'Owl' }));

    expect(params.get('search')).toBe('Owl');
    expect(params.has('species')).toBe(false);
  });

  it('drops the species parameter even when the field was cleared', () => {
    const params = new URLSearchParams({ species: 'Robin' });
    applyDetectionFiltersToParams(params, emptyDetectionFilters());

    expect(params.has('species')).toBe(false);
    expect(params.has('search')).toBe(false);
  });

  describe('the hour band', () => {
    it('writes a single hour as a bare number', () => {
      const params = new URLSearchParams();
      applyDetectionFiltersToParams(params, filters({ hourStart: '7', hourEnd: '7' }));
      expect(params.get('hourRange')).toBe('7');
    });

    it('writes a range as start-end', () => {
      const params = new URLSearchParams();
      applyDetectionFiltersToParams(params, filters({ hourStart: '6', hourEnd: '9' }));
      expect(params.get('hourRange')).toBe('6-9');
    });

    it('completes an open end with the opposite extreme', () => {
      // The API has no spelling for a half-open band, so "from 18:00" is sent as
      // 18 through 23 rather than dropped.
      expect(hourBandToParam(filters({ hourStart: '18' }))).toBe('18-23');
      expect(hourBandToParam(filters({ hourEnd: '5' }))).toBe('0-5');
    });

    it('omits the parameter when the band is unbounded', () => {
      const params = new URLSearchParams({ hourRange: '6-9' });
      applyDetectionFiltersToParams(params, emptyDetectionFilters());
      expect(params.has('hourRange')).toBe(false);
    });
  });

  it('drops the drill-down parameters it has folded into the fields', () => {
    // Left in place, `date` and `hour` would keep narrowing the list from
    // parameters no control in the panel shows or can clear.
    const params = new URLSearchParams({ date: '2026-09-10', hour: '7', duration: '3' });
    applyDetectionFiltersToParams(params, filters({ startDate: '2026-01-01' }));

    expect(params.has('date')).toBe(false);
    expect(params.has('hour')).toBe(false);
    expect(params.has('duration')).toBe(false);
    expect(params.get('start_date')).toBe('2026-01-01');
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
