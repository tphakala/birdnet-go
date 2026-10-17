/**
 * Tests for the equalizer response client: request building, response
 * validation, and the debounced loader that keeps the last edit authoritative.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { loggers } from '$lib/utils/logger';
import {
  EQUALIZER_RESPONSE_DEBOUNCE_MS,
  EQUALIZER_RESPONSE_URL,
  buildEqualizerResponseRequest,
  createEqualizerResponseLoader,
  fetchEqualizerResponse,
  findNearestPoint,
  parseEqualizerResponse,
  type EqualizerResponse,
  type EqualizerResponseRequest,
  type EqualizerResponseState,
} from './equalizer';

const { apiPost } = vi.hoisted(() => ({
  apiPost:
    vi.fn<(url: string, data?: unknown, options?: { signal?: AbortSignal }) => Promise<unknown>>(),
}));

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: { post: apiPost },
}));

/** A minimal valid backend body for a request with `filterCount` filters. */
function validBody(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    sampleRate: 48000,
    enabled: true,
    points: [
      { hz: 20, gainDb: 0 },
      { hz: 1000, gainDb: -3 },
      { hz: 20000, gainDb: -24 },
    ],
    skipped: [],
    ...overrides,
  };
}

function requestFor(frequency: number, sampleRate?: number): EqualizerResponseRequest {
  return buildEqualizerResponseRequest(
    { enabled: true, filters: [{ type: 'LowPass', frequency, q: 0.707, passes: 1 }] },
    sampleRate
  );
}

function responseFor(request: EqualizerResponseRequest): EqualizerResponse {
  return {
    sampleRate: request.sampleRate ?? 48000,
    enabled: request.equalizer.enabled,
    points: [
      { hz: 20, gainDb: 0 },
      { hz: request.equalizer.filters.at(0)?.frequency ?? 1000, gainDb: -3 },
    ],
    skipped: [],
  };
}

describe('buildEqualizerResponseRequest', () => {
  it('strips the filter id and keeps width in Hz', () => {
    const req = buildEqualizerResponseRequest({
      enabled: true,
      filters: [{ id: 'abc', type: 'BandReject', frequency: 1000, width: 250, passes: 0 }],
    });
    expect(req.equalizer.filters).toEqual([
      { type: 'BandReject', frequency: 1000, width: 250, passes: 0 },
    ]);
  });

  it('sends enabled false as given', () => {
    const req = buildEqualizerResponseRequest({ enabled: false, filters: [] });
    expect(req.equalizer.enabled).toBe(false);
  });

  it('omits sampleRate when none or 0 is passed', () => {
    expect(buildEqualizerResponseRequest({ enabled: true, filters: [] })).not.toHaveProperty(
      'sampleRate'
    );
    expect(buildEqualizerResponseRequest({ enabled: true, filters: [] }, 0)).not.toHaveProperty(
      'sampleRate'
    );
    expect(
      buildEqualizerResponseRequest({ enabled: true, filters: [] }, Number.NaN)
    ).not.toHaveProperty('sampleRate');
  });

  it('includes sampleRate when passed', () => {
    expect(buildEqualizerResponseRequest({ enabled: true, filters: [] }, 96000).sampleRate).toBe(
      96000
    );
  });

  it('never sends sourceId, minHz, maxHz or points', () => {
    const req = buildEqualizerResponseRequest(
      {
        enabled: true,
        filters: [{ type: 'HighPass', frequency: 100, q: 0.7, gain: 0, passes: 2 }],
      },
      48000
    );
    expect(Object.keys(req).sort()).toEqual(['equalizer', 'sampleRate']);
    expect(Object.keys(req.equalizer).sort()).toEqual(['enabled', 'filters']);
    expect(Object.keys(req.equalizer.filters[0] ?? {}).sort()).toEqual([
      'frequency',
      'gain',
      'passes',
      'q',
      'type',
    ]);
  });
});

