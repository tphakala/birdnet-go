import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

import { CHART_REGISTRY } from './charts';
import type { AnalyticsParams } from './types';
import { finalCumulative, type AccumulationData } from '../components/charts/d3/utils/accumulation';

// Verifies the species-accumulation registry entry: its placement/flags, the custom countDataPoints
// (final cumulative species, not per-day length), and the fetcher's defensive coercion of the array
// payload. jsdom has no layout engine; these assert on data, not rendering.

const def = CHART_REGISTRY.find(c => c.id === 'species-accumulation');
// Guard at module scope so the rest of the file sees a defined def without non-null assertions
// (forbidden by lint); a missing entry fails every test with a clear message.
if (!def) {
  throw new Error('species-accumulation chart def is required');
}
const chartDef = def;

function makeParams(): AnalyticsParams {
  return {
    tab: 'biodiversity',
    range: 'month',
    start: '2026-03-01',
    end: '2026-03-31',
    species: [],
    source: '',
    startDate: new Date('2026-03-01T00:00:00'),
    endDate: new Date('2026-03-31T00:00:00'),
  };
}

describe('species-accumulation chart def', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('is registered in the biodiversity group as an all-species chart', () => {
    expect(chartDef.group).toBe('biodiversity');
    // The curve is inherently all-species; the sibling diversity chart is also species:false, so the
    // biodiversity tab shows no (dead) species selector.
    expect(chartDef.supports.species).toBe(false);
    expect(chartDef.supports.source).toBe(false);
    expect(chartDef.minDataPoints).toBe(2);
    expect(chartDef.size).toBe('full');
  });

  it('counts data points as the final cumulative species, not the per-day length', () => {
    const data: AccumulationData = {
      points: [
        { date: '2026-03-01', cumulativeSpecies: 1, newSpecies: 1 },
        { date: '2026-03-02', cumulativeSpecies: 3, newSpecies: 2 },
        { date: '2026-03-03', cumulativeSpecies: 3, newSpecies: 0 },
      ],
    };
    expect(chartDef.countDataPoints?.(data)).toBe(3);

    // A long all-zero curve must count as 0 (not its 30-day length), so ChartCard shows the
    // not-enough-data state instead of an empty flat line.
    const flat: AccumulationData = {
      points: Array.from({ length: 30 }, (_, i) => ({
        date: `2026-03-${String(i + 1).padStart(2, '0')}`,
        cumulativeSpecies: 0,
        newSpecies: 0,
      })),
    };
    expect(chartDef.countDataPoints?.(flat)).toBe(0);
  });

  it('fetches and coerces the array payload, dropping malformed entries', async () => {
    const payload = [
      { date: '2026-03-01', cumulativeSpecies: 1, newSpecies: 1 },
      { date: '2026-03-02', cumulativeSpecies: 2, newSpecies: 1 },
      null,
      { cumulativeSpecies: 5, newSpecies: 1 }, // missing date -> dropped
      { date: '2026-03-03', cumulativeSpecies: 'x', newSpecies: Number.NaN }, // non-finite -> 0
    ];
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: () => Promise.resolve(payload),
    });
    vi.stubGlobal('fetch', fetchMock);

    const result = (await chartDef.fetch(makeParams())) as AccumulationData;
    expect(fetchMock).toHaveBeenCalledOnce();
    const url = fetchMock.mock.calls[0][0] as string;
    expect(url).toContain('/api/v2/analytics/species/accumulation');
    // All-species chart: the species filter is never sent.
    expect(url).not.toContain('species=');

    expect(result.points).toHaveLength(3);
    expect(result.points[0]).toEqual({ date: '2026-03-01', cumulativeSpecies: 1, newSpecies: 1 });
    // Non-finite values coerce to 0 rather than leaking NaN into the chart.
    expect(result.points[2]).toEqual({ date: '2026-03-03', cumulativeSpecies: 0, newSpecies: 0 });
    expect(finalCumulative(result)).toBe(2);
  });

  it('throws when the payload is not an array', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        statusText: 'OK',
        json: () => Promise.resolve({ not: 'an array' }),
      })
    );
    await expect(chartDef.fetch(makeParams())).rejects.toThrow();
  });
});
