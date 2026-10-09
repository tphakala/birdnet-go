/**
 * Tests for the acoustic model availability store.
 *
 * Covers the verdict mapping (ok / none_installed / load_failed / "" / unknown),
 * the guest guard, in-flight sharing, the queued fetch of an invalidate,
 * subscribe refcounting and the watch path that keeps a single topology SSE
 * open and refreshes on the (debounced) topology event and on reconnect.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  TOPOLOGY_EVENT,
  TOPOLOGY_ONLY_FILTER,
  TOPOLOGY_REFRESH_DEBOUNCE_MS,
  acousticDefaultTargets,
  acousticFailingModels,
  acousticModelAvailability,
  acousticModelsError,
  acousticModelsLoaded,
  acousticModelsState,
  analysisCadence,
  invalidateAcousticModels,
  refreshAcousticModels,
  resetAcousticModelsForTest,
  subscribeAcousticModels,
  watchAcousticModels,
} from './acousticModels.svelte';

type Listener = (event: Event) => void;

const { apiGet, isGuestMode, sse } = vi.hoisted(() => ({
  apiGet: vi.fn<(url: string) => Promise<unknown>>(),
  isGuestMode: vi.fn<() => boolean>(() => false),
  sse: {
    instances: [] as Array<{ url: string; listeners: Map<string, Listener>; closed: boolean }>,
  },
}));

vi.mock('$lib/utils/api', () => ({
  api: { get: apiGet },
}));

vi.mock('$lib/stores/appState.svelte', () => ({
  isGuestMode,
}));

vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class ReconnectingEventSource {
    private readonly record: { url: string; listeners: Map<string, Listener>; closed: boolean };
    constructor(url: string) {
      this.record = { url, listeners: new Map(), closed: false };
      sse.instances.push(this.record);
    }
    addEventListener(type: string, listener: Listener): void {
      this.record.listeners.set(type, listener);
    }
    close(): void {
      this.record.closed = true;
    }
  },
}));

const INFERENCE_URL = '/api/v2/system/inference';

function snapshot(acousticModelsState: unknown, defaultTargets: unknown = []) {
  return { acousticModelsState, defaultTargets, models: [], snapshotAtUnix: 1 };
}

function fire(index: number, type: string): void {
  const listener = sse.instances.at(index)?.listeners.get(type);
  expect(listener, `no ${type} listener on SSE #${index}`).toBeDefined();
  listener?.(new Event(type));
}

async function flush(): Promise<void> {
  await new Promise(resolve => setTimeout(resolve, 0));
}

describe('acousticModels store', () => {
  beforeEach(() => {
    resetAcousticModelsForTest();
    apiGet.mockReset();
    isGuestMode.mockReset();
    isGuestMode.mockReturnValue(false);
    sse.instances.length = 0;
  });

  afterEach(() => {
    resetAcousticModelsForTest();
  });

  it('starts unknown with no verdict', () => {
    expect(acousticModelsState()).toBeNull();
    expect(acousticModelsLoaded()).toBe(false);
    expect(acousticModelAvailability()).toEqual({ kind: 'unknown' });
  });

  describe('refreshAcousticModels', () => {
    it('maps "ok" to ready with the registry-ID default targets in order', async () => {
      apiGet.mockResolvedValueOnce(snapshot('ok', ['BirdNET_V2.4', 'Perch_V2']));
      await refreshAcousticModels();

      expect(apiGet).toHaveBeenCalledWith(INFERENCE_URL);
      expect(acousticModelsLoaded()).toBe(true);
      expect(acousticModelsError()).toBe(false);
      expect(acousticDefaultTargets()).toEqual(['BirdNET_V2.4', 'Perch_V2']);
      expect(acousticModelAvailability()).toEqual({
        kind: 'ready',
        defaultTargets: ['BirdNET_V2.4', 'Perch_V2'],
      });
    });

    it.each(['none_installed', 'load_failed'] as const)('maps %s to none', async reason => {
      apiGet.mockResolvedValueOnce(snapshot(reason));
      await refreshAcousticModels();
      expect(acousticModelsState()).toBe(reason);
      expect(acousticModelAvailability()).toEqual({ kind: 'none', reason });
    });

    it('lists the loaded models whose health is failing, tolerating malformed entries', async () => {
      apiGet.mockResolvedValueOnce({
        ...snapshot('ok'),
        models: [
          { id: 'BirdNET_V2.4', name: 'BirdNET v2.4', health: { state: 'failing' } },
          { id: 'Perch_V2', name: 'Perch v2', health: { state: 'ok' } },
          { id: 'Bat', name: '', health: { state: 'failing' } },
          { id: 'NoHealth', name: 'Old server' },
          null,
          'junk',
        ],
      });
      await refreshAcousticModels();

      expect(acousticFailingModels()).toEqual([
        { id: 'BirdNET_V2.4', name: 'BirdNET v2.4' },
        { id: 'Bat', name: 'Bat' },
      ]);
    });

    it('clears the failing models when the next snapshot has none', async () => {
      apiGet.mockResolvedValueOnce({
        ...snapshot('ok'),
        models: [{ id: 'm', name: 'M', health: { state: 'failing' } }],
      });
      await refreshAcousticModels();
      expect(acousticFailingModels()).toHaveLength(1);

      apiGet.mockResolvedValueOnce({
        ...snapshot('ok'),
        models: [{ id: 'm', name: 'M', health: { state: 'ok' } }],
      });
      await refreshAcousticModels();
      expect(acousticFailingModels()).toEqual([]);
    });

    it('treats the "" sentinel and a missing field as no verdict (unknown)', async () => {
      apiGet.mockResolvedValueOnce(snapshot(''));
      await refreshAcousticModels();
      expect(acousticModelsState()).toBe('');
      expect(acousticModelAvailability()).toEqual({ kind: 'unknown' });

      apiGet.mockResolvedValueOnce({ models: [], snapshotAtUnix: 1 });
      await refreshAcousticModels();
      expect(acousticModelsState()).toBe('');
      expect(acousticModelAvailability()).toEqual({ kind: 'unknown' });
    });

    it('keeps an unrecognised verdict but reports it as unknown', async () => {
      apiGet.mockResolvedValueOnce(snapshot('degraded'));
      await refreshAcousticModels();
      expect(acousticModelsState()).toBe('degraded');
      expect(acousticModelAvailability()).toEqual({ kind: 'unknown' });
    });

    it('keeps the planned analysis cadence from the same snapshot', async () => {
      const cadence = {
        status: 'capped',
        filterLevel: 5,
        configuredOverlapSec: 2.8,
        effectiveOverlapSec: 1.8,
        minBaseStepMs: 1200,
        estimatedDutyConfigured: 1.4,
        estimatedDutyEffective: 0.58,
        dutyCeiling: 0.75,
        sourceCount: 1,
        modelCount: 2,
        unknownLatencyModels: ['Perch_V2'],
        models: [
          {
            id: 'BirdNET_V2.4',
            name: 'BirdNET v2.4',
            clipMs: 3000,
            stepMs: 1200,
            probeLatencyMs: 166,
            confirmations: 4,
            windowsInReference: 5,
          },
        ],
      };
      apiGet.mockResolvedValueOnce({ ...snapshot('ok'), analysisCadence: cadence });
      await refreshAcousticModels();
      // The duty estimates and probe latencies are not kept: nothing reads them.
      expect(analysisCadence()).toEqual({
        status: 'capped',
        filterLevel: 5,
        configuredOverlapSec: 2.8,
        effectiveOverlapSec: 1.8,
        minBaseStepMs: 1200,
        sourceCount: 1,
        modelCount: 2,
        unknownLatencyModels: ['Perch_V2'],
        models: [
          {
            id: 'BirdNET_V2.4',
            name: 'BirdNET v2.4',
            clipMs: 3000,
            stepMs: 1200,
            confirmations: 4,
            windowsInReference: 5,
          },
        ],
      });
    });

    it('reports no cadence for an older server, a plan not yet published, or a malformed one', async () => {
      apiGet.mockResolvedValueOnce(snapshot('ok'));
      await refreshAcousticModels();
      expect(analysisCadence()).toBeNull();

      apiGet.mockResolvedValueOnce({
        ...snapshot('ok'),
        analysisCadence: { status: 'sideways', models: 'none' },
      });
      await refreshAcousticModels();
      expect(analysisCadence()).toBeNull();
    });

    const validCadence = () => ({
      status: 'ok',
      filterLevel: 2,
      configuredOverlapSec: 2,
      effectiveOverlapSec: 2,
      minBaseStepMs: 0,
      estimatedDutyConfigured: 0.2,
      estimatedDutyEffective: 0.2,
      dutyCeiling: 0.75,
      sourceCount: 1,
      modelCount: 1,
      unknownLatencyModels: [],
      models: [
        {
          id: 'BirdNET_V2.4',
          name: 'BirdNET v2.4',
          clipMs: 3000,
          stepMs: 1000,
          confirmations: 2,
          windowsInReference: 6,
        },
      ],
    });

    it.each([
      ['an unknown status', { status: 'sideways' }],
      ['a missing filter level', { filterLevel: undefined }],
      ['a missing configured overlap', { configuredOverlapSec: undefined }],
      ['a non-finite effective overlap', { effectiveOverlapSec: Number.NaN }],
      ['models that are not a list', { models: 'none' }],
    ])('rejects a cadence with %s', async (_label, override) => {
      apiGet.mockResolvedValueOnce({
        ...snapshot('ok'),
        analysisCadence: { ...validCadence(), ...override },
      });
      await refreshAcousticModels();
      expect(analysisCadence()).toBeNull();
    });

    it.each([
      ['id', { id: 7 }],
      ['clipMs', { clipMs: '3000' }],
      ['stepMs', { stepMs: undefined }],
      ['confirmations', { confirmations: Number.POSITIVE_INFINITY }],
      ['windowsInReference', { windowsInReference: null }],
    ])('drops a cadence model with a bad %s', async (_field, override) => {
      const cadence = validCadence();
      const [model] = cadence.models;
      apiGet.mockResolvedValueOnce({
        ...snapshot('ok'),
        analysisCadence: { ...cadence, models: [{ ...model, ...override }] },
      });
      await refreshAcousticModels();
      expect(analysisCadence()?.models).toEqual([]);
    });

    it('clears a held cadence when the next snapshot has none', async () => {
      apiGet.mockResolvedValueOnce({ ...snapshot('ok'), analysisCadence: validCadence() });
      await refreshAcousticModels();
      expect(analysisCadence()).not.toBeNull();

      apiGet.mockResolvedValueOnce(snapshot('ok'));
      await refreshAcousticModels();
      expect(analysisCadence()).toBeNull();
    });

    it('defaults optional numbers that are null or not finite to zero', async () => {
      apiGet.mockResolvedValueOnce({
        ...snapshot('ok'),
        analysisCadence: { ...validCadence(), sourceCount: null, minBaseStepMs: Number.NaN },
      });
      await refreshAcousticModels();
      expect(analysisCadence()).toMatchObject({ sourceCount: 0, minBaseStepMs: 0 });
    });

    it('keeps only the string entries of unknownLatencyModels', async () => {
      apiGet.mockResolvedValueOnce({
        ...snapshot('ok'),
        analysisCadence: { ...validCadence(), unknownLatencyModels: [7, 'Perch_V2', null] },
      });
      await refreshAcousticModels();
      expect(analysisCadence()?.unknownLatencyModels).toEqual(['Perch_V2']);
    });

    it('defaults the optional numbers to zero', async () => {
      const rest: Record<string, unknown> = validCadence();
      for (const key of ['minBaseStepMs', 'sourceCount', 'modelCount']) {
        Reflect.deleteProperty(rest, key);
      }
      apiGet.mockResolvedValueOnce({ ...snapshot('ok'), analysisCadence: rest });
      await refreshAcousticModels();
      expect(analysisCadence()).toMatchObject({
        minBaseStepMs: 0,
        sourceCount: 0,
        modelCount: 0,
      });
    });

    it('drops malformed cadence model entries and a null unknown list', async () => {
      apiGet.mockResolvedValueOnce({
        ...snapshot('ok'),
        analysisCadence: {
          status: 'ok',
          filterLevel: 2,
          configuredOverlapSec: 2,
          effectiveOverlapSec: 2,
          minBaseStepMs: 0,
          estimatedDutyConfigured: 0.2,
          estimatedDutyEffective: 0.2,
          dutyCeiling: 0.75,
          sourceCount: 1,
          modelCount: 1,
          unknownLatencyModels: null,
          models: [
            null,
            { id: 'x' },
            {
              id: 'BirdNET_V2.4',
              name: 'BirdNET v2.4',
              clipMs: 3000,
              stepMs: 1000,
              confirmations: 2,
              windowsInReference: 6,
            },
          ],
        },
      });
      await refreshAcousticModels();
      const cadence = analysisCadence();
      expect(cadence?.unknownLatencyModels).toEqual([]);
      expect(cadence?.models.map(m => m.id)).toEqual(['BirdNET_V2.4']);
    });

    it('ignores a malformed defaultTargets payload', async () => {
      apiGet.mockResolvedValueOnce(snapshot('ok', 'BirdNET_V2.4'));
      await refreshAcousticModels();
      expect(acousticDefaultTargets()).toEqual([]);
    });

    it('never calls the auth-protected endpoint for a guest', async () => {
      isGuestMode.mockReturnValue(true);
      await refreshAcousticModels();
      expect(apiGet).not.toHaveBeenCalled();
      expect(acousticModelAvailability()).toEqual({ kind: 'unknown' });
    });

    it('records a failed fetch without throwing and stays unknown', async () => {
      apiGet.mockRejectedValueOnce(new Error('offline'));
      await expect(refreshAcousticModels()).resolves.toBeUndefined();
      expect(acousticModelsError()).toBe(true);
      expect(acousticModelsLoaded()).toBe(false);
      expect(acousticModelAvailability()).toEqual({ kind: 'unknown' });
    });

    it('shares one request between concurrent callers', async () => {
      let resolve!: (value: unknown) => void;
      apiGet.mockImplementationOnce(() => new Promise(res => (resolve = res)));

      const first = refreshAcousticModels();
      const second = refreshAcousticModels();
      expect(second).toBe(first);
      expect(apiGet).toHaveBeenCalledTimes(1);

      resolve(snapshot('ok'));
      await first;
      expect(apiGet).toHaveBeenCalledTimes(1);
      expect(acousticModelsState()).toBe('ok');
    });
  });

  describe('invalidateAcousticModels', () => {
    it('fetches at once when nothing is running', async () => {
      apiGet.mockResolvedValueOnce(snapshot('ok'));
      await invalidateAcousticModels();
      expect(apiGet).toHaveBeenCalledTimes(1);
      expect(acousticModelsState()).toBe('ok');
    });

    it('queues one fetch behind a running one and shares it between later callers', async () => {
      let resolve!: (value: unknown) => void;
      apiGet.mockImplementationOnce(() => new Promise(res => (resolve = res)));
      apiGet.mockResolvedValueOnce(snapshot('none_installed'));

      const first = refreshAcousticModels();
      const second = invalidateAcousticModels();
      const third = invalidateAcousticModels();
      expect(second).not.toBe(first);
      expect(third).toBe(second);
      expect(apiGet).toHaveBeenCalledTimes(1);

      // The running request was sent before the change the later callers want,
      // so they get exactly one more request, and its answer wins.
      resolve(snapshot('ok'));
      await second;
      expect(apiGet).toHaveBeenCalledTimes(2);
      expect(acousticModelsState()).toBe('none_installed');

      // Nothing stays queued: a later invalidate is a single new request.
      apiGet.mockResolvedValueOnce(snapshot('ok'));
      await invalidateAcousticModels();
      expect(apiGet).toHaveBeenCalledTimes(3);
    });

    it('never calls the auth-protected endpoint for a guest', async () => {
      isGuestMode.mockReturnValue(true);
      await invalidateAcousticModels();
      expect(apiGet).not.toHaveBeenCalled();
    });
  });

  describe('subscribeAcousticModels', () => {
    it('fetches for the first subscriber and serves later ones from cache', async () => {
      apiGet.mockResolvedValue(snapshot('ok'));
      const first = subscribeAcousticModels();
      await flush();
      const second = subscribeAcousticModels();
      await flush();

      expect(apiGet).toHaveBeenCalledTimes(1);
      first();
      second();
    });

    it('refetches when a subscriber mounts after everyone left', async () => {
      apiGet.mockResolvedValue(snapshot('ok'));
      const first = subscribeAcousticModels();
      await flush();
      first();

      const second = subscribeAcousticModels();
      await flush();
      expect(apiGet).toHaveBeenCalledTimes(2);
      second();
    });

    it('retries for a new subscriber while nothing has loaded yet', async () => {
      apiGet.mockRejectedValueOnce(new Error('offline'));
      const first = subscribeAcousticModels();
      await flush();
      expect(acousticModelsLoaded()).toBe(false);

      apiGet.mockResolvedValueOnce(snapshot('none_installed'));
      const second = subscribeAcousticModels();
      await flush();
      expect(apiGet).toHaveBeenCalledTimes(2);
      expect(acousticModelAvailability()).toEqual({ kind: 'none', reason: 'none_installed' });
      first();
      second();
    });
  });

  describe('watchAcousticModels', () => {
    it('opens one topology-only stream for any number of watchers and closes it with the last', async () => {
      apiGet.mockResolvedValue(snapshot('none_installed'));
      const first = watchAcousticModels();
      const second = watchAcousticModels();
      await flush();

      expect(sse.instances).toHaveLength(1);
      expect(sse.instances[0]?.url).toContain('/api/v2/system/metrics/stream');
      expect(sse.instances[0]?.url).toContain(
        `metrics=${encodeURIComponent(TOPOLOGY_ONLY_FILTER)}`
      );

      first();
      expect(sse.instances[0]?.closed).toBe(false);
      second();
      expect(sse.instances[0]?.closed).toBe(true);
    });

    it('refreshes once a burst of topology-changed events goes quiet', async () => {
      vi.useFakeTimers();
      try {
        apiGet.mockResolvedValueOnce(snapshot('none_installed'));
        const unwatch = watchAcousticModels();
        await vi.advanceTimersByTimeAsync(0);
        expect(acousticModelAvailability()).toEqual({ kind: 'none', reason: 'none_installed' });

        apiGet.mockResolvedValueOnce(snapshot('ok', ['BirdNET_V2.4']));
        fire(0, TOPOLOGY_EVENT);
        await vi.advanceTimersByTimeAsync(TOPOLOGY_REFRESH_DEBOUNCE_MS - 1);
        fire(0, TOPOLOGY_EVENT);
        await vi.advanceTimersByTimeAsync(TOPOLOGY_REFRESH_DEBOUNCE_MS - 1);
        expect(apiGet).toHaveBeenCalledTimes(1);

        await vi.advanceTimersByTimeAsync(1);
        expect(apiGet).toHaveBeenCalledTimes(2);
        expect(acousticModelAvailability()).toEqual({
          kind: 'ready',
          defaultTargets: ['BirdNET_V2.4'],
        });
        unwatch();
      } finally {
        vi.useRealTimers();
      }
    });

    it('drops a pending topology refresh when the last watcher leaves', async () => {
      vi.useFakeTimers();
      try {
        apiGet.mockResolvedValue(snapshot('ok'));
        const unwatch = watchAcousticModels();
        await vi.advanceTimersByTimeAsync(0);
        fire(0, TOPOLOGY_EVENT);
        unwatch();
        await vi.advanceTimersByTimeAsync(TOPOLOGY_REFRESH_DEBOUNCE_MS);
        expect(apiGet).toHaveBeenCalledTimes(1);
      } finally {
        vi.useRealTimers();
      }
    });

    it('skips the initial connected event once loaded but refreshes on a reconnect', async () => {
      apiGet.mockResolvedValue(snapshot('ok'));
      const unwatch = watchAcousticModels();
      await flush();
      expect(apiGet).toHaveBeenCalledTimes(1);

      fire(0, 'connected');
      await flush();
      expect(apiGet).toHaveBeenCalledTimes(1);

      fire(0, 'connected');
      await flush();
      expect(apiGet).toHaveBeenCalledTimes(2);
      unwatch();
    });

    it('skips a reconnect refresh while a fetch is running', async () => {
      apiGet.mockResolvedValueOnce(snapshot('ok'));
      const unwatch = watchAcousticModels();
      await flush();
      fire(0, 'connected'); // initial connect, already loaded

      let resolve!: (value: unknown) => void;
      apiGet.mockImplementationOnce(() => new Promise(res => (resolve = res)));
      const running = invalidateAcousticModels();
      expect(apiGet).toHaveBeenCalledTimes(2);

      fire(0, 'connected');
      await flush();
      expect(apiGet).toHaveBeenCalledTimes(2);

      resolve(snapshot('ok'));
      await running;
      unwatch();
    });

    it('uses the initial connected event to retry a failed mount fetch', async () => {
      apiGet.mockRejectedValueOnce(new Error('offline'));
      const unwatch = watchAcousticModels();
      await flush();
      expect(acousticModelsLoaded()).toBe(false);

      apiGet.mockResolvedValueOnce(snapshot('load_failed'));
      fire(0, 'connected');
      await flush();
      expect(acousticModelAvailability()).toEqual({ kind: 'none', reason: 'load_failed' });
      unwatch();
    });

    it('opens no stream for a guest', async () => {
      isGuestMode.mockReturnValue(true);
      const unwatch = watchAcousticModels();
      await flush();
      expect(sse.instances).toHaveLength(0);
      expect(apiGet).not.toHaveBeenCalled();
      unwatch();
    });
  });
});
