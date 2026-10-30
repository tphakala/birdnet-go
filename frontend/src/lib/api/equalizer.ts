/**
 * Client for the equalizer response endpoint.
 *
 * The backend builds the same filter chain the audio path builds and returns
 * its magnitude response, so the settings preview shows what audio actually
 * gets. There is no client-side DSP fallback: when the request fails, the
 * preview shows an error instead of a curve.
 */
import { api } from '$lib/utils/api';
import { createDebounce } from '$lib/utils/debounce';
import { loggers } from '$lib/utils/logger';
import { isPlainObject } from '$lib/utils/security';
import type { EqualizerFilterType } from '$lib/stores/settings';

const logger = loggers.settings;

/** Endpoint that computes the response curve of an (unsaved) equalizer configuration. */
export const EQUALIZER_RESPONSE_URL = '/api/v2/system/audio/equalizer/response';

/** Delay after the last edit before the response is requested. */
export const EQUALIZER_RESPONSE_DEBOUNCE_MS = 250;

/** One point of the response curve. */
export interface EqualizerResponsePoint {
  hz: number;
  gainDb: number;
}

/**
 * Why the backend left a filter out of the chain. The first three mirror the
 * backend's closed set; anything else the server sends maps to `unknown`.
 */
export type EqualizerSkipReason = 'unknown_type' | 'invalid_parameters' | 'add_failed' | 'unknown';

/** A filter the audio chain does not apply. */
export interface EqualizerSkippedFilter {
  /** Position of the filter in the posted `equalizer.filters`. */
  index: number;
  type: string;
  reason: EqualizerSkipReason;
  /**
   * The backend's specific reason, in English (for example which parameter
   * is out of range); empty when the response carries none.
   */
  message: string;
}

/** Validated response of the equalizer response endpoint. */
export interface EqualizerResponse {
  /** Sample rate the curve was computed at, in Hz. */
  sampleRate: number;
  enabled: boolean;
  /** Lowest frequency the backend swept, in Hz. */
  minHz: number;
  /** Highest frequency the backend swept, in Hz; the graph sizes its axis from it. */
  maxHz: number;
  /** Curve points, ascending and unique in `hz`. */
  points: EqualizerResponsePoint[];
  skipped: EqualizerSkippedFilter[];
}

/**
 * A filter as the preview accepts it. Both the settings store's filter and
 * the equalizer editor's local filter fit; `id` is accepted and never sent.
 */
export interface EqualizerResponseFilterInput {
  id?: string;
  type: EqualizerFilterType;
  frequency: number;
  q?: number;
  width?: number;
  gain?: number;
  passes?: number;
}

/** A filter as it is posted to the backend. */
export interface EqualizerResponseRequestFilter {
  type: EqualizerFilterType;
  frequency: number;
  q?: number;
  /** Bandwidth in Hz. */
  width?: number;
  gain?: number;
  passes?: number;
}

/** Body of the equalizer response request. */
export interface EqualizerResponseRequest {
  equalizer: {
    enabled: boolean;
    filters: EqualizerResponseRequestFilter[];
  };
  /** Rate to compute at; omitted to use the server's configured rate. */
  sampleRate?: number;
}

/** A response together with the request it answers. */
export interface EqualizerResponseReady {
  request: EqualizerResponseRequest;
  response: EqualizerResponse;
}

/** State the loader reports to its owner. */
export type EqualizerResponseState =
  | { status: 'loading'; previous?: EqualizerResponseReady }
  | ({ status: 'ready' } & EqualizerResponseReady)
  | { status: 'error' };

const OPTIONAL_FILTER_FIELDS = ['q', 'width', 'gain', 'passes'] as const;

/**
 * Build the request body from an equalizer configuration. Only the fields the
 * backend reads are copied, so the filter `id` is dropped. `sampleRate` is
 * added only when it is a finite number above 0, because a source's 0 means
 * "use the default rate" while the backend rejects an explicit 0.
 */