describe('parseEqualizerResponse', () => {
  it('accepts a valid response', () => {
    const parsed = parseEqualizerResponse(
      validBody({
        skipped: [{ index: 0, type: 'LowPass', reason: 'invalid_parameters', message: 'x' }],
      }),
      1
    );
    expect(parsed.sampleRate).toBe(48000);
    expect(parsed.points).toHaveLength(3);
    expect(parsed.skipped).toEqual([
      { index: 0, type: 'LowPass', reason: 'invalid_parameters', message: 'x' },
    ]);
  });

  it('keeps the backend reason message and defaults a missing one to empty', () => {
    const message = 'WidthHz 10000 is too wide at Frequency 20 (lower band edge <= 0)';
    const parsed = parseEqualizerResponse(
      validBody({
        skipped: [
          { index: 0, type: 'BandReject', reason: 'invalid_parameters', message },
          { index: 1, type: 'LowPass', reason: 'add_failed' },
          { index: 2, type: 'LowPass', reason: 'add_failed', message: 42 },
        ],
      }),
      3
    );
    expect(parsed.skipped.map(entry => entry.message)).toEqual([message, '', '']);
  });

  it('rejects the global mock shape {data: []}', () => {
    expect(() => parseEqualizerResponse({ data: [] }, 0)).toThrow();
  });

  it('rejects an empty points array', () => {
    expect(() => parseEqualizerResponse(validBody({ points: [] }), 0)).toThrow();
  });

  it('rejects non-finite, non-positive or descending hz', () => {
    const cases = [
      [{ hz: Number.POSITIVE_INFINITY, gainDb: 0 }],
      [{ hz: 0, gainDb: 0 }],
      [{ hz: -5, gainDb: 0 }],
      [
        { hz: 100, gainDb: 0 },
        { hz: 50, gainDb: 0 },
      ],
      [
        { hz: 100, gainDb: 0 },
        { hz: 100, gainDb: 0 },
      ],
      [{ hz: 100, gainDb: Number.NaN }],
      [{ hz: '100', gainDb: 0 }],
    ];
    for (const points of cases) {
      expect(() => parseEqualizerResponse(validBody({ points }), 0)).toThrow();
    }
  });

  it('rejects a non-positive sampleRate', () => {
    expect(() => parseEqualizerResponse(validBody({ sampleRate: 0 }), 0)).toThrow();
    expect(() => parseEqualizerResponse(validBody({ sampleRate: -48000 }), 0)).toThrow();
  });

  it('rejects a skipped index outside the posted filters', () => {
    const skipped = [{ index: 1, type: 'LowPass', reason: 'add_failed' }];
    expect(() => parseEqualizerResponse(validBody({ skipped }), 1)).toThrow();
    const negative = [{ index: -1, type: 'LowPass', reason: 'add_failed' }];
    expect(() => parseEqualizerResponse(validBody({ skipped: negative }), 1)).toThrow();
  });

  it('rejects a repeated skipped index', () => {
    const skipped = [
      { index: 0, type: 'LowPass', reason: 'add_failed' },
      { index: 0, type: 'LowPass', reason: 'add_failed' },
    ];
    expect(() => parseEqualizerResponse(validBody({ skipped }), 2)).toThrow();
  });

  it.each([
    ['enabled missing', { enabled: undefined }],
    ['enabled not a boolean', { enabled: 'true' }],
    ['sampleRate NaN', { sampleRate: Number.NaN }],
    ['skipped not an array', { skipped: null }],
    [
      'skipped index not an integer',
      { skipped: [{ index: 0.5, type: 'LowPass', reason: 'add_failed' }] },
    ],
    ['skipped type not a string', { skipped: [{ index: 0, type: 1, reason: 'add_failed' }] }],
    ['skipped reason not a string', { skipped: [{ index: 0, type: 'LowPass', reason: 2 }] }],
    ['skipped entry not an object', { skipped: ['LowPass'] }],
    ['point not an object', { points: [20] }],
  ])('rejects a malformed body: %s', (_name, overrides) => {
    expect(() => parseEqualizerResponse(validBody(overrides), 2)).toThrow();
  });

  it('maps an unrecognised skip reason to unknown', () => {
    const skipped = [{ index: 0, type: 'LowPass', reason: 'something_new' }];
    expect(parseEqualizerResponse(validBody({ skipped }), 1).skipped[0]?.reason).toBe('unknown');
  });

  it('accepts a disabled response with empty skipped', () => {
    const parsed = parseEqualizerResponse(validBody({ enabled: false }), 2);
    expect(parsed.enabled).toBe(false);
    expect(parsed.skipped).toEqual([]);
  });
});

