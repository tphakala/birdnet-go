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
        id: 20481,
        timestamp: '2026-07-26T08:14:40-04:00',
        confidence: 0.94,
      },
      {
        id: 20480,
        timestamp: '2026-07-26T08:14:00-04:00',
        confidence: 0.9,
      },
    ],
  };
}

/** Search response for the date_asc call: oldest first (Spinus tristis). */
function ascResponse() {
  return {
    total: 820,
    pages: 41,
    currentPage: 1,
    results: [
      {
        id: 1,
        timestamp: '2026-06-25T09:21:51-04:00',
        confidence: 0.81,
      },
    ],
  };
}

/** Search response for Cardinalis cardinalis (date_asc: oldest first). */
function cardinalAscResponse() {
  return {
    total: 42,
    pages: 3,
    currentPage: 1,
    results: [
      {
        id: 100,
        timestamp: '2026-05-01T10:30:00-04:00', // Completely different timestamp
        confidence: 0.88,
      },
    ],
  };
}

/** Search response for Cardinalis cardinalis (date_desc: newest first). */
function cardinalDescResponse() {
  return {
    total: 42,
    pages: 3,
    currentPage: 1,
    results: [
      {
        id: 200,
        timestamp: '2026-07-20T15:45:00-04:00', // Different from Spinus
        confidence: 0.91,
      },
    ],
  };
}

