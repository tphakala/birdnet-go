/**
 * Shared lookup of a detection's clip sample rate.
 *
 * List and search responses do not carry the rate for older bat detections;
 * the single-detection endpoint reads it from the clip. Every audio player
 * that needs it asks through this module, so players for the same detection
 * (a remount, the same row shown twice) share one request and its answer.
 */

import { api } from '$lib/utils/api';

/** Longest wait for the detection endpoint before the rate counts as unknown. */
export const CLIP_RATE_LOOKUP_TIMEOUT_MS = 1500;

/** Most detections whose lookup is kept; the oldest entry is dropped first. */
export const CLIP_RATE_CACHE_MAX_ENTRIES = 200;

/**
 * The server's work timeout for filling a missing clip rate in the background
 * (clipRateWorkTimeout in internal/api/v2/detections).
 */
const SERVER_CLIP_RATE_FILL_TIMEOUT_MS = 5_000;

/** Extra time added to the server's work timeout before a lookup asks again. */
const CLIP_RATE_FILL_MARGIN_MS = 1_000;

/**
 * How long an answer without a rate is reused. When the detail request returns
 * before the server's background fill has finished, the fill stores the rate
 * within its work timeout; the margin on top covers the round trip, so a lookup
 * after that gets the stored rate.
 */
export const UNKNOWN_CLIP_RATE_RETRY_MS =
  SERVER_CLIP_RATE_FILL_TIMEOUT_MS + CLIP_RATE_FILL_MARGIN_MS;

/** True for a usable sample rate in Hz: a positive integer. */
export const isKnownSampleRate = (rate: unknown): rate is number =>
  typeof rate === 'number' && Number.isInteger(rate) && rate > 0;

interface LookupEntry {
  promise: Promise<number | undefined>;
  controller: AbortController;
  waiters: number;
  settled: boolean;
}

const cache = new Map<string, LookupEntry>();

function readClipSampleRate(data: unknown): number | undefined {
  if (typeof data !== 'object' || data === null || !('clipSampleRate' in data)) return undefined;
  return isKnownSampleRate(data.clipSampleRate) ? data.clipSampleRate : undefined;
}

function forget(detectionId: string, entry: LookupEntry): void {
  if (cache.get(detectionId) === entry) cache.delete(detectionId);
}

function startLookup(detectionId: string): LookupEntry {
  const controller = new AbortController();
  // An abort through our own signal counts as a caller abort in the api client,
  // so a timeout is not reported as a failed request.
  const timeoutId = setTimeout(() => controller.abort(), CLIP_RATE_LOOKUP_TIMEOUT_MS);
  const entry: LookupEntry = {
    controller,
    waiters: 0,
    settled: false,
    promise: Promise.resolve(undefined),
  };
  entry.promise = api
    .get<unknown>(`/api/v2/detections/${encodeURIComponent(detectionId)}`, {
      signal: controller.signal,
    })
    .then(readClipSampleRate)
    .catch(() => undefined)
    .then(rate => {
      clearTimeout(timeoutId);
      entry.settled = true;
      if (controller.signal.aborted) {
        // Timed out or abandoned: not an answer worth keeping.
        forget(detectionId, entry);
      } else if (rate === undefined) {
        setTimeout(() => forget(detectionId, entry), UNKNOWN_CLIP_RATE_RETRY_MS);
      }
      return rate;
    });

  cache.set(detectionId, entry);
  if (cache.size > CLIP_RATE_CACHE_MAX_ENTRIES) {
    const oldest = cache.keys().next().value;
    if (oldest !== undefined) cache.delete(oldest);
  }
  return entry;
}

/**
 * Resolves the clip sample rate of a detection in Hz, or undefined when it is
 * unknown, the lookup failed, timed out, or `signal` was aborted. Aborting the
 * last waiter of a pending lookup cancels its request and drops it from the
 * cache, so an abort never leaves a wrong answer behind.
 */
export function lookupClipSampleRate(
  detectionId: string,
  signal: AbortSignal
): Promise<number | undefined> {
  if (signal.aborted) return Promise.resolve(undefined);

  const entry = cache.get(detectionId) ?? startLookup(detectionId);
  if (entry.settled) return entry.promise;

  entry.waiters++;
  return new Promise(resolve => {
    const onAbort = () => {
      entry.waiters--;
      if (!entry.settled && entry.waiters === 0) {
        entry.controller.abort();
        forget(detectionId, entry);
      }
      resolve(undefined);
    };
    signal.addEventListener('abort', onAbort, { once: true });
    void entry.promise.then(rate => {
      signal.removeEventListener('abort', onAbort);
      if (signal.aborted) return;
      entry.waiters--;
      resolve(rate);
    });
  });
}

/** Empties the cache. For tests. */
export function clearClipSampleRateCache(): void {
  for (const entry of cache.values()) {
    if (!entry.settled) entry.controller.abort();
  }
  cache.clear();
}
