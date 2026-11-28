/**
 * Tests for the useAudibleBats composable.
 *
 * The composable fetches the detection clip (optionally denoised) and expands
 * it client-side into a WAV object URL. fetch and URL object-URL helpers are mocked.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { useAudibleBats } from './useAudibleBats.svelte';
import { makeBatWavBuffer } from '../../test/audioFixtures';
import type { AudibleBatsSettings } from '$lib/desktop/features/dashboard/components/AudibleBatsButton.svelte';

const SETTINGS: AudibleBatsSettings = {
  expansion: 10,
};

function okResponse(sampleRate = 256000) {
  return {
    ok: true,
    status: 200,
    arrayBuffer: async () => makeBatWavBuffer(sampleRate),
  };
}

function errorResponse(message: string, status = 422) {
  return new Response(JSON.stringify({ message }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('useAudibleBats', () => {
  let revokeObjectURL: ReturnType<typeof vi.fn<(url: string) => void>>;
  let urlCounter: number;

  beforeEach(() => {
    urlCounter = 0;
    vi.spyOn(URL, 'createObjectURL').mockImplementation(() => `blob:mock-${++urlCounter}`);
    revokeObjectURL = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('fetches denoised clip via GET when getDenoise returns a preset', async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(okResponse()));
    vi.stubGlobal('fetch', fetchMock);
    let preset = 'light-medium';

    const bats = useAudibleBats({ getDetectionId: () => 5, getDenoise: () => preset });
    await bats.enable(SETTINGS);
    preset = '';
    await bats.enable(SETTINGS);

    const urls = fetchMock.mock.calls.map(c => String(c[0]));
    expect(urls).toEqual(['/api/v2/audio/5?denoise=light-medium', '/api/v2/audio/5']);
  });

  it('falls back to the stored clip when the denoised clip returns 422, 500, or 503', async () => {
    for (const status of [422, 500, 503]) {
      const fetchMock = vi
        .fn()
        .mockResolvedValueOnce(errorResponse('denoise failed', status))
        .mockResolvedValueOnce(okResponse());
      vi.stubGlobal('fetch', fetchMock);

      const bats = useAudibleBats({ getDetectionId: () => 9, getDenoise: () => 'light-medium' });
      await bats.enable(SETTINGS);

      expect(bats.active).toBe(true);
      expect(bats.error).toBeNull();
      expect(fetchMock.mock.calls.map(c => String(c[0]))).toEqual([
        '/api/v2/audio/9?denoise=light-medium',
        '/api/v2/audio/9',
      ]);
    }
  });

  it('surfaces localized UI error instead of raw technical details when expansion fails', async () => {
    const fetchMock = vi.fn().mockResolvedValue(okResponse(48000));
    vi.stubGlobal('fetch', fetchMock);
    const onActivate = vi.fn();

    const bats = useAudibleBats({ getDetectionId: () => 8, onActivate });
    await bats.enable(SETTINGS);

    expect(bats.active).toBe(false);
    expect(bats.url).toBeNull();
    expect(bats.error).toBe('media.audio.audibleBats.error');
    expect(onActivate).not.toHaveBeenCalled();
  });

  it('tracks URL lifecycle across enable, disable, and regeneration failure', async () => {
    let fail = false;
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockImplementation(() =>
          Promise.resolve(fail ? errorResponse('regen failed', 404) : okResponse())
        )
    );
    const onActivate = vi.fn();
    const onDeactivate = vi.fn();

    const bats = useAudibleBats({
      getDetectionId: () => 12,
      onActivate,
      onDeactivate,
    });

    await bats.enable(SETTINGS);
    expect(bats.active).toBe(true);
    expect(bats.url).toBe('blob:mock-1');
    expect(onActivate).toHaveBeenCalledWith('blob:mock-1');

    fail = true;
    await bats.enable({ expansion: 20 });
    expect(bats.active).toBe(false);
    expect(bats.error).toBe('regen failed');
    expect(bats.url).toBeNull();
    expect(onDeactivate).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-1');

    fail = false;
    await bats.enable(SETTINGS);
    bats.disable();
    expect(bats.active).toBe(false);
    expect(bats.url).toBeNull();
    expect(onDeactivate).toHaveBeenCalledTimes(2);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-2');
  });

  it('ignores superseded requests when aborted by a newer enable', async () => {
    let resolveFirst: (value: unknown) => void = () => {};
    let firstSignal: AbortSignal | undefined;
    let call = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
        call++;
        if (call === 1) {
          firstSignal = init?.signal ?? undefined;
          return new Promise(resolve => {
            resolveFirst = resolve;
          });
        }
        return Promise.resolve(okResponse());
      })
    );

    const bats = useAudibleBats({ getDetectionId: () => 22 });
    const first = bats.enable(SETTINGS);
    await bats.enable({ expansion: 20 });
    expect(firstSignal?.aborted).toBe(true);

    resolveFirst(okResponse());
    await first;
    expect(bats.active).toBe(true);
    expect(bats.url).toBe('blob:mock-1');
  });
});
