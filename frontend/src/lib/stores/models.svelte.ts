import { untrack } from 'svelte';
import { api } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';

const logger = loggers.audio;

/** One enabled classifier model as listed by GET /api/v2/models. */
export interface BackendModel {
  /** Config alias persisted in source model lists (e.g. "birdnet", "perch_v2"). */
  id: string;
  /**
   * Classifier registry ID (e.g. "BirdNET_V2.4"): the join key against the
   * registry IDs in GET /api/v2/system/inference `defaultTargets`. Absent on a
   * server that predates the field; callers then fall back to an id heuristic.
   */
  registryId?: string;
  name: string;
  category: string;
  minSampleRate?: number;
  recommendedSampleRate?: number;
}

export const DEFAULT_MODEL_ID = 'birdnet';

/**
 * Stand-in list used only while the model list has never been fetched or the
 * fetch failed, so the source editors stay usable when the API is unreachable.
 * Once a fetch has succeeded the real list is authoritative, even when it is
 * empty (N=0: no model enabled).
 */
const FALLBACK_MODELS: BackendModel[] = [
  { id: DEFAULT_MODEL_ID, name: 'BirdNET v2.4 (TFLite)', category: 'bird' },
];

type ModelsFetchState = 'idle' | 'loading' | 'loaded' | 'error';

let fetchedModels = $state<BackendModel[]>([]);
let fetchState = $state<ModelsFetchState>('idle');
let activeFetch: AbortController | null = null;
let subscribers = 0;

/**
 * Enabled models for the source editors. Empty while a fetch is in flight (the
 * editors render a labelled loading state), the fetched list once loaded (empty
 * at N=0), and the built-in fallback when nothing has been fetched yet or the
 * fetch failed.
 */
export function getAvailableModels(): BackendModel[] {
  switch (fetchState) {
    case 'loaded':
      return fetchedModels;
    case 'loading':
      return [];
    case 'idle':
    case 'error':
      return FALLBACK_MODELS;
  }
}

/** True once GET /api/v2/models has succeeded; the list is then authoritative even when empty. */
export function modelsLoaded(): boolean {
  return fetchState === 'loaded';
}

/** True while a fetch is in flight and no list has been loaded yet. */
export function modelsLoading(): boolean {
  return fetchState === 'loading';
}

function abortActiveFetch(): void {
  if (activeFetch) {
    activeFetch.abort();
    activeFetch = null;
  }
  if (fetchState === 'loading') {
    fetchState = 'idle';
  }
}

export function invalidateModels(): void {
  abortActiveFetch();
  fetchedModels = [];
  fetchState = 'idle';
}

export function fetchModels(): () => void {
  subscribers++;

  if (fetchState !== 'loaded' && !activeFetch) {
    const controller = new AbortController();
    activeFetch = controller;
    fetchState = 'loading';

    untrack(() => {
      void (async () => {
        try {
          const data = await api.get<BackendModel[]>('/api/v2/models', {
            signal: controller.signal,
          });
          if (controller.signal.aborted) return;
          if (Array.isArray(data)) {
            fetchedModels = data;
            fetchState = 'loaded';
          } else {
            logger.warn('Fetched models response is not an array', {
              component: 'modelsStore',
            });
            fetchState = 'error';
          }
        } catch (err: unknown) {
          if (controller.signal.aborted) return;
          if (err instanceof Error && err.name !== 'AbortError') {
            logger.error('Failed to fetch models', err, {
              component: 'modelsStore',
              action: 'fetchModels',
            });
          }
          fetchState = 'error';
        } finally {
          if (activeFetch === controller) {
            activeFetch = null;
          }
        }
      })();
    });
  }

  return () => {
    subscribers--;
    if (subscribers === 0) {
      abortActiveFetch();
    }
  };
}

/** Test-only: drop all cached state so each test starts from a cold store. */
export function resetModelsForTest(): void {
  abortActiveFetch();
  fetchedModels = [];
  fetchState = 'idle';
  subscribers = 0;
}
