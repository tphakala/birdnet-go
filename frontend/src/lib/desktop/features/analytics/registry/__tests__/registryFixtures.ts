// Shared fixtures for the registry chart tests. This is a plain helper module, not a test file:
// Vitest only collects *.test.ts / *.spec.ts, so it is never run on its own.
import { vi, type Mock } from 'vitest';

import type { AnalyticsParams, ChartPropsContext } from '../types';

/**
 * Analytics params for March 2026 (`month` range, local-midnight dates) with an empty species
 * selection and no source filter. Pass overrides for the fields a test cares about.
 */
export function makeAnalyticsParams(overrides: Partial<AnalyticsParams> = {}): AnalyticsParams {
  return {
    range: 'month',
    start: '2026-03-01',
    end: '2026-03-31',
    species: [],
    source: '',
    startDate: new Date('2026-03-01T00:00:00'),
    endDate: new Date('2026-03-31T00:00:00'),
    ...overrides,
  };
}

/**
 * A chart props context whose species map holds the given scientific name to common name pairs,
 * given either as tuples or as a record. Defaults to an empty map.
 */
export function makeChartCtx(
  names: ReadonlyArray<readonly [string, string]> | Record<string, string> = []
): ChartPropsContext {
  // Normalize explicitly: new Map({}) throws, so a plain object never reaches the constructor.
  const entries = Array.isArray(names) ? names : Object.entries(names);
  return {
    options: {},
    onParamsChange: vi.fn(),
    speciesNames: new Map(entries),
  };
}

/**
 * Stub the global `fetch` with an ok 200 response whose `json()` resolves `payload`. Returns the
 * mock so a test can read `mock.calls`. Callers restore it with `vi.unstubAllGlobals()`.
 */
export function stubFetchJson(payload: unknown): Mock {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    status: 200,
    statusText: 'OK',
    json: () => Promise.resolve(payload),
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}
