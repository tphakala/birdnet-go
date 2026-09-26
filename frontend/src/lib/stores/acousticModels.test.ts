/**
 * Tests for the acoustic model availability store.
 *
 * Covers the verdict mapping (ok / none_installed / load_failed / "" / unknown),
 * the guest guard, in-flight de-duplication, subscribe refcounting and the
 * watch path that keeps a single topology SSE open and refreshes on the
 * topology event and on reconnect.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  TOPOLOGY_EVENT,
  TOPOLOGY_ONLY_FILTER,
  acousticDefaultTargets,
  acousticFailingModels,
  acousticModelAvailability,
  acousticModelsError,
  acousticModelsLoaded,
  acousticModelsState,
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

      // A later refresh is a new request again.
      apiGet.mockResolvedValueOnce(snapshot('ok'));
      await refreshAcousticModels();
      expect(apiGet).toHaveBeenCalledTimes(2);
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

    it('refreshes on the topology-changed event', async () => {
      apiGet.mockResolvedValueOnce(snapshot('none_installed'));
      const unwatch = watchAcousticModels();
      await flush();
      expect(acousticModelAvailability()).toEqual({ kind: 'none', reason: 'none_installed' });

      apiGet.mockResolvedValueOnce(snapshot('ok', ['BirdNET_V2.4']));
      fire(0, TOPOLOGY_EVENT);
      await flush();

      expect(apiGet).toHaveBeenCalledTimes(2);
      expect(acousticModelAvailability()).toEqual({
        kind: 'ready',
        defaultTargets: ['BirdNET_V2.4'],
      });
      unwatch();
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
