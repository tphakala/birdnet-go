import { describe, it, expect } from 'vitest';

import {
  DEFAULT_RANGE,
  formatDateForAPI,
  parseAnalyticsParams,
  resolveDateRange,
  serializeAnalyticsParams,
} from './analyticsParams';
import type { AnalyticsParams, ChartGroup } from './types';

// Fixed clock for deterministic rolling-window resolution.
const NOW = new Date(2026, 5, 19, 12, 0, 0); // 2026-06-19 local
const DEFAULT_TAB: ChartGroup = 'patterns';

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

describe('parseAnalyticsParams', () => {
  it('returns defaults for an empty query', () => {
    const p = parseAnalyticsParams('', { defaultTab: DEFAULT_TAB, now: NOW });
    expect(p.tab).toBe(DEFAULT_TAB);
    expect(p.range).toBe(DEFAULT_RANGE);
    expect(p.species).toEqual([]);
    expect(p.source).toBe('');
  });

  it('parses a full query (with or without leading ?)', () => {
    const search = '?tab=trends&range=week&species=Turdus%20merula,Parus%20major&source=mic1';
    const p = parseAnalyticsParams(search, { defaultTab: DEFAULT_TAB, now: NOW });
    expect(p.tab).toBe('trends');
    expect(p.range).toBe('week');
    expect(p.species).toEqual(['Turdus merula', 'Parus major']);
    expect(p.source).toBe('mic1');

    const p2 = parseAnalyticsParams('tab=biodiversity', { defaultTab: DEFAULT_TAB, now: NOW });
    expect(p2.tab).toBe('biodiversity');
  });

  it('falls back on invalid tab/range values', () => {
    const p = parseAnalyticsParams('?tab=bogus&range=decade', {
      defaultTab: DEFAULT_TAB,
      now: NOW,
    });
    expect(p.tab).toBe(DEFAULT_TAB);
    expect(p.range).toBe(DEFAULT_RANGE);
  });

  it('parses custom range with start/end into Date objects', () => {
    const p = parseAnalyticsParams('?range=custom&start=2026-03-01&end=2026-03-31', {
      defaultTab: DEFAULT_TAB,
      now: NOW,
    });
    expect(p.range).toBe('custom');
    expect(p.start).toBe('2026-03-01');
    expect(p.end).toBe('2026-03-31');
    expect(formatDateForAPI(p.startDate)).toBe('2026-03-01');
    expect(formatDateForAPI(p.endDate)).toBe('2026-03-31');
  });

  it('trims and drops empty species entries', () => {
    const p = parseAnalyticsParams('?species=Turdus%20merula,,%20Parus%20major%20,', {
      defaultTab: DEFAULT_TAB,
      now: NOW,
    });
    expect(p.species).toEqual(['Turdus merula', 'Parus major']);
  });
});

describe('serializeAnalyticsParams', () => {
  const base = (overrides: Partial<AnalyticsParams>): AnalyticsParams => ({
    ...parseAnalyticsParams('', { defaultTab: DEFAULT_TAB, now: NOW }),
    ...overrides,
  });

  it('omits defaults and empties', () => {
    const qs = serializeAnalyticsParams(base({}), { defaultTab: DEFAULT_TAB });
    expect(qs).toBe('');
  });

  it('omits the default tab but serialises others', () => {
    expect(serializeAnalyticsParams(base({ tab: DEFAULT_TAB }), { defaultTab: DEFAULT_TAB })).toBe(
      ''
    );
    expect(
      serializeAnalyticsParams(base({ tab: 'biodiversity' }), { defaultTab: DEFAULT_TAB })
    ).toContain('tab=biodiversity');
  });

  it('only serialises start/end for custom ranges', () => {
    const preset = serializeAnalyticsParams(
      base({ range: 'week', start: '2026-01-01', end: '2026-02-01' }),
      { defaultTab: DEFAULT_TAB }
    );
    expect(preset).toBe('range=week');

    const custom = serializeAnalyticsParams(
      base({ range: 'custom', start: '2026-01-01', end: '2026-02-01' }),
      { defaultTab: DEFAULT_TAB }
    );
    expect(custom).toContain('range=custom');
    expect(custom).toContain('start=2026-01-01');
    expect(custom).toContain('end=2026-02-01');
  });

  it('serialises species as a comma list', () => {
    const qs = serializeAnalyticsParams(base({ species: ['Turdus merula', 'Parus major'] }), {
      defaultTab: DEFAULT_TAB,
    });
    const parsed = new URLSearchParams(qs);
    expect(parsed.get('species')).toBe('Turdus merula,Parus major');
  });
});

describe('round-trip (reload + Back/Forward fidelity)', () => {
  // parse(serialize(p)) must equal p for the URL-relevant fields, so reloading a
  // serialised URL and navigating Back/Forward restore the same view.
  const cases: Partial<AnalyticsParams>[] = [
    {},
    { tab: 'trends' },
    { tab: 'biodiversity', range: 'year' },
    { range: 'week', species: ['Turdus merula', 'Parus major', 'Erithacus rubecula'] },
    { range: 'custom', start: '2026-01-01', end: '2026-02-01', species: ['Parus major'] },
    { source: 'mic-2' },
  ];

  it.each(cases)('round-trips %o', overrides => {
    const original = {
      ...parseAnalyticsParams('', { defaultTab: DEFAULT_TAB, now: NOW }),
      ...overrides,
    };
    const qs = serializeAnalyticsParams(original, { defaultTab: DEFAULT_TAB });
    const restored = parseAnalyticsParams(qs, { defaultTab: DEFAULT_TAB, now: NOW });

    expect(restored.tab).toBe(original.tab);
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
