/**
 * Acoustic model availability store.
 *
 * Mirrors the classifier verdict served by GET /api/v2/system/inference
 * (`acousticModelsState`, `defaultTargets`, and the loaded models whose every
 * analysis fails) as module-level rune state shared by the dashboard banner and
 * the audio source editors.
 *
 * - subscribeAcousticModels(): fetch once for the first subscriber (and again on
 *   every remount after the last one left), cached for the rest. Editors use it.
 * - watchAcousticModels(): subscribe plus one shared topology SSE while any
 *   watcher is mounted, so the banner clears the moment a model loads or recovers. Only the
 *   dashboard banner uses it, so a dashboard holds a single extra SSE.
 *
 * There are no timers: state refreshes on (re)mount, on SSE (re)connect and on
 * the topology-changed event. The endpoint and the stream are auth-protected, so
 * a guest viewer never calls them and the state stays "unknown".
 */
import { untrack } from 'svelte';
import { api } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';
import { isGuestMode } from '$lib/stores/appState.svelte';
import { buildAppUrl } from '$lib/utils/urlHelpers';
import { ReconnectingEventSource } from '$lib/utils/ReconnectingEventSource';
import {
  MODEL_HEALTH_FAILING,
  type InferenceStatusResponse,
} from '$lib/desktop/features/system/inference.types';
import { isNoAcousticModelState, type AcousticModelAvailability } from '$lib/types/models';

const logger = loggers.ui;

const INFERENCE_ENDPOINT = '/api/v2/system/inference';
const METRICS_STREAM_ENDPOINT = '/api/v2/system/metrics/stream';

/**
 * `?metrics=` filter naming no real metric key. The stream then carries only its
 * connected, heartbeat and topology events, never per-metric samples.
 */
export const TOPOLOGY_ONLY_FILTER = 'inference.topology';

/** Must match the backend constant (system.inference_topology_changed). */
export const TOPOLOGY_EVENT = 'system.inference_topology_changed';

const CONNECTED_EVENT = 'connected';
const SSE_MAX_RETRY_MS = 30000;
const STATE_OK = 'ok';

/** Last classifier verdict string as served; null until the first successful fetch. */
let state = $state<string | null>(null);
/** Registry IDs of the default targets, in DefaultTargets order. */
let defaultTargets = $state<string[]>([]);
/** Loaded models whose every analysis window fails, in snapshot order. */
let failingModels = $state<FailingModel[]>([]);
/** True once a fetch has succeeded; the state is then meaningful. */
let loaded = $state(false);
/** True when the most recent fetch failed (a stale `state` may still be shown). */
let error = $state(false);

let subscribers = 0;
let watchers = 0;
let inFlight: Promise<void> | null = null;
let topologySource: ReconnectingEventSource | null = null;

export function acousticModelsState(): string | null {
  return state;
}

export function acousticModelsLoaded(): boolean {
  return loaded;
}

export function acousticModelsError(): boolean {
  return error;
}

export function acousticDefaultTargets(): readonly string[] {
  return defaultTargets;
}

/** A loaded model that fails every analysis window. */
export interface FailingModel {
  id: string;
  name: string;
}

/** Loaded models whose every analysis fails (health state "failing"). */
export function acousticFailingModels(): readonly FailingModel[] {
  return failingModels;
}

/** Picks the failing models out of a snapshot's `models` list, tolerating older servers. */
function failingModelsOf(models: unknown): FailingModel[] {
  if (!Array.isArray(models)) return [];
  const out: FailingModel[] = [];
  for (const m of models) {
    if (typeof m !== 'object' || m === null) continue;
    const { id, name, health } = m as { id?: unknown; name?: unknown; health?: unknown };
    const state =
      typeof health === 'object' && health !== null
        ? (health as { state?: unknown }).state
        : undefined;
    if (state === MODEL_HEALTH_FAILING && typeof id === 'string') {
      out.push({ id, name: typeof name === 'string' && name !== '' ? name : id });
    }
  }
  return out;
}

/**
 * The editors' view of availability. Unknown until a fetch succeeds and for the
 * "" sentinel or any unrecognised verdict; none for the two no-model verdicts;
 * ready (with the registry-ID default targets) for "ok".
 */
export function acousticModelAvailability(): AcousticModelAvailability {
  if (!loaded || state === null) return { kind: 'unknown' };
  if (isNoAcousticModelState(state)) return { kind: 'none', reason: state };
  if (state === STATE_OK) return { kind: 'ready', defaultTargets };
  return { kind: 'unknown' };
}