describe('fetchEqualizerResponse', () => {
  afterEach(() => {
    apiPost.mockReset();
  });

  it('posts the request to the response endpoint with the caller signal', async () => {
    apiPost.mockResolvedValueOnce(validBody());
    const controller = new AbortController();
    const request = requestFor(15000, 96000);
    const result = await fetchEqualizerResponse(request, controller.signal);
    expect(apiPost).toHaveBeenCalledWith(EQUALIZER_RESPONSE_URL, request, {
      signal: controller.signal,
    });
    expect(result.points).toHaveLength(3);
  });
});

/** A fetcher whose calls the test settles one by one. */
function controllableFetcher() {
  const calls: Array<{
    request: EqualizerResponseRequest;
    signal: AbortSignal | undefined;
    resolve: (value: EqualizerResponse) => void;
    reject: (reason: unknown) => void;
  }> = [];
  const fetcher = vi.fn(
    (request: EqualizerResponseRequest, signal?: AbortSignal) =>
      new Promise<EqualizerResponse>((resolve, reject) => {
        calls.push({ request, signal, resolve, reject });
      })
  );
  return { calls, fetcher };
}

/** The request a ready state answers, or undefined for any other state. */
function readyRequest(
  state: EqualizerResponseState | undefined
): EqualizerResponseRequest | undefined {
  return state?.status === 'ready' ? state.request : undefined;
}

