import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import Search from './Search.svelte';

const SEARCH_ENDPOINT = '/api/v2/search';
const DETECTION_ENDPOINT = '/api/v2/detections/';
const BAT_ROW_ID = '77';

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

describe('Search bat clip sample rate', () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('resolves the rate once when an old bat row is expanded, never while it is only listed', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes(SEARCH_ENDPOINT)) {
        return Promise.resolve(jsonResponse({ results: [batRow], total: 1, pages: 1 }));
      }
      if (url.includes(DETECTION_ENDPOINT)) {
        return Promise.resolve(jsonResponse({ id: 77, clipSampleRate: 384_000 }));
      }
      return Promise.resolve(jsonResponse({ data: [] }));
    });
    vi.stubGlobal('fetch', fetchMock);
    const detailCalls = () =>
      fetchMock.mock.calls.map(call => String(call[0])).filter(u => u.includes(DETECTION_ENDPOINT));

    const { container } = render(Search);
    const submit = container.querySelector<HTMLButtonElement>('button[type="submit"]');
    expect(submit).not.toBeNull();
    if (submit) await fireEvent.click(submit);

    const expand = await waitFor(() => {
      const button = container.querySelector<HTMLButtonElement>(
        `button[aria-controls="expanded-row-${BAT_ROW_ID}"]`
      );
      expect(button).not.toBeNull();
      return button;
    });
    expect(detailCalls()).toHaveLength(0);

    if (expand) await fireEvent.click(expand);

    await waitFor(() => {
      expect(screen.getByAltText('components.audio.spectrogramAlt').getAttribute('src')).toContain(
        'sr=384000'
      );
    });
    expect(detailCalls()).toEqual([expect.stringContaining(`${DETECTION_ENDPOINT}${BAT_ROW_ID}`)]);
  });

  it('passes a known rate from the search row to the player without a detail request', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes(SEARCH_ENDPOINT)) {
        return Promise.resolve(
          jsonResponse({ results: [{ ...batRow, clipSampleRate: 192_000 }], total: 1, pages: 1 })
        );
      }
      return Promise.resolve(jsonResponse({ data: [] }));
    });
    vi.stubGlobal('fetch', fetchMock);

    const { container } = render(Search);
    const submit = container.querySelector<HTMLButtonElement>('button[type="submit"]');
    if (submit) await fireEvent.click(submit);
    const expand = await waitFor(() => {
      const button = container.querySelector<HTMLButtonElement>(
        `button[aria-controls="expanded-row-${BAT_ROW_ID}"]`
      );
      expect(button).not.toBeNull();
      return button;
    });
    if (expand) await fireEvent.click(expand);

    await waitFor(() => {
      expect(screen.getByAltText('components.audio.spectrogramAlt').getAttribute('src')).toContain(
        'sr=192000'
      );
    });
    expect(
      fetchMock.mock.calls.map(call => String(call[0])).filter(u => u.includes(DETECTION_ENDPOINT))
    ).toHaveLength(0);
  });
});
