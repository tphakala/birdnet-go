import { describe, it, expect } from 'vitest';

import {
  DEFAULT_RANGE,
  formatDateForAPI,
  parseAnalyticsParams,
  resolveDateRange,
  serializeAnalyticsParams,
} from './analyticsParams';
import type { AnalyticsParams } from './types';

// Fixed clock for deterministic rolling-window resolution.
const NOW = new Date(2026, 5, 19, 12, 0, 0); // 2026-06-19 local

describe('formatDateForAPI', () => {
  it('formats a date as local YYYY-MM-DD with zero padding', () => {
    expect(formatDateForAPI(new Date(2026, 0, 5))).toBe('2026-01-05');
    expect(formatDateForAPI(new Date(2026, 11, 31))).toBe('2026-12-31');
  });
});

describe('resolveDateRange', () => {
  it('resolves preset windows ending at now', () => {
    const [weekStart, weekEnd] = resolveDateRange('week', '', '', NOW);
    expect(formatDateForAPI(weekEnd)).toBe('2026-06-19');
    expect(formatDateForAPI(weekStart)).toBe('2026-06-12');

    const [monthStart] = resolveDateRange('month', '', '', NOW);
    expect(formatDateForAPI(monthStart)).toBe('2026-05-20'); // 30 days back

    const [yearStart] = resolveDateRange('year', '', '', NOW);
    expect(formatDateForAPI(yearStart)).toBe('2025-06-19');
  });

  it('uses provided custom dates', () => {
    const [start, end] = resolveDateRange('custom', '2026-01-01', '2026-02-15', NOW);
    expect(formatDateForAPI(start)).toBe('2026-01-01');
    expect(formatDateForAPI(end)).toBe('2026-02-15');
  });

  it('falls back to a one-month window for custom with missing dates', () => {
    const [start, end] = resolveDateRange('custom', '', '', NOW);
    expect(formatDateForAPI(start)).toBe('2026-05-20');
    expect(formatDateForAPI(end)).toBe('2026-06-19');
  });
});

describe('parseAnalyticsParams (tab removed)', () => {
  it('ignores a legacy tab param without surfacing it', () => {
    const p = parseAnalyticsParams('?tab=patterns&range=week');
    expect('tab' in p).toBe(false);
    expect(p.range).toBe('week');
  });

  it('parses species as a deduped list and source as a string', () => {
    const p = parseAnalyticsParams('?species=A,A,B&source=mic-1');
    expect(p.species).toEqual(['A', 'B']);
    expect(p.source).toBe('mic-1');
  });

  it('returns defaults for an empty query', () => {
    const p = parseAnalyticsParams('', { now: NOW });
    expect(p.range).toBe(DEFAULT_RANGE);
    expect(p.species).toEqual([]);
    expect(p.source).toBe('');
  });

  it('parses range and source from query', () => {
    const p = parseAnalyticsParams(
      '?range=week&species=Turdus%20merula,Parus%20major&source=mic1',
      {
        now: NOW,
      }
    );
    expect(p.range).toBe('week');
    expect(p.species).toEqual(['Turdus merula', 'Parus major']);
    expect(p.source).toBe('mic1');
  });

  it('falls back on invalid range values', () => {
    const p = parseAnalyticsParams('?range=decade', { now: NOW });
    expect(p.range).toBe(DEFAULT_RANGE);
  });

  it('parses custom range with start/end into Date objects', () => {
    const p = parseAnalyticsParams('?range=custom&start=2026-03-01&end=2026-03-31', { now: NOW });
    expect(p.range).toBe('custom');
    expect(p.start).toBe('2026-03-01');
    expect(p.end).toBe('2026-03-31');
    expect(formatDateForAPI(p.startDate)).toBe('2026-03-01');
    expect(formatDateForAPI(p.endDate)).toBe('2026-03-31');
  });

  it('trims and drops empty species entries', () => {
    const p = parseAnalyticsParams('?species=Turdus%20merula,,%20Parus%20major%20,', { now: NOW });
    expect(p.species).toEqual(['Turdus merula', 'Parus major']);
  });
});

describe('serializeAnalyticsParams (tab removed)', () => {
  it('never writes a tab key', () => {
    const p = parseAnalyticsParams('?range=week&species=A');
    const qs = serializeAnalyticsParams(p);
    expect(qs).not.toContain('tab=');
    expect(qs).toContain('range=week');
    expect(qs).toContain('species=A');
  });

  const base = (overrides: Partial<AnalyticsParams>): AnalyticsParams => ({
    ...parseAnalyticsParams('', { now: NOW }),
    ...overrides,
  });

  it('omits defaults and empties', () => {
    const qs = serializeAnalyticsParams(base({}));
    expect(qs).toBe('');
  });

  it('only serialises start/end for custom ranges', () => {
    const preset = serializeAnalyticsParams(
      base({ range: 'week', start: '2026-01-01', end: '2026-02-01' })
    );
    expect(preset).toBe('range=week');

    const custom = serializeAnalyticsParams(
      base({ range: 'custom', start: '2026-01-01', end: '2026-02-01' })
    );
    expect(custom).toContain('range=custom');
    expect(custom).toContain('start=2026-01-01');
    expect(custom).toContain('end=2026-02-01');
  });

  it('serialises species as a comma list', () => {
    const qs = serializeAnalyticsParams(base({ species: ['Turdus merula', 'Parus major'] }));
    const parsed = new URLSearchParams(qs);
    expect(parsed.get('species')).toBe('Turdus merula,Parus major');
  });
});

describe('round-trip (reload + Back/Forward fidelity)', () => {
  // parse(serialize(p)) must equal p for the URL-relevant fields, so reloading a
  // serialised URL and navigating Back/Forward restore the same view.
  const cases: Partial<AnalyticsParams>[] = [
    {},
    { range: 'week' },
    { range: 'year' },
    { range: 'week', species: ['Turdus merula', 'Parus major', 'Erithacus rubecula'] },
    { range: 'custom', start: '2026-01-01', end: '2026-02-01', species: ['Parus major'] },
    { source: 'mic-2' },
  ];

  it.each(cases)('round-trips %o', overrides => {
    const original = {
      ...parseAnalyticsParams('', { now: NOW }),
      ...overrides,
    };
    const qs = serializeAnalyticsParams(original);
    const restored = parseAnalyticsParams(qs, { now: NOW });

    expect(restored.range).toBe(original.range);
    expect(restored.species).toEqual(original.species);
    expect(restored.source).toBe(original.source);
    // Presets carry empty start/end (omitted from the URL), custom carries them;
    // both round-trip back to the original value.
    expect(restored.start).toBe(original.start);
    expect(restored.end).toBe(original.end);
    // The derived Date pair the charts consume must be resolved consistently
    // from the restored range/start/end (catches a parse that swaps or fails to
    // re-resolve the dates).
    const [expStart, expEnd] = resolveDateRange(restored.range, restored.start, restored.end, NOW);
    expect(formatDateForAPI(restored.startDate)).toBe(formatDateForAPI(expStart));
    expect(formatDateForAPI(restored.endDate)).toBe(formatDateForAPI(expEnd));
  });
});
