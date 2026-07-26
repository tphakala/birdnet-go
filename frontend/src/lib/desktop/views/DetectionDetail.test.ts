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

describe('DetectionDetail audio download', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('uses the ID-based endpoint when downloading the original audio', async () => {
    const detection = makeDetection({
      id: 1239,
      scientificName: 'Phalaenoptilus nuttallii',
      commonName: 'Common Poorwill',
      clipName: 'phalaenoptilus_nuttallii_88p_20260720T051601Z.m4a',
    });

    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL) => {
        if (String(input).includes('/api/v2/detections/1239')) {
          return Promise.resolve(jsonResponse(detection));
        }
        return Promise.resolve(jsonResponse({}));
      })
    );

    const { container } = detailTest.render({ detectionId: '1239' });

    await waitFor(() => {
      expect(container.querySelector('a.meta-download')).not.toBeNull();
    });

    const downloadLink = container.querySelector<HTMLAnchorElement>('a.meta-download');
    expect(downloadLink?.getAttribute('href')).toBe('/api/v2/audio/1239');
    // Keep the attribute valueless so the response's Content-Disposition header
    // supplies the canonical filename and extension.
    expect(downloadLink).toHaveAttribute('download', '');
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
});
