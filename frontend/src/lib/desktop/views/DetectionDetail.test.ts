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

/** A fetch Response carrying a JSON body. */
function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

/** One request the page made: method and path with query. */
interface Call {
  method: string;
  url: string;
}

/** A route answer that builds its own Response from the call. */
function isResponder(value: unknown): value is (_call: Call) => Promise<Response> {
  return typeof value === 'function';
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
          if (isResponder(answer)) {
            return answer({ method: init?.method ?? 'GET', url });
          }
          return Promise.resolve(jsonResponse(answer));
        }
      }
      return Promise.resolve(jsonResponse({}));
    })
  );
  return calls;
}

/** The neighbours answer of a detection on the current database format. */
function neighborsOf(id: number) {
  return {
    detectionId: id,
    previous: id - 1,
    next: id + 1,
    previousSameSpecies: null,
    nextSameSpecies: null,
  };
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
    let answer: ((_response: Response) => void) | undefined;
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

    answer?.(jsonResponse(makeDetection({ id: 5, commonName: 'Loaded bird' })));
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
    let resolveStale: ((_response: Response) => void) | undefined;
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

    resolveStale?.(
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
        {
          hourly: {
            time: '2024-01-01T10:00:00Z',
            temperature: 12,
            weather_desc: 'clear sky',
            humidity: 55,
          },
        },
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
    let answerSpecies: ((_response: Response) => void) | undefined;
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

    answerSpecies?.(
      jsonResponse({ rarity: { status: 'rare', score: 0.05, location_based: true } })
    );
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

describe('DetectionDetail saves that finish around a step', () => {
  // Regression: a save on one detection that finished after the user had stepped to the
  // next one refreshed the new detection in place of its own load, which cancelled that
  // load, so the new detection never got its sun times.
  it('lets the step load the new detection when a save on the old one finishes during it', async () => {
    let answerReview: ((_response: Response) => void) | undefined;
    let answerNext: ((_response: Response) => void) | undefined;
    const calls = stubApi([
      [
        '/api/v2/detections/60/review',
        () =>
          new Promise<Response>(resolve => {
            answerReview = resolve;
          }),
      ],
      ['/api/v2/detections/60', makeDetection({ id: 60, date: '2024-02-01' })],
      [
        '/api/v2/detections/61',
        () =>
          new Promise<Response>(resolve => {
            answerNext = resolve;
          }),
      ],
      ['/api/v2/search', { total: 0 }],
    ]);

    const { rerender } = detailTest.render({ detectionId: '60' });
    await waitFor(() => {
      expect(count(calls, '/api/v2/weather/sun/2024-02-01')).toBe(1);
    });
    await fireEvent.click(
      screen.getByRole('button', { name: /detections\.detail\.review\.correct/ })
    );
    await rerender({ detectionId: '61' });
    await settle();

    answerReview?.(jsonResponse({}));
    await settle();
    answerNext?.(jsonResponse(makeDetection({ id: 61, date: '2024-02-02' })));

    await waitFor(() => {
      expect(count(calls, '/api/v2/weather/sun/2024-02-02')).toBe(1);
    });
  });

  // A save that answers after the user stepped to a detection of the same species still
  // changes that species' review counts, so the step recounts them.
  it('recounts the reviews when a save on the old detection finishes during a step', async () => {
    let answerReview: ((_response: Response) => void) | undefined;
    let answerNext: ((_response: Response) => void) | undefined;
    const calls = stubApi([
      [
        '/api/v2/detections/62/review',
        () =>
          new Promise<Response>(resolve => {
            answerReview = resolve;
          }),
      ],
      ['/api/v2/detections/62', makeDetection({ id: 62 })],
      [
        '/api/v2/detections/63',
        (call: Call) =>
          call.url.endsWith('/api/v2/detections/63')
            ? new Promise<Response>(resolve => {
                answerNext = resolve;
              })
            : Promise.resolve(jsonResponse({})),
      ],
      ['/api/v2/search', { total: 0 }],
    ]);

    const { rerender } = detailTest.render({ detectionId: '62' });
    await waitFor(() => {
      expect(count(calls, '/api/v2/search')).toBe(2);
    });
    await fireEvent.click(
      screen.getByRole('button', { name: /detections\.detail\.review\.correct/ })
    );
    await rerender({ detectionId: '63' });
    await settle();
    answerReview?.(jsonResponse({}));
    await settle();
    answerNext?.(jsonResponse(makeDetection({ id: 63 })));

    await waitFor(() => {
      expect(count(calls, '/api/v2/search')).toBe(4);
    });
  });

  it('recounts the reviews, without reloading the new detection, when a save finishes after the step', async () => {
    let answerReview: ((_response: Response) => void) | undefined;
    const calls = stubApi([
      [
        '/api/v2/detections/64/review',
        () =>
          new Promise<Response>(resolve => {
            answerReview = resolve;
          }),
      ],
      ['/api/v2/detections/64', makeDetection({ id: 64 })],
      ['/api/v2/detections/65', makeDetection({ id: 65 })],
      ['/api/v2/search', { total: 0 }],
    ]);

    const { rerender } = detailTest.render({ detectionId: '64' });
    await waitFor(() => {
      expect(count(calls, '/api/v2/search')).toBe(2);
    });
    await fireEvent.click(
      screen.getByRole('button', { name: /detections\.detail\.review\.correct/ })
    );
    await rerender({ detectionId: '65' });
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /detections\.detail\.review\.correct/ })
      ).not.toBeNull();
    });
    await settle();

    answerReview?.(jsonResponse({}));
    await waitFor(() => {
      expect(count(calls, '/api/v2/search')).toBe(4);
    });
    expect(calls.filter(call => call.url.endsWith('/api/v2/detections/65')).length).toBe(1);
  });

  // Regression: a step that cancelled the refresh after a verdict also lost the recount of
  // the species' reviews, which a step within one species does not make again.
  it('recounts the reviews when a step cancels the refresh after a verdict', async () => {
    let answerRefresh: ((_response: Response) => void) | undefined;
    let loads = 0;
    const calls = stubApi([
      ['/api/v2/detections/70/review', {}],
      [
        '/api/v2/detections/70',
        (call: Call) => {
          if (!call.url.endsWith('/api/v2/detections/70')) return Promise.resolve(jsonResponse({}));
          loads += 1;
          if (loads === 1) return Promise.resolve(jsonResponse(makeDetection({ id: 70 })));
          return new Promise<Response>(resolve => {
            answerRefresh = resolve;
          });
        },
      ],
      ['/api/v2/detections/71', makeDetection({ id: 71 })],
      ['/api/v2/search', { total: 0 }],
    ]);

    const { rerender } = detailTest.render({ detectionId: '70' });
    await waitFor(() => {
      expect(count(calls, '/api/v2/search')).toBe(2);
    });
    await fireEvent.click(
      screen.getByRole('button', { name: /detections\.detail\.review\.correct/ })
    );
    await waitFor(() => {
      expect(loads).toBe(2);
    });

    await rerender({ detectionId: '71' });
    await waitFor(() => {
      expect(count(calls, '/api/v2/search')).toBe(4);
    });
    answerRefresh?.(jsonResponse(makeDetection({ id: 70, verified: 'correct' })));
  });
});

