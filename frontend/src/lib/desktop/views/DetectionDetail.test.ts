import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { waitFor, cleanup, screen, fireEvent } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../test/render-helpers';
import DetectionDetail from './DetectionDetail.svelte';
import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
import type { Detection } from '$lib/types/detection.types';
import { appState } from '$lib/stores/appState.svelte';
import { auth } from '$lib/stores/auth';

// Heavy / context-dependent children are not relevant to the page's data flow.
vi.mock('$lib/desktop/components/media/AudioPlayer.svelte');
vi.mock('$lib/desktop/components/data/ConfidenceCircle.svelte');
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
function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: status === 200 ? 'OK' : 'Error',
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

/** One request the page made: method and path with query. */
interface Call {
  method: string;
  url: string;
}

/**
 * Stubs fetch with a router: `routes` maps a URL fragment to the answer (a body,
 * or a function of the call returning a promise of a Response). The first
 * matching fragment wins; anything else answers {}. Returns the call log.
 */
function stubApi(routes: [string, unknown][]): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ method: init?.method ?? 'GET', url });
      for (const [fragment, answer] of routes) {
        if (url.includes(fragment)) {
          if (typeof answer === 'function') {
            return (answer as (_call: Call) => Promise<Response>)({
              method: init?.method ?? 'GET',
              url,
            });
          }
          return Promise.resolve(jsonResponse(answer));
        }
      }
      return Promise.resolve(jsonResponse({}));
    })
  );
  return calls;
}

/** Lets pending promise callbacks and the DOM update after them run. */
async function settle() {
  await new Promise(resolve => setTimeout(resolve, 0));
  await new Promise(resolve => setTimeout(resolve, 0));
}

const count = (calls: Call[], fragment: string) =>
  calls.filter(call => call.url.includes(fragment)).length;

