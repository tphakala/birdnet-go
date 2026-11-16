/**
 * Tests for the enabled-models store.
 *
 * The store must expose the fetched list as authoritative once a fetch has
 * succeeded, including an EMPTY list at N=0 (no phantom BirdNET), while still
 * degrading to the built-in fallback when nothing has been fetched or the fetch
 * failed.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup } from '@testing-library/svelte';
import { renderTyped } from '../../test/render-helpers';
import ModelsSubscriber from './ModelsSubscriber.test.svelte';
import {
  DEFAULT_MODEL_ID,
  fetchModels,
  getAvailableModels,
  invalidateModels,
  modelsLoaded,
  modelsLoading,
  refreshModels,
  resetModelsForTest,
  watchModels,
  type BackendModel,
} from './models.svelte';

const { apiGet } = vi.hoisted(() => ({
  apiGet: vi.fn<(url: string, options?: { signal?: AbortSignal }) => Promise<unknown>>(),
}));

const { refreshKeepingEdits, settingsLoaded, watchAcoustic, unwatchAcoustic, topology, guest } =
  vi.hoisted(() => {
    // A minimal readable store standing in for settingsDataLoaded.
    const listeners = new Set<(value: boolean) => void>();
    const settingsLoaded = {
      value: true,
      subscribe(listener: (value: boolean) => void) {
        listener(settingsLoaded.value);
        listeners.add(listener);
        return () => listeners.delete(listener);
      },
      set(value: boolean) {
        settingsLoaded.value = value;
        for (const listener of [...listeners]) listener(value);
      },
    };
    return {
      refreshKeepingEdits: vi.fn<() => Promise<void>>(),
      settingsLoaded,
      watchAcoustic: vi.fn(),
      unwatchAcoustic: vi.fn(),
      topology: { listener: null as (() => void) | null, unlisten: vi.fn() },
      guest: { value: false },
    };
  });

vi.mock('$lib/utils/api', () => ({
  api: { get: apiGet },
}));

vi.mock('$lib/stores/settings', () => ({
  settingsActions: { refreshKeepingEdits },
  settingsDataLoaded: settingsLoaded,
}));

vi.mock('$lib/stores/appState.svelte', () => ({
  isGuestMode: () => guest.value,
}));

vi.mock('$lib/stores/acousticModels.svelte', () => ({
  watchAcousticModels: () => {
    watchAcoustic();
    return unwatchAcoustic;
  },
  onTopologyChanged: (listener: () => void) => {
    topology.listener = listener;
    return topology.unlisten;
  },
}));

const PERCH: BackendModel = {
  id: 'perch_v2',
  registryId: 'Perch_V2',
  name: 'Perch v2',
  category: 'bird',
};

/** A pending GET whose outcome the test controls. */
function deferredGet() {
  let resolve!: (value: unknown) => void;
  let reject!: (reason: unknown) => void;
  apiGet.mockImplementationOnce(
    () =>
      new Promise<unknown>((res, rej) => {
        resolve = res;
        reject = rej;
      })
  );
  return {
    resolve: (value: unknown) => resolve(value),
    reject: (reason: unknown) => reject(reason),
  };
}

/** Let the store's fetch continuation (after `await api.get`) run. */
async function settle(): Promise<void> {
  await new Promise(resolve => setTimeout(resolve, 0));
}

