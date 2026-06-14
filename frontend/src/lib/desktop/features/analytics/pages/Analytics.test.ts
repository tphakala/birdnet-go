import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { waitFor, cleanup, fireEvent } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import { api } from '$lib/utils/api';
import Analytics from './Analytics.svelte';

// api.get is the data source we drive for the race.
vi.mock('$lib/utils/api', () => ({
  api: { get: vi.fn() },
  ApiError: class ApiError extends Error {},
}));

// D3 charts and source badge are irrelevant to the fetch-sequence logic and
// pull in heavy/contextual dependencies, so stub them out.
vi.mock('../components/charts/d3/BarChart.svelte');
vi.mock('../components/charts/d3/LineChart.svelte');
vi.mock('../components/charts/d3/NewSpeciesTimelineChart.svelte');
vi.mock('$lib/desktop/features/dashboard/components/SourceBadge.svelte');

const analyticsTest = createComponentTestFactory(Analytics);

describe('Analytics fetch-sequence race (#978)', () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset();
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  // Regression: a slower earlier fetchData() run must not overwrite a newer run's
  // data. The fix stamps each run with analyticsFetchSeq and each fetcher commits
  // only when its captured token still matches the latest run.
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
            scientificName: 'Fresh-sci',
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

    const { container } = analyticsTest.render({});

    // The initial run (on mount) fires all six fetchers; the recent one is held.
    await waitFor(() => {
      expect(vi.mocked(api.get).mock.calls.length).toBeGreaterThanOrEqual(6);
    });

    // Trigger a second fetchData() via the filter form while run 1 is still in
    // flight (submitting the form bypasses the loading-disabled button).
    const form = container.querySelector('form');
    expect(form).not.toBeNull();
    await fireEvent.submit(form as HTMLFormElement);

    // Run 2 resolves and renders the fresh detection.
    await waitFor(() => {
      expect(container.textContent).toContain('Fresh-sci');
    });

    // The stale run-1 recent response now arrives; the sequence guard must drop it.
    resolveStaleRecent([
      {
        id: 'stale',
        timestamp: '2024-01-01T08:00:00',
        commonName: 'Stale Bird',
        scientificName: 'Stale-sci',
        confidence: 0.5,
      },
    ]);
    // Flush the production stale-handling path: await the promise it awaits, then
    // a macrotask so every microtask hop (the sequence guard, state commit) and
    // the Svelte DOM flush complete before asserting absence. A microtask-only
    // flush (await tick) under-drains and lets the negative assertion fire early.
    await staleRecent;
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(container.textContent).toContain('Fresh-sci');
    expect(container.textContent).not.toContain('Stale-sci');
  });
});