beforeEach(() => {
  vi.clearAllMocks();
  auth.setSecurity(false, true);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('DetectionDetail loading and errors', () => {
  it('shows one visible loading label in the skeleton until the detection arrives', async () => {
    let answer!: (_response: Response) => void;
    stubApi([
      [
        '/api/v2/detections/5',
        () =>
          new Promise<Response>(resolve => {
            answer = resolve;
          }),
      ],
    ]);

    const { container } = detailTest.render({ detectionId: '5' });

    await waitFor(() => {
      expect(screen.getByText('detections.aria.loading')).toBeInTheDocument();
    });
    expect(container.querySelectorAll('[data-skeleton]').length).toBeGreaterThan(0);

    answer(jsonResponse(makeDetection({ id: 5, commonName: 'Loaded bird' })));
    await waitFor(() => {
      expect(container.querySelector('#species-heading')?.textContent).toContain('Loaded bird');
    });
    expect(screen.queryByText('detections.aria.loading')).toBeNull();
    // Once every card's own request has answered, no placeholder is left.
    await settle();
    expect(container.querySelector('[data-skeleton]')).toBeNull();
  });

  it('announces a load error once, through a single alert', async () => {
    stubApi([['/api/v2/detections/6', () => Promise.resolve(jsonResponse({}, 404))]]);

    detailTest.render({ detectionId: '6' });

    await waitFor(() => {
      expect(screen.getAllByRole('alert')).toHaveLength(1);
    });
    expect(screen.getByRole('alert').textContent).toContain('detections.errors.notFound');
    expect(screen.queryByText('detections.aria.loading')).toBeNull();
  });

  // Regression: navigating from detection A to B while A's request is still in
  // flight must not let A's late response overwrite B. The fix captures the
  // AbortController signal locally and checks the captured signal (not the shared
  // controller reference, which by then points at B's non-aborted controller).
  it('does not let a stale detection response overwrite a newer one', async () => {
    let resolveStale!: (_response: Response) => void;
    const staleResponse = new Promise<Response>(resolve => {
      resolveStale = resolve;
    });
    stubApi([
      ['/api/v2/detections/det-a', () => staleResponse],
      [
        '/api/v2/detections/det-b',
        makeDetection({ id: 2, scientificName: FRESH_SCIENTIFIC, commonName: 'Fresh B' }),
      ],
    ]);

    const { container, rerender } = detailTest.render({ detectionId: 'det-a' });

    await rerender({ detectionId: 'det-b' });
    await waitFor(() => {
      expect(container.textContent).toContain(FRESH_SCIENTIFIC);
    });

    resolveStale(
      jsonResponse(
        makeDetection({ id: 1, scientificName: STALE_SCIENTIFIC, commonName: 'Stale A' })
      )
    );
    await staleResponse;
    await settle();

    expect(container.textContent).toContain(FRESH_SCIENTIFIC);
    expect(container.textContent).not.toContain(STALE_SCIENTIFIC);
  });
});

describe('DetectionDetail panels', () => {
  it('fills the panels from their responses', async () => {
    stubApi([
      [
        '/api/v2/detections/10/neighbors',
        { detectionId: 10, previous: 9, next: null, previousSameSpecies: 3, nextSameSpecies: null },
      ],
      ['/api/v2/detections/10/predictions', { detectionId: 10, modelPredictions: [] }],
      [
        '/api/v2/detections/10',
        {
          ...makeDetection({ id: 10, scientificName: 'Turdus merula', commonName: 'Blackbird' }),
          modelContributions: [{ modelName: 'BirdNET', modelVersion: '2.4', hitCount: 2 }],
        },
      ],
      [
        '/api/v2/species?scientific_name=',
        { rarity: { status: 'uncommon', score: 0.42, location_based: true } },
      ],
      [
        '/api/v2/weather/detection/10',
        { hourly: { temperature: 12, weather_desc: 'clear sky', humidity: 55 } },
      ],
    ]);

    const { container } = detailTest.render({ detectionId: '10' });

    await waitFor(() => {
      expect(container.querySelector('#species-heading')?.textContent).toContain('Blackbird');
    });
    await waitFor(() => {
      expect(screen.getByRole('meter').getAttribute('aria-valuenow')).toBe('42');
    });
    expect(container.querySelector('.dd-weather-temp')?.textContent).toContain('12');
    expect(container.textContent).toContain('BirdNET 2.4');
    // The older link names the neighbour; the newer one has nowhere to go and says so.
    const olderLink = container.querySelector<HTMLAnchorElement>('a[href$="/ui/detections/9"]');
    expect(olderLink).not.toBeNull();
    expect(screen.getByRole('link', { name: 'detections.detail.nav.noNewer' })).toHaveAttribute(
      'aria-disabled',
      'true'
    );
  });

  it('marks the species as set by a reviewer from the detection’s original species', async () => {
    stubApi([
      [
        '/api/v2/detections/11',
        makeDetection({
          id: 11,
          scientificName: 'Turdus philomelos',
          commonName: 'Song Thrush',
          originalSpecies: {
            scientificName: 'Turdus merula',
            commonName: 'Blackbird',
            speciesCode: 'eurbla',
          },
        }),
      ],
    ]);

    const { container } = detailTest.render({ detectionId: '11' });

    await waitFor(() => {
      expect(container.textContent).toContain('detections.detail.identity.setByReviewer');
    });
    expect(container.textContent).toContain('detections.detail.log.speciesChanged');
  });

  it('does not mark a species changed back to the model’s own pick', async () => {
    stubApi([
      [
        '/api/v2/detections/12',
        makeDetection({
          id: 12,
          scientificName: 'Turdus merula',
          commonName: 'Blackbird',
          originalSpecies: {
            scientificName: 'Turdus merula',
            commonName: 'Blackbird',
            speciesCode: 'eurbla',
          },
        }),
      ],
    ]);

    const { container } = detailTest.render({ detectionId: '12' });

    await waitFor(() => {
      expect(container.querySelector('#species-heading')?.textContent).toContain('Blackbird');
    });
    expect(container.textContent).not.toContain('detections.detail.identity.setByReviewer');
    expect(container.textContent).not.toContain('detections.detail.log.speciesChanged');
  });
});

describe('DetectionDetail stepping and refreshing', () => {
  // Regression: stepping to another detection used to cancel the species requests too,
  // while they are only made again when the species changes, so a step within one
  // species left the range score, taxonomy and statistics empty for good.
  it('keeps the species data loading across a step within the same species', async () => {
    let answerSpecies!: (_response: Response) => void;
    const speciesAnswer = new Promise<Response>(resolve => {
      answerSpecies = resolve;
    });
    const calls = stubApi([
      ['/api/v2/detections/21', makeDetection({ id: 21, scientificName: 'Turdus merula' })],
      ['/api/v2/detections/22', makeDetection({ id: 22, scientificName: 'Turdus merula' })],
      ['/api/v2/species?scientific_name=', () => speciesAnswer],
    ]);

    const { container, rerender } = detailTest.render({ detectionId: '21' });
    await waitFor(() => {
      expect(count(calls, '/api/v2/species?scientific_name=')).toBe(1);
    });

    await rerender({ detectionId: '22' });
    await waitFor(() => {
      expect(count(calls, '/api/v2/detections/22')).toBeGreaterThan(0);
    });
    await settle();

    answerSpecies(jsonResponse({ rarity: { status: 'rare', score: 0.05, location_based: true } }));
    await waitFor(() => {
      expect(container.querySelector('[role="meter"]')?.getAttribute('aria-valuenow')).toBe('5');
    });
    // The species was not asked for again: the first answer is the one shown.
    expect(count(calls, '/api/v2/species?scientific_name=')).toBe(1);
  });

  it('asks for the detection, its weather, neighbours and predictions at the same time', async () => {
    const calls = stubApi([['/api/v2/detections/30', () => new Promise<Response>(() => {})]]);

    detailTest.render({ detectionId: '30' });
    await settle();

    // The detection has not answered, yet the requests that need only its ID are out.
    expect(count(calls, '/api/v2/weather/detection/30')).toBe(1);
    expect(count(calls, '/api/v2/detections/30/neighbors')).toBe(1);
    expect(count(calls, '/api/v2/detections/30/predictions')).toBe(1);
    // Sun times need the detection's date.
    expect(count(calls, '/api/v2/weather/sun/')).toBe(0);
  });

  it('after a verdict, re-counts reviews but leaves weather, sun and lists alone', async () => {
    let verified: Detection['verified'] = 'unverified';
    const calls = stubApi([
      ['/api/v2/detections/40/review', {}],
      ['/api/v2/detections/40/', {}],
      [
        '/api/v2/detections/40',
        () => Promise.resolve(jsonResponse(makeDetection({ id: 40, verified }))),
      ],
      ['/api/v2/search', { total: 0 }],
    ]);

    detailTest.render({ detectionId: '40' });
    await waitFor(() => {
      expect(count(calls, '/api/v2/search')).toBe(2);
    });
    await settle();
    const before = {
      weather: count(calls, '/api/v2/weather/detection/'),
      sun: count(calls, '/api/v2/weather/sun/'),
      predictions: count(calls, '/predictions'),
      daily: count(calls, '/analytics/time/daily'),
    };

    verified = 'correct';
    await fireEvent.click(
      screen.getByRole('button', { name: /detections\.detail\.review\.correct/ })
    );

    await waitFor(() => {
      expect(count(calls, '/api/v2/search')).toBe(4);
    });
    await settle();
    expect(calls.some(call => call.method === 'POST' && call.url.includes('/40/review'))).toBe(
      true
    );
    expect(count(calls, '/api/v2/weather/detection/')).toBe(before.weather);
    expect(count(calls, '/api/v2/weather/sun/')).toBe(before.sun);
    expect(count(calls, '/predictions')).toBe(before.predictions);
    expect(count(calls, '/analytics/time/daily')).toBe(before.daily);
  });

  it('after a species change, reloads the lists and species data but not weather or sun', async () => {
    let scientificName = 'Turdus merula';
    const calls = stubApi([
      ['/api/v2/detections/50/review', {}],
      ['/api/v2/detections/50/neighbors', {}],
      ['/api/v2/detections/50/predictions', {}],
      [
        '/api/v2/detections/50',
        () => Promise.resolve(jsonResponse(makeDetection({ id: 50, scientificName }))),
      ],
      ['/api/v2/search', { total: 0 }],
    ]);

    detailTest.render({ detectionId: '50' });
    await waitFor(() => {
      expect(count(calls, '/api/v2/species?scientific_name=Turdus%20merula')).toBe(1);
    });
    await settle();
    const before = {
      weather: count(calls, '/api/v2/weather/detection/'),
      sun: count(calls, '/api/v2/weather/sun/'),
      predictions: count(calls, '/predictions'),
      neighbors: count(calls, '/neighbors'),
      daily: count(calls, '/analytics/time/daily'),
    };

    // Any change that reloads the detection will do; the reloaded detection names another species.
    scientificName = 'Turdus philomelos';
    await fireEvent.click(
      screen.getByRole('button', { name: /detections\.detail\.review\.correct/ })
    );

    await waitFor(() => {
      expect(count(calls, '/api/v2/species?scientific_name=Turdus%20philomelos')).toBe(1);
    });
    await settle();
    expect(count(calls, '/predictions')).toBe(before.predictions + 1);
    expect(count(calls, '/neighbors')).toBe(before.neighbors + 1);
    expect(count(calls, '/analytics/time/daily')).toBe(before.daily + 1);
    expect(count(calls, '/api/v2/weather/detection/')).toBe(before.weather);
    expect(count(calls, '/api/v2/weather/sun/')).toBe(before.sun);
  });
});

describe('DetectionDetail daily counts', () => {
  // Regression: after a step the stats card kept drawing the previous detection's daily
  // counts, under the new species' name, until the new counts answered.
  it('does not show the previous detection\u2019s daily counts while the new ones load', async () => {
    const calls = stubApi([
      ['/api/v2/detections/96', makeDetection({ id: 96, scientificName: 'Turdus merula' })],
      [
        '/api/v2/detections/97',
        makeDetection({ id: 97, scientificName: 'Erithacus rubecula', date: '2024-03-01' }),
      ],
      [
        'species=Turdus%20merula',
        (call: Call) =>
          call.url.includes('/analytics/time/daily')
            ? Promise.resolve(jsonResponse({ data: [{ date: '2024-01-01', count: 7 }] }))
            : Promise.resolve(jsonResponse({})),
      ],
      [
        'species=Erithacus%20rubecula',
        (call: Call) =>
          call.url.includes('/analytics/time/daily')
            ? new Promise<Response>(() => {})
            : Promise.resolve(jsonResponse({})),
      ],
    ]);

    const { container, rerender } = detailTest.render({ detectionId: '96' });
    await waitFor(() => {
      expect(container.querySelectorAll('.dd-daily-bar').length).toBeGreaterThan(0);
    });

    await rerender({ detectionId: '97' });
    await waitFor(() => {
      expect(count(calls, 'species=Erithacus%20rubecula')).toBeGreaterThan(0);
    });
    await settle();

    expect(container.querySelector('.dd-daily-bar')).toBeNull();
    expect(container.querySelector('.dd-daily-bars [data-skeleton]')).not.toBeNull();
  });
});

describe('DetectionDetail recording', () => {
  it('downloads the saved recording from the ID-based endpoint', async () => {
    stubApi([
      [
        '/api/v2/detections/1239',
        makeDetection({ id: 1239, clipName: 'phalaenoptilus_nuttallii_88p.m4a' }),
      ],
    ]);

    const { container } = detailTest.render({ detectionId: '1239' });

    await waitFor(() => {
      expect(container.querySelector('a.dd-rec-download')).not.toBeNull();
    });
    const downloadLink = container.querySelector<HTMLAnchorElement>('a.dd-rec-download');
    expect(downloadLink?.getAttribute('href')).toBe('/api/v2/audio/1239');
    // Keep the attribute valueless so the response's Content-Disposition header
    // supplies the canonical filename and extension.
    expect(downloadLink).toHaveAttribute('download', '');
  });

  it('downloads the denoised clip when a playback denoise default is set', async () => {
    stubApi([['/api/v2/detections/1240', makeDetection({ id: 1240, clipName: 'clip.m4a' })]]);
    appState.playbackDenoise = 'light-medium';

    try {
      const { container } = detailTest.render({ detectionId: '1240' });

      await waitFor(() => {
        expect(container.querySelector('a.dd-rec-download')).not.toBeNull();
      });
      expect(container.querySelector('a.dd-rec-download')?.getAttribute('href')).toBe(
        '/api/v2/audio/1240?denoise=light-medium'
      );
    } finally {
      appState.playbackDenoise = '';
    }
  });

  it('passes the clip sample rate and the denoised spectrogram to the player', async () => {
    stubApi([
      [
        '/api/v2/detections/1242',
        makeDetection({
          id: 1242,
          clipName: 'bat_1242.wav',
          modelType: 'bat',
          clipSampleRate: 384_000,
        }),
      ],
    ]);

    detailTest.render({ detectionId: '1242' });

    await waitFor(() => {
      expect(vi.mocked(AudioPlayer)).toHaveBeenCalled();
    });
    // A Svelte 5 component is called as (internals, props); the legacy typings
    // describe a one-argument constructor, so read the call as a plain list.
    const lastCall: unknown[] | undefined = vi.mocked(AudioPlayer).mock.calls.at(-1);
    expect(lastCall?.[1]).toMatchObject({
      modelType: 'bat',
      clipSampleRate: 384_000,
      spectrogramFollowsDenoise: true,
    });
  });

  it('shows the kept spectrogram image without a player or download link', async () => {
    stubApi([
      ['/api/v2/detections/1241', makeDetection({ id: 1241, clipName: '', spectrogramOnly: true })],
    ]);

    const { container } = detailTest.render({ detectionId: '1241' });

    await waitFor(() => {
      expect(container.querySelector('img.spectrogram-img')).not.toBeNull();
    });
    expect(container.querySelector('img.spectrogram-img')?.getAttribute('src')).toContain(
      '/api/v2/spectrogram/1241?size=lg&raw=false'
    );
    expect(container.querySelector('a.dd-rec-download')).toBeNull();
    expect(vi.mocked(AudioPlayer)).not.toHaveBeenCalled();
  });
});