describe('models store', () => {
  beforeEach(() => {
    resetModelsForTest();
    apiGet.mockReset();
  });

  afterEach(() => {
    resetModelsForTest();
  });

  it('offers the built-in fallback before anything has been fetched', () => {
    expect(getAvailableModels().map(m => m.id)).toEqual([DEFAULT_MODEL_ID]);
    // The fallback is not a loaded model, so its name carries no backend suffix.
    expect(getAvailableModels().map(m => m.name)).toEqual(['BirdNET v2.4']);
    expect(modelsLoaded()).toBe(false);
    expect(modelsLoading()).toBe(false);
  });

  it('reports loading with an empty list while the fetch is in flight', () => {
    deferredGet();
    const unsubscribe = fetchModels();

    expect(modelsLoading()).toBe(true);
    expect(getAvailableModels()).toEqual([]);
    expect(apiGet).toHaveBeenCalledTimes(1);
    expect(apiGet.mock.calls[0]?.[0]).toBe('/api/v2/models');

    unsubscribe();
  });

  it('treats an empty fetched list as authoritative (N=0: no phantom BirdNET)', async () => {
    const pending = deferredGet();
    const unsubscribe = fetchModels();
    pending.resolve([]);
    await settle();

    expect(modelsLoaded()).toBe(true);
    expect(modelsLoading()).toBe(false);
    expect(getAvailableModels()).toEqual([]);

    unsubscribe();
  });

  it('exposes the fetched models including registryId', async () => {
    const pending = deferredGet();
    const unsubscribe = fetchModels();
    pending.resolve([PERCH]);
    await settle();

    expect(getAvailableModels()).toEqual([PERCH]);
    expect(getAvailableModels()[0]?.registryId).toBe('Perch_V2');

    unsubscribe();
  });

  it('does not refetch for a second subscriber once loaded', async () => {
    const pending = deferredGet();
    const first = fetchModels();
    pending.resolve([PERCH]);
    await settle();

    const second = fetchModels();
    expect(apiGet).toHaveBeenCalledTimes(1);

    first();
    second();
  });

  it('falls back to the built-in list when the fetch fails, and retries on the next subscribe', async () => {
    const failing = deferredGet();
    const first = fetchModels();
    failing.reject(new Error('boom'));
    await settle();

    expect(modelsLoaded()).toBe(false);
    expect(modelsLoading()).toBe(false);
    expect(getAvailableModels().map(m => m.id)).toEqual([DEFAULT_MODEL_ID]);
    first();

    const retry = deferredGet();
    const second = fetchModels();
    expect(apiGet).toHaveBeenCalledTimes(2);
    retry.resolve([PERCH]);
    await settle();
    expect(getAvailableModels()).toEqual([PERCH]);
    second();
  });

  it('aborts an in-flight fetch when the last subscriber leaves and returns to the fallback', () => {
    deferredGet();
    const unsubscribe = fetchModels();
    const signal = apiGet.mock.calls[0]?.[1]?.signal;
    expect(signal?.aborted).toBe(false);

    unsubscribe();

    expect(signal?.aborted).toBe(true);
    expect(modelsLoading()).toBe(false);
    expect(getAvailableModels().map(m => m.id)).toEqual([DEFAULT_MODEL_ID]);
  });

  it('invalidateModels drops the loaded list so the next subscriber refetches', async () => {
    const pending = deferredGet();
    const first = fetchModels();
    pending.resolve([PERCH]);
    await settle();
    first();

    invalidateModels();
    expect(modelsLoaded()).toBe(false);
    expect(getAvailableModels().map(m => m.id)).toEqual([DEFAULT_MODEL_ID]);

    deferredGet();
    const second = fetchModels();
    expect(apiGet).toHaveBeenCalledTimes(2);
    second();
  });
});

