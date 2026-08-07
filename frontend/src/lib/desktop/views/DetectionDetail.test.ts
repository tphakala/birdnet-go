import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { waitFor, cleanup, fireEvent } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../test/render-helpers';
import DetectionDetail from './DetectionDetail.svelte';
import type { Detection } from '$lib/types/detection.types';

// Heavy / context-dependent children are not relevant to the fetch-race logic.
vi.mock('$lib/desktop/components/media/AudioPlayer.svelte');
vi.mock('$lib/desktop/components/data/ConfidenceCircle.svelte');
vi.mock('$lib/desktop/components/data/WeatherDetails.svelte');
vi.mock('$lib/desktop/features/dashboard/components/SourceBadge.svelte');
vi.mock('$lib/desktop/components/ui/VerificationBadges.svelte');

const detailTest = createComponentTestFactory(DetectionDetail);

/** Build a minimal valid Detection for the detail view. */
function makeDetection(overrides: Partial<Detection>): Detection {
  return {
    id: 1,
    date: '2024-01-01',
    time: '10:00:00',
    timestamp: '2024-01-01T10:00:00Z',
    beginTime: '2024-01-01T10:00:00Z',
    endTime: '2024-01-01T10:00:03Z',
    speciesCode: 'spc',
    scientificName: 'Default scientific',
    commonName: 'Default common',
    confidence: 0.9,
    verified: 'unverified',
    locked: false,
    ...overrides,
  };
}

// Sentinel scientific names referenced by both the fixtures and the assertions,
// so a typo cannot silently desync the two.
const FRESH_SCIENTIFIC = 'Fresh-sci-B';
const STALE_SCIENTIFIC = 'Stale-sci-A';

// Distinctive lifetime total for the first detection's history, so the assertion
// that it disappears cannot pass by coincidence against other rendered numbers.
const HISTORY_STALE_TOTAL = 777;
const HISTORY_NEXT_COMMON = 'Next-common-B';

/** Minimal fetch Response stub carrying a JSON body. */
function jsonResponse(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: new Headers({ 'content-type': 'application/json' }),
    json: () => Promise.resolve(body),
    // Serialize lazily and reject (never throw synchronously) so this
    // Promise-returning method honors its contract even on a non-serializable body.
    text: () => {
      try {
        return Promise.resolve(JSON.stringify(body));
      } catch (error) {
        return Promise.reject(error instanceof Error ? error : new Error(String(error)));
      }
    },
  } as unknown as Response;
}

describe('DetectionDetail stale-response race (#978)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  // Regression: navigating from detection A to B while A's request is still in
  // flight must not let A's late response overwrite B. The fix captures the
  // AbortController signal locally and checks the captured signal (not the shared
  // controller reference, which by then points at B's non-aborted controller).
  it('does not let a stale detection response overwrite a newer one', async () => {
    let resolveStale!: (r: Response) => void;
    const staleResponse = new Promise<Response>(resolve => {
      resolveStale = resolve;
    });

    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        // Detection A: held in flight until we resolve it manually (after switching to B).
        if (url.includes('/api/v2/detections/det-a')) {
          return staleResponse;
        }
        // Detection B: resolves immediately and becomes the current detection.
        if (url.includes('/api/v2/detections/det-b')) {
          return Promise.resolve(
            jsonResponse(
              makeDetection({ id: 2, scientificName: FRESH_SCIENTIFIC, commonName: 'Fresh B' })
            )
          );
        }
        // Secondary species/taxonomy/attribution endpoints: irrelevant here.
        return Promise.resolve(jsonResponse({}));
      })
    );

    const { container, rerender } = detailTest.render({ detectionId: 'det-a' });

    // Switch to detection B before A resolves.
    await rerender({ detectionId: 'det-b' });
    await waitFor(() => {
      expect(container.textContent).toContain(FRESH_SCIENTIFIC);
    });

    // A's response now arrives late; the captured-signal guard must drop it.
    resolveStale(
      jsonResponse(
        makeDetection({ id: 1, scientificName: STALE_SCIENTIFIC, commonName: 'Stale A' })
      )
    );
    // Flush the production stale-handling path: await the promise it awaits, then
    // a macrotask so every microtask hop (response.json, the captured-signal
    // guard) and the Svelte DOM flush complete before asserting. A microtask-only
    // flush (await tick) under-drains and lets the negative assertion fire early.
    await staleResponse;
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(container.textContent).toContain(FRESH_SCIENTIFIC);
    expect(container.textContent).not.toContain(STALE_SCIENTIFIC);
  });
});

