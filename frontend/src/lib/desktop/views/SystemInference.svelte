<!--
  SystemInference - AI Models & Inference subpage.

  Consumes the GET /api/v2/system/inference snapshot, renders hardware and
  inference backends, and per-model cards with a latency sparkline, compute
  device, approximate host RAM, a schedule/paused indicator, an activity pulse,
  a "Last heard" table of recent detections, and attached audio sources. Live
  updates arrive over the existing metrics SSE stream (SSE first, polling
  fallback), and the page re-fetches the snapshot when the backend broadcasts a
  topology change. A periodic ~30s snapshot refresh keeps headline stats and
  recent detections current without reconnecting the SSE stream.

  The Audio pipeline card is intentionally hidden for now (see the template);
  the backend still returns snapshot.audio for a future refactor.

  snapshot.models is the single source of truth: series for models that are
  not in the current snapshot are ignored (orphan-safe), and missing or null
  fields degrade gracefully instead of crashing.

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import { ReconnectingEventSource } from '$lib/utils/ReconnectingEventSource';
  import { loggers } from '$lib/utils/logger';
  import { connectionState } from '$lib/stores/connectionState.svelte';
  import { formatBytesCompact, formatNumber } from '$lib/utils/formatters';
  import { getLocalTimeString, formatLocalDateTime } from '$lib/utils/date';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import Badge from '$lib/desktop/components/ui/Badge.svelte';
  import StatusPill from '$lib/desktop/components/ui/StatusPill.svelte';
  import Sparkline from '$lib/desktop/features/system/components/Sparkline.svelte';
  import { Brain, Cpu, MemoryStick, Activity, Minus, Pause, MapPinOff } from '@lucide/svelte';
  import type {
    InferenceStatusResponse,
    InferenceModel,
    InferenceLastDetection,
    BackendStatus,
    OpenVINOBackendStatus,
  } from '$lib/desktop/features/system/inference.types';
  import type { StatusVariant } from '$lib/desktop/components/ui/StatusPill.svelte';

  const logger = loggers.ui;

  // Maximum number of sparkline data points to retain per series.
  const MAX_HISTORY_POINTS = 60;

  // Polling fallback interval in milliseconds (used when SSE is unavailable).
  const POLLING_INTERVAL_MS = 5000;

  // Snapshot endpoint and the topology-change SSE event name. The event string
  // must match the backend constant (system.inference_topology_changed).
  const INFERENCE_ENDPOINT = '/api/v2/system/inference';
  const TOPOLOGY_EVENT = 'system.inference_topology_changed';

  // Sparkline color, matching the existing system charts palette.
  const LATENCY_COLOR = '#3b82f6'; // blue

  // Interval for periodic snapshot-only refreshes (does not reconnect SSE).
  const SNAPSHOT_REFRESH_MS = 30000;

  // Conversions for spec display.
  const HZ_PER_KHZ = 1000;

  // Flat 0 baseline shown in the latency sparkline before real samples flow (the
  // chart needs >= 2 points to draw a line; an all-zeros series renders as a flat
  // line at the bottom). Used until the live series has at least two points.
  const EMPTY_SPARKLINE_BASELINE = [0, 0];

  // Tolerance (seconds) for treating the same species in two models' feeds as one
  // co-detection. Detection timestamps are per-model wall-clock at second
  // granularity, and models analyze different segment lengths, so co-detections of
  // one bird land a few seconds apart; this stays well under the per-species
  // throttle so it never matches two different occurrences within a model.
  const CO_DETECTION_TOLERANCE_SEC = 3;

  // Rows per column in the two-column Last-heard layout (backend retains 2x this).
  const LAST_HEARD_COLUMN_ROWS = 10;

  interface MetricPoint {
    timestamp: string;
    value: number;
  }

  interface MetricsHistoryResponse {
    metrics: Record<string, MetricPoint[]>;
  }

  // Snapshot of the current inference topology and stats.
  let snapshot = $state<InferenceStatusResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  // Per-series history keyed by the snapshot-provided metric key. Only keys
  // that belong to a current snapshot model are populated (orphan-safe).
  let seriesByKey = $state<Record<string, number[]>>({});

  // SSE connection reference.
  let metricsSSE: ReconnectingEventSource | null = null;

  // Polling fallback timeout reference.
  let pollingTimeout: ReturnType<typeof setTimeout> | null = null;

  // Component-level active flag shared between the lifecycle effect and the
  // async loaders, so a late resolve after unmount is a no-op.
  let componentActive = { current: false };

  // Generation counter for snapshot loads. Each loadSnapshot bumps it; in-flight
  // history/poll work checks it and bails if a newer load has started, so rapid
  // topology events cannot let a stale response overwrite newer state.
  let currentFetchId = 0;

  // Append a value to a history array, keeping it capped at MAX_HISTORY_POINTS.
  function appendHistory(arr: number[], value: number): number[] {
    const next = [...arr, value];
    return next.length > MAX_HISTORY_POINTS ? next.slice(next.length - MAX_HISTORY_POINTS) : next;
  }

  // Collect the per-model metric keys we actually consume live: avgMs feeds the
  // latency sparkline, and throughput feeds the activity pulse (its own sparkline
  // was removed, but the series still drives "is inference happening"). RTF and
  // error-rate are rendered from the 30s snapshot (model.stats.*), not a live
  // series, so they are intentionally NOT subscribed. The audio queue-depth key
  // is also omitted while the Audio card is hidden (see the template). When there
  // are no models this returns '' and the caller falls through to polling.
  function metricKeysParam(): string {
    if (!snapshot) return '';
    const keys: string[] = [];
    for (const m of snapshot.models) {
      keys.push(m.metricKeys.avgMs, m.metricKeys.throughput);
    }
    return keys.join(',');
  }

  // Set of metric keys belonging to current snapshot models.
  // Used to ignore series for models that are no longer present (orphan-safe).
  // Mirrors metricKeysParam: only the live-consumed keys (avgMs, throughput).
  // Derived so it is recomputed only when the snapshot changes.
  const validKeys = $derived.by(() => {
    const keys = new Set<string>();
    if (!snapshot) return keys;
    for (const m of snapshot.models) {
      keys.add(m.metricKeys.avgMs);
      keys.add(m.metricKeys.throughput);
    }
    return keys;
  });

  // Stable display order: sort by name (locale-aware), tie-broken by id, so cards
  // do not reshuffle when the backend returns models in a different order.
  let sortedModels = $derived(
    snapshot
      ? [...snapshot.models].sort((a, b) => {
          // Case-insensitive, matching the backend's name -> id ordering so the
          // client and API agree on order for mixed-case model names.
          const byName = a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
          return byName !== 0 ? byName : a.id.localeCompare(b.id);
        })
      : []
  );

  // True while the page has loaded a snapshot with no models and is polling for
  // them to appear. Lets poll() hand off to the live SSE transport once models
  // load (a topology event has no transport in the zero-models state).
  let awaitingModels = false;

  function disconnectStream(): void {
    if (metricsSSE) {
      metricsSSE.close();
      metricsSSE = null;
    }
  }

  function stopPolling(): void {
    if (pollingTimeout) {
      clearTimeout(pollingTimeout);
      pollingTimeout = null;
    }
  }

  // Seed historical series for the current snapshot's metric keys, then connect
  // the live stream. Transport is mutually exclusive (SSE vs polling), mirroring
  // System.svelte: a successful history seed goes live over SSE; any failure (or
  // having no models to stream yet) falls back to polling instead.
  async function loadHistory(active: { current: boolean }, fetchId: number): Promise<void> {
    const keys = metricKeysParam();
    if (!keys) {
      // No models loaded yet: nothing to stream. Poll the snapshot so the page
      // picks up models once they load (the topology SSE event has no transport here).
      awaitingModels = true;
      startPollingFallback(active, fetchId);
      return;
    }
    awaitingModels = false;
    try {
      const data = await api.get<MetricsHistoryResponse>(
        `/api/v2/system/metrics/history?points=${MAX_HISTORY_POINTS}&metrics=${encodeURIComponent(keys)}`
      );
      if (!active.current || fetchId !== currentFetchId) return;
      const next: Record<string, number[]> = {}; // rebuild fresh: drops orphan series for models no longer present
      for (const [key, points] of Object.entries(data.metrics)) {
        if (!validKeys.has(key)) continue;
        // eslint-disable-next-line security/detect-object-injection -- key is gated by validKeys membership
        next[key] = points.map(p => p.value);
      }
      seriesByKey = next;
      connectStream(); // go live only after a successful history seed (mirrors System.svelte)
    } catch {
      if (!active.current || fetchId !== currentFetchId) return;
      logger.debug('Inference metrics history not available, falling back to polling');
      startPollingFallback(active, fetchId);
    }
  }

  // Connect to the metrics SSE stream for live latency / RTF / throughput updates
  // and topology-change notifications. Closes any prior connection first.
  function connectStream(): void {
    disconnectStream();
    const keys = metricKeysParam();
    if (!keys) return;

    metricsSSE = new ReconnectingEventSource(
      `/api/v2/system/metrics/stream?metrics=${encodeURIComponent(keys)}`,
      { max_retry_time: 30000 }
    );

    metricsSSE.addEventListener('metrics', (event: Event) => {
      try {
        // eslint-disable-next-line no-undef
        const messageEvent = event as MessageEvent;
        const metrics = JSON.parse(messageEvent.data) as Record<string, { value: number }>;
        const next: Record<string, number[]> = { ...seriesByKey };
        let changed = false;
        for (const [key, point] of Object.entries(metrics)) {
          // Ignore orphan keys for models not in the current snapshot.
          if (!validKeys.has(key)) continue;
          // eslint-disable-next-line security/detect-object-injection -- key is gated by validKeys membership
          next[key] = appendHistory(next[key] ?? [], point.value);
          changed = true;
        }
        if (changed) seriesByKey = next;
      } catch {
        // Ignore malformed events.
      }
    });

    // Topology changed: re-fetch the snapshot so keys and the stream rebuild
    // for the new model/source layout. Close the current SSE first.
    metricsSSE.addEventListener(TOPOLOGY_EVENT, () => {
      if (!componentActive.current) return;
      disconnectStream();
      loadSnapshot(componentActive);
    });
  }

  // Re-fetch the snapshot on a fixed interval when SSE is unavailable. The loop
  // stays alive while offline but skips the actual fetch.
  function startPollingFallback(active: { current: boolean }, fetchId: number): void {
    // Clear any scheduled poll so a re-entrant call (repeated topology re-fetch
    // failures, or zero-models then a later call) does not run overlapping loops.
    stopPolling();

    async function poll(): Promise<void> {
      if (!active.current || fetchId !== currentFetchId) return;

      if (!connectionState.isOnline) {
        // Skip the API call but keep the polling loop alive.
        if (active.current && fetchId === currentFetchId) {
          pollingTimeout = setTimeout(poll, POLLING_INTERVAL_MS);
        }
        return;
      }

      try {
        const data = await api.get<InferenceStatusResponse>(INFERENCE_ENDPOINT);
        if (!active.current || fetchId !== currentFetchId) return;
        snapshot = data;
        error = null;
        // If we started with no models and they have now loaded, hand off from
        // snapshot polling to the live SSE transport (seed history + stream).
        if (awaitingModels && metricKeysParam()) {
          stopPolling();
          loadHistory(active, fetchId);
          return;
        }
      } catch {
        // Silently ignore polling failures; keep showing the last snapshot.
      }

      if (active.current && fetchId === currentFetchId) {
        pollingTimeout = setTimeout(poll, POLLING_INTERVAL_MS);
      }
    }

    pollingTimeout = setTimeout(poll, POLLING_INTERVAL_MS);
  }

  // Periodic snapshot-only refresh: updates snapshot values (headline stats,
  // lastDetection, audio) WITHOUT reconnecting the SSE stream or reseeding
  // history. Respects componentActive and currentFetchId so a superseded
  // refresh bails without clobbering newer topology-triggered state.
  async function refreshSnapshot(
    active: { current: boolean },
    activeFetchId: number
  ): Promise<void> {
    if (!active.current || activeFetchId !== currentFetchId) return;
    try {
      const data = await api.get<InferenceStatusResponse>(INFERENCE_ENDPOINT);
      if (!active.current || activeFetchId !== currentFetchId) return;
      snapshot = data;
    } catch {
      // Silently ignore refresh failures; keep showing the last snapshot.
    }
  }

  // Load the snapshot, then seed history and pick a live transport. loadHistory
  // owns the SSE-vs-poll decision (mutually exclusive), so loadSnapshot does not
  // connect the stream itself. On failure show a friendly error and poll.
  async function loadSnapshot(active: { current: boolean }): Promise<void> {
    // Bump the generation so any older in-flight history/poll work bails out and
    // cannot overwrite this load's state if responses arrive out of order.
    const fetchId = ++currentFetchId;
    try {
      const data = await api.get<InferenceStatusResponse>(INFERENCE_ENDPOINT);
      if (!active.current || fetchId !== currentFetchId) return;
      snapshot = data;
      loading = false;
      error = null;
      await loadHistory(active, fetchId);
    } catch (err: unknown) {
      if (!active.current || fetchId !== currentFetchId) return;
      logger.debug('Failed to load inference status', {
        error: err instanceof Error ? err.message : 'Unknown error',
      });
      // Keep a stale snapshot visible if one already exists; only surface the
      // error banner when there is nothing to show.
      error = t('system.inference.error');
      loading = false;
      startPollingFallback(active, fetchId);
    }
  }

  // Lifecycle: load on mount, start periodic snapshot refresh, tear down on unmount.
  $effect(() => {
    componentActive.current = true;
    loadSnapshot(componentActive);

    const snapshotInterval = setInterval(() => {
      refreshSnapshot(componentActive, currentFetchId);
    }, SNAPSHOT_REFRESH_MS);

    return () => {
      componentActive.current = false;
      clearInterval(snapshotInterval);
      disconnectStream();
      stopPolling();
    };
  });

  // --- Display helpers -----------------------------------------------------

  function backendVariant(available: boolean): StatusVariant {
    return available ? 'success' : 'neutral';
  }

  function backendLabel(available: boolean): string {
    return available ? t('system.inference.available') : t('system.inference.notAvailable');
  }

  // ONNX/TFLite backend rows share the same shape.
  interface BackendRow {
    label: string;
    status: BackendStatus;
  }

  let simpleBackends = $derived.by((): BackendRow[] => {
    if (!snapshot) return [];
    return [
      { label: t('system.inference.backendTflite'), status: snapshot.backends.tflite },
      { label: t('system.inference.backendOnnx'), status: snapshot.backends.onnx },
    ];
  });

  let openvino = $derived<OpenVINOBackendStatus | null>(
    snapshot ? snapshot.backends.openvino : null
  );

  // Spec line for a model: sample rate in kHz, segment length in seconds.
  function sampleRateKhz(hz: number): string {
    return (hz / HZ_PER_KHZ).toFixed(hz % HZ_PER_KHZ === 0 ? 0 : 1);
  }

  // RTF is absent or meaningless when there are no invocations.
  function rtfDisplay(model: InferenceModel): string {
    const { invocations, rtf } = model.stats;
    if (invocations <= 0 || rtf == null) return '-';
    return rtf.toFixed(3);
  }

  function ramDisplay(model: InferenceModel): string {
    const bytes = model.memory.approxRssBytes;
    if (bytes == null) return t('system.inference.notMeasured');
    return formatBytesCompact(bytes);
  }

  // Throughput, avgLatency, and maxLatency are meaningless at zero invocations:
  // show a dash placeholder matching the rtfDisplay pattern.
  function throughputDisplay(model: InferenceModel, latestValue: number): string {
    if (model.stats.invocations <= 0) return '-';
    return latestValue.toFixed(1) + t('system.inference.throughputUnit');
  }

  function avgLatencyDisplay(model: InferenceModel): string {
    if (model.stats.invocations <= 0) return '-';
    return model.stats.avgMs.toFixed(1) + ' ' + t('system.inference.unitMs');
  }

  function maxLatencyDisplay(model: InferenceModel): string {
    if (model.stats.invocations <= 0) return '-';
    return model.stats.maxMs.toFixed(1) + ' ' + t('system.inference.unitMs');
  }

  // Compact, readable summary for the latency sparkline: current value and the
  // series peak in ms (the bare line carries no scale on its own).
  function latencySummary(series: number[]): string {
    if (series.length === 0) return '';
    const current = series[series.length - 1] ?? 0;
    const peak = Math.max(...series);
    return `${current.toFixed(1)} ${t('system.inference.unitMs')} · ${t('system.inference.peak')} ${peak.toFixed(1)}`;
  }

  // Short names of other loaded models whose feed contains the same species within
  // CO_DETECTION_TOLERANCE_SEC of this detection, for cross-model correlation.
  function coDetectingModels(modelId: string, d: InferenceLastDetection): string[] {
    if (!snapshot) return [];
    const key = d.scientificName || d.species;
    if (!key) return [];
    const names: string[] = [];
    for (const m of snapshot.models) {
      if (m.id === modelId) continue;
      const hit = m.recentDetections?.some(
        o =>
          (o.scientificName || o.species) === key &&
          Math.abs(o.atUnix - d.atUnix) <= CO_DETECTION_TOLERANCE_SEC
      );
      if (hit) names.push(m.detectionName || m.name);
    }
    return names;
  }
