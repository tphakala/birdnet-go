/**
 * Tests for the useAudibleBats composable.
 *
 * The composable fetches the detection clip (optionally denoised) and expands
 * it client-side into a WAV object URL. fetch and URL object-URL helpers are mocked.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { useAudibleBats } from './useAudibleBats.svelte';
import type { AudibleBatsSettings } from '$lib/desktop/features/dashboard/components/AudibleBatsButton.svelte';

const SETTINGS: AudibleBatsSettings = {
  expansion: 10,
};

function makeBatWavBuffer(sampleRate = 256000): ArrayBuffer {
  const numSamples = 4;
  const buf = new ArrayBuffer(44 + numSamples * 2);
  const view = new DataView(buf);
  const writeStr = (off: number, text: string) => {
    for (let i = 0; i < text.length; i++) view.setUint8(off + i, text.charCodeAt(i));
  };
  writeStr(0, 'RIFF');
  view.setUint32(4, buf.byteLength - 8, true);
  writeStr(8, 'WAVE');
  writeStr(12, 'fmt ');
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  writeStr(36, 'data');
  view.setUint32(40, numSamples * 2, true);
  return buf;
}

function okResponse(sampleRate = 256000) {
  return {
    ok: true,
    status: 200,
    arrayBuffer: async () => makeBatWavBuffer(sampleRate),
  };
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

  it('surfaces client-side validation error when source sample rate is too low', async () => {
    const fetchMock = vi.fn().mockResolvedValue(okResponse(48000));
    vi.stubGlobal('fetch', fetchMock);
    const onActivate = vi.fn();

    const bats = useAudibleBats({ getDetectionId: () => 8, onActivate });
    await bats.enable(SETTINGS);

    expect(bats.active).toBe(false);
    expect(bats.url).toBeNull();
    expect(bats.error).toContain('96000 Hz');
    expect(onActivate).not.toHaveBeenCalled();
  });

  it('tracks URL lifecycle across enable, disable, and regeneration failure', async () => {
    let fail = false;
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockImplementation(() =>
          Promise.resolve(fail ? errorResponse('regen failed') : okResponse())
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
