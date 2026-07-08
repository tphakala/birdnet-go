import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

import { CHART_REGISTRY } from './charts';
import type { AnalyticsParams, ChartPropsContext } from './types';

// Verifies the acoustic-succession registry entry: its placement/flags, the default (array-length)
// data-point count, the fetcher's defensive coercion of the array payload, and the common-name
// enrichment in mapProps. jsdom has no layout engine; these assert on data, not rendering.

const def = CHART_REGISTRY.find(c => c.id === 'acoustic-succession');
// Guard at module scope so the rest of the file sees a defined def without non-null assertions
// (forbidden by lint); a missing entry fails every test with a clear message.
if (!def) {
  throw new Error('acoustic-succession chart def is required');
}
const chartDef = def;

interface SuccessionRow {
  scientificName: string;
  commonName: string;
  counts: number[];
  total: number;
}

function makeParams(): AnalyticsParams {
  return {
    range: 'month',
    start: '2026-03-01',
    end: '2026-03-31',
    species: ['Turdus merula'],
    source: '',
    startDate: new Date('2026-03-01T00:00:00'),
    endDate: new Date('2026-03-31T00:00:00'),
  };
}

function makeCtx(names: Record<string, string>): ChartPropsContext {
  return {
    options: {},
    onParamsChange: vi.fn(),
    speciesNames: new Map(Object.entries(names)),
  };
}

describe('acoustic-succession chart def', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('is registered in the patterns group as a top-N streamgraph', () => {
    expect(chartDef.group).toBe('patterns');
    // Like the sibling ridgeline, supports.species lets the patterns tab's auto-select run; the chart
    // ignores the selection and shows the top N (its note says so).
    expect(chartDef.supports.species).toBe(true);
    expect(chartDef.supports.source).toBe(false);
    // A streamgraph needs a few bands to read as a handover, not just two.
    expect(chartDef.minDataPoints).toBe(3);
    expect(chartDef.size).toBe('full');
    expect(chartDef.maxSpecies).toBe(6);
    // No custom countDataPoints: the fetch result is the raw row array, so the band count drives the
    // not-enough-data gate.
    expect(chartDef.countDataPoints).toBeUndefined();
  });

  it('fetches the top-N succession payload, coercing counts and dropping nameless rows', async () => {
    const overlong = Array.from({ length: 30 }, (_, i) => i); // 30 buckets -> truncated to 24
    const payload = [
      {
        scientificName: 'Turdus merula',
        counts: [...Array.from({ length: 6 }, () => 0), 30, ...Array.from({ length: 17 }, () => 0)],
        total: 30,
      },
      null,
      { counts: [1, 2, 3], total: 6 }, // missing scientificName -> dropped
      { scientificName: 'Apus apus', counts: overlong, total: 'x' }, // non-finite total -> 0; counts truncated
      { scientificName: 'Strix aluco', counts: [-5, 'bad', 4], total: 4 }, // negative/non-finite -> 0
    ];
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: () => Promise.resolve(payload),
    });
    vi.stubGlobal('fetch', fetchMock);

    const result = (await chartDef.fetch(makeParams())) as SuccessionRow[];
    expect(fetchMock).toHaveBeenCalledOnce();
    const url = fetchMock.mock.calls[0][0] as string;
    expect(url).toContain('/api/v2/analytics/time/succession');
    // Top-N chart: the species filter is never sent even though params has a selection. The limit is.
    expect(url).not.toContain('species=');
    expect(url).toContain('limit=6');

    expect(result).toHaveLength(3);
    // Every row's counts are coerced to exactly 24 finite, non-negative buckets.
    for (const row of result) {
      expect(row.counts).toHaveLength(24);
      expect(row.counts.every(c => Number.isFinite(c) && c >= 0)).toBe(true);
    }
    expect(result[0].scientificName).toBe('Turdus merula');
    expect(result[0].counts[6]).toBe(30);
    // Non-finite total coerces to 0; over-long counts are truncated to 24.
    const apus = result.find(r => r.scientificName === 'Apus apus');
    expect(apus?.total).toBe(0);
    expect(apus?.counts).toHaveLength(24);
    // Negative and non-finite counts coerce to 0.
    const strix = result.find(r => r.scientificName === 'Strix aluco');
    expect(strix?.counts[0]).toBe(0);
    expect(strix?.counts[1]).toBe(0);
    expect(strix?.counts[2]).toBe(4);
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

  it('enriches the series with the resolved common name, falling back to the scientific name', () => {
    expect(chartDef.mapProps).toBeDefined();
    const raw: SuccessionRow[] = [
      {
        scientificName: 'Turdus merula',
        commonName: '',
        counts: Array.from({ length: 24 }, () => 1),
        total: 24,
      },
      {
        scientificName: 'Apus apus',
        commonName: '',
        counts: Array.from({ length: 24 }, () => 2),
        total: 48,
      },
    ];
    const ctx = makeCtx({ 'Turdus merula': 'Eurasian Blackbird' });
    const props = chartDef.mapProps?.(raw, makeParams(), ctx) ?? {};
    const series = props.series as SuccessionRow[];
    expect(series).toHaveLength(2);
    expect(series[0].commonName).toBe('Eurasian Blackbird');
    // No mapping -> falls back to the scientific name.
    expect(series[1].commonName).toBe('Apus apus');
    expect(props.noteKey).toBe('analytics.advanced.charts.succession.note');
  });
});