/** Let promise continuations run. */
async function settle(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

describe('createEqualizerResponseLoader', () => {
  let states: EqualizerResponseState[];
  const onState = (s: EqualizerResponseState) => {
    states.push(s);
  };
  const last = () => states.at(-1);

  beforeEach(() => {
    vi.useFakeTimers();
    states = [];
    vi.mocked(loggers.settings.warn).mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('debounces a burst of requests into one fetch of the last body', () => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    loader.request(requestFor(1000));
    loader.request(requestFor(2000));
    loader.request(requestFor(3000));
    vi.advanceTimersByTime(EQUALIZER_RESPONSE_DEBOUNCE_MS - 1);
    expect(fetcher).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(calls[0].request).toEqual(requestFor(3000));
  });

  it.each(['newer first', 'older first'])(
    'ignores a response that settles after a newer request was issued (%s)',
    async order => {
      const { calls, fetcher } = controllableFetcher();
      const loader = createEqualizerResponseLoader(onState, { fetcher });
      const a = requestFor(1000);
      const b = requestFor(2000);
      loader.request(a);
      vi.runAllTimers();
      loader.request(b);
      vi.runAllTimers();
      const callA = calls.at(0);
      const callB = calls.at(1);
      if (!callA || !callB) throw new Error('expected two fetches');
      if (order === 'newer first') {
        callB.resolve(responseFor(b));
        await settle();
        callA.resolve(responseFor(a));
      } else {
        callA.resolve(responseFor(a));
        await settle();
        callB.resolve(responseFor(b));
      }
      await settle();
      expect(last()?.status).toBe('ready');
      expect(readyRequest(last())).toEqual(b);
      expect(states.filter(s => s.status === 'ready')).toHaveLength(1);
    }
  );

  it('drops an in-flight response that settles during a newer edit debounce window', async () => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    const a = requestFor(1000);
    loader.request(a);
    vi.runAllTimers();
    loader.request(requestFor(2000));
    const callA = calls.at(0);
    if (!callA) throw new Error('expected a fetch');
    expect(callA.signal?.aborted).toBe(true);
    callA.resolve(responseFor(a));
    await settle();
    expect(states.some(s => s.status === 'ready')).toBe(false);
    expect(last()?.status).toBe('loading');
  });

  it('restores the cached ready response when an edit is reverted', async () => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    const a = requestFor(1000);
    loader.request(a);
    vi.runAllTimers();
    calls[0].resolve(responseFor(a));
    await settle();
    // Revert inside the debounce window.
    loader.request(requestFor(2000));
    loader.request(a);
    expect(last()).toEqual({ status: 'ready', request: a, response: responseFor(a) });
    vi.runAllTimers();
    expect(fetcher).toHaveBeenCalledTimes(1);
    // Revert while the other request is in flight.
    loader.request(requestFor(3000));
    vi.runAllTimers();
    expect(fetcher).toHaveBeenCalledTimes(2);
    loader.request(a);
    expect(calls[1].signal?.aborted).toBe(true);
    expect(last()?.status).toBe('ready');
    calls[1].resolve(responseFor(requestFor(3000)));
    await settle();
    expect(readyRequest(last())).toEqual(a);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('drops a superseded request that rejects with a non-abort error', async () => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    loader.request(requestFor(1000));
    vi.runAllTimers();
    const b = requestFor(2000);
    loader.request(b);
    calls[0].reject(new Error('server said no'));
    await settle();
    expect(states.some(s => s.status === 'error')).toBe(false);
    expect(loggers.settings.warn).not.toHaveBeenCalled();
    vi.runAllTimers();
    calls[1].resolve(responseFor(b));
    await settle();
    expect(last()?.status).toBe('ready');
  });

  it('does not resurrect a pre-error curve as previous', async () => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    const a = requestFor(1000);
    loader.request(a);
    vi.runAllTimers();
    calls[0].resolve(responseFor(a));
    await settle();
    loader.request(requestFor(2000));
    vi.runAllTimers();
    calls[1].reject(new Error('boom'));
    await settle();
    expect(last()?.status).toBe('error');
    loader.request(requestFor(3000));
    expect(last()).toEqual({ status: 'loading' });
  });

  it('retry cancels a pending debounce and emits loading', () => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    const a = requestFor(1000);
    loader.request(a);
    loader.retry();
    expect(last()).toEqual({ status: 'loading' });
    expect(fetcher).toHaveBeenCalledTimes(1);
    vi.runAllTimers();
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(calls[0].request).toEqual(a);
  });

  it('aborts the superseded request signal', () => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    loader.request(requestFor(1000));
    vi.runAllTimers();
    expect(calls[0].signal?.aborted).toBe(false);
    loader.request(requestFor(2000));
    expect(calls[0].signal?.aborted).toBe(true);
  });

  type Step =
    | { op: 'request'; body: EqualizerResponseRequest }
    | { op: 'flush' }
    | { op: 'resolveAll'; reverse: boolean }
    | { op: 'fail' }
    | { op: 'retry' };

  const A = requestFor(1000);
  const B = requestFor(2000);
  const C = requestFor(3000);
  const removed = buildEqualizerResponseRequest({ enabled: true, filters: [] });

  const sequences: Array<{ name: string; steps: Step[]; fetches: number }> = [
    {
      name: 'A, B, A inside the debounce window',
      steps: [
        { op: 'request', body: A },
        { op: 'flush' },
        { op: 'resolveAll', reverse: false },
        { op: 'request', body: B },
        { op: 'request', body: A },
        { op: 'flush' },
      ],
      fetches: 1,
    },
    {
      name: 'A, B, A while B is in flight',
      steps: [
        { op: 'request', body: A },
        { op: 'flush' },
        { op: 'resolveAll', reverse: false },
        { op: 'request', body: B },
        { op: 'flush' },
        { op: 'request', body: A },
        { op: 'resolveAll', reverse: true },
      ],
      fetches: 2,
    },
    {
      name: 'A, A',
      steps: [
        { op: 'request', body: A },
        { op: 'flush' },
        { op: 'request', body: A },
        { op: 'flush' },
        { op: 'resolveAll', reverse: false },
      ],
      fetches: 1,
    },
    {
      name: 'A, remove filter, add filter',
      steps: [
        { op: 'request', body: A },
        { op: 'flush' },
        { op: 'request', body: removed },
        { op: 'flush' },
        { op: 'request', body: B },
        { op: 'flush' },
        { op: 'resolveAll', reverse: true },
      ],
      fetches: 3,
    },
    {
      name: 'A, error, A',
      steps: [
        { op: 'request', body: A },
        { op: 'flush' },
        { op: 'fail' },
        { op: 'request', body: B },
        { op: 'request', body: A },
        { op: 'flush' },
        { op: 'resolveAll', reverse: false },
      ],
      fetches: 2,
    },
    {
      name: 'A, B, retry, C',
      steps: [
        { op: 'request', body: A },
        { op: 'flush' },
        { op: 'request', body: B },
        { op: 'flush' },
        { op: 'fail' },
        { op: 'retry' },
        { op: 'request', body: C },
        { op: 'flush' },
        { op: 'resolveAll', reverse: true },
      ],
      fetches: 4,
    },
  ];

  it.each(sequences)('ends on the last issued body: $name', async ({ steps, fetches }) => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    let lastIssued: EqualizerResponseRequest | undefined;
    let settled = 0;
    for (const step of steps) {
      switch (step.op) {
        case 'request':
          lastIssued = step.body;
          loader.request(step.body);
          break;
        case 'flush':
          vi.runAllTimers();
          break;
        case 'resolveAll': {
          const pending = calls.slice(settled);
          settled = calls.length;
          const ordered = step.reverse ? [...pending].reverse() : pending;
          for (const call of ordered) {
            call.resolve(responseFor(call.request));
            await settle();
          }
          break;
        }
        case 'fail': {
          const pending = calls.slice(settled);
          settled = calls.length;
          for (const call of pending) call.reject(new Error('boom'));
          await settle();
          break;
        }
        case 'retry':
          loader.retry();
          break;
      }
    }
    expect(last()?.status).toBe('ready');
    expect(readyRequest(last())).toEqual(lastIssued);
    expect(fetcher).toHaveBeenCalledTimes(fetches);
  });

  it('does not refetch an unchanged body', async () => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    loader.request(A);
    vi.runAllTimers();
    calls[0].resolve(responseFor(A));
    await settle();
    const count = states.length;
    loader.request(requestFor(1000));
    vi.runAllTimers();
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(states).toHaveLength(count);
  });

  it('refetches the same body after an error', async () => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    loader.request(A);
    vi.runAllTimers();
    calls[0].reject(new Error('boom'));
    await settle();
    expect(last()?.status).toBe('error');
    expect(loggers.settings.warn).toHaveBeenCalledTimes(1);
    loader.request(A);
    vi.runAllTimers();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('keeps the previous ready data while a new request is pending', async () => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    loader.request(A);
    vi.runAllTimers();
    calls[0].resolve(responseFor(A));
    await settle();
    loader.request(B);
    expect(last()).toEqual({
      status: 'loading',
      previous: { request: A, response: responseFor(A) },
    });
  });

  it('emits error for a malformed response', async () => {
    apiPost.mockResolvedValueOnce({ data: [] });
    const loader = createEqualizerResponseLoader(onState);
    loader.request(A);
    vi.runAllTimers();
    await settle();
    await settle();
    expect(last()).toEqual({ status: 'error' });
    apiPost.mockReset();
  });

  it('ignores AbortError and writes nothing after dispose', async () => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    loader.request(A);
    vi.runAllTimers();
    const count = states.length;
    loader.dispose();
    expect(calls[0].signal?.aborted).toBe(true);
    calls[0].reject(new DOMException('aborted', 'AbortError'));
    await settle();
    loader.request(B);
    loader.retry();
    vi.runAllTimers();
    expect(states).toHaveLength(count);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(loggers.settings.warn).not.toHaveBeenCalled();
  });

  it('cancels a pending debounced request on dispose', () => {
    const { fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    loader.request(A);
    loader.dispose();
    vi.runAllTimers();
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('retry sends the last body immediately', async () => {
    const { calls, fetcher } = controllableFetcher();
    const loader = createEqualizerResponseLoader(onState, { fetcher });
    loader.request(A);
    vi.runAllTimers();
    calls[0].reject(new Error('boom'));
    await settle();
    loader.retry();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(calls[1].request).toEqual(A);
    calls[1].resolve(responseFor(A));
    await settle();
    expect(last()?.status).toBe('ready');
  });
});

describe('findNearestPoint', () => {
  const points = [
    { hz: 100, gainDb: 1 },
    { hz: 200, gainDb: 2 },
    { hz: 400, gainDb: 3 },
  ];

  it('returns undefined for no points', () => {
    expect(findNearestPoint([], 100)).toBeUndefined();
  });

  it('returns the first point below the range', () => {
    expect(findNearestPoint(points, 10)).toEqual(points[0]);
  });

  it('returns the last point above the range', () => {
    expect(findNearestPoint(points, 1000)).toEqual(points[2]);
  });

  it('returns an exact hit', () => {
    expect(findNearestPoint(points, 200)).toEqual(points[1]);
  });

  it('returns the nearer neighbour and the lower one on a tie', () => {
    expect(findNearestPoint(points, 260)).toEqual(points[1]);
    expect(findNearestPoint(points, 350)).toEqual(points[2]);
    expect(findNearestPoint(points, 300)).toEqual(points[1]);
  });
});
