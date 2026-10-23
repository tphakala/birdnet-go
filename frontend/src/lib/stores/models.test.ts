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
  resetModelsForTest,
  type BackendModel,
} from './models.svelte';

const { apiGet } = vi.hoisted(() => ({
  apiGet: vi.fn<(url: string, options?: { signal?: AbortSignal }) => Promise<unknown>>(),
}));

vi.mock('$lib/utils/api', () => ({
  api: { get: apiGet },
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
