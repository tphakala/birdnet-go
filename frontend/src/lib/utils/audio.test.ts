import { describe, it, expect, vi, afterEach } from 'vitest';
import {
  DASHBOARD_AUDIO_RETRY_DELAYS_MS,
  MAX_AUDIO_PROBE_RETRY_AFTER_MS,
  pendingRetryDelayMs,
  playButtonLabelKey,
  probeAudioSource,
} from './audio';

describe('playButtonLabelKey', () => {
  it('reads as loading while loading, even when playing', () => {
    expect(playButtonLabelKey(true, true)).toBe('media.audio.loading');
    expect(playButtonLabelKey(true, false)).toBe('media.audio.loading');
  });

  it('reads as pause when playing and play otherwise', () => {
    expect(playButtonLabelKey(false, true)).toBe('media.audio.pause');
    expect(playButtonLabelKey(false, false)).toBe('media.audio.play');
  });
});

describe('DASHBOARD_AUDIO_RETRY_DELAYS_MS', () => {
  it('keeps the schedule that spans the backend pending window', () => {
    expect(DASHBOARD_AUDIO_RETRY_DELAYS_MS).toEqual([2000, 4000, 8000, 16000, 30000, 30000]);
  });
});

describe('probeAudioSource', () => {
  const URL_UNDER_PROBE = '/api/v2/audio/42';
  const RETRY_AFTER_SECONDS = 7;
  const HUGE_RETRY_AFTER_SECONDS = 86400;

  function respond(status: number, headers: Record<string, string> = {}) {
    const cancel = vi.fn().mockResolvedValue(undefined);
    // Partial Response: the probe reads only status, headers and body.cancel.
    const response = {
      status,
      headers: new Headers(headers),
      body: { cancel },
    } as unknown as Response;
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(response);
    return cancel;
  }

  const originalFetchImpl = vi.mocked(globalThis.fetch).getMockImplementation();

  afterEach(() => {
    vi.mocked(globalThis.fetch).mockReset();
    if (originalFetchImpl) vi.mocked(globalThis.fetch).mockImplementation(originalFetchImpl);
  });

  it('sends one ranged GET of the url with the abort signal', async () => {
    respond(206);
    const controller = new AbortController();
    await probeAudioSource(URL_UNDER_PROBE, controller.signal);
    expect(globalThis.fetch).toHaveBeenCalledTimes(1);
    expect(globalThis.fetch).toHaveBeenCalledWith(URL_UNDER_PROBE, {
      headers: { Range: 'bytes=0-0' },
      signal: controller.signal,
    });
  });

  it('classifies 404 as missing', async () => {
    respond(404);
    expect(await probeAudioSource(URL_UNDER_PROBE, new AbortController().signal)).toEqual({
      kind: 'missing',
    });
  });

  it('classifies 503 as pending and reads Retry-After in ms', async () => {
    respond(503, { 'Retry-After': String(RETRY_AFTER_SECONDS) });
    expect(await probeAudioSource(URL_UNDER_PROBE, new AbortController().signal)).toEqual({
      kind: 'pending',
      retryAfterMs: RETRY_AFTER_SECONDS * 1000,
    });
  });

  it('caps a huge Retry-After and ignores one that is not a number of seconds', async () => {
    respond(503, { 'Retry-After': String(HUGE_RETRY_AFTER_SECONDS) });
    expect(await probeAudioSource(URL_UNDER_PROBE, new AbortController().signal)).toEqual({
      kind: 'pending',
      retryAfterMs: MAX_AUDIO_PROBE_RETRY_AFTER_MS,
    });
    for (const value of ['Wed, 21 Oct 2026 07:28:00 GMT', '-5', '', 'abc']) {
      respond(503, { 'Retry-After': value });
      expect(await probeAudioSource(URL_UNDER_PROBE, new AbortController().signal)).toEqual({
        kind: 'pending',
        retryAfterMs: undefined,
      });
    }
    respond(503, { 'Retry-After': '0' });
    expect(await probeAudioSource(URL_UNDER_PROBE, new AbortController().signal)).toEqual({
      kind: 'pending',
      retryAfterMs: 0,
    });
  });

  it('classifies 206, 200 and 500 as unknown', async () => {
    for (const status of [206, 200, 500]) {
      respond(status);
      expect(await probeAudioSource(URL_UNDER_PROBE, new AbortController().signal)).toEqual({
        kind: 'unknown',
      });
    }
  });

  it('classifies a network error and an abort as unknown without rejecting', async () => {
    vi.mocked(globalThis.fetch).mockRejectedValueOnce(new TypeError('Failed to fetch'));
    expect(await probeAudioSource(URL_UNDER_PROBE, new AbortController().signal)).toEqual({
      kind: 'unknown',
    });
    const controller = new AbortController();
    controller.abort();
    vi.mocked(globalThis.fetch).mockRejectedValueOnce(new DOMException('aborted', 'AbortError'));
    expect(await probeAudioSource(URL_UNDER_PROBE, controller.signal)).toEqual({
      kind: 'unknown',
    });
  });

  it('cancels the response body instead of reading it', async () => {
    const cancel = respond(200);
    await probeAudioSource(URL_UNDER_PROBE, new AbortController().signal);
    expect(cancel).toHaveBeenCalledTimes(1);
  });
});

describe('pendingRetryDelayMs', () => {
  const SCHEDULED_MS = 2000;

  it('uses Retry-After only when it is longer than the scheduled delay', () => {
    expect(pendingRetryDelayMs({ kind: 'pending', retryAfterMs: 9000 }, SCHEDULED_MS)).toBe(9000);
    expect(pendingRetryDelayMs({ kind: 'pending', retryAfterMs: 500 }, SCHEDULED_MS)).toBe(
      SCHEDULED_MS
    );
    expect(pendingRetryDelayMs({ kind: 'pending' }, SCHEDULED_MS)).toBe(SCHEDULED_MS);
  });
});