function applySnapshot(data: unknown): void {
  const snapshot: Partial<InferenceStatusResponse> =
    typeof data === 'object' && data !== null ? (data as Partial<InferenceStatusResponse>) : {};
  // An older server omits the field; treat that like the "" sentinel (no verdict).
  state = typeof snapshot.acousticModelsState === 'string' ? snapshot.acousticModelsState : '';
  defaultTargets = Array.isArray(snapshot.defaultTargets)
    ? snapshot.defaultTargets.filter((target): target is string => typeof target === 'string')
    : [];
  failingModels = failingModelsOf(snapshot.models);
}

async function fetchSnapshot(): Promise<void> {
  try {
    const data = await api.get<InferenceStatusResponse>(INFERENCE_ENDPOINT);
    applySnapshot(data);
    loaded = true;
    error = false;
  } catch (err: unknown) {
    error = true;
    logger.warn('Failed to fetch acoustic model state', {
      component: 'acousticModelsStore',
      error: err instanceof Error ? err.message : String(err),
    });
  }
}

/**
 * Re-read the verdict. Concurrent callers share one request; guests get a
 * resolved no-op because the endpoint would only answer 401.
 */
export function refreshAcousticModels(): Promise<void> {
  if (isGuestMode()) return Promise.resolve();
  if (inFlight) return inFlight;
  const run = fetchSnapshot().finally(() => {
    if (inFlight === run) inFlight = null;
  });
  inFlight = run;
  return run;
}

/**
 * Register an interest in the verdict. Fetches for the first subscriber (so a
 * remount after everyone left refreshes) and for any subscriber while nothing
 * has loaded yet (so a failed fetch is retried). Returns the unsubscribe.
 */
export function subscribeAcousticModels(): () => void {
  subscribers++;
  // untrack: callers invoke this from $effect, and the fetch decision must not
  // register `loaded` as an effect dependency (that would re-run the effect and
  // refetch as soon as the first fetch lands).
  untrack(() => {
    if (subscribers === 1 || !loaded) {
      void refreshAcousticModels();
    }
  });
  return () => {
    subscribers = Math.max(0, subscribers - 1);
  };
}

function openTopologyStream(): void {
  if (topologySource || isGuestMode()) return;
  const url = buildAppUrl(
    `${METRICS_STREAM_ENDPOINT}?metrics=${encodeURIComponent(TOPOLOGY_ONLY_FILTER)}`
  );
  let source: ReconnectingEventSource;
  try {
    source = new ReconnectingEventSource(url, { max_retry_time: SSE_MAX_RETRY_MS });
  } catch (err: unknown) {
    // EventSource is unavailable (no browser support); mount-time refreshes still apply.
    logger.warn('Acoustic model topology stream unavailable', {
      component: 'acousticModelsStore',
      error: err instanceof Error ? err.message : String(err),
    });
    return;
  }
  topologySource = source;

  source.addEventListener(TOPOLOGY_EVENT, () => {
    void refreshAcousticModels();
  });

  // The server sends `connected` on every (re)connection. The first one lands
  // right after the subscribe fetch, so it only refreshes when that fetch has
  // not succeeded; later ones mean a reconnect (server restart) and always refresh.
  let initialConnect = true;
  source.addEventListener(CONNECTED_EVENT, () => {
    if (initialConnect) {
      initialConnect = false;
      if (loaded && !error) return;
    }
    void refreshAcousticModels();
  });
}

function closeTopologyStream(): void {
  if (!topologySource) return;
  topologySource.close();
  topologySource = null;
}

/**
 * Subscribe and additionally keep the topology SSE open while any watcher is
 * mounted, so the verdict updates live. Returns the unwatch.
 */
export function watchAcousticModels(): () => void {
  const unsubscribe = subscribeAcousticModels();
  watchers++;
  untrack(() => {
    if (watchers === 1) openTopologyStream();
  });
  return () => {
    unsubscribe();
    watchers = Math.max(0, watchers - 1);
    if (watchers === 0) closeTopologyStream();
  };
}

/** Test-only: close the stream and drop all cached state so each test starts cold. */
export function resetAcousticModelsForTest(): void {
  closeTopologyStream();
  state = null;
  defaultTargets = [];
  failingModels = [];
  loaded = false;
  error = false;
  subscribers = 0;
  watchers = 0;
  inFlight = null;
}
