import { describe, it, expect, vi, beforeEach } from 'vitest';
import { flushSync } from 'svelte';
import { useSpeciesHistory, HISTORY_WINDOW_DAYS } from './useSpeciesHistory.svelte';

// vi.mock is hoisted above the imports, so its factory cannot close over ordinary
// consts declared below (they are still in TDZ when the factory runs). vi.hoisted
// lifts the spies alongside it. Same pattern as ImportActivityCard.test.ts.
const { post, get } = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn() }));

vi.mock('$lib/utils/api', () => ({
  api: { post, get },
}));

vi.mock('$lib/i18n', () => ({
  t: (key: string) => key,
}));

/** Search response for the date_desc call: newest first. */
function descResponse() {
  return {
    total: 820,
    pages: 41,
    currentPage: 1,
    results: [
      {
        id: '20481',
        timestamp: '2026-07-26T08:14:40-04:00',
        confidence: 0.94,
        verified: 'unverified',
        locked: false,
      },
      {
        id: '20480',
        timestamp: '2026-07-26T08:14:00-04:00',
        confidence: 0.9,
        verified: 'unverified',
        locked: false,
      },
    ],
  };
}

/** Search response for the date_asc call: oldest first. */
function ascResponse() {
  return {
    total: 820,
    pages: 41,
    currentPage: 1,
    results: [
      {
        id: '1',
        timestamp: '2026-06-25T09:21:51-04:00',
        confidence: 0.81,
        verified: 'unverified',
        locked: false,
      },
    ],
  };
}

/**
 * Daily analytics as the server actually returns it: descending by date, and
 * days with no detections omitted entirely.
 */
function dailyResponse() {
  return {
    start_date: '2026-06-27',
    end_date: '2026-07-26',
    species: 'Spinus tristis',
    total: 12,
    data: [
      { date: '2026-07-26', count: 4 },
      { date: '2026-07-24', count: 8 },
    ],
  };
}

function wireHappyPath() {
  post.mockImplementation((_url: string, body: { sortBy: string }) =>
    Promise.resolve(body.sortBy === 'date_asc' ? ascResponse() : descResponse())
  );
  get.mockResolvedValue(dailyResponse());
}