describe('DetectionDetail panels per detection', () => {
  // Regression: the note box kept its draft when the user stepped to the next detection,
  // so saving it attached the note to a detection it was not written for.
  it('starts the next detection with an empty note box', async () => {
    stubApi([
      ['/api/v2/detections/90', makeDetection({ id: 90 })],
      ['/api/v2/detections/91', makeDetection({ id: 91 })],
    ]);

    const { rerender } = detailTest.render({ detectionId: '90' });
    const box = await screen.findByLabelText<HTMLTextAreaElement>('detections.detail.log.addNote');
    await fireEvent.input(box, { target: { value: 'Heard a second call' } });
    expect(box.value).toBe('Heard a second call');

    await rerender({ detectionId: '91' });
    await waitFor(() => {
      expect(
        screen.getByLabelText<HTMLTextAreaElement>('detections.detail.log.addNote').value
      ).toBe('');
    });
  });
});

describe('DetectionDetail neighbours', () => {
  // Regression: one failed neighbours request removed the step links for the rest of the
  // view, and with them the link the user had just stepped with.
  it('keeps the step links, blocked and saying why, when the neighbours request fails', async () => {
    stubApi([
      ['/api/v2/detections/95/neighbors', () => Promise.resolve(jsonResponse({}, 500))],
      ['/api/v2/detections/95', makeDetection({ id: 95 })],
    ]);

    const { container } = detailTest.render({ detectionId: '95' });

    await waitFor(() => {
      expect(container.querySelectorAll('.dd-nav-step[aria-disabled="true"]')).toHaveLength(4);
    });
    // The reason is in each link's name (DetailNavBar's own test checks the names differ).
    for (const link of container.querySelectorAll('.dd-nav-step')) {
      expect(link.getAttribute('aria-label')).toContain('detections.detail.nav.stepFailed');
    }
    expect(container.querySelector('.dd-nav-groups')).toHaveAttribute('aria-busy', 'false');
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

describe('DetectionDetail species dialog', () => {
  // Regression: the dialog stayed open across a change of detection with the earlier pick,
  // and Confirm then changed the species of a detection it was not opened for.
  it('closes when another detection is shown', async () => {
    stubApi([
      ['/neighbors', neighborsOf(98)],
      ['/api/v2/detections/98', makeDetection({ id: 98 })],
      ['/api/v2/detections/99', makeDetection({ id: 99 })],
    ]);
    // The Modal stays in the document and shows its open state with the visible class.
    const dialog = () =>
      screen.getByRole('dialog', { name: 'detections.detail.species.dialogTitle', hidden: true });

    const { rerender } = detailTest.render({ detectionId: '98' });
    await fireEvent.click(
      await screen.findByRole('button', { name: /detections\.detail\.review\.wrongSpecies/ })
    );
    await waitFor(() => {
      expect(dialog()).toHaveClass('visible');
    });

    await rerender({ detectionId: '99' });
    await waitFor(() => {
      expect(dialog()).not.toHaveClass('visible');
    });
  });
});

describe('DetectionDetail review counts', () => {
  // Regression: the counts asked for after a verdict shared the species request's signal
  // with the first counts, so a slow first answer arriving last put the old counts back.
  it('keeps the counts asked for after a verdict when the first counts answer later', async () => {
    const firstAnswers: ((_response: Response) => void)[] = [];
    let searches = 0;
    let verified: Detection['verified'] = 'unverified';
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.includes('/api/v2/search')) {
          searches += 1;
          if (searches <= 2) {
            return new Promise<Response>(resolve => {
              firstAnswers.push(resolve);
            });
          }
          const correct = String(init?.body).includes('"correct"');
          return Promise.resolve(jsonResponse({ total: correct ? 5 : 0 }));
        }
        if (url.endsWith('/api/v2/detections/80')) {
          return Promise.resolve(jsonResponse(makeDetection({ id: 80, verified })));
        }
        return Promise.resolve(jsonResponse({}));
      })
    );

    const { container } = detailTest.render({ detectionId: '80' });
    await waitFor(() => {
      expect(searches).toBe(2);
    });
    verified = 'correct';
    await fireEvent.click(
      screen.getByRole('button', { name: /detections\.detail\.review\.correct/ })
    );
    await waitFor(() => {
      expect(container.querySelector<HTMLElement>('.dd-reviews-correct')?.style.width).toBe('100%');
    });

    // The first counts (no reviews correct, one false positive) answer last.
    firstAnswers[0]?.(jsonResponse({ total: 0 }));
    firstAnswers[1]?.(jsonResponse({ total: 1 }));
    await settle();

    expect(container.querySelector<HTMLElement>('.dd-reviews-correct')?.style.width).toBe('100%');
  });
});