describe('models store subscribed from an effect', () => {
  beforeEach(() => {
    resetModelsForTest();
    apiGet.mockReset();
  });

  afterEach(() => {
    // Unmount before the reset: unmounting afterwards would drive the
    // subscriber count to -1 and leak into the next test.
    cleanup();
    resetModelsForTest();
  });

  it('runs the subscribing effect once and sends one request while the fetch is pending', () => {
    deferredGet();
    const onEffectRun = vi.fn();

    const view = renderTyped(ModelsSubscriber, { props: { onEffectRun } });

    expect(onEffectRun).toHaveBeenCalledTimes(1);
    expect(apiGet).toHaveBeenCalledTimes(1);
    expect(apiGet.mock.calls[0]?.[1]?.signal?.aborted).toBe(false);
    expect(view.getByTestId('loading').textContent).toBe('true');
  });

  it('updates readers when the fetch resolves without rerunning the subscribing effect', async () => {
    const pending = deferredGet();
    const onEffectRun = vi.fn();
    const view = renderTyped(ModelsSubscriber, { props: { onEffectRun } });

    pending.resolve([PERCH]);
    await settle();

    expect(view.getByTestId('loading').textContent).toBe('false');
    expect(view.getByTestId('count').textContent).toBe('1');
    expect(onEffectRun).toHaveBeenCalledTimes(1);
    expect(apiGet).toHaveBeenCalledTimes(1);
  });

  it('does not refetch while mounted after a failed fetch, and refetches on remount', async () => {
    const failing = deferredGet();
    const onEffectRun = vi.fn();
    const first = renderTyped(ModelsSubscriber, { props: { onEffectRun } });

    failing.reject(new Error('boom'));
    await settle();
    await settle();

    expect(apiGet).toHaveBeenCalledTimes(1);
    expect(onEffectRun).toHaveBeenCalledTimes(1);
    expect(getAvailableModels().map(m => m.id)).toEqual([DEFAULT_MODEL_ID]);

    first.unmount();
    deferredGet();
    renderTyped(ModelsSubscriber, { props: { onEffectRun } });

    expect(apiGet).toHaveBeenCalledTimes(2);
  });

  it('aborts the request when the last mounted subscriber unmounts, not before', () => {
    deferredGet();
    const a = renderTyped(ModelsSubscriber);
    const b = renderTyped(ModelsSubscriber);

    expect(apiGet).toHaveBeenCalledTimes(1);
    const signal = apiGet.mock.calls[0]?.[1]?.signal;

    a.unmount();
    expect(signal?.aborted).toBe(false);

    b.unmount();
    expect(signal?.aborted).toBe(true);
    expect(modelsLoading()).toBe(false);
  });

  it('ignores a response that arrives after its request was aborted and the store restarted', async () => {
    const stale = deferredGet();
    const first = renderTyped(ModelsSubscriber);
    first.unmount();

    deferredGet();
    const second = renderTyped(ModelsSubscriber);
    expect(apiGet).toHaveBeenCalledTimes(2);

    stale.resolve([PERCH]);
    await settle();

    // The aborted response must not mark the list loaded or free the newer request.
    expect(modelsLoaded()).toBe(false);
    expect(second.getByTestId('loading').textContent).toBe('true');
    expect(second.getByTestId('count').textContent).toBe('0');

    // A further subscriber still sees the newer request in flight.
    renderTyped(ModelsSubscriber);
    expect(apiGet).toHaveBeenCalledTimes(2);
  });

  it('ignores a rejection that arrives after its request was aborted and the store restarted', async () => {
    const stale = deferredGet();
    const first = renderTyped(ModelsSubscriber);
    first.unmount();

    deferredGet();
    const second = renderTyped(ModelsSubscriber);
    expect(apiGet).toHaveBeenCalledTimes(2);

    stale.reject(new DOMException('The operation was aborted.', 'AbortError'));
    await settle();

    // The aborted request must not flip the newer request to the error fallback.
    expect(modelsLoading()).toBe(true);
    expect(second.getByTestId('count').textContent).toBe('0');

    renderTyped(ModelsSubscriber);
    expect(apiGet).toHaveBeenCalledTimes(2);
  });

  type Step = 'mount' | 'resolve' | 'reject' | 'invalidate' | { unmount: number };

  it.each<{ name: string; steps: Step[]; requests: number; firstAborted: boolean }>([
    {
      name: 'mount, mount, unmount, unmount',
      steps: ['mount', 'mount', { unmount: 0 }, { unmount: 1 }],
      requests: 1,
      firstAborted: true,
    },
    {
      name: 'mount, resolve, unmount, mount (cached)',
      steps: ['mount', 'resolve', { unmount: 0 }, 'mount'],
      requests: 1,
      firstAborted: false,
    },
    {
      name: 'mount, reject, unmount, mount (retry on next subscribe)',
      steps: ['mount', 'reject', { unmount: 0 }, 'mount'],
      requests: 2,
      firstAborted: false,
    },
    {
      name: 'mount, unmount, mount (first aborted)',
      steps: ['mount', { unmount: 0 }, 'mount'],
      requests: 2,
      firstAborted: true,
    },
    {
      name: 'mount A, mount B, resolve, unmount A, mount C',
      steps: ['mount', 'mount', 'resolve', { unmount: 0 }, 'mount'],
      requests: 1,
      firstAborted: false,
    },
    {
      name: 'mount, resolve, invalidate, unmount, mount',
      steps: ['mount', 'resolve', 'invalidate', { unmount: 0 }, 'mount'],
      requests: 2,
      firstAborted: false,
    },
  ])('handles $name', async ({ steps, requests, firstAborted }) => {
    const settlers: Array<{ resolve: (v: unknown) => void; reject: (r: unknown) => void }> = [];
    apiGet.mockImplementation(
      () =>
        new Promise<unknown>((resolve, reject) => {
          settlers.push({ resolve, reject });
        })
    );
    const onEffectRun = vi.fn();
    const views: Array<ReturnType<typeof renderTyped>> = [];
    let mounts = 0;

    for (const step of steps) {
      if (step === 'mount') {
        views.push(renderTyped(ModelsSubscriber, { props: { onEffectRun } }));
        mounts++;
      } else if (step === 'resolve') {
        settlers[settlers.length - 1]?.resolve([PERCH]);
        await settle();
      } else if (step === 'reject') {
        settlers[settlers.length - 1]?.reject(new Error('boom'));
        await settle();
      } else if (step === 'invalidate') {
        invalidateModels();
      } else {
        views[step.unmount]?.unmount();
      }
    }

    expect(onEffectRun).toHaveBeenCalledTimes(mounts);
    expect(apiGet).toHaveBeenCalledTimes(requests);
    expect(apiGet.mock.calls[0]?.[1]?.signal?.aborted).toBe(firstAborted);
  });
});