export function buildEqualizerResponseRequest(
  equalizer: { enabled: boolean; filters: ReadonlyArray<EqualizerResponseFilterInput> },
  sampleRate?: number
): EqualizerResponseRequest {
  const filters = equalizer.filters.map(filter => {
    const out: EqualizerResponseRequestFilter = {
      type: filter.type,
      frequency: filter.frequency,
    };
    for (const field of OPTIONAL_FILTER_FIELDS) {
      // eslint-disable-next-line security/detect-object-injection -- field comes from a fixed allow-list
      const value = filter[field];
      if (typeof value === 'number') {
        // eslint-disable-next-line security/detect-object-injection -- field comes from a fixed allow-list
        out[field] = value;
      }
    }
    return out;
  });

  const request: EqualizerResponseRequest = {
    equalizer: { enabled: equalizer.enabled, filters },
  };
  if (sampleRate !== undefined && Number.isFinite(sampleRate) && sampleRate > 0) {
    request.sampleRate = sampleRate;
  }
  return request;
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value);
}

function toSkipReason(reason: string): EqualizerSkipReason {
  switch (reason) {
    case 'unknown_type':
    case 'invalid_parameters':
    case 'add_failed':
      return reason;
    default:
      return 'unknown';
  }
}

/**
 * Validate a response body. Throws unless it has a positive `sampleRate`, a
 * boolean `enabled`, a non-empty `points` array strictly ascending in `hz`
 * with finite values and `hz` above 0, and `skipped` entries whose `index`
 * points into the `filterCount` posted filters, each index at most once.
 * Each skipped entry keeps the backend's `message`, or '' when it has none.
 */
export function parseEqualizerResponse(data: unknown, filterCount: number): EqualizerResponse {
  if (!isPlainObject(data)) {
    throw new Error('equalizer response is not an object');
  }
  const { sampleRate, enabled, minHz, maxHz, points, skipped } = data;
  if (!isFiniteNumber(sampleRate) || sampleRate <= 0) {
    throw new Error('equalizer response has no valid sampleRate');
  }
  if (typeof enabled !== 'boolean') {
    throw new Error('equalizer response has no enabled flag');
  }
  if (!isFiniteNumber(minHz) || minHz <= 0 || !isFiniteNumber(maxHz) || maxHz <= minHz) {
    throw new Error('equalizer response has no valid frequency range');
  }
  if (!Array.isArray(points) || points.length === 0) {
    throw new Error('equalizer response has no points');
  }

  const parsedPoints: EqualizerResponsePoint[] = [];
  let previousHz = 0;
  for (const point of points) {
    if (!isPlainObject(point)) {
      throw new Error('equalizer response point is not an object');
    }
    const { hz, gainDb } = point;
    if (!isFiniteNumber(hz) || !isFiniteNumber(gainDb) || hz <= previousHz) {
      throw new Error('equalizer response point is invalid or out of order');
    }
    parsedPoints.push({ hz, gainDb });
    previousHz = hz;
  }

  if (!Array.isArray(skipped)) {
    throw new Error('equalizer response has no skipped list');
  }
  const parsedSkipped: EqualizerSkippedFilter[] = [];
  const seenIndices = new Set<number>();
  for (const entry of skipped) {
    if (!isPlainObject(entry)) {
      throw new Error('equalizer skipped entry is not an object');
    }
    const { index, type, reason, message } = entry;
    if (
      typeof index !== 'number' ||
      !Number.isInteger(index) ||
      index < 0 ||
      index >= filterCount ||
      typeof type !== 'string' ||
      typeof reason !== 'string' ||
      seenIndices.has(index)
    ) {
      throw new Error('equalizer skipped entry is invalid');
    }
    seenIndices.add(index);
    parsedSkipped.push({
      index,
      type,
      reason: toSkipReason(reason),
      message: typeof message === 'string' ? message : '',
    });
  }

  return { sampleRate, enabled, minHz, maxHz, points: parsedPoints, skipped: parsedSkipped };
}

/** Request the response curve for `request` and validate the result. */
export async function fetchEqualizerResponse(
  request: EqualizerResponseRequest,
  signal?: AbortSignal
): Promise<EqualizerResponse> {
  const result = await api.post<unknown>(EQUALIZER_RESPONSE_URL, request, { signal });
  return parseEqualizerResponse(result, request.equalizer.filters.length);
}

/** Options of {@link createEqualizerResponseLoader}. */
export interface EqualizerResponseLoaderOptions {
  /** Debounce delay; defaults to {@link EQUALIZER_RESPONSE_DEBOUNCE_MS}. */
  delayMs?: number;
  /** Replaces the network call, for tests. */
  fetcher?: typeof fetchEqualizerResponse;
}

