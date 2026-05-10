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
let subscribers = 0;

export function getAvailableModels(): BackendModel[] {
  return fetchedModels.length > 0 ? fetchedModels : FALLBACK_MODELS;
}

export function fetchModels(): () => void {
  subscribers++;

  if (fetchedModels.length === 0 && !activeFetch) {
    const controller = new AbortController();
    activeFetch = controller;

    untrack(() => {
      api
        .get<BackendModel[]>('/api/v2/models', { signal: controller.signal })
        .then(data => {
          if (controller.signal.aborted) return;
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
          if (activeFetch === controller) {
            activeFetch = null;
          }
        });
    });
  }

  return () => {
    subscribers--;
    if (subscribers === 0 && activeFetch) {
      activeFetch.abort();
      activeFetch = null;
    }
  };
}
