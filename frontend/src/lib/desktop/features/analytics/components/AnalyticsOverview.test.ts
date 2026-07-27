import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { waitFor, cleanup } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import { api } from '$lib/utils/api';
import AnalyticsOverview from './AnalyticsOverview.svelte';
import type { AnalyticsParams } from '../registry/types';

// api.get is the data source we drive for the race.
vi.mock('$lib/utils/api', () => ({
  api: { get: vi.fn() },
  ApiError: class ApiError extends Error {},
}));

// D3 charts and source badge are irrelevant to the fetch-sequence logic and
// pull in heavy/contextual dependencies, so stub them out.
vi.mock('./charts/d3/BarChart.svelte');
vi.mock('./charts/d3/LineChart.svelte');
vi.mock('./charts/d3/NewSpeciesTimelineChart.svelte');
vi.mock('$lib/desktop/features/dashboard/components/SourceBadge.svelte');

const overviewTest = createComponentTestFactory(AnalyticsOverview);

// Two distinct date ranges. Switching between them changes the hub's resolved
// range, which is what drives a refetch (the old standalone page used a form).
function makeParams(start: string, end: string): AnalyticsParams {
  return {
    range: 'custom',
    start,
    end,
    species: [],
    source: '',
    startDate: new Date(`${start}T00:00:00`),
    endDate: new Date(`${end}T00:00:00`),
  };
}
const RANGE_A = makeParams('2024-01-01', '2024-01-07');
const RANGE_B = makeParams('2024-02-01', '2024-02-07');

// Sentinel scientific names referenced by both the mock data and the assertions,
// so a typo cannot silently desync the two.
const FRESH_SCIENTIFIC = 'Fresh-sci';
const STALE_SCIENTIFIC = 'Stale-sci';

describe('AnalyticsOverview fetch-sequence race (#978)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.get).mockReset();
  });

  afterEach(() => {
    cleanup();
  });

  // Regression: a slower earlier fetchData() run must not overwrite a newer run's
  // data. The fix stamps each run with analyticsFetchSeq and each fetcher commits
  // only when its captured token still matches the latest run. Carried over from
  // the standalone Analytics page when it folded into the hub's Overview tab; the
  // refetch trigger is now a date-range change instead of a filter-form submit.
  it('does not let a stale recent-detections response overwrite a newer run', async () => {
    let recentCalls = 0;
    let resolveStaleRecent!: (value: unknown) => void;
    const staleRecent = new Promise<unknown>(resolve => {
      resolveStaleRecent = resolve;
    });

    vi.mocked(api.get).mockImplementation((url: string): Promise<unknown> => {
      if (url.includes('/api/v2/detections/recent')) {
        recentCalls += 1;
        // First run's recent fetch is held in flight; the second resolves at once.
        if (recentCalls === 1) return staleRecent;
        return Promise.resolve([
          {
            id: 'fresh',
            timestamp: '2024-01-02T08:00:00',
            commonName: 'Fresh Bird',
            scientificName: FRESH_SCIENTIFIC,
            confidence: 0.9,
          },
        ]);
      }
      if (url.includes('/api/v2/analytics/time/daily')) {
        return Promise.resolve({ data: [] });
      }
      // summary, hourly distribution, new species all accept an empty array.
      return Promise.resolve([]);
    });

    const { container, rerender } = overviewTest.render({ params: RANGE_A });

    // The initial run (on mount) fires all six fetchers; the recent one is held.
    await waitFor(() => {
      expect(vi.mocked(api.get).mock.calls.length).toBeGreaterThanOrEqual(6);
    });

    // Trigger a second fetchData() by changing the shared date range while run 1
    // is still in flight.
    await rerender({ params: RANGE_B });

    // Run 2 resolves and renders the fresh detection.
    await waitFor(() => {
      expect(container.textContent).toContain(FRESH_SCIENTIFIC);
    });

    // The stale run-1 recent response now arrives; the sequence guard must drop it.
    resolveStaleRecent([
      {
        id: 'stale',
        timestamp: '2024-01-01T08:00:00',
        commonName: 'Stale Bird',
        scientificName: STALE_SCIENTIFIC,
        confidence: 0.5,
      },
    ]);
    // Flush the production stale-handling path: await the promise it awaits, then
    // a macrotask so every microtask hop (the sequence guard, state commit) and
    // the Svelte DOM flush complete before asserting absence. A microtask-only
    // flush (await tick) under-drains and lets the negative assertion fire early.
    await staleRecent;
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(container.textContent).toContain(FRESH_SCIENTIFIC);
    expect(container.textContent).not.toContain(STALE_SCIENTIFIC);
  });
});

describe('AnalyticsOverview total-failure error banner', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.get).mockReset();
  });

  afterEach(() => {
    cleanup();
  });

  // When every request fails the user must see a global error, not just six
  // empty charts. Each fetcher rethrows after logging so fetchData can tell a
  // total failure apart from partial/empty results.
  it('shows the error banner when every request fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('network down'));

    const { container } = overviewTest.render({ params: RANGE_A });

    await waitFor(() => {
      expect(container.textContent).toContain('analytics.loadingError');
    });
  });

  // A partial failure is communicated by the affected chart's own empty state,
  // so the global banner must stay hidden when at least one request succeeds.
  it('does not show the error banner on a partial failure', async () => {
    vi.mocked(api.get).mockImplementation((url: string): Promise<unknown> => {
      // Only the new-species endpoint fails; everything else resolves.
      if (url.includes('/api/v2/analytics/species/detections/new')) {
        return Promise.reject(new Error('boom'));
      }
      if (url.includes('/api/v2/analytics/time/daily')) {
        return Promise.resolve({ data: [] });
      }
      return Promise.resolve([]);
    });

    const { container } = overviewTest.render({ params: RANGE_A });

    // Let all six fetchers settle, then a macrotask so the error-state commit
    // (if any) and the Svelte DOM flush complete before asserting absence.
    await waitFor(() => {
      expect(vi.mocked(api.get).mock.calls.length).toBeGreaterThanOrEqual(6);
    });
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(container.textContent).not.toContain('analytics.loadingError');
  });
});