describe('DetectionDetail history tab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('does not request species history until the history tab is opened', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/v2/detections/')) {
        return Promise.resolve(
          jsonResponse(
            makeDetection({ id: 42, scientificName: 'Spinus tristis', date: '2026-07-26' })
          )
        );
      }
      return Promise.resolve(jsonResponse({ results: [], total: 0, data: [] }));
    });
    vi.stubGlobal('fetch', fetchMock);

    detailTest.render({ detectionId: '42' });

    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([u]) => String(u).includes('/api/v2/detections/42'))).toBe(
        true
      );
    });

    expect(fetchMock.mock.calls.some(([u]) => String(u).includes('/api/v2/search'))).toBe(false);
    expect(
      fetchMock.mock.calls.some(([u]) => String(u).includes('/api/v2/analytics/time/daily'))
    ).toBe(false);
  });

  it('requests species history when the history tab is activated', async () => {
    // Freeze "today" far from the fixture's detection date (2026-07-26). If the
    // window were ever anchored on the current date instead of the viewed
    // detection's date, the asserted start/end below would not match and this
    // test would fail — proving the anchoring, not just that a request happened.
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date('2020-01-01T00:00:00Z'));

    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/v2/detections/')) {
        return Promise.resolve(
          jsonResponse(
            makeDetection({ id: 42, scientificName: 'Spinus tristis', date: '2026-07-26' })
          )
        );
      }
      if (url.includes('/api/v2/search')) {
        return Promise.resolve(jsonResponse({ results: [], total: 0 }));
      }
      return Promise.resolve(jsonResponse({ data: [] }));
    });
    vi.stubGlobal('fetch', fetchMock);

    const { getByRole } = detailTest.render({ detectionId: '42' });
    await waitFor(() => getByRole('tab', { name: /history/i }));
    await fireEvent.click(getByRole('tab', { name: /history/i }));

    await waitFor(() => {
      expect(
        fetchMock.mock.calls.some(([u]) => String(u).includes('/api/v2/analytics/time/daily'))
      ).toBe(true);
    });

    const dailyCall = fetchMock.mock.calls.find(([u]) =>
      String(u).includes('/api/v2/analytics/time/daily')
    );
    const dailyUrl = String(dailyCall?.[0]);
    // Window is the 30 days ending on the detection's own date, not on today.
    expect(dailyUrl).toContain('start_date=2026-06-27');
    expect(dailyUrl).toContain('end_date=2026-07-26');
  });

  // Regression: the detail view is reused across detections — App.svelte renders
  // <DetectionDetail {detectionId} /> with no keyed block — so navigating A -> B
  // must not leave A's species history on screen when B cannot load one. B here
  // has a valid date but no scientific name, which the loader's guard rejects
  // before any request is made, so nothing overwrites the previous state. What
  // clears it is the detection effect's teardown calling speciesHistory.reset().
  it('drops the previous species history when navigating to a detection without a species', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/v2/detections/hist-a')) {
        return Promise.resolve(
          jsonResponse(
            makeDetection({ id: 42, scientificName: 'Spinus tristis', date: '2026-07-26' })
          )
        );
      }
      if (url.includes('/api/v2/detections/hist-b')) {
        return Promise.resolve(
          jsonResponse(
            makeDetection({
              id: 43,
              scientificName: '',
              commonName: HISTORY_NEXT_COMMON,
              date: '2026-07-26',
            })
          )
        );
      }
      if (url.includes('/api/v2/search')) {
        return Promise.resolve(jsonResponse({ results: [], total: HISTORY_STALE_TOTAL }));
      }
      return Promise.resolve(jsonResponse({ data: [] }));
    });
    vi.stubGlobal('fetch', fetchMock);

    const { container, getByRole, rerender } = detailTest.render({ detectionId: 'hist-a' });
    await waitFor(() => getByRole('tab', { name: /history/i }));
    await fireEvent.click(getByRole('tab', { name: /history/i }));

    // First detection's history is on screen.
    await waitFor(() => {
      expect(container.textContent).toContain(String(HISTORY_STALE_TOTAL));
    });

    // Navigate to a detection with no species. The history tab stays selected.
    await rerender({ detectionId: 'hist-b' });
    await waitFor(() => {
      expect(container.textContent).toContain(HISTORY_NEXT_COMMON);
    });

    expect(container.textContent).not.toContain(String(HISTORY_STALE_TOTAL));
  });
});
