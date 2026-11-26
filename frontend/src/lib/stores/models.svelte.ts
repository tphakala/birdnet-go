import { untrack } from 'svelte';
import { get } from 'svelte/store';
import { api } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';
import { isGuestMode } from '$lib/stores/appState.svelte';
import { watchAcousticModels, onTopologyChanged } from '$lib/stores/acousticModels.svelte';
import { settingsActions, settingsDataLoaded } from '$lib/stores/settings';

const logger = loggers.audio;

/** Load state of a listed model. */
export type ModelLoadState = 'loaded' | 'failed' | 'pending';

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
  /**
   * Live execution backend of the loaded model (e.g. "ONNX"). Absent when the
   * model is not loaded or the server withheld host data.
   */
  backend?: string;
  /** Live runtime precision of the loaded model (e.g. "INT8"). Absent like `backend`. */
  precision?: string;
  /**
   * Load state of the model: loaded, failed to load, or waiting to load. Absent
   * on a server that predates the field, which counts as loaded.
   */
  state?: ModelLoadState;
  /**
   * Stable OpenVINO reason code of the model's last load failure (for example
   * "openvino.gpu_failed"). Absent when the model is loaded, failed for another
   * reason, or the server withheld host data.
   */
  failureReason?: string;
}

export const DEFAULT_MODEL_ID = 'birdnet';

/**
 * Stand-in list used only while the model list has never been fetched or the
 * fetch failed, so the source editors stay usable when the API is unreachable.
 * Once a fetch has succeeded the real list is authoritative, even when it is
 * empty (N=0: no model enabled).
 */
const FALLBACK_MODELS: BackendModel[] = [
  { id: DEFAULT_MODEL_ID, name: 'BirdNET v2.4', category: 'bird' },
];

type ModelsFetchState = 'idle' | 'loading' | 'loaded' | 'error';

let fetchedModels = $state<BackendModel[]>([]);
let fetchState = $state<ModelsFetchState>('idle');
let activeFetch: AbortController | null = null;
let subscribers = 0;
let activeRefresh: AbortController | null = null;
let refreshQueued = false;
// Set while the settings form still has to drop aliases of removed models, and
// kept when that refresh fails so the next refresh retries it.
let settingsRefreshPending = false;
// Set while a pending form refresh waits for the initial settings load.
let stopWaitingForSettings: (() => void) | null = null;
// Counts model list changes, so a form refresh clears the pending flag only
// when no newer change arrived while it ran.
let settingsChangeGeneration = 0;
let watchers = 0;
let unlistenTopology: (() => void) | null = null;

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

/** True when the last fetch failed and no list has been loaded; the editors then show the fallback. */
export function modelsFailed(): boolean {
  return fetchState === 'error';
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
  activeRefresh?.abort();
  activeRefresh = null;
  refreshQueued = false;
  fetchedModels = [];
  fetchState = 'idle';
}

/**
 * Fetch the model list unless it is loaded or a fetch is already in flight.
 * Reads and writes `fetchState`, so it must run untracked (see fetchModels).
 */
function startFetchIfNeeded(): void {
  if (fetchState === 'loaded' || activeFetch) return;

  const controller = new AbortController();
  activeFetch = controller;
  fetchState = 'loading';
  void loadModels(controller);
}

async function loadModels(controller: AbortController): Promise<void> {
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
      // A topology event that arrived during this fetch may postdate its response.
      const rerun = refreshQueued && fetchState === 'loaded';
      refreshQueued = false;
      if (rerun) void refreshModels();
    }
  }
}

/**
 * Register an interest in the model list: fetches it unless it is loaded or a
 * fetch is in flight. Returns the unsubscribe, which aborts an in-flight fetch
 * when the last subscriber leaves.
 *
 * Callers subscribe from `$effect`, so the fetch decision runs untracked. A
 * tracked read of `fetchState` would make the effect depend on the state this
 * call writes: the effect would rerun at once, its cleanup would abort the
 * request and reset the state, and the loop would end only in
 * effect_update_depth_exceeded.
 */
export function fetchModels(): () => void {
  subscribers++;
  untrack(startFetchIfNeeded);

  return () => {
    subscribers--;
    if (subscribers === 0) {
      abortActiveFetch();
    }
  };
}

function idSet(models: readonly BackendModel[]): string {
  return models
    .map(m => m.id)
    .sort()
    .join('\n');
}

