import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

import { CHART_REGISTRY } from './charts';
import { makeAnalyticsParams, makeChartCtx, stubFetchJson } from './__tests__/registryFixtures';
import type { PhenologyData, PhenologyDatum } from '../components/charts/d3/utils/phenology';

// Verifies the species-phenology registry entry: its placement/flags, the default (array-length)
// data-point count, the fetcher's defensive coercion of the array payload, and the common-name
// enrichment in mapProps. jsdom has no layout engine; these assert on data, not rendering.

const def = CHART_REGISTRY.find(c => c.id === 'species-phenology');
// Guard at module scope so the rest of the file sees a defined def without non-null assertions
// (forbidden by lint); a missing entry fails every test with a clear message.
if (!def) {
  throw new Error('species-phenology chart def is required');
}
const chartDef = def;

describe('species-phenology chart def', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('is registered in the biodiversity group as an all-species chart', () => {
    expect(chartDef.group).toBe('biodiversity');
    // Top-N by volume, never per-species; the biodiversity siblings are also species:false, so no
    // (dead) species selector is shown.
    expect(chartDef.supports.species).toBe(false);
    expect(chartDef.supports.source).toBe(false);
    expect(chartDef.minDataPoints).toBe(2);
    expect(chartDef.size).toBe('full');
    // No custom countDataPoints: the fetch result is the raw row array, so ChartCard's default
    // array-length count (the species count) drives the not-enough-data gate.
    expect(chartDef.countDataPoints).toBeUndefined();
  });

  it('fetches and coerces the array payload, dropping rows missing a name or either date', async () => {
    const payload = [
      {
        scientificName: 'Apus apus',
        commonName: 'Common Swift',
        firstSeen: '2026-03-01',
        lastSeen: '2026-03-20',
        count: 40,
      },
      null,
      { scientificName: 'X', firstSeen: '2026-03-01' }, // missing lastSeen -> dropped
      { firstSeen: '2026-03-01', lastSeen: '2026-03-02', count: 1 }, // missing name -> dropped
      {
        scientificName: 'Hirundo rustica',
        firstSeen: '2026-03-05',
        lastSeen: '2026-03-28',
        count: 'x',
      }, // non-finite count -> 0
    ];
    const fetchMock = stubFetchJson(payload);

    const result = (await chartDef.fetch(makeAnalyticsParams())) as PhenologyDatum[];
    expect(fetchMock).toHaveBeenCalledOnce();
    const url = fetchMock.mock.calls[0][0] as string;
    expect(url).toContain('/api/v2/analytics/species/phenology');
    // All-species chart: the species filter is never sent. The top-N limit is.
    expect(url).not.toContain('species=');
    expect(url).toContain('limit=12');

    expect(result).toHaveLength(2);
    expect(result[0]).toEqual({
      scientificName: 'Apus apus',
      commonName: 'Common Swift',
      firstSeen: '2026-03-01',
      lastSeen: '2026-03-20',
      count: 40,
    });
    // Non-finite count coerces to 0 rather than leaking NaN into the chart.
    // No commonName in the payload -> falls back to the scientific name.
    expect(result[1]).toEqual({
      scientificName: 'Hirundo rustica',
      commonName: 'Hirundo rustica',
      firstSeen: '2026-03-05',
      lastSeen: '2026-03-28',
      count: 0,
    });
  });

  it('throws when the payload is not an array', async () => {
    stubFetchJson({ not: 'an array' });
    await expect(chartDef.fetch(makeAnalyticsParams())).rejects.toThrow();
  });

  it('uses the payload common name even when the hub species map is empty (#4459)', () => {
    expect(chartDef.mapProps).toBeDefined();
    const raw: PhenologyDatum[] = [
      {
        scientificName: 'Apus apus',
        commonName: 'Common Swift',
        firstSeen: '2026-03-01',
        lastSeen: '2026-03-20',
        count: 40,
      },
      {
        scientificName: 'Hirundo rustica',
        commonName: 'Hirundo rustica',
        firstSeen: '2026-03-05',
        lastSeen: '2026-03-28',
        count: 25,
      },
    ];
    // A fresh load of the biodiversity tab never fetches the species summary, so the map is empty.
    const props = chartDef.mapProps?.(raw, makeAnalyticsParams(), makeChartCtx({})) ?? {};
    const data = props.data as PhenologyData;
    expect(data.rows).toHaveLength(2);
    expect(data.rows[0].commonName).toBe('Common Swift');
    expect(data.rows[1].commonName).toBe('Hirundo rustica');
  });
});
