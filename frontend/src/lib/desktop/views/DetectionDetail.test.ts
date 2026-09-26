import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { waitFor, cleanup } from '@testing-library/svelte';
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

describe('DetectionDetail rarity location coordinates', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  // Render the detail view with a species-rarity payload, stubbing the detection and the
  // species-info endpoint (which carries rarity) and ignoring everything else.
  function renderWithRarity(rarity: Record<string, unknown>) {
    const detection = makeDetection({
      id: 77,
      scientificName: 'Turdus merula',
      commonName: 'Blackbird',
    });
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('/api/v2/detections/77')) {
          return Promise.resolve(jsonResponse(detection));
        }
        // The species-info endpoint (not /species/taxonomy) supplies rarity.
        if (url.includes('/api/v2/species?scientific_name=')) {
          return Promise.resolve(jsonResponse({ scientific_name: 'Turdus merula', rarity }));
        }
        return Promise.resolve(jsonResponse({}));
      })
    );
    return detailTest.render({ detectionId: '77' });
  }

  function raritySection(container: HTMLElement): HTMLElement | null {
    return container.querySelector<HTMLElement>('section[aria-labelledby="rarity-heading"]');
  }

  // A station configured at exactly 0.0 latitude/longitude (equator / prime meridian). The
  // API now sends latitude:0 / longitude:0 (previously dropped by float64+omitempty), and
  // the component must render the based-on-location line without throwing on toFixed(0).
  it('renders the location line when coordinates are exactly 0', async () => {
    const { container } = renderWithRarity({
      status: 'rare',
      score: 0.08,
      location_based: true,
      latitude: 0,
      longitude: 0,
      date: '2024-06-15',
      threshold_applied: 0.05,
    });

    await waitFor(() => {
      expect(raritySection(container)).not.toBeNull();
    });

    // The based-on-location paragraph is the only <p> inside the rarity section.
    expect(raritySection(container)?.querySelectorAll('p')).toHaveLength(1);
  });

  // Defensive: if the API ever reports location_based while omitting the coordinates (a
  // contract violation), the != null guard must hide the line instead of crashing on
  // undefined.toFixed. The rarity status/score still render.
  it('omits the location line and does not crash when coordinates are missing', async () => {
    const { container } = renderWithRarity({
      status: 'unknown',
      score: 0,
      location_based: true,
      date: '2024-06-15',
      threshold_applied: 0.05,
    });

    await waitFor(() => {
      expect(raritySection(container)).not.toBeNull();
    });

    const section = raritySection(container);
    expect(section?.querySelector('.rarity-label')).not.toBeNull();
    expect(section?.querySelectorAll('p')).toHaveLength(0);
  });
});
