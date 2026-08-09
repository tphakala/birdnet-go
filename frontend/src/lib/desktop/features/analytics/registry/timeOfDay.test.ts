import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

import { CHART_REGISTRY } from './charts';
import type { AnalyticsParams } from './types';

// Verifies the time-of-day registry entry requests the whole selected date range (it used to ask
// for a single day - the range's start - so a long range rendered one day from months ago) and
// converts the range totals the endpoint returns into an average per day.

const def = CHART_REGISTRY.find(c => c.id === 'time-of-day-species');
// Guard at module scope so the rest of the file sees a defined def without non-null assertions
// (forbidden by lint); a missing entry fails every test with a clear message.
if (!def) {
  throw new Error('time-of-day-species chart def is required');
}
const chartDef = def;

interface TimeOfDayRow {
  species: string;
  data: { hour: number; count: number }[];
}

// 2026-03-01 .. 2026-03-31 inclusive is 31 days, so a total of 62 averages to exactly 2/day.
function makeParams(species: string[] = ['Turdus merula']): AnalyticsParams {
  return {
    range: 'month',
    start: '2026-03-01',
    end: '2026-03-31',
    species,
    source: '',
    startDate: new Date('2026-03-01T00:00:00'),
    endDate: new Date('2026-03-31T00:00:00'),
  };
}

function stubFetch(payload: unknown) {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    status: 200,
    statusText: 'OK',
    json: () => Promise.resolve(payload),
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

describe('time-of-day chart def', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('requests the full selected date range, not a single day', async () => {
    const fetchMock = stubFetch({});
    await chartDef.fetch(makeParams());

    const url = new URL(fetchMock.mock.calls[0][0] as string, 'http://localhost');
    expect(url.pathname).toContain('/api/v2/analytics/time/hourly/batch');
    expect(url.searchParams.get('start_date')).toBe('2026-03-01');
    expect(url.searchParams.get('end_date')).toBe('2026-03-31');
    // The legacy single-date param must be gone: sending it pinned the chart to one day.
    expect(url.searchParams.get('date')).toBeNull();
    expect(url.searchParams.getAll('species')).toEqual(['Turdus merula']);
  });

  it('averages the range totals per day', async () => {
    // The endpoint returns each hour summed over the range; 62 across 31 days is 2 per day.
    const hourly = Array.from({ length: 24 }, (_, hour) => ({
      hour,
      count: hour === 12 ? 62 : 0,
    }));
    stubFetch({ 'Turdus merula': hourly });

    const rows = (await chartDef.fetch(makeParams())) as TimeOfDayRow[];
    expect(rows).toHaveLength(1);

    const noon = rows[0].data.find(d => d.hour === 12);
    expect(noon?.count).toBe(2);
    // Hours with no detections stay at zero rather than becoming NaN.
    expect(rows[0].data.find(d => d.hour === 0)?.count).toBe(0);
  });

  it('returns empty without a request when no species are selected', async () => {
    const fetchMock = stubFetch({});
    const result = await chartDef.fetch(makeParams([]));

    expect(result).toEqual([]);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