describe('models store live refresh', () => {
  const BIRDNET: BackendModel = { id: 'birdnet', name: 'BirdNET v2.4', category: 'bird' };

  beforeEach(() => {
    resetModelsForTest();
    apiGet.mockReset();
    refreshKeepingEdits.mockReset().mockResolvedValue();
    settingsLoaded.value = true;
    watchAcoustic.mockReset();
    unwatchAcoustic.mockReset();
    topology.listener = null;
    topology.unlisten.mockReset();
    guest.value = false;
  });

  /** Load the store with an initial list through the public fetch path. */
  async function loadWith(models: BackendModel[]): Promise<() => void> {
    apiGet.mockResolvedValueOnce(models);
    const unwatch = watchModels();
    await settle();
    return unwatch;
  }

  it('keeps the shown list while a refresh is in flight and keeps it when the refresh fails', async () => {
    const unwatch = await loadWith([BIRDNET, PERCH]);

    const pending = deferredGet();
    const refresh = refreshModels();
    expect(modelsLoading()).toBe(false);
    expect(getAvailableModels().map(m => m.id)).toEqual(['birdnet', 'perch_v2']);

    pending.reject(new Error('offline'));
    await refresh;
    expect(getAvailableModels().map(m => m.id)).toEqual(['birdnet', 'perch_v2']);
    expect(modelsLoaded()).toBe(true);
    unwatch();
  });

  it('refetches on a topology event and refreshes the settings form once per changed id set', async () => {
    const unwatch = await loadWith([BIRDNET, PERCH]);
    expect(topology.listener).not.toBeNull();

    // A minus one: the id set changed.
    apiGet.mockResolvedValueOnce([BIRDNET]);
    topology.listener?.();
    await settle();
    expect(getAvailableModels().map(m => m.id)).toEqual(['birdnet']);
    expect(refreshKeepingEdits).toHaveBeenCalledTimes(1);

    // Same set again (a backend change only): list updates, form is left alone.
    apiGet.mockResolvedValueOnce([{ ...BIRDNET, backend: 'ONNX' }]);
    topology.listener?.();
    await settle();
    expect(getAvailableModels()[0]?.backend).toBe('ONNX');
    expect(refreshKeepingEdits).toHaveBeenCalledTimes(1);

    // A again: the id set changed back.
    apiGet.mockResolvedValueOnce([BIRDNET, PERCH]);
    topology.listener?.();
    await settle();
    expect(getAvailableModels().map(m => m.id)).toEqual(['birdnet', 'perch_v2']);
    expect(refreshKeepingEdits).toHaveBeenCalledTimes(2);
    unwatch();
  });

  it('refetches once more when a topology event arrives during the first fetch', async () => {
    const first = deferredGet();
    const unwatch = watchModels();
    topology.listener?.();
    expect(apiGet).toHaveBeenCalledTimes(1);

    apiGet.mockResolvedValueOnce([BIRDNET, PERCH]);
    first.resolve([BIRDNET]);
    await settle();
    await settle();

    expect(apiGet).toHaveBeenCalledTimes(2);
    expect(getAvailableModels().map(m => m.id)).toEqual(['birdnet', 'perch_v2']);
    unwatch();
  });

  it('does a plain fetch, not an in-place refresh, before the first list has loaded', async () => {
    apiGet.mockResolvedValueOnce([BIRDNET]);
    await refreshModels();
    await settle();

    expect(apiGet).toHaveBeenCalledTimes(1);
    expect(modelsLoaded()).toBe(true);
    expect(refreshKeepingEdits).not.toHaveBeenCalled();
  });

  it('drops a refresh response that lands after the store was invalidated', async () => {
    const unwatch = await loadWith([BIRDNET, PERCH]);

    const pending = deferredGet();
    const refresh = refreshModels();
    invalidateModels();
    pending.resolve([BIRDNET]);
    await refresh;

    expect(modelsLoaded()).toBe(false);
    expect(refreshKeepingEdits).not.toHaveBeenCalled();
    unwatch();
  });

  it('waits for the initial settings load before refreshing the form after a list change', async () => {
    const unwatch = await loadWith([BIRDNET, PERCH]);
    settingsLoaded.set(false);

    apiGet.mockResolvedValueOnce([BIRDNET]);
    await refreshModels();
    expect(refreshKeepingEdits).not.toHaveBeenCalled();

    settingsLoaded.set(true);
    await settle();
    expect(refreshKeepingEdits).toHaveBeenCalledTimes(1);

    // Done: a later refresh with the same ids leaves the form alone.
    apiGet.mockResolvedValueOnce([BIRDNET]);
    await refreshModels();
    expect(refreshKeepingEdits).toHaveBeenCalledTimes(1);
    unwatch();
  });

  it('keeps a later list change pending when an earlier form refresh succeeds after it', async () => {
    const unwatch = await loadWith([BIRDNET, PERCH]);
    settingsLoaded.set(false);
    apiGet.mockResolvedValueOnce([BIRDNET]);
    await refreshModels();

    // Rebase A starts when the settings load, and is still running when a
    // second change arrives whose own rebase B fails.
    let finishA: () => void = () => {};
    refreshKeepingEdits.mockImplementationOnce(
      () => new Promise<void>(resolve => (finishA = resolve))
    );
    settingsLoaded.set(true);
    refreshKeepingEdits.mockRejectedValueOnce(new Error('settings offline'));
    apiGet.mockResolvedValueOnce([]);
    await refreshModels();
    finishA();
    await settle();
    expect(refreshKeepingEdits).toHaveBeenCalledTimes(2);

    // The second change is still pending, so the next refresh retries it.
    apiGet.mockResolvedValueOnce([]);
    await refreshModels();
    expect(refreshKeepingEdits).toHaveBeenCalledTimes(3);
    unwatch();
  });

  it('retries the settings refresh on the next refresh when it failed', async () => {
    const unwatch = await loadWith([BIRDNET, PERCH]);

    refreshKeepingEdits.mockRejectedValueOnce(new Error('settings offline'));
    apiGet.mockResolvedValueOnce([BIRDNET]);
    await refreshModels();
    expect(refreshKeepingEdits).toHaveBeenCalledTimes(1);

    apiGet.mockResolvedValueOnce([BIRDNET]);
    await refreshModels();
    expect(refreshKeepingEdits).toHaveBeenCalledTimes(2);

    apiGet.mockResolvedValueOnce([BIRDNET]);
    await refreshModels();
    expect(refreshKeepingEdits).toHaveBeenCalledTimes(2);
    unwatch();
  });

  it('refetches a loaded list when a watcher arrives after nobody watched it', async () => {
    const first = await loadWith([BIRDNET, PERCH]);
    first();

    apiGet.mockResolvedValueOnce([BIRDNET]);
    const second = watchModels();
    await settle();

    expect(apiGet).toHaveBeenCalledTimes(2);
    expect(getAvailableModels().map(m => m.id)).toEqual(['birdnet']);
    second();
  });

  it('registers one topology listener for any number of watchers', async () => {
    const first = await loadWith([BIRDNET]);
    const listener = topology.listener;
    const second = watchModels();
    expect(topology.listener).toBe(listener);

    first();
    expect(topology.unlisten).not.toHaveBeenCalled();
    second();
    expect(topology.unlisten).toHaveBeenCalledTimes(1);
  });

  it('queues one more refetch behind a refresh that is running', async () => {
    const unwatch = await loadWith([BIRDNET]);

    const first = deferredGet();
    topology.listener?.();
    topology.listener?.();
    topology.listener?.();
    first.resolve([BIRDNET]);
    apiGet.mockResolvedValueOnce([BIRDNET]);
    await settle();
    await settle();

    // One initial load, one running refresh, one queued rerun for the burst.
    expect(apiGet).toHaveBeenCalledTimes(3);
    unwatch();
  });

  it('watches the topology stream and releases everything on unwatch', async () => {
    const unwatch = await loadWith([BIRDNET]);
    expect(watchAcoustic).toHaveBeenCalledTimes(1);

    unwatch();
    expect(topology.unlisten).toHaveBeenCalledTimes(1);
    expect(unwatchAcoustic).toHaveBeenCalledTimes(1);
  });

  it('gives a guest the plain fetch without the topology stream', async () => {
    guest.value = true;
    const unwatch = await loadWith([BIRDNET]);

    expect(getAvailableModels().map(m => m.id)).toEqual(['birdnet']);
    expect(watchAcoustic).not.toHaveBeenCalled();
    expect(topology.listener).toBeNull();
    unwatch();
  });
});