/**
 * Daily analytics with days that had no detections omitted entirely, and the
 * rows deliberately not in ascending date order: the two datastore backends
 * disagree on direction (legacy orders ascending, v2 descending), so the
 * composable must be order-agnostic.
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
    await h.load('Spinus tristis', '2026-07-26', 20481);
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
    await h.load('Spinus tristis', '2026-07-26', 20481);
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
    await h.load('Spinus tristis', '2026-07-26', 20481);
    flushSync();

    expect(h.data?.recent.map(r => r.id)).toEqual([20480]);
  });

  it('reports an error and keeps data null when a request fails', async () => {
    post.mockRejectedValue(new Error('boom'));
    get.mockResolvedValue(dailyResponse());

    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', 20481);
    flushSync();

    expect(h.error).toBe('detections.history.loadError');
    expect(h.data).toBeNull();
    expect(h.isLoading).toBe(false);
  });

  it('does not refetch the same species and anchor date twice', async () => {
    wireHappyPath();
    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', 20481);
    flushSync();
    const callsAfterFirst = post.mock.calls.length;

    await h.load('Spinus tristis', '2026-07-26', 20481);
    flushSync();

    expect(post.mock.calls.length).toBe(callsAfterFirst);
  });

  it('refetches when the species changes', async () => {
    wireHappyPath();
    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', 20481);
    flushSync();
    const callsAfterFirst = post.mock.calls.length;

    await h.load('Cardinalis cardinalis', '2026-07-26', 20481);
    flushSync();

    expect(post.mock.calls.length).toBeGreaterThan(callsAfterFirst);
  });

  it('sends an exact scientific-name filter and no free-text species term', async () => {
    wireHappyPath();
    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', 20481);
    flushSync();

    const [url, body] = post.mock.calls[0] as [string, Record<string, unknown>];
    expect(url).toBe('/api/v2/search');
    expect(body.speciesScientific).toEqual(['Spinus tristis']);
    expect(body.species).toBe('');
  });

  it('reset clears data and allows a refetch', async () => {
    wireHappyPath();
    const h = useSpeciesHistory();
    await h.load('Spinus tristis', '2026-07-26', 20481);
    flushSync();

    h.reset();
    flushSync();
    expect(h.data).toBeNull();

    await h.load('Spinus tristis', '2026-07-26', 20481);
    flushSync();
    expect(h.data?.totalDetections).toBe(820);
  });

  it('rejects stale responses when newer load completes first (signal-captured check)', async () => {
    /**
     * Regression test for signal-capture guard against overlapping requests.
     * If changed to check `controller?.signal.aborted` instead of the captured `signal`,
     * this test fails (stale data overwrites newer).
     *
     * Scenario:
     * 1. Start load for species A (Spinus tristis, total: 820) — hangs
     * 2. Start load for species B (Cardinalis cardinalis, total: 42) — resolves immediately
     * 3. Await B → data reflects species B
     * 4. Resolve A's pending response → must not overwrite B's data
     *
     * With distinct fixtures (different totals, timestamps), any overwrite is detectable.
     */
    let resolveSpeciesA: (value: unknown) => void = () => {};
    const pendingA = new Promise(resolve => {
      resolveSpeciesA = resolve;
    });

    post.mockImplementation(
      (url: string, body: { sortBy: string; speciesScientific: string[] }) => {
        const species = body.speciesScientific[0];

        // Species A (Spinus tristis) branches on sortBy and returns controlled pending
        if (species === 'Spinus tristis') {
          // Resolve with the appropriate fixture based on sort order when resolved
          return pendingA.then(() => (body.sortBy === 'date_asc' ? ascResponse() : descResponse()));
        }

        // Species B (Cardinalis cardinalis) branches on sortBy
        if (species === 'Cardinalis cardinalis') {
          return Promise.resolve(
            body.sortBy === 'date_asc' ? cardinalAscResponse() : cardinalDescResponse()
          );
        }

        // Fallback
        return Promise.resolve(body.sortBy === 'date_asc' ? ascResponse() : descResponse());
      }
    );

    get.mockResolvedValue(dailyResponse());

    const h = useSpeciesHistory();

    // Start load for species A (will hang indefinitely)
    h.load('Spinus tristis', '2026-07-26', 20481);

    // Without awaiting A, start and await load for species B (resolves immediately)
    await h.load('Cardinalis cardinalis', '2026-07-26', 20481);
    flushSync();

    // Species B should be loaded: total is 42, not 820
    expect(h.data?.totalDetections).toBe(42);
    expect(h.data?.lastHeard).toBe('2026-07-20T15:45:00-04:00'); // Cardinal's timestamp
    expect(h.data?.firstHeard).toBe('2026-05-01T10:30:00-04:00'); // Cardinal's first
    expect(h.isLoading).toBe(false);
    expect(h.error).toBeNull();

    // Save reference to species B's data
    const speciesBData = {
      totalDetections: h.data?.totalDetections,
      lastHeard: h.data?.lastHeard,
      firstHeard: h.data?.firstHeard,
    };

    // Now resolve the stale promise from species A (with Spinus data: total 820)
    resolveSpeciesA(descResponse());
    await new Promise(resolve => setTimeout(resolve, 0)); // Let microtasks run
    flushSync();

    // Species B data must still be present (stale A response must not overwrite it)
    expect(h.data?.totalDetections).toBe(speciesBData.totalDetections);
    expect(h.data?.lastHeard).toBe(speciesBData.lastHeard);
    expect(h.data?.firstHeard).toBe(speciesBData.firstHeard);
    expect(h.isLoading).toBe(false);
    expect(h.error).toBeNull();
  });

  it('maintains correct isLoading with three overlapping requests (finally guard)', async () => {
    /**
     * Regression test for the `controller === active` guard in the finally block.
     *
     * If the guard is removed, a superseded request's finally block will
     * unconditionally set `isLoading = false`, reporting "not loading" while
     * a genuinely newer request is still in flight.
     *
     * Scenario:
     * 1. Start load A (Spinus tristis) — hangs
     * 2. Start load B (Cardinalis cardinalis) — hangs, aborts A
     * 3. Start load C (third species) — hangs, aborts B
     * 4. Do NOT await C — leave it pending
     * 5. Resolve A's promise → A's finally must not clear isLoading
     * 6. Assert isLoading is still true because C is genuinely in flight
     */
    let resolveSpeciesA: (value: unknown) => void = () => {};
    let resolveSpeciesC: (value: unknown) => void = () => {};

    const pendingA = new Promise(resolve => {
      resolveSpeciesA = resolve;
    });
    // Species B's promise is intentionally never resolved: B is superseded by C
    // before its resolution would matter to the `controller === active` guard.
    const pendingB = new Promise<never>(() => {});
    const pendingC = new Promise(resolve => {
      resolveSpeciesC = resolve;
    });

    post.mockImplementation(
      (url: string, body: { sortBy: string; speciesScientific: string[] }) => {
        const species = body.speciesScientific[0];

        if (species === 'Spinus tristis') {
          return pendingA.then(() => (body.sortBy === 'date_asc' ? ascResponse() : descResponse()));
        }

        if (species === 'Cardinalis cardinalis') {
          return pendingB.then(() =>
            body.sortBy === 'date_asc' ? cardinalAscResponse() : cardinalDescResponse()
          );
        }

        // Species C (any other species): use a third deferred promise
        return pendingC.then(() => (body.sortBy === 'date_asc' ? ascResponse() : descResponse()));
      }
    );

    get.mockResolvedValue(dailyResponse());

    const h = useSpeciesHistory();

    // Start load A (Spinus tristis) — will hang on pendingA
    h.load('Spinus tristis', '2026-07-26', 20481);

    // Start load B (Cardinalis cardinalis) without awaiting A — aborts A
    h.load('Cardinalis cardinalis', '2026-07-26', 20481);

    // Start load C (third species: Turdus migratorius) without awaiting B — aborts B
    // This will use pendingC
    h.load('Turdus migratorius', '2026-07-26', 20481);

    // Do NOT await C yet — leave it pending in flight
    flushSync();

    // C is now the active load and should still be loading
    expect(h.isLoading).toBe(true);

    // Resolve A's pending promise — it was superseded before reaching finally
    resolveSpeciesA(descResponse());
    await new Promise(resolve => setTimeout(resolve, 0)); // Let microtasks run
    flushSync();

    // A's finally must not have cleared isLoading — C is still in flight
    expect(h.isLoading).toBe(true);
    expect(h.error).toBeNull();

    // Now resolve C to complete the load
    resolveSpeciesC(descResponse());
    await new Promise(resolve => setTimeout(resolve, 0));
    flushSync();

    // Only now should isLoading be false (C completed)
    expect(h.isLoading).toBe(false);
  });
});
