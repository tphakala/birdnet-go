import { describe, it, expect, afterEach, vi } from 'vitest';

import { CHART_REGISTRY, readCommonName } from './charts';
import { makeAnalyticsParams, makeChartCtx, stubFetchJson } from './__tests__/registryFixtures';

// Regression tests for #4459: charts that show server-chosen species must take the common name from
// their own payload, not from the hub's species map (empty on tabs without a species filter).

describe('readCommonName', () => {
  it('returns a non-empty string common name', () => {
    expect(readCommonName({ commonName: 'Eurasian Blackbird' }, 'Turdus merula')).toBe(
      'Eurasian Blackbird'
    );
  });

  it('trims surrounding whitespace from the payload name', () => {
    expect(readCommonName({ commonName: '  Eurasian Blackbird \n' }, 'Turdus merula')).toBe(
      'Eurasian Blackbird'
    );
  });

  it.each([
    ['missing', {}],
    ['empty', { commonName: '' }],
    ['blank', { commonName: '   ' }],
    ['null', { commonName: null }],
    ['non-string', { commonName: 42 }],
  ])('falls back to the scientific name when the field is %s', (_label, item) => {
    expect(readCommonName(item, 'Turdus merula')).toBe('Turdus merula');
  });
});

describe('server-chosen species charts read commonName from the payload', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const cases: Array<{ id: string; row: Record<string, unknown> }> = [
    { id: 'species-ridgeline', row: { buckets: [], total: 3 } },
    { id: 'acoustic-succession', row: { counts: [], total: 3 } },
    { id: 'confidence-distribution', row: { bins: [], total: 3 } },
    {
      id: 'species-phenology',
      row: { firstSeen: '2026-03-01', lastSeen: '2026-03-20', count: 3 },
    },
  ];

  describe.each(cases)('$id', ({ id, row }) => {
    const def = CHART_REGISTRY.find(c => c.id === id);
    if (!def?.mapProps) {
      throw new Error(`${id} chart def with mapProps is required`);
    }
    const { fetch: fetchChart, mapProps } = def;

    it('parses commonName, falling back to the scientific name when missing or empty', async () => {
      stubFetchJson([
        { scientificName: 'Turdus merula', commonName: 'Eurasian Blackbird', ...row },
        { scientificName: 'Parus major', commonName: '', ...row },
        { scientificName: 'Apus apus', ...row },
      ]);
      const result = (await fetchChart(
        makeAnalyticsParams({ species: ['Turdus merula'] })
      )) as Array<{ commonName: string }>;
      expect(result.map(r => r.commonName)).toEqual([
        'Eurasian Blackbird',
        'Parus major',
        'Apus apus',
      ]);
    });

    it('maps the payload name to the chart with an empty hub species map', async () => {
      stubFetchJson([
        { scientificName: 'Turdus merula', commonName: 'Eurasian Blackbird', ...row },
      ]);
      const result = await fetchChart(makeAnalyticsParams({ species: ['Turdus merula'] }));
      const props = mapProps(
        result,
        makeAnalyticsParams({ species: ['Turdus merula'] }),
        makeChartCtx()
      );
      const rows = (props.series ?? (props.data as { rows: unknown[] }).rows) as Array<{
        commonName: string;
      }>;
      expect(rows[0].commonName).toBe('Eurasian Blackbird');
    });

    it('keeps the payload name when the species map has a conflicting name', async () => {
      stubFetchJson([
        { scientificName: 'Turdus merula', commonName: 'Eurasian Blackbird', ...row },
      ]);
      const params = makeAnalyticsParams({ species: ['Turdus merula'] });
      const result = await fetchChart(params);
      const props = mapProps(result, params, makeChartCtx({ 'Turdus merula': 'Map Blackbird' }));
      const rows = (props.series ?? (props.data as { rows: unknown[] }).rows) as Array<{
        commonName: string;
      }>;
      expect(rows[0].commonName).toBe('Eurasian Blackbird');
    });
  });
});
