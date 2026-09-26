/**
 * Tests for the enabled-models store.
 *
 * The store must expose the fetched list as authoritative once a fetch has
 * succeeded, including an EMPTY list at N=0 (no phantom BirdNET), while still
 * degrading to the built-in fallback when nothing has been fetched or the fetch
 * failed.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
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