describe('useSpeciesHistory', () => {
  beforeEach(() => {
    post.mockReset();
    get.mockReset();
  });

  it('starts empty and not loading', () => {
    const h = useSpeciesHistory();
    expect(h.data).toBeNull();
    expect(h.isLoading).toBe(false);
    expect(h.error).toBeNull();
  });

  it('derives first heard, last heard and lifetime total', async () => {
    wireHappyPath();
    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', '20481');
    flushSync();

    expect(h.data?.firstHeard).toBe('2026-06-25T09:21:51-04:00');
    expect(h.data?.lastHeard).toBe('2026-07-26T08:14:40-04:00');
    expect(h.data?.totalDetections).toBe(820);
    expect(h.error).toBeNull();
    expect(h.isLoading).toBe(false);
  });

  it('zero-fills and ascending-sorts the daily counts across the whole window', async () => {
    wireHappyPath();
    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', '20481');
    flushSync();

    const counts = h.data?.dailyCounts ?? [];
    // One slot per day in the window, no gaps collapsed.
    expect(counts).toHaveLength(HISTORY_WINDOW_DAYS);
    // Ascending: the window ends on the anchor date, so the last slot is 2026-07-26.
    expect(counts[HISTORY_WINDOW_DAYS - 1]).toBe(4);
    // 2026-07-25 reported nothing, so it must read 0, not be skipped.
    expect(counts[HISTORY_WINDOW_DAYS - 2]).toBe(0);
    expect(counts[HISTORY_WINDOW_DAYS - 3]).toBe(8);
    // Days before any reported data are zeros, not undefined.
    expect(counts[0]).toBe(0);
    expect(counts.every(c => Number.isFinite(c))).toBe(true);
  });

  it('excludes the detection being viewed from the recent list', async () => {
    wireHappyPath();
    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', '20481');
    flushSync();

    expect(h.data?.recent.map(r => r.id)).toEqual(['20480']);
  });

  it('reports an error and keeps data null when a request fails', async () => {
    post.mockRejectedValue(new Error('boom'));
    get.mockResolvedValue(dailyResponse());

    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', '20481');
    flushSync();

    expect(h.error).toBe('detections.history.loadError');
    expect(h.data).toBeNull();
    expect(h.isLoading).toBe(false);
  });

  it('does not refetch the same species and anchor date twice', async () => {
    wireHappyPath();
    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', '20481');
    flushSync();
    const callsAfterFirst = post.mock.calls.length;

    await h.load('Spinus tristis', '2026-07-26', '20481');
    flushSync();

    expect(post.mock.calls.length).toBe(callsAfterFirst);
  });

  it('refetches when the species changes', async () => {
    wireHappyPath();
    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', '20481');
    flushSync();
    const callsAfterFirst = post.mock.calls.length;

    await h.load('Cardinalis cardinalis', '2026-07-26', '20481');
    flushSync();

    expect(post.mock.calls.length).toBeGreaterThan(callsAfterFirst);
  });

  it('sends an exact scientific-name filter and no free-text species term', async () => {
    wireHappyPath();
    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', '20481');
    flushSync();

    const [url, body] = post.mock.calls[0] as [string, Record<string, unknown>];
    expect(url).toBe('/api/v2/search');
    expect(body.speciesScientific).toEqual(['Spinus tristis']);
    expect(body.species).toBe('');
  });

  it('reset clears data and allows a refetch', async () => {
    wireHappyPath();
    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', '20481');
    flushSync();

    h.reset();
    flushSync();
    expect(h.data).toBeNull();

    await h.load('Spinus tristis', '2026-07-26', '20481');
    flushSync();
    expect(h.data?.totalDetections).toBe(820);
  });

  it('rejects stale responses from superseded requests (race condition guard)', async () => {
    /**
     * Regression test for signal-capture guard against overlapping requests.
     * If the code changes to check `controller` directly instead of the captured
     * `signal`, or removes the `controller === active` guard in finally, this test
     * will fail (stale data will overwrite the newer response).
     *
     * Scenario:
     * 1. Start load for species A — api.post hangs (pending promise)
     * 2. Start load for species B — api.post resolves immediately
     * 3. Await load for B → completes, data set to species B results
     * 4. Resolve pending promise for species A → must not overwrite data
     */
    // Create controlled promise for species A that we can resolve manually
    let resolveSpeciesA: (value: unknown) => void = () => {};
    const pendingA = new Promise(resolve => {
      resolveSpeciesA = resolve;
    });

    post.mockImplementation((url: string, body: { sortBy: string }) => {
      // species A (Spinus tristis) returns the pending promise
      if (body.speciesScientific?.[0] === 'Spinus tristis') {
        return pendingA;
      }
      // species B (Cardinalis cardinalis) resolves immediately
      return Promise.resolve(body.sortBy === 'date_asc' ? ascResponse() : descResponse());
    });

    get.mockResolvedValue(dailyResponse());

    const h = useSpeciesHistory();

    // Start load for species A (will hang)
    h.load('Spinus tristis', '2026-07-26', '20481');

    // Without awaiting A, start load for species B (resolves immediately)
    await h.load('Cardinalis cardinalis', '2026-07-26', '20481');
    flushSync();

    // Species B should be loaded
    expect(h.data?.totalDetections).toBe(820);
    expect(h.isLoading).toBe(false);
    expect(h.error).toBeNull();
    const speciesBLastHeard = h.data?.lastHeard;

    // Now resolve the stale promise from species A
    resolveSpeciesA(descResponse());
    await new Promise(resolve => setTimeout(resolve, 0)); // Let microtasks run
    flushSync();

    // Species B must still be the loaded data (not overwritten by stale A response)
    expect(h.data?.lastHeard).toBe(speciesBLastHeard);
    expect(h.data?.totalDetections).toBe(820);
    expect(h.isLoading).toBe(false);
    expect(h.error).toBeNull();
  });
});
