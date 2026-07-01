/**
 * Tests for the useAudibleBats composable.
 *
 * The composable owns only the request/blob lifecycle (no $effect/onMount), so it
 * can be exercised directly. fetch and the URL object-URL helpers are mocked.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { useAudibleBats } from './useAudibleBats.svelte';
import type { AudibleBatsSettings } from '$lib/desktop/features/dashboard/components/AudibleBatsButton.svelte';

const SETTINGS: AudibleBatsSettings = {
  expansion: 10,
};

function okResponse() {
  // Body is a plain string, not a Blob: jsdom's Blob global is incompatible with
  // undici's Response internals in this test environment ("stream is not a
  // function"). The composable only forwards response.blob() into the (mocked)
  // URL.createObjectURL, so the body's actual bytes are irrelevant here.
  return new Response('wav-data', { status: 200 });
}

function errorResponse(message: string) {
  return new Response(JSON.stringify({ message }), {
    status: 422,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('useAudibleBats', () => {
  let revokeObjectURL: ReturnType<typeof vi.fn<(url: string) => void>>;
  let urlCounter: number;

  beforeEach(() => {
    urlCounter = 0;
    vi.spyOn(URL, 'createObjectURL').mockImplementation(() => `blob:mock-${++urlCounter}`);
    revokeObjectURL = vi.fn<(url: string) => void>();
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(revokeObjectURL);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('generates a derived URL and reports active on enable', async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(okResponse()));
    vi.stubGlobal('fetch', fetchMock);
    const onActivate = vi.fn();

    const bats = useAudibleBats({ getDetectionId: () => 123, onActivate });
    await bats.enable(SETTINGS);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toContain('/api/v2/audio/123/audible-bats');
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body)).toEqual({ expansion: 10, normalize: false, gain_db: 0 });

    expect(bats.active).toBe(true);
    expect(bats.generating).toBe(false);
    expect(bats.error).toBeNull();
    expect(bats.url).toBe('blob:mock-1');
    expect(onActivate).toHaveBeenCalledWith('blob:mock-1');
  });

  it('surfaces the server message and stays inactive on failure', async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(errorResponse('boom')));
    vi.stubGlobal('fetch', fetchMock);

    const bats = useAudibleBats({ getDetectionId: () => 1 });
    await bats.enable(SETTINGS);

    expect(bats.active).toBe(false);
    expect(bats.generating).toBe(false);
    expect(bats.error).toBe('boom');
    expect(bats.url).toBeNull();
  });

  it('returns to normal playback and revokes the URL on disable', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation(() => Promise.resolve(okResponse()))
    );
    const onDeactivate = vi.fn();

    const bats = useAudibleBats({ getDetectionId: () => 1, onDeactivate });
    await bats.enable(SETTINGS);
    expect(bats.active).toBe(true);

    bats.disable();

    expect(bats.active).toBe(false);
    expect(bats.url).toBeNull();
    expect(onDeactivate).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-1');
  });

  it('reset clears state without invoking the swap callbacks', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation(() => Promise.resolve(okResponse()))
    );
    const onDeactivate = vi.fn();

    const bats = useAudibleBats({ getDetectionId: () => 1, onDeactivate });
    await bats.enable(SETTINGS);

    bats.reset();

    expect(bats.active).toBe(false);
    expect(bats.url).toBeNull();
    expect(onDeactivate).not.toHaveBeenCalled();
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-1');
  });

  it('revokes the previous derived URL when regenerating', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation(() => Promise.resolve(okResponse()))
    );

    const bats = useAudibleBats({ getDetectionId: () => 1 });
    await bats.enable(SETTINGS);
    expect(bats.url).toBe('blob:mock-1');

    await bats.enable({ ...SETTINGS, expansion: 20 });
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-1');
    expect(bats.url).toBe('blob:mock-2');
  });

  it('deactivates and revokes the stale URL when a regeneration request fails', async () => {
    const fetchMock = vi
      .fn()
      .mockImplementationOnce(() => Promise.resolve(okResponse()))
      .mockImplementationOnce(() => Promise.resolve(errorResponse('regen failed')));
    vi.stubGlobal('fetch', fetchMock);
    const onDeactivate = vi.fn();

    const bats = useAudibleBats({ getDetectionId: () => 1, onDeactivate });
    await bats.enable(SETTINGS);
    expect(bats.url).toBe('blob:mock-1');

    await bats.enable({ ...SETTINGS, expansion: 20 });

    expect(bats.active).toBe(false);
    expect(bats.error).toBe('regen failed');
    expect(bats.url).toBeNull();
    expect(onDeactivate).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-1');
  });
});