/**
 * Refetches the list in place: the shown list stays until the new one arrives, so
 * a refresh never flashes the loading state, and a failed refresh keeps the last
 * list. A refresh that arrives while one runs queues one more behind it, and one
 * that arrives while the first fetch runs queues one that starts only if that
 * fetch succeeds. When the set of listed ids changed, the settings form
 * is refreshed once, keeping unsaved edits, because the server strips aliases of
 * removed models from the sources. That form refresh waits for the initial
 * settings load when it is still running, and one that failed or was overtaken
 * by a newer list change is retried by the next refresh. Before the first
 * successful fetch it is the plain fetch.
 */
export async function refreshModels(): Promise<void> {
  if (fetchState !== 'loaded') {
    if (activeFetch) {
      refreshQueued = true;
    } else {
      untrack(startFetchIfNeeded);
    }
    return;
  }
  if (activeRefresh) {
    refreshQueued = true;
    return;
  }
  const controller = new AbortController();
  activeRefresh = controller;
  try {
    const data = await api.get<BackendModel[]>('/api/v2/models', { signal: controller.signal });
    if (controller.signal.aborted || !Array.isArray(data)) return;
    if (idSet(data) !== idSet(fetchedModels)) {
      settingsRefreshPending = true;
      settingsChangeGeneration++;
    }
    fetchedModels = data;
    if (settingsRefreshPending) await syncSettingsForm();
  } catch (err: unknown) {
    if (!controller.signal.aborted) {
      logger.warn('Failed to refresh models', {
        component: 'modelsStore',
        error: err instanceof Error ? err.message : String(err),
      });
    }
  } finally {
    if (activeRefresh === controller) activeRefresh = null;
    if (refreshQueued) {
      refreshQueued = false;
      void refreshModels();
    }
  }
}

/**
 * Subscribes to the model list and keeps it live: the list is fetched, and a
 * server topology change (a model installed, removed, loaded or unloaded)
 * refetches it over the shared topology stream. Guests get the plain fetch, since
 * the stream is for signed-in users only. Returns the unwatch.
 */
export function watchModels(): () => void {
  const unsubscribe = fetchModels();
  if (isGuestMode()) return unsubscribe;
  const unwatchAcoustic = watchAcousticModels();
  // One shared listener for all watchers, so one event refetches once.
  watchers++;
  if (watchers === 1) {
    unlistenTopology = onTopologyChanged(() => void refreshModels());
    // The list may have changed while nothing watched it, so a loaded list is
    // refetched when the first watcher arrives.
    untrack(() => {
      if (fetchState === 'loaded') void refreshModels();
    });
  }
  return () => {
    watchers--;
    if (watchers === 0) {
      unlistenTopology?.();
      unlistenTopology = null;
    }
    unwatchAcoustic();
    unsubscribe();
  };
}

/**
 * Drops aliases of removed models from the settings form. No rebase can run
 * before the settings have loaded, and a load already in flight may carry the
 * old list, so it waits for the load and runs then. The pending flag clears
 * only after a rebase that started after the latest list change succeeded.
 */
async function syncSettingsForm(): Promise<void> {
  if (!get(settingsDataLoaded)) {
    waitForSettingsLoad();
    return;
  }
  const generation = settingsChangeGeneration;
  await settingsActions.refreshKeepingEdits();
  if (generation === settingsChangeGeneration) settingsRefreshPending = false;
}

function waitForSettingsLoad(): void {
  if (stopWaitingForSettings) return;
  stopWaitingForSettings = settingsDataLoaded.subscribe(loaded => {
    // The first call runs inside subscribe, before the unsubscriber is stored.
    if (!loaded || !stopWaitingForSettings) return;
    stopWaitingForSettings();
    stopWaitingForSettings = null;
    if (!settingsRefreshPending) return;
    syncSettingsForm().catch((err: unknown) => {
      logger.warn('Failed to refresh settings after a model list change', {
        component: 'modelsStore',
        error: err instanceof Error ? err.message : String(err),
      });
    });
  });
}

/** Test-only: drop all cached state so each test starts from a cold store. */
export function resetModelsForTest(): void {
  unlistenTopology?.();
  unlistenTopology = null;
  watchers = 0;
  abortActiveFetch();
  activeRefresh?.abort();
  activeRefresh = null;
  refreshQueued = false;
  settingsRefreshPending = false;
  stopWaitingForSettings?.();
  stopWaitingForSettings = null;
  settingsChangeGeneration = 0;
  fetchedModels = [];
  fetchState = 'idle';
  subscribers = 0;
}
