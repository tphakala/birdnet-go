import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { api } from '$lib/utils/api';
import {
  clearClipSampleRateCache,
  isKnownSampleRate,
  lookupClipSampleRate,
  CLIP_RATE_CACHE_MAX_ENTRIES,
  CLIP_RATE_LOOKUP_TIMEOUT_MS,
} from './clipSampleRateLookup';

vi.mock('$lib/utils/api', () => ({ api: { get: vi.fn() } }));

const RATE_384K = 384_000;

interface PendingGet {
  url: string;
  signal: AbortSignal | undefined;
  resolve: (body: unknown) => void;
  reject: (error: Error) => void;
}

describe('lookupClipSampleRate', () => {
  let gets: PendingGet[];

  beforeEach(() => {
    vi.useFakeTimers();
    clearClipSampleRateCache();
    gets = [];
    vi.mocked(api.get).mockImplementation(
      (url, options) =>
        new Promise((resolve, reject) => {
          const signal = options?.signal ?? undefined;
          signal?.addEventListener('abort', () =>
            reject(new DOMException('The operation was aborted.', 'AbortError'))
          );
          gets.push({ url, signal, resolve, reject });
        })
    );
  });

  afterEach(() => {
    vi.mocked(api.get).mockReset();
    vi.clearAllTimers();
    vi.useRealTimers();
  });

  it('shares one request between lookups of the same detection', async () => {
    const first = lookupClipSampleRate('42', new AbortController().signal);
    const second = lookupClipSampleRate('42', new AbortController().signal);

    expect(gets).toHaveLength(1);
    expect(gets[0]?.url).toBe('/api/v2/detections/42');
    gets[0]?.resolve({ id: 42, clipSampleRate: RATE_384K });

    await expect(first).resolves.toBe(RATE_384K);
    await expect(second).resolves.toBe(RATE_384K);
    // A later lookup is answered from the cache.
    await expect(lookupClipSampleRate('42', new AbortController().signal)).resolves.toBe(RATE_384K);
    expect(gets).toHaveLength(1);
  });

  it('keeps the request alive while another lookup still waits for it', async () => {
    const leaving = new AbortController();
    const first = lookupClipSampleRate('42', leaving.signal);
    const second = lookupClipSampleRate('42', new AbortController().signal);

    leaving.abort();
    await expect(first).resolves.toBeUndefined();
    expect(gets[0]?.signal?.aborted).toBe(false);

    gets[0]?.resolve({ clipSampleRate: RATE_384K });
    await expect(second).resolves.toBe(RATE_384K);
  });

  it('aborts the request when its only lookup is aborted, and does not cache it', async () => {
    const controller = new AbortController();
    const lookup = lookupClipSampleRate('42', controller.signal);

    controller.abort();

    await expect(lookup).resolves.toBeUndefined();
    expect(gets[0]?.signal?.aborted).toBe(true);
    void lookupClipSampleRate('42', new AbortController().signal);
    expect(gets).toHaveLength(2);
  });

  it('gives up at the timeout without caching the timed-out request', async () => {
    const lookup = lookupClipSampleRate('42', new AbortController().signal);

    await vi.advanceTimersByTimeAsync(CLIP_RATE_LOOKUP_TIMEOUT_MS - 1);
    expect(gets[0]?.signal?.aborted).toBe(false);
    await vi.advanceTimersByTimeAsync(1);

    await expect(lookup).resolves.toBeUndefined();
    expect(gets[0]?.signal?.aborted).toBe(true);
    void lookupClipSampleRate('42', new AbortController().signal);
    expect(gets).toHaveLength(2);
  });

  it('caches an unknown rate for 6 s, then asks again', async () => {
    const lookup = lookupClipSampleRate('42', new AbortController().signal);
    gets[0]?.resolve({ id: 42 });
    await expect(lookup).resolves.toBeUndefined();

    // Still cached just before 6 s (the server's 5 s fill work timeout plus 1 s).
    await vi.advanceTimersByTimeAsync(5_999);
    await expect(lookupClipSampleRate('42', new AbortController().signal)).resolves.toBeUndefined();
    expect(gets).toHaveLength(1);

    await vi.advanceTimersByTimeAsync(1);
    void lookupClipSampleRate('42', new AbortController().signal);
    expect(gets).toHaveLength(2);
  });

  it.each([
    ['an error', (get: PendingGet) => get.reject(new Error('Not found'))],
    ['an invalid rate', (get: PendingGet) => get.resolve({ clipSampleRate: 'fast' })],
    ['a non-object body', (get: PendingGet) => get.resolve(null)],
  ])('answers undefined after %s', async (_name, answer) => {
    const lookup = lookupClipSampleRate('42', new AbortController().signal);
    expect(gets).toHaveLength(1);
    answer(gets[0]);
    await expect(lookup).resolves.toBeUndefined();
  });

  it('answers undefined at once for a lookup whose signal is already aborted', async () => {
    const controller = new AbortController();
    controller.abort();

    await expect(lookupClipSampleRate('42', controller.signal)).resolves.toBeUndefined();
    expect(gets).toHaveLength(0);
  });

  it('keeps the 200 newest detections and drops only the oldest', async () => {
    expect(CLIP_RATE_CACHE_MAX_ENTRIES).toBe(200);

    // Detections 0 to 200: one more than the bound.
    for (let id = 0; id <= 200; id++) {
      const lookup = lookupClipSampleRate(String(id), new AbortController().signal);
      gets.at(-1)?.resolve({ clipSampleRate: RATE_384K });
      await lookup;
    }
    expect(gets).toHaveLength(201);

    // Detections 1 to 200 are answered from the cache.
    for (let id = 1; id <= 200; id++) {
      await expect(lookupClipSampleRate(String(id), new AbortController().signal)).resolves.toBe(
        RATE_384K
      );
    }
    expect(gets).toHaveLength(201);

    // Only detection 0, the oldest, is asked again.
    void lookupClipSampleRate('0', new AbortController().signal);
    expect(gets).toHaveLength(202);
    expect(gets[201]?.url).toBe('/api/v2/detections/0');
  });
});

describe('isKnownSampleRate', () => {
  it.each([
    [RATE_384K, true],
    [1, true],
    [0, false],
    [-RATE_384K, false],
    [RATE_384K + 0.5, false],
    [Number.NaN, false],
    ['384000', false],
    [undefined, false],
  ])('%s is known: %s', (rate, known) => {
    expect(isKnownSampleRate(rate)).toBe(known);
  });
});
