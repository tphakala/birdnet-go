<!--
  SystemInference - AI Models & Inference subpage.

  Consumes the GET /api/v2/system/inference snapshot, renders hardware,
  inference backends, audio pipeline metrics, and per-model cards with
  latency / RTF / throughput sparklines, approximate host RAM, last
  detection, activity pulse, and attached audio sources. Live updates arrive
  over the existing metrics SSE stream (SSE first, polling fallback), and the
  page re-fetches the snapshot when the backend broadcasts a topology change.
  A periodic ~30s snapshot refresh keeps headline stats and lastDetection
  current without reconnecting the SSE stream.

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
  import { formatBytesCompact, formatNumber, formatRelativeTime } from '$lib/utils/formatters';
  import Badge from '$lib/desktop/components/ui/Badge.svelte';
  import StatusPill from '$lib/desktop/components/ui/StatusPill.svelte';
  import Sparkline from '$lib/desktop/features/system/components/Sparkline.svelte';
  import { Brain, Cpu, MemoryStick, Activity, Minus } from '@lucide/svelte';
  import type {
    InferenceStatusResponse,
    InferenceModel,
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

  // Sparkline colors, chosen to match the existing system charts palette.
  const LATENCY_COLOR = '#3b82f6'; // blue
  const RTF_COLOR = '#8b5cf6'; // violet
  const THROUGHPUT_COLOR = '#10b981'; // emerald
  const AUDIO_COLOR = '#06b6d4'; // cyan

  // Interval for periodic snapshot-only refreshes (does not reconnect SSE).
  const SNAPSHOT_REFRESH_MS = 30000;

  // Conversions for spec display.
  const HZ_PER_KHZ = 1000;

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

  // Collect every metric key across the snapshot models and audio pipeline.
  // The audio queue-depth key is included regardless of model count so the
  // Audio card sparkline receives data even before any model is loaded.
  // When neither audio keys nor model keys are present the function returns ''
  // and the caller falls through to the awaitingModels / polling path.
  function metricKeysParam(): string {
    if (!snapshot) return '';
    const keys: string[] = [];
    if (snapshot.audio) {
      keys.push(snapshot.audio.metricKeys.queueDepth);
    }
    for (const m of snapshot.models) {
      keys.push(
        m.metricKeys.avgMs,
        m.metricKeys.rtf,
        m.metricKeys.throughput,
        m.metricKeys.errorRate
      );
    }
    return keys.join(',');
  }

  // Set of metric keys belonging to current snapshot models and audio pipeline.
  // Used to ignore series for models that are no longer present (orphan-safe).
  // Derived so it is recomputed only when the snapshot changes.
  const validKeys = $derived.by(() => {
    const keys = new Set<string>();
    if (!snapshot) return keys;
    if (snapshot.audio) {
      keys.add(snapshot.audio.metricKeys.queueDepth);
    }
    for (const m of snapshot.models) {
      keys.add(m.metricKeys.avgMs);
      keys.add(m.metricKeys.rtf);
      keys.add(m.metricKeys.throughput);
      keys.add(m.metricKeys.errorRate);
    }
    return keys;
  });

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

  // Spec line for a model: sample rate in kHz, clip length in seconds.
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
</script>

<div class="space-y-4">
  <h2 class="text-xl font-semibold flex items-center gap-2">
    <Brain class="w-5 h-5 shrink-0" aria-hidden="true" />
    {t('system.inference.title')}
  </h2>

  {#if loading}
    <div
      class="flex items-center gap-3 p-4 text-sm text-base-content/70"
      role="status"
      aria-live="polite"
    >
      <span
        class="animate-spin h-5 w-5 border-2 border-blue-500 border-t-transparent rounded-full"
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
    <!-- Hardware -->
    <div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
      <h3 class="text-xs font-semibold uppercase tracking-wider mb-3 text-muted">
        {t('system.inference.sectionHardware')}
      </h3>
      <div class="space-y-2.5">
        {#if snapshot.hardware.arch}
          <div class="flex items-center gap-3">
            <Cpu class="w-3.5 h-3.5 shrink-0 text-muted" aria-hidden="true" />
            <span class="text-sm text-muted">{t('system.inference.architecture')}</span>
            <span class="text-sm font-mono tabular-nums truncate">{snapshot.hardware.arch}</span>
          </div>
        {/if}
        {#if snapshot.hardware.cpuModel}
          <div class="flex items-center gap-3">
            <Cpu class="w-3.5 h-3.5 shrink-0 text-muted" aria-hidden="true" />
            <span class="text-sm text-muted">{t('system.inference.cpu')}</span>
            <span class="text-sm truncate">{snapshot.hardware.cpuModel}</span>
          </div>
        {/if}
        {#if snapshot.hardware.environment}
          <div class="flex items-center gap-3">
            <span class="text-sm text-muted">{t('system.inference.environment')}</span>
            <span class="text-sm truncate">{snapshot.hardware.environment}</span>
          </div>
        {/if}
        <div class="flex items-center gap-3">
          <span class="text-sm text-muted">{t('system.inference.fp16')}</span>
          {#if snapshot.hardware.fp16}
            <StatusPill variant="success" label={t('system.inference.fp16Supported')} size="xs" />
          {:else}
            <StatusPill variant="neutral" label={t('system.inference.fp16Unsupported')} size="xs" />
          {/if}
        </div>
      </div>
    </div>

    <!-- Inference backends -->
    <div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
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
              <StatusPill variant="neutral" label={t('system.inference.notAvailable')} size="xs" />
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

    <!-- Audio pipeline -->
    {#if snapshot.audio}
      <div
        class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm"
      >
        <h3 class="text-xs font-semibold uppercase tracking-wider mb-3 text-muted">
          {t('system.inference.sectionAudio')}
        </h3>
        <div class="space-y-2.5">
          <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs">
            <span class="text-muted">
              {t('system.inference.queueDepth')}:
              <span class="font-mono tabular-nums text-base-content">
                {snapshot.audio.queueDepth}
              </span>
            </span>
            <span class="text-muted">
              {t('system.inference.queueCapacity')}:
              <span class="font-mono tabular-nums text-base-content">
                {snapshot.audio.queueCapacity}
              </span>
            </span>
            <span class="text-muted">
              {t('system.inference.droppedChunks')}:
              <span class="font-mono tabular-nums text-base-content">
                {formatNumber(snapshot.audio.droppedChunksTotal)}
              </span>
            </span>
          </div>
          <div>
            <div class="text-[11px] text-muted mb-1 flex items-center gap-1">
              <Activity class="w-3 h-3 shrink-0" aria-hidden="true" />
              {t('system.inference.queueDepthChart')}
            </div>
            <div class="h-10">
              <Sparkline
                data={seriesByKey[snapshot.audio.metricKeys.queueDepth] ?? []}
                color={AUDIO_COLOR}
              />
            </div>
          </div>
        </div>
      </div>
    {/if}

    <!-- Models -->
    <div>
      <h3 class="text-xs font-semibold uppercase tracking-wider mb-3 text-muted">
        {t('system.inference.sectionModels')}
      </h3>

      {#if snapshot.models.length === 0}
        <div
          class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-6 shadow-sm text-center text-sm text-base-content/70"
        >
          {t('system.inference.noModels')}
        </div>
      {:else}
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
          {#each snapshot.models as model (model.id)}
            {@const latencySeries = seriesByKey[model.metricKeys.avgMs] ?? []}
            {@const rtfSeries = seriesByKey[model.metricKeys.rtf] ?? []}
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
                <Badge
                  variant={model.isStock ? 'neutral' : 'accent'}
                  size="sm"
                  text={model.isStock ? t('system.inference.stock') : t('system.inference.custom')}
                />
                <span
                  class="ml-auto flex items-center gap-1"
                  role="status"
                  aria-label={isActive
                    ? t('system.inference.activityActive')
                    : t('system.inference.activityIdle')}
                >
                  {#if isActive}
                    <Activity class="w-3 h-3 text-green-500 animate-pulse" aria-hidden="true" />
                  {:else}
                    <Minus class="w-3 h-3 text-base-content/30" aria-hidden="true" />
                  {/if}
                </span>
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

              <!-- Last seen -->
              <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs">
                <span class="text-muted">{t('system.inference.lastSeen')}:</span>
                {#if model.lastDetection}
                  <span class="text-base-content">
                    {model.lastDetection.species}
                  </span>
                  <span class="font-mono tabular-nums text-base-content">
                    {Math.round(model.lastDetection.confidence * 100)}%
                  </span>
                  <span class="text-muted">
                    {formatRelativeTime(model.lastDetection.atUnix * 1000)}
                  </span>
                {:else}
                  <span class="text-base-content/70">{t('system.inference.lastSeenNever')}</span>
                {/if}
              </div>

              <!-- Stats line -->
              <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs">
                <span class="text-muted">
                  {t('system.inference.invocations')}:
                  <span class="font-mono tabular-nums text-base-content">
                    {formatNumber(model.stats.invocations)}
                  </span>
                </span>
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
                <span class="text-muted" title={t('system.inference.rtfLabel')}>
                  {t('system.inference.rtf')}:
                  <span
                    class="font-mono tabular-nums text-base-content"
                    aria-describedby={`rtf-help-${model.id}`}>{rtfDisplay(model)}</span
                  >
                  <span id={`rtf-help-${model.id}`} class="sr-only">
                    {t('system.inference.rtfLabel')}
                  </span>
                </span>
                <span class="text-muted">
                  {t('system.inference.throughput')}:
                  <span class="font-mono tabular-nums text-base-content">
                    {throughputDisplay(model, throughputLatest)}
                  </span>
                </span>
                {#if model.stats.errorRate !== undefined}
                  <span class="text-muted">
                    {t('system.inference.errorRate')}:
                    <span class="font-mono tabular-nums text-base-content">
                      {Math.round(model.stats.errorRate * 100)}%
                    </span>
                  </span>
                {/if}
                {#if model.stats.loadFailures !== undefined && model.stats.loadFailures > 0}
                  <span class="text-muted">
                    {t('system.inference.loadFailures')}:
                    <span class="font-mono tabular-nums text-base-content">
                      {model.stats.loadFailures}
                    </span>
                  </span>
                {/if}
              </div>

              <!-- Sparklines -->
              <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <div>
                  <div class="text-[11px] text-muted mb-1 flex items-center gap-1">
                    <Activity class="w-3 h-3 shrink-0" aria-hidden="true" />
                    {t('system.inference.latencyChart')}
                  </div>
                  <div class="h-10">
                    <Sparkline data={latencySeries} color={LATENCY_COLOR} />
                  </div>
                </div>
                <div>
                  <div class="text-[11px] text-muted mb-1 flex items-center gap-1">
                    <Activity class="w-3 h-3 shrink-0" aria-hidden="true" />
                    {t('system.inference.rtfChart')}
                  </div>
                  <div class="h-10">
                    <Sparkline data={rtfSeries} color={RTF_COLOR} />
                  </div>
                </div>
                <div>
                  <div class="text-[11px] text-muted mb-1 flex items-center gap-1">
                    <Activity class="w-3 h-3 shrink-0" aria-hidden="true" />
                    {t('system.inference.throughputChart')}
                  </div>
                  <div class="h-10">
                    <Sparkline data={throughputSeries} color={THROUGHPUT_COLOR} />
                  </div>
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
                <div class="text-[11px] text-muted mb-1">{t('system.inference.sources')}</div>
                {#if model.sources.length === 0}
                  <span class="text-xs text-base-content/70">{t('system.inference.noSources')}</span
                  >
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