/** Handle returned by {@link createEqualizerResponseLoader}. */
export interface EqualizerResponseLoader {
  /** Ask for the response of `body`; repeated equal bodies are ignored. */
  request(body: EqualizerResponseRequest): void;
  /** Send the last requested body again at once. */
  retry(): void;
  /** Cancel pending work; the loader reports nothing afterwards. */
  dispose(): void;
}

/**
 * Create a loader that debounces requests and reports state through `onState`.
 *
 * The last request always wins: each accepted request supersedes the previous
 * one at call time (it aborts the in-flight fetch and bumps a sequence
 * number), and a fetch that settles after it was superseded or after
 * `dispose()` reports nothing. A body equal to the last ready one is answered
 * from that response without a fetch, and the body that failed is sent again
 * when it is requested again.
 */
export function createEqualizerResponseLoader(
  onState: (state: EqualizerResponseState) => void,
  options: EqualizerResponseLoaderOptions = {}
): EqualizerResponseLoader {
  const fetcher = options.fetcher ?? fetchEqualizerResponse;
  let seq = 0;
  let controller: AbortController | undefined;
  let lastKey: string | undefined;
  let lastBody: EqualizerResponseRequest | undefined;
  let lastReady: (EqualizerResponseReady & { key: string }) | undefined;
  let disposed = false;

  const scheduleSend = createDebounce(
    (body: EqualizerResponseRequest, key: string, mySeq: number) => {
      void send(body, key, mySeq);
    },
    options.delayMs ?? EQUALIZER_RESPONSE_DEBOUNCE_MS
  );

  /** Invalidate every earlier request and return the new sequence number. */
  function supersede(): number {
    seq += 1;
    controller?.abort();
    controller = undefined;
    return seq;
  }

  async function send(body: EqualizerResponseRequest, key: string, mySeq: number): Promise<void> {
    if (disposed || mySeq !== seq) return;
    const ownController = new AbortController();
    controller = ownController;
    const isStale = () => disposed || mySeq !== seq || ownController.signal.aborted;
    try {
      const response = await fetcher(body, ownController.signal);
      if (isStale()) return;
      lastReady = { key, request: body, response };
      onState({ status: 'ready', request: body, response });
    } catch (error) {
      if (isStale()) return;
      logger.warn('Equalizer response request failed', error, {
        component: 'equalizerResponse',
      });
      lastKey = undefined;
      lastReady = undefined;
      onState({ status: 'error' });
    }
  }

  return {
    request(body) {
      if (disposed) return;
      const key = JSON.stringify(body);
      if (key === lastKey) return;
      lastKey = key;
      lastBody = body;
      const mySeq = supersede();
      if (lastReady?.key === key) {
        scheduleSend.cancel();
        onState({ status: 'ready', request: lastReady.request, response: lastReady.response });
        return;
      }
      onState(
        lastReady
          ? {
              status: 'loading',
              previous: { request: lastReady.request, response: lastReady.response },
            }
          : { status: 'loading' }
      );
      scheduleSend(body, key, mySeq);
    },

    retry() {
      if (disposed || !lastBody) return;
      scheduleSend.cancel();
      const key = JSON.stringify(lastBody);
      lastKey = key;
      const mySeq = supersede();
      onState({ status: 'loading' });
      void send(lastBody, key, mySeq);
    },

    dispose() {
      disposed = true;
      scheduleSend.cancel();
      controller?.abort();
      controller = undefined;
    },
  };
}

/**
 * Return the point whose `hz` is nearest to `hz`, or undefined when there are
 * none. `points` must be ascending in `hz`. On an exact tie the lower
 * frequency wins.
 */
export function findNearestPoint(
  points: ReadonlyArray<EqualizerResponsePoint>,
  hz: number
): EqualizerResponsePoint | undefined {
  if (points.length === 0) return undefined;
  // First index whose hz is >= the target.
  let lo = 0;
  let hi = points.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    const point = points.at(mid);
    if (point !== undefined && point.hz < hz) lo = mid + 1;
    else hi = mid;
  }
  const above = points.at(lo);
  const below = lo > 0 ? points.at(lo - 1) : undefined;
  if (above === undefined) return below;
  if (below === undefined) return above;
  return hz - below.hz <= above.hz - hz ? below : above;
}