describe('DetectionDetail loading paths', () => {
  it('does not recount the reviews after a note, which leaves the verdict as it was', async () => {
    const calls = stubApi([
      ['/api/v2/detections/110/review', {}],
      ['/api/v2/detections/110', makeDetection({ id: 110 })],
      ['/api/v2/search', { total: 0 }],
    ]);

    detailTest.render({ detectionId: '110' });
    await waitFor(() => {
      expect(count(calls, '/api/v2/search')).toBe(2);
    });
    const box = await screen.findByLabelText<HTMLTextAreaElement>('detections.detail.log.addNote');
    await fireEvent.input(box, { target: { value: 'A note' } });
    await fireEvent.click(screen.getByRole('button', { name: 'detections.detail.log.saveNote' }));
    await waitFor(() => {
      expect(calls.filter(call => call.url.endsWith('/api/v2/detections/110')).length).toBe(2);
    });
    await settle();

    expect(count(calls, '/api/v2/search')).toBe(2);
  });

  it('drops a late answer for the species the page has left', async () => {
    let answerOld: ((_response: Response) => void) | undefined;
    stubApi([
      ['/api/v2/detections/111', makeDetection({ id: 111, scientificName: 'Turdus merula' })],
      ['/api/v2/detections/112', makeDetection({ id: 112, scientificName: 'Erithacus rubecula' })],
      [
        'species?scientific_name=Turdus%20merula',
        () =>
          new Promise<Response>(resolve => {
            answerOld = resolve;
          }),
      ],
      [
        'species?scientific_name=Erithacus%20rubecula',
        { rarity: { status: 'rare', score: 0.05, location_based: true } },
      ],
    ]);

    const { container, rerender } = detailTest.render({ detectionId: '111' });
    await waitFor(() => {
      expect(answerOld).toBeDefined();
    });
    await rerender({ detectionId: '112' });
    await waitFor(() => {
      expect(container.querySelector('[role="meter"]')?.getAttribute('aria-valuenow')).toBe('5');
    });

    answerOld?.(jsonResponse({ rarity: { status: 'common', score: 0.9, location_based: true } }));
    await settle();

    expect(container.querySelector('[role="meter"]')?.getAttribute('aria-valuenow')).toBe('5');
  });

  it('makes the previous detection inert, and leaves the step links usable, while the next loads', async () => {
    stubApi([
      ['/api/v2/detections/113', makeDetection({ id: 113 })],
      [
        '/api/v2/detections/114',
        (call: Call) =>
          call.url.endsWith('/api/v2/detections/114')
            ? new Promise<Response>(() => {})
            : Promise.resolve(jsonResponse({})),
      ],
    ]);

    const { container, rerender } = detailTest.render({ detectionId: '113' });
    await screen.findByRole('button', { name: /detections\.detail\.review\.correct/ });
    await rerender({ detectionId: '114' });
    await settle();

    const grids = [...container.querySelectorAll<HTMLElement>('.dd-grid')];
    expect(grids.length).toBe(2);
    expect(grids.every(grid => grid.inert)).toBe(true);
    // Svelte sets inert as a property, which jsdom does not reflect as an attribute, so
    // the nav's ancestors are checked one by one.
    const inertAncestors: HTMLElement[] = [];
    for (let node = container.querySelector('nav'); node; node = node.parentElement) {
      if (node.inert) inertAncestors.push(node);
    }
    expect(container.querySelector('nav')).not.toBeNull();
    expect(inertAncestors).toEqual([]);
    // The links the user stepped with are still there, so their focus stays.
    expect(container.querySelectorAll('.dd-nav-step')).toHaveLength(4);
  });

  it('keeps the detection on screen, with no error, when a refresh fails', async () => {
    let loads = 0;
    stubApi([
      ['/api/v2/detections/115/review', {}],
      [
        '/api/v2/detections/115',
        (call: Call) => {
          if (!call.url.endsWith('/api/v2/detections/115'))
            return Promise.resolve(jsonResponse({}));
          loads += 1;
          return Promise.resolve(
            loads === 1
              ? jsonResponse(makeDetection({ id: 115, commonName: 'Kept bird' }))
              : jsonResponse({}, 500)
          );
        },
      ],
    ]);

    const { container } = detailTest.render({ detectionId: '115' });
    await fireEvent.click(
      await screen.findByRole('button', { name: /detections\.detail\.review\.correct/ })
    );
    await waitFor(() => {
      expect(loads).toBe(2);
    });
    await settle();

    expect(container.querySelector('#species-heading')?.textContent).toContain('Kept bird');
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('leaves no placeholder once every request has failed', async () => {
    stubApi([
      ['/api/v2/detections/116/', () => Promise.resolve(jsonResponse({}, 500))],
      ['/api/v2/detections/116', makeDetection({ id: 116 })],
      ['/api/v2/', () => Promise.resolve(jsonResponse({}, 404))],
    ]);

    const { container } = detailTest.render({ detectionId: '116' });
    await screen.findByRole('button', { name: /detections\.detail\.review\.correct/ });
    await waitFor(() => {
      expect(container.querySelector('[data-skeleton]')).toBeNull();
    });
  });

  it('keeps loaded species data on screen through a step within the species', async () => {
    const calls = stubApi([
      ['/api/v2/detections/117', makeDetection({ id: 117, scientificName: 'Turdus merula' })],
      ['/api/v2/detections/118', makeDetection({ id: 118, scientificName: 'Turdus merula' })],
      [
        'species?scientific_name=',
        { rarity: { status: 'rare', score: 0.05, location_based: true } },
      ],
    ]);

    const { container, rerender } = detailTest.render({ detectionId: '117' });
    await waitFor(() => {
      expect(container.querySelector('[role="meter"]')?.getAttribute('aria-valuenow')).toBe('5');
    });
    await rerender({ detectionId: '118' });
    await waitFor(() => {
      expect(count(calls, '/api/v2/detections/118')).toBeGreaterThan(0);
    });
    await settle();

    expect(container.querySelector('[role="meter"]')?.getAttribute('aria-valuenow')).toBe('5');
    expect(count(calls, 'species?scientific_name=')).toBe(1);
  });
});

describe('DetectionDetail stepping back to a detection with a save out', () => {
  // Regression: the step's request for the saved detection may have been sent before the save
  // went through; refreshing in place would then leave its old verdict on screen for good.
  it('starts the step again when the save finishes for the detection being stepped to', async () => {
    let answerReview: ((_response: Response) => void) | undefined;
    let loads = 0;
    const calls = stubApi([
      [
        '/api/v2/detections/60/review',
        () =>
          new Promise<Response>(resolve => {
            answerReview = resolve;
          }),
      ],
      [
        '/api/v2/detections/60',
        (call: Call) => {
          if (!call.url.endsWith('/api/v2/detections/60')) return Promise.resolve(jsonResponse({}));
          loads += 1;
          // The step back (second load) never answers on its own: it was sent before the save.
          if (loads === 2) return new Promise<Response>(() => {});
          return Promise.resolve(
            jsonResponse(
              makeDetection({ id: 60, verified: loads === 1 ? 'unverified' : 'correct' })
            )
          );
        },
      ],
      ['/api/v2/detections/61', makeDetection({ id: 61, date: '2024-03-03' })],
      ['/api/v2/search', { total: 0 }],
    ]);

    const { rerender } = detailTest.render({ detectionId: '60' });
    await fireEvent.click(
      await screen.findByRole('button', { name: /detections\.detail\.review\.correct/ })
    );
    await rerender({ detectionId: '61' });
    await waitFor(() => expect(count(calls, '/api/v2/detections/61')).toBeGreaterThan(0));
    await settle();
    await rerender({ detectionId: '60' });
    await waitFor(() => expect(loads).toBe(2));

    answerReview?.(jsonResponse({}));

    await waitFor(() => expect(loads).toBe(3));
    // The step's own loads ran again: the detection's sun times were asked for once at
    // the first load and once more by the restarted step, not skipped by an in-place reload.
    await waitFor(() => expect(count(calls, '/api/v2/weather/sun/2024-01-01')).toBe(2));
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /detections\.detail\.review\.correct/ })
      ).toHaveAttribute('aria-pressed', 'true');
    });
  });
});