</script>

<!--
  Renders a jargon stat as "label: value" with a plain-English explanation
  available on hover (title) and to screen readers (sr-only + aria-describedby),
  mirroring the existing RTF / approximate-RAM pattern. helpId must be unique.
-->
{#snippet stat(label: string, help: string, value: string, helpId: string)}
  <span class="text-muted" title={help}>
    {label}:
    <span class="font-mono tabular-nums text-base-content" aria-describedby={helpId}>{value}</span>
    <span id={helpId} class="sr-only">{help}</span>
  </span>
{/snippet}

<div class="space-y-4">
  {#if loading}
    <div
      class="flex items-center gap-3 p-4 text-sm text-base-content/70"
      role="status"
      aria-live="polite"
    >
      <span
        class="animate-spin motion-reduce:animate-none h-5 w-5 border-2 border-blue-500 border-t-transparent rounded-full"
        aria-hidden="true"
      ></span>
      <span>{t('system.inference.loading')}</span>
    </div>
  {:else if error && !snapshot}
    <div
      role="alert"
      class="p-4 rounded-lg bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400"
    >
      {error}
    </div>
  {:else if snapshot}
    <!-- Top context row: hardware and inference backends as compact cards.
         The Audio pipeline card is intentionally hidden for now (see below). -->
    <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
      <!-- Hardware -->
      <div
        class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm"
      >
        <h3 class="text-xs font-semibold uppercase tracking-wider mb-3 text-muted">
          {t('system.inference.sectionHardware')}
        </h3>
        <div class="space-y-2.5">
          {#if snapshot.hardware.arch}
            <div class="flex items-center gap-3">
              <Cpu class="w-3.5 h-3.5 shrink-0 text-muted" aria-hidden="true" />
              <span class="text-sm text-muted">{t('system.inference.architecture')}</span>
              <span class="text-sm font-mono tabular-nums truncate" title={snapshot.hardware.arch}
                >{snapshot.hardware.arch}</span
              >
            </div>
          {/if}
          {#if snapshot.hardware.cpuModel}
            <div class="flex items-center gap-3">
              <Cpu class="w-3.5 h-3.5 shrink-0 text-muted" aria-hidden="true" />
              <span class="text-sm text-muted">{t('system.inference.cpu')}</span>
              <span class="text-sm truncate" title={snapshot.hardware.cpuModel}
                >{snapshot.hardware.cpuModel}</span
              >
            </div>
          {/if}
          {#if snapshot.hardware.environment}
            <div class="flex items-center gap-3">
              <span class="text-sm text-muted">{t('system.inference.environment')}</span>
              <span class="text-sm truncate" title={snapshot.hardware.environment}
                >{snapshot.hardware.environment}</span
              >
            </div>
          {/if}
          <div class="flex items-center gap-3">
            <span
              class="text-sm text-muted"
              title={t('system.inference.fp16Help')}
              aria-describedby="help-fp16"
            >
              {t('system.inference.fp16')}
            </span>
            <span id="help-fp16" class="sr-only">{t('system.inference.fp16Help')}</span>
            {#if snapshot.hardware.fp16}
              <StatusPill variant="success" label={t('system.inference.fp16Supported')} size="xs" />
            {:else}
              <StatusPill
                variant="neutral"
                label={t('system.inference.fp16Unsupported')}
                size="xs"
              />
            {/if}
          </div>
        </div>
      </div>

      <!-- Inference backends -->
      <div
        class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm"
      >
        <h3 class="text-xs font-semibold uppercase tracking-wider mb-3 text-muted">
          {t('system.inference.sectionBackends')}
        </h3>
        <div class="space-y-2.5">
          {#each simpleBackends as row (row.label)}
            <div class="flex items-center gap-3 flex-wrap">
              <span class="text-sm min-w-32">{row.label}</span>
              <StatusPill
                variant={backendVariant(row.status.available)}
                label={backendLabel(row.status.available)}
                size="xs"
              />
              {#if row.status.available && row.status.initialized}
                <StatusPill variant="info" label={t('system.inference.initialized')} size="xs" />
              {/if}
              {#if row.status.version}
                <span class="text-xs text-muted font-mono tabular-nums">
                  {t('system.inference.version')}: {row.status.version}
                </span>
              {/if}
            </div>
          {/each}

          {#if openvino}
            <div class="flex items-center gap-3 flex-wrap">
              <span class="text-sm min-w-32">{t('system.inference.backendOpenvino')}</span>
              {#if !openvino.supported}
                <StatusPill
                  variant="neutral"
                  label={t('system.inference.notAvailable')}
                  size="xs"
                />
              {:else if openvino.active}
                <StatusPill variant="success" label={t('system.inference.active')} size="xs" />
              {:else}
                <StatusPill variant="neutral" label={t('system.inference.inactive')} size="xs" />
              {/if}
              {#if openvino.supported && openvino.devices && openvino.devices.length > 0}
                <span class="text-xs text-muted">{t('system.inference.devices')}:</span>
                {#each openvino.devices as device (device)}
                  <Badge variant="neutral" size="sm" text={device} />
                {/each}
              {/if}
            </div>
          {/if}
        </div>
      </div>

      <!--
        Audio pipeline card: intentionally DISABLED for now.

        As built it was low signal (a bare queue-depth / dropped-chunks readout)
        and it squeezed the Inference Backends card too narrow. It is hidden on
        purpose until it can be refactored into something genuinely useful
        (per-source pipeline health, backlog trends, drop-cause attribution).

        The backend still returns `snapshot.audio` and its i18n keys are kept, so
        re-enabling is just a matter of restoring the markup. Tracked in the
        Phase A spec (Forgejo #1144). Do NOT delete the audio types/fields.
      -->
    </div>

    <!-- Models -->
    <div>
      <h3 class="text-xs font-semibold uppercase tracking-wider mb-3 text-muted">
        {t('system.inference.sectionModels')}
      </h3>

      {#if sortedModels.length === 0}
        <div
          class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-6 shadow-sm text-center text-sm text-muted"
        >
          <p>{t('system.inference.noModels')}</p>
          <p class="mt-2">
            {t('system.inference.noModelsHint')}
            <a href={buildAppUrl('/ui/settings/audio')} class="text-primary underline">
              {t('system.inference.noModelsHintLink')}
            </a>
          </p>
        </div>
      {:else}
        <div class="space-y-3">
          {#each sortedModels as model (model.id)}
            {@const latencySeries = seriesByKey[model.metricKeys.avgMs] ?? []}
            {@const throughputSeries = seriesByKey[model.metricKeys.throughput] ?? []}
            {@const throughputLatest =
              throughputSeries.length > 0 ? throughputSeries[throughputSeries.length - 1] : 0}
            {@const isActive = throughputSeries.length > 0 && throughputLatest > 0}
            <div
              class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col gap-3"
            >
              <!-- Header -->
              <div class="flex items-center gap-2 flex-wrap">
                <Brain class="w-4 h-4 shrink-0 text-muted" aria-hidden="true" />
                <span class="text-sm font-semibold truncate">{model.name}</span>
                <Badge variant="primary" size="sm" text={model.backend} />
                {#if model.quantization}
                  <Badge variant="secondary" size="sm" text={model.quantization} />
                {/if}
                {#if model.device}
                  <Badge
                    variant="info"
                    size="sm"
                    text={model.device}
                    title={t('system.inference.deviceHelp')}
                  />
                {/if}
                {#if model.paused}
                  <!-- Schedule-gated model that is currently off-schedule: explain the
                       flat latency line instead of showing a bare "idle" dash. -->
                  <span
                    class="ml-auto flex items-center gap-1.5"
                    role="status"
                    aria-label={t('system.inference.activityPaused')}
                    title={t('system.inference.pausedScheduleHelp')}
                  >
                    <Pause class="w-3 h-3 shrink-0 text-amber-500" aria-hidden="true" />
                    <span class="text-xs text-amber-600 dark:text-amber-400">
                      {t('system.inference.paused')}{#if model.scheduleLabel}<span
                          class="text-muted">&nbsp;({model.scheduleLabel})</span
                        >{/if}
                    </span>
                  </span>
                {:else}
                  <span
                    class="ml-auto flex items-center gap-1"
                    role="status"
                    aria-label={isActive
                      ? t('system.inference.activityActive')
                      : t('system.inference.activityIdle')}
                  >
                    {#if isActive}
                      <Activity
                        class="w-3 h-3 text-green-500 animate-pulse motion-reduce:animate-none"
                        aria-hidden="true"
                      />
                    {:else}
                      <Minus class="w-3 h-3 text-base-content/30" aria-hidden="true" />
                    {/if}
                  </span>
                {/if}
              </div>

              <!-- Spec line -->
              <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs">
                <span class="text-muted">
                  {t('system.inference.sampleRate')}:
                  <span class="font-mono tabular-nums text-base-content">
                    {sampleRateKhz(model.spec.sampleRate)}
                    {t('system.inference.unitKhz')}
                  </span>
                </span>
                <span class="text-muted">
                  {t('system.inference.clipLength')}:
                  <span class="font-mono tabular-nums text-base-content">
                    {model.spec.clipLengthSec}
                    {t('system.inference.unitSec')}
                  </span>
                </span>
                <span class="text-muted">
                  {t('system.inference.species')}:
                  <span class="font-mono tabular-nums text-base-content">
                    {formatNumber(model.numSpecies)}
                  </span>
                </span>
              </div>

              <!-- Stats line -->
              <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs">
                {@render stat(
                  t('system.inference.invocations'),
                  t('system.inference.invocationsHelp'),
                  formatNumber(model.stats.invocations),
                  `help-invocations-${model.id}`
                )}
                <span class="text-muted">
                  {t('system.inference.avgLatency')}:
                  <span class="font-mono tabular-nums text-base-content">
                    {avgLatencyDisplay(model)}
                  </span>
                </span>
                <span class="text-muted">
                  {t('system.inference.maxLatency')}:
                  <span class="font-mono tabular-nums text-base-content">
                    {maxLatencyDisplay(model)}
                  </span>
                </span>
                <span class="text-muted" title={t('system.inference.rtfHelp')}>
                  {t('system.inference.rtf')}:
                  <span
                    class="font-mono tabular-nums text-base-content"
                    aria-describedby={`rtf-help-${model.id}`}>{rtfDisplay(model)}</span
                  >
                  <span id={`rtf-help-${model.id}`} class="sr-only">
                    {t('system.inference.rtfHelp')}
                  </span>
                </span>
                {@render stat(
                  t('system.inference.throughput'),
                  t('system.inference.throughputHelp'),
                  throughputDisplay(model, throughputLatest),
                  `help-throughput-${model.id}`
                )}
                {#if model.stats.errorRate !== undefined}
                  {@render stat(
                    t('system.inference.errorRate'),
                    t('system.inference.errorRateHelp'),
                    Math.round(model.stats.errorRate * 100) + '%',
                    `help-error-rate-${model.id}`
                  )}
                {/if}
                {#if model.stats.loadFailures !== undefined && model.stats.loadFailures > 0}
                  {@render stat(
                    t('system.inference.loadFailures'),
                    t('system.inference.loadFailuresHelp'),
                    String(model.stats.loadFailures),
                    `help-load-failures-${model.id}`
                  )}
                {/if}
              </div>

              <!-- Latency sparkline (full width) -->
              <div>
                <div class="text-xs text-muted mb-1 flex items-center gap-1">
                  <Activity class="w-3 h-3 shrink-0" aria-hidden="true" />
                  {t('system.inference.latencyChart')}
                  {#if latencySeries.length > 0}
                    <span class="ml-auto font-mono tabular-nums text-base-content">
                      {latencySummary(latencySeries)}
                    </span>
                  {/if}
                </div>
                <div class="h-10">
                  <!-- Before real samples flow, draw a flat 0 baseline (the chart
                       needs >= 2 points) instead of an empty/placeholder state. -->
                  <Sparkline
                    data={latencySeries.length >= 2 ? latencySeries : EMPTY_SPARKLINE_BASELINE}
                    color={LATENCY_COLOR}
                    decorative
                  />
                </div>
              </div>

              <!-- Recent detections (Last heard): a per-species-throttled feed (the
                   same species is recorded at most once per the model's segment
                   interval). Shown as two columns of ten (newest ten on the left,
                   the next ten on the right) with absolute timestamps and the other
                   models that detected the same species within the tolerance, so
                   detections can be correlated across models. -->
              <div>
                <div class="text-xs text-muted">{t('system.inference.lastHeard')}</div>
                <!-- The feed shows everything each model fires on above the base
                     threshold, so it includes non-bird, human, and out-of-range
                     predictions that are not saved. Explain it so they are not
                     mistaken for saved detections. -->
                <div class="text-[11px] text-muted mb-1 leading-snug">
                  {t('system.inference.lastHeardHint')}
                </div>

                {#snippet feedTable(rows: InferenceLastDetection[])}
                  <table class="w-full text-xs table-fixed">
                    <thead class="text-muted">
                      <tr>
                        <th class="text-left font-normal py-0.5 pr-2">
                          {t('system.inference.species')}
                        </th>
                        <th
                          class="text-left font-normal py-0.5 w-12 whitespace-nowrap"
                          title={t('common.labels.confidence')}
                          aria-label={t('common.labels.confidence')}
                        >
                          {t('system.inference.confidenceColumn')}
                        </th>
                        <th class="text-left font-normal py-0.5 w-16 whitespace-nowrap">
                          {t('system.inference.heardWhen')}
                        </th>
                        <th
                          class="text-left font-normal py-0.5 pl-2 w-20 whitespace-nowrap"
                          title={t('system.inference.coDetectedHelp', {
                            seconds: CO_DETECTION_TOLERANCE_SEC,
                          })}
                          aria-label={t('system.inference.coDetectedHelp', {
                            seconds: CO_DETECTION_TOLERANCE_SEC,
                          })}
                        >
                          {t('system.inference.coDetectedColumn')}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {#each rows as d, i (`${d.scientificName || d.species}-${d.atUnix}-${i}`)}
                        {@const coNames = coDetectingModels(model.id, d)}
                        <tr class="border-t border-[var(--border-100)]">
                          <td class="py-0.5 pr-2 text-base-content">
                            <div class="flex items-center gap-1 min-w-0">
                              {#if !d.inRange}
                                <!-- Did not pass the range filter (non-avian, human,
                                     or out-of-range): shown for diagnostics but not
                                     saved as a detection. -->
                                <span
                                  class="shrink-0 inline-flex text-muted"
                                  role="img"
                                  title={t('system.inference.outOfRangeHelp')}
                                  aria-label={t('system.inference.outOfRangeHelp')}
                                >
                                  <MapPinOff class="w-3 h-3" aria-hidden="true" />
                                </span>
                              {/if}
                              <span
                                class="truncate"
                                title={d.scientificName
                                  ? `${d.species} (${d.scientificName})`
                                  : d.species}
                              >
                                {d.species}
                              </span>
                            </div>
                          </td>
                          <td class="text-left py-0.5 font-mono tabular-nums text-base-content">
                            {Math.round(d.confidence * 100)}%
                          </td>
                          <td
                            class="text-left py-0.5 font-mono tabular-nums text-muted whitespace-nowrap"
                            title={formatLocalDateTime(new Date(d.atUnix * 1000))}
                          >
                            {getLocalTimeString(new Date(d.atUnix * 1000))}
                          </td>
                          <td
                            class="truncate py-0.5 pl-2 text-muted"
                            title={coNames.length > 0 ? coNames.join(', ') : undefined}
                          >
                            {#if coNames.length > 0}
                              {coNames.join(', ')}
                            {:else}
                              -
                            {/if}
                          </td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                {/snippet}

                <!-- min-height sized for the full ten rows per column so the card
                     does not resize as detections fill in after a restart. -->
                <div class="min-h-[12rem]">
                  {#if model.recentDetections && model.recentDetections.length > 0}
                    {@const left = model.recentDetections.slice(0, LAST_HEARD_COLUMN_ROWS)}
                    {@const right = model.recentDetections.slice(
                      LAST_HEARD_COLUMN_ROWS,
                      LAST_HEARD_COLUMN_ROWS * 2
                    )}
                    <!-- Two newspaper columns: newest ten on the left, next ten on
                         the right. The left table stays half-width even before the
                         right column fills, so the species column never hogs the card. -->
                    <div class="grid grid-cols-1 lg:grid-cols-2 gap-x-6 items-start">
                      {@render feedTable(left)}
                      {#if right.length > 0}
                        {@render feedTable(right)}
                      {/if}
                    </div>
                  {:else}
                    <div class="text-xs text-muted">{t('system.inference.lastHeardNever')}</div>
                  {/if}
                </div>
              </div>

              <!-- Approx RAM -->
              <div class="flex items-center gap-2 text-xs">
                <MemoryStick class="w-3.5 h-3.5 shrink-0 text-muted" aria-hidden="true" />
                <span
                  class="text-muted"
                  title={t('system.inference.approxRamTooltip')}
                  aria-describedby={`ram-help-${model.id}`}
                >
                  {t('system.inference.approxRam')}:
                </span>
                <span class="font-mono tabular-nums text-base-content">{ramDisplay(model)}</span>
                <span id={`ram-help-${model.id}`} class="sr-only">
                  {t('system.inference.approxRamTooltip')}
                </span>
              </div>

              <!-- Sources -->
              <div>
                <div class="text-xs text-muted mb-1">{t('system.inference.sources')}</div>
                {#if model.sources.length === 0}
                  <span class="text-xs text-muted">{t('system.inference.noSources')}</span>
                {:else}
                  <div class="flex flex-wrap gap-1.5">
                    {#each model.sources as source (source.id)}
                      <Badge variant="ghost" size="sm">
                        {source.name}{#if source.type}
                          <span class="text-muted ml-1">({source.type})</span>
                        {/if}{#if source.fallback}
                          <span class="text-muted ml-1">
                            - {t('system.inference.primaryFallback')}
                          </span>
                        {/if}
                      </Badge>
                    {/each}
                  </div>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}
</div>
