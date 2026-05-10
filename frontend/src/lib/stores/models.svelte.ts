import { untrack } from 'svelte';
import { api } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';

const logger = loggers.audio;

export interface BackendModel {
  id: string;
  name: string;
  category: string;
  minSampleRate?: number;
  recommendedSampleRate?: number;
}

export const DEFAULT_MODEL_ID = 'birdnet';

const FALLBACK_MODELS: BackendModel[] = [
  { id: DEFAULT_MODEL_ID, name: 'BirdNET v2.4 (TFLite)', category: 'bird' },
];

let fetchedModels = $state<BackendModel[]>([]);
let activeFetch: AbortController | null = null;

export const availableModels = $derived(fetchedModels.length > 0 ? fetchedModels : FALLBACK_MODELS);

export function fetchModels(): () => void {
  if (fetchedModels.length > 0 || activeFetch) {
    return () => {};
  }

  const controller = new AbortController();
  activeFetch = controller;

  untrack(() => {
    api
      .get<BackendModel[]>('/api/v2/models', { signal: controller.signal })
      .then(data => {
        if (Array.isArray(data) && data.length > 0) {
          fetchedModels = data;
        } else {
          logger.warn('Fetched models response is empty or not an array', {
            component: 'modelsStore',
          });
        }
      })
      .catch((err: unknown) => {
        if (err instanceof Error && err.name !== 'AbortError') {
          logger.error('Failed to fetch models', err, {
            component: 'modelsStore',
            action: 'fetchModels',
          });
        }
      })
      .finally(() => {
        activeFetch = null;
      });
  });

  return () => controller.abort();
}
