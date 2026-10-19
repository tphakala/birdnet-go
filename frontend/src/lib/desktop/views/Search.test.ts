import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { clearClipSampleRateCache } from '$lib/utils/clipSampleRateLookup';
import Search from './Search.svelte';

const SEARCH_ENDPOINT = '/api/v2/search';
const DETECTION_ENDPOINT = '/api/v2/detections/';
const SPECTROGRAM_ENDPOINT = '/api/v2/spectrogram/';
// Rate the stubbed detail endpoint resolves, and the rate one search row carries.
const RESOLVED_RATE = 384_000;
const ROW_RATE = 192_000;
const srParam = (rate: number) => `sr=${rate}`;
const SPECTROGRAM_ALT = 'components.audio.spectrogramAlt';
const OVERLAY_PLAY_SELECTOR = 'button.btn-primary.btn-sm';
const BAT_ROW_ID = '77';
const RATED_ROW_ID = '78';
// Bound for each waitFor: it polls, and the mocked responses resolve at once.
const WAIT_TIMEOUT_MS = 5000;
// Per-test budget, above the sum of the waits in the longest case (six), so a
// slow run fails on the waitFor that stalled rather than on the test timeout.
const TEST_TIMEOUT_MS = 40_000;

/** Minimal fetch Response stub carrying a JSON body. */
function jsonResponse(body: unknown): Response {
  // A partial Response: the code under test reads only these members.
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: new Headers({ 'content-type': 'application/json' }),
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response;
}

// An older bat detection: search responses never carry its clip sample rate.
const batRow = {
  id: BAT_ROW_ID,
  timestamp: '2026-10-01 22:15:00',
  timeOfDay: 'night',
  commonName: 'Common Pipistrelle',
  scientificName: 'Pipistrellus pipistrellus',
  confidence: 0.91,
  verified: 'unverified',
  locked: false,
  hasAudio: true,
  modelType: 'bat',
};

const ratedRow = {
  ...batRow,
  id: RATED_ROW_ID,
  commonName: "Daubenton's Bat",
  scientificName: 'Myotis daubentonii',
  clipSampleRate: ROW_RATE,
};

/** Stubs fetch: the search answers with `rows`, the detail endpoint with RESOLVED_RATE. */
function stubFetch(rows: unknown[]) {
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes(SEARCH_ENDPOINT)) {
      return Promise.resolve(jsonResponse({ results: rows, total: rows.length, pages: 1 }));
    }
    if (url.includes(DETECTION_ENDPOINT)) {
      const id = Number(url.split(DETECTION_ENDPOINT)[1]);
      return Promise.resolve(jsonResponse({ id, clipSampleRate: RESOLVED_RATE }));
    }
    return Promise.resolve(jsonResponse({ data: [] }));
  });
  vi.stubGlobal('fetch', fetchMock);
  const detailCalls = () =>
    fetchMock.mock.calls.map(call => String(call[0])).filter(u => u.includes(DETECTION_ENDPOINT));
  return { detailCalls };
}

async function submitSearch(container: HTMLElement) {
  const submit = container.querySelector<HTMLButtonElement>('button[type="submit"]');
  expect(submit).not.toBeNull();
  if (submit) await fireEvent.click(submit);
}

async function waitForElement<T extends Element>(find: () => T | null): Promise<T> {
  return waitFor(
    () => {
      const el = find();
      if (!el) throw new Error('element not rendered yet');
      return el;
    },
    { timeout: WAIT_TIMEOUT_MS }
  );
}

async function expectSpectrogramSrc(fragment: string) {
  await waitFor(
    () => {
      expect(screen.getByAltText(SPECTROGRAM_ALT).getAttribute('src')).toContain(fragment);
    },
    { timeout: WAIT_TIMEOUT_MS }
  );
}

describe('Search bat clip sample rate', () => {
  beforeEach(() => {
    // The rate lookup is shared across players: start every test without it.
    clearClipSampleRateCache();
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it(
    'resolves the rate once when an old bat row is expanded, never while it is only listed',
    async () => {
      const { detailCalls } = stubFetch([batRow]);

      const { container } = render(Search);
      await submitSearch(container);

      const expand = await waitForElement(() =>
        container.querySelector<HTMLButtonElement>(
          `button[aria-controls="expanded-row-${BAT_ROW_ID}"]`
        )
      );
      expect(detailCalls()).toHaveLength(0);

      await fireEvent.click(expand);

      await expectSpectrogramSrc(srParam(RESOLVED_RATE));
      expect(detailCalls()).toEqual([
        expect.stringContaining(`${DETECTION_ENDPOINT}${BAT_ROW_ID}`),
      ]);
    },
    TEST_TIMEOUT_MS
  );

  it(
    'passes a known rate from the search row to the player without a detail request',
    async () => {
      const { detailCalls } = stubFetch([{ ...batRow, clipSampleRate: ROW_RATE }]);

      const { container } = render(Search);
      await submitSearch(container);
      const expand = await waitForElement(() =>
        container.querySelector<HTMLButtonElement>(
          `button[aria-controls="expanded-row-${BAT_ROW_ID}"]`
        )
      );
      await fireEvent.click(expand);

      await expectSpectrogramSrc(srParam(ROW_RATE));
      expect(detailCalls()).toHaveLength(0);
    },
    TEST_TIMEOUT_MS
  );

  it(
    'passes the row rate to the overlay player and resets it for the next row',
    async () => {
      const { detailCalls } = stubFetch([ratedRow, batRow]);

      const { container } = render(Search);
      await submitSearch(container);
      await waitForElement(() =>
        container.querySelectorAll<HTMLButtonElement>(OVERLAY_PLAY_SELECTOR).item(1)
      );

      // Row with a known rate: the overlay player uses it, no detail request.
      const playButtons = () =>
        container.querySelectorAll<HTMLButtonElement>(OVERLAY_PLAY_SELECTOR);
      await fireEvent.click(playButtons()[0]);
      await expectSpectrogramSrc(`${SPECTROGRAM_ENDPOINT}${RATED_ROW_ID}`);
      await expectSpectrogramSrc(srParam(ROW_RATE));
      expect(detailCalls()).toHaveLength(0);

      // Close it and open the row without a rate: the previous rate is not reused.
      await fireEvent.keyDown(document, { key: 'Escape' });
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull(), {
        timeout: WAIT_TIMEOUT_MS,
      });
      await fireEvent.click(playButtons()[1]);
      await expectSpectrogramSrc(`${SPECTROGRAM_ENDPOINT}${BAT_ROW_ID}`);
      await expectSpectrogramSrc(srParam(RESOLVED_RATE));
      expect(detailCalls()).toEqual([
        expect.stringContaining(`${DETECTION_ENDPOINT}${BAT_ROW_ID}`),
      ]);
    },
    TEST_TIMEOUT_MS
  );
});
