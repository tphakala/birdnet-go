/**
 * Acoustic model availability store.
 *
 * Mirrors the classifier verdict served by GET /api/v2/system/inference
 * (`acousticModelsState`, `defaultTargets`, and the loaded models whose every
 * analysis fails) and the planned analysis cadence (`analysisCadence`) as
 * module-level rune state shared by the dashboard banner, the audio source
 * editors and the false positive filter readout on the analysis settings page.
 *
 * - subscribeAcousticModels(): fetch once for the first subscriber (and again on
 *   every remount after the last one left), cached for the rest. Editors use it.
 * - watchAcousticModels(): subscribe plus one shared topology SSE while any
 *   watcher is mounted, so the banner clears the moment a model loads or recovers
 *   and the cadence readout follows a re-plan. The dashboard banner and the
 *   analysis settings page use it; watchers share the one SSE.
 *
 * There is no polling: state refreshes on (re)mount, on SSE (re)connect, on the
 * topology-changed event (debounced, since one change can announce itself more
 * than once, and queued behind a request already running) and on an explicit
 * refresh or invalidate. The endpoint and the
 * stream are auth-protected, so a guest viewer never calls them and the state
 * stays "unknown".
 */
import { untrack } from 'svelte';
import { api } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';
import { isGuestMode } from '$lib/stores/appState.svelte';
import { buildAppUrl } from '$lib/utils/urlHelpers';
import { ReconnectingEventSource } from '$lib/utils/ReconnectingEventSource';
import { isPlainObject } from '$lib/utils/security';
import {
  MODEL_HEALTH_FAILING,
  type AnalysisCadenceInfo,
  type AnalysisCadenceModel,
  type AnalysisCadenceStatus,
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
/**
 * Trailing debounce for topology-changed events: a re-plan, a reconfigure and a
 * model load can each announce the same change, so a burst becomes one fetch.
 */
export const TOPOLOGY_REFRESH_DEBOUNCE_MS = 300;
const STATE_OK = 'ok';

/** Last classifier verdict string as served; null until the first successful fetch. */
let state = $state<string | null>(null);
/** Registry IDs of the default targets, in DefaultTargets order. */
let defaultTargets = $state<string[]>([]);
/** Loaded models whose every analysis window fails, in snapshot order. */
let failingModels = $state<FailingModel[]>([]);
/** Planned analysis cadence; null until a plan is published, or on older servers. */
let cadence = $state<AnalysisCadenceInfo | null>(null);
/** True once a fetch has succeeded; the state is then meaningful. */
let loaded = $state(false);
/** True when the most recent fetch failed (a stale `state` may still be shown). */
let error = $state(false);

let subscribers = 0;
let watchers = 0;
let inFlight: Promise<void> | null = null;
/** One fetch queued behind inFlight by invalidateAcousticModels, shared by its callers. */
let queued: Promise<void> | null = null;
let topologySource: ReconnectingEventSource | null = null;
let topologyRefreshTimer: ReturnType<typeof setTimeout> | null = null;

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

/**
 * The planned analysis cadence from the last successful fetch, or null when the
 * server has not published a plan yet, is too old to report one, or sent a
 * malformed one.
 */
export function analysisCadence(): AnalysisCadenceInfo | null {
  return cadence;
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
    if (!isPlainObject(m)) continue;
    const { id, name, health } = m;
    const state = isPlainObject(health) ? health.state : undefined;
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

const CADENCE_STATUSES: readonly AnalysisCadenceStatus[] = [
  'ok',
  'capped',
  'overloaded',
  'filterOff',
];

function isCadenceStatus(value: unknown): value is AnalysisCadenceStatus {
  return CADENCE_STATUSES.some(status => status === value);
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value);
}

/** Validates one cadence model entry; null when a field the readout needs is malformed. */
function cadenceModelOf(value: unknown): AnalysisCadenceModel | null {
  if (!isPlainObject(value)) return null;
  const { id, name, clipMs, stepMs, confirmations, windowsInReference } = value;
  if (
    typeof id !== 'string' ||
    !isFiniteNumber(clipMs) ||
    !isFiniteNumber(stepMs) ||
    !isFiniteNumber(confirmations) ||
    !isFiniteNumber(windowsInReference)
  ) {
    return null;
  }
  return {
    id,
    name: typeof name === 'string' && name !== '' ? name : id,
    clipMs,
    stepMs,
    confirmations,
    windowsInReference,
  };
}

/**
 * Validates the snapshot's `analysisCadence`; null when absent or malformed.
 * Fields nothing in the UI reads (the duty estimates and probe latencies) are
 * not kept.
 */
function analysisCadenceOf(value: unknown): AnalysisCadenceInfo | null {
  if (!isPlainObject(value)) return null;
  const {
    status,
    filterLevel,
    configuredOverlapSec,
    effectiveOverlapSec,
    minBaseStepMs,
    sourceCount,
    modelCount,
    unknownLatencyModels,
    models,
  } = value;
  if (
    !isCadenceStatus(status) ||
    !isFiniteNumber(filterLevel) ||
    !isFiniteNumber(configuredOverlapSec) ||
    !isFiniteNumber(effectiveOverlapSec) ||
    !Array.isArray(models)
  ) {
    return null;
  }
  const numberOr = (v: unknown): number => (isFiniteNumber(v) ? v : 0);
  return {
    status,
    filterLevel,
    configuredOverlapSec,
    effectiveOverlapSec,
    minBaseStepMs: numberOr(minBaseStepMs),
    sourceCount: numberOr(sourceCount),
    modelCount: numberOr(modelCount),
    unknownLatencyModels: Array.isArray(unknownLatencyModels)
      ? unknownLatencyModels.filter((id): id is string => typeof id === 'string')
      : [],
    models: models.map(cadenceModelOf).filter((m): m is AnalysisCadenceModel => m !== null),
  };
}

function applySnapshot(data: unknown): void {
  const snapshot: Record<string, unknown> = isPlainObject(data) ? data : {};
  // An older server omits the field; treat that like the "" sentinel (no verdict).
  state = typeof snapshot.acousticModelsState === 'string' ? snapshot.acousticModelsState : '';
  defaultTargets = Array.isArray(snapshot.defaultTargets)
    ? snapshot.defaultTargets.filter((target): target is string => typeof target === 'string')
    : [];
  failingModels = failingModelsOf(snapshot.models);
  // An older server, or one whose pipeline has not published a plan yet, omits it.
  cadence = analysisCadenceOf(snapshot.analysisCadence);
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
 * Re-read the verdict after a change the caller has just made (a settings save
 * the server has applied). A running request may predate that change, so a call
 * during one queues exactly one more fetch after it; every caller that arrives
 * meanwhile shares that queued fetch. Without a running request it is a plain
 * refresh. Guests get a resolved no-op.
 */
export function invalidateAcousticModels(): Promise<void> {
  if (isGuestMode()) return Promise.resolve();
  if (!inFlight) return refreshAcousticModels();
  queued ??= inFlight.then(() => {
    queued = null;
    return refreshAcousticModels();
  });
  return queued;
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

  source.addEventListener(TOPOLOGY_EVENT, scheduleTopologyRefresh);

  // The server sends `connected` on every (re)connection. The first one lands
  // right after the subscribe fetch, so it only refreshes when that fetch has
  // not succeeded; later ones mean a reconnect (server restart) and refresh
  // unless a fetch is already running.
  let initialConnect = true;
  source.addEventListener(CONNECTED_EVENT, () => {
    if (initialConnect) {
      initialConnect = false;
      if (loaded && !error) return;
    }
    if (inFlight) return;
    void refreshAcousticModels();
  });
}

/**
 * Refreshes once a burst of topology-changed events has gone quiet. The event
 * announces a server change that a request already running may predate, so the
 * refresh queues behind it.
 */
function scheduleTopologyRefresh(): void {
  cancelTopologyRefresh();
  topologyRefreshTimer = setTimeout(() => {
    topologyRefreshTimer = null;
    void invalidateAcousticModels();
  }, TOPOLOGY_REFRESH_DEBOUNCE_MS);
}

function cancelTopologyRefresh(): void {
  if (topologyRefreshTimer === null) return;
  clearTimeout(topologyRefreshTimer);
  topologyRefreshTimer = null;
}

function closeTopologyStream(): void {
  cancelTopologyRefresh();
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
  cadence = null;
  loaded = false;
  error = false;
  subscribers = 0;
  watchers = 0;
  inFlight = null;
  queued = null;
}