describe('DetectionDetail conditions after a step', () => {
  // The previous detection's weather and sun must not read as the new detection's.
  it('shows placeholders, not the previous weather, until the new answers arrive', async () => {
    stubApi([
      [
        '/api/v2/weather/detection/80',
        { hourly: { time: '2024-01-01T10:00:00Z', temperature: 11, weather_icon: '01' } },
      ],
      ['/api/v2/weather/detection/81', () => new Promise<Response>(() => {})],
      ['/api/v2/detections/80', makeDetection({ id: 80 })],
      ['/api/v2/detections/81', makeDetection({ id: 81 })],
    ]);

    const { container, rerender } = detailTest.render({ detectionId: '80' });
    await waitFor(() => {
      expect(container.querySelector('.dd-weather-temp')?.textContent).toContain('11');
    });

    await rerender({ detectionId: '81' });
    await waitFor(() => {
      expect(container.querySelector('#species-heading')).not.toBeNull();
      expect(container.querySelector('.dd-grid-top')).not.toHaveClass('dd-switching');
    });

    const temperature = container.querySelector('.dd-weather-temp');
    expect(temperature?.textContent).not.toContain('11');
    expect(temperature?.querySelector('[data-skeleton]')).not.toBeNull();
  });
});

describe('DetectionDetail answers of the wrong shape', () => {
  it('treats a detection body that is not an object as a failed load', async () => {
    stubApi([['/api/v2/detections/90', []]]);

    detailTest.render({ detectionId: '90' });

    expect(await screen.findByRole('alert')).toHaveTextContent('detections.errors.loadFailed');
  });

  it('reads weather, neighbours and daily bodies of the wrong shape as no answer', async () => {
    stubApi([
      ['/api/v2/detections/91/neighbors', null],
      ['/api/v2/weather/detection/91', { hourly: 'warm' }],
      ['/api/v2/analytics/time/daily', [1, 2]],
      ['/api/v2/detections/91', makeDetection({ id: 91 })],
    ]);

    const { container } = detailTest.render({ detectionId: '91' });

    await screen.findByRole('button', { name: /detections\.detail\.review\.correct/ });
    await waitFor(() => {
      expect(container.querySelector('[data-skeleton]')).toBeNull();
    });
    expect(screen.queryByRole('alert')).toBeNull();
  });
});

describe('DetectionDetail species change before the database format is known', () => {
  it('offers the change only once the neighbours route has answered', async () => {
    let answerNeighbors: ((_response: Response) => void) | undefined;
    stubApi([
      [
        '/neighbors',
        () =>
          new Promise<Response>(resolve => {
            answerNeighbors = resolve;
          }),
      ],
      ['/api/v2/detections/92', makeDetection({ id: 92 })],
    ]);

    detailTest.render({ detectionId: '92' });
    const wrongSpecies = await screen.findByRole('button', {
      name: /detections\.detail\.review\.wrongSpecies/,
    });
    expect(wrongSpecies).toHaveAttribute('aria-disabled', 'true');
    const reason = document.getElementById(wrongSpecies.getAttribute('aria-describedby') ?? '');
    expect(reason?.textContent).toContain('detections.detail.review.databaseChecking');

    answerNeighbors?.(jsonResponse(neighborsOf(92)));
    await waitFor(() => {
      expect(wrongSpecies).not.toHaveAttribute('aria-disabled');
    });
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
