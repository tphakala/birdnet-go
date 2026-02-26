<script lang="ts">
  import MetricStrip from '$lib/desktop/features/system/components/MetricStrip.svelte';
  import SystemDetailsCard from '$lib/desktop/features/system/components/SystemDetailsCard.svelte';
  import StorageCard from '$lib/desktop/features/system/components/StorageCard.svelte';
  import SystemProcessTable from '$lib/desktop/features/system/components/SystemProcessTable.svelte';
  import { DatabaseDashboard } from '$lib/desktop/components/database';
  import TerminalPage from '$lib/desktop/features/system/TerminalPage.svelte';
  import { t } from '$lib/i18n';
  import { RefreshCw } from '@lucide/svelte';
  import { api, ApiError } from '$lib/utils/api';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { dashboardSettings } from '$lib/stores/settings';
  import { loggers } from '$lib/utils/logger';
  import {
    convertTemperature,
    getTemperatureSymbol,
    type TemperatureUnit,
  } from '$lib/utils/formatters';
  import ReconnectingEventSource from 'reconnecting-eventsource';

  const logger = loggers.ui;

  // Determine which subpage to show
  let currentSubpage = $derived.by(() => {
    const path = navigation.currentPath;
    if (path === '/ui/system/database') return 'database';
    if (path === '/ui/system/terminal') return 'terminal';
    return 'overview';
  });

  // Maximum number of sparkline data points to retain
  const MAX_HISTORY_POINTS = 60;

  // Polling fallback interval in milliseconds
  const POLLING_INTERVAL_MS = 5000;

  // Process name displayed in place of the Go binary name
  const BIRDNET_PROCESS_NAME = 'BirdNET-Go';

  // Type definitions
  interface SystemInfo {
    os_display: string;
    hostname: string;
    uptime_seconds: number;
    num_cpu: number;
    system_model?: string;
    time_zone?: string;
  }

  interface DiskInfo {
    mountpoint: string;
    total: number;
    used: number;
    usage_percent: number;
  }

  interface MemoryInfo {
    total: number;
    used: number;
    free: number;
    available: number;
    buffers: number;
    cached: number;
    usedPercent: number;
  }

  interface TemperatureInfo {
    is_available: boolean;
    celsius?: number;
  }

  interface ProcessInfo {
    pid: number;
    name: string;
    status: string;
    cpu: number;
    memory: number;
    uptime: number;
  }

  interface MetricPoint {
    timestamp: string;
    value: number;
  }

  interface MetricsHistoryResponse {
    metrics: Record<string, MetricPoint[]>;
  }

  interface ResourcesResponse {
    cpu_usage_percent: number;
    memory_total: number;
    memory_used: number;
    memory_free: number;
    memory_available: number;
    memory_buffers: number;
    memory_cached: number;
    memory_usage_percent: number;
  }

  // System information state
  let systemInfo = $state<SystemInfo>({} as SystemInfo);
  let diskUsage = $state<DiskInfo[]>([]);
  let memoryUsage = $state<MemoryInfo>({} as MemoryInfo);
  let systemTemperature = $state<TemperatureInfo>({ is_available: false });
  let processes = $state<ProcessInfo[]>([]);
  let isLoading = $state(true);

  // Sparkline history arrays
  let cpuHistory = $state<number[]>([]);
  let memoryHistory = $state<number[]>([]);
  let temperatureHistory = $state<number[]>([]);

  // Toggle for showing all processes
  let showAllProcesses = $state(false);

  // SSE connection reference
  let metricsSSE: ReconnectingEventSource | null = null;

  // Polling fallback timeout reference (used when SSE endpoints aren't available)
  let pollingTimeout: ReturnType<typeof setTimeout> | null = null;

  // Map user's temperature preference to TemperatureUnit format
  // Settings store uses 'celsius'/'fahrenheit', formatters use 'metric'/'imperial'/'standard'
  let temperatureUnit = $derived.by((): TemperatureUnit => {
    const setting = $dashboardSettings?.temperatureUnit;
    if (setting === 'fahrenheit') return 'imperial';
    return 'metric';
  });
  let tempSymbol = $derived(getTemperatureSymbol(temperatureUnit));

  // Computed values for MetricStrip — derived from history arrays so they
  // stay in sync with both SSE and polling updates
  let cpuPercent = $derived(cpuHistory.length > 0 ? cpuHistory[cpuHistory.length - 1] : 0);
  let memoryPercent = $derived(
    memoryHistory.length > 0 ? memoryHistory[memoryHistory.length - 1] : 0
  );
  let temperatureCelsius = $derived(
    temperatureHistory.length > 0
      ? temperatureHistory[temperatureHistory.length - 1]
      : (systemTemperature.celsius ?? 0)
  );
  let temperatureDisplay = $derived(convertTemperature(temperatureCelsius, temperatureUnit));
  let temperatureHistoryConverted = $derived(
    temperatureHistory.map(c => convertTemperature(c, temperatureUnit))
  );

  // Append a value to a history array, keeping it capped at MAX_HISTORY_POINTS
  function appendHistory(arr: number[], value: number): number[] {
    const next = [...arr, value];
    return next.length > MAX_HISTORY_POINTS ? next.slice(next.length - MAX_HISTORY_POINTS) : next;
  }

  // Load initial metrics history for sparklines, then connect SSE if available.
  // Falls back to polling existing endpoints when metrics history isn't available.
  // The `active` flag prevents orphaned resources if the component unmounts mid-flight.
  async function loadMetricsHistory(active: { current: boolean }): Promise<void> {
    try {
      const data = await api.get<MetricsHistoryResponse>(
        `/api/v2/system/metrics/history?points=${MAX_HISTORY_POINTS}&metrics=cpu.total,memory.used_percent,cpu.temperature`
      );

      if (!active.current) return;

      if (data.metrics['cpu.total']) {
        cpuHistory = data.metrics['cpu.total'].map((p: MetricPoint) => p.value);
      }
      if (data.metrics['memory.used_percent']) {
        memoryHistory = data.metrics['memory.used_percent'].map((p: MetricPoint) => p.value);
      }
      if (data.metrics['cpu.temperature']) {
        temperatureHistory = data.metrics['cpu.temperature'].map((p: MetricPoint) => p.value);
      }

      // Only connect SSE if the history endpoint succeeded and component is still mounted
      connectMetricsStream();
    } catch {
      if (!active.current) return;
      logger.debug('Metrics history endpoint not available, falling back to polling');
      startPollingFallback(active);
    }
  }

  // Poll existing resource/temperature endpoints to accumulate sparkline data.
  // Uses recursive setTimeout instead of setInterval to avoid overlapping requests.
  function startPollingFallback(active: { current: boolean }): void {
    // Seed initial points from data already loaded
    // (cpuHistory is already seeded in loadMemoryUsage)
    if (memoryUsage.usedPercent > 0) {
      memoryHistory = appendHistory(memoryHistory, memoryUsage.usedPercent);
    }
    if (systemTemperature.is_available && systemTemperature.celsius != null) {
      temperatureHistory = appendHistory(temperatureHistory, systemTemperature.celsius);
    }

    async function poll(): Promise<void> {
      if (!active.current) return;

      try {
        const data = await api.get<ResourcesResponse>('/api/v2/system/resources');
        if (!active.current) return;
        cpuHistory = appendHistory(cpuHistory, data.cpu_usage_percent);
        memoryHistory = appendHistory(memoryHistory, data.memory_usage_percent);

        // Also update the memory state so cards stay current
        memoryUsage = {
          total: data.memory_total,
          used: data.memory_used,
          free: data.memory_free,
          available: data.memory_available,
          buffers: data.memory_buffers,
          cached: data.memory_cached,
          usedPercent: data.memory_usage_percent,
        };
      } catch {
        // Silently ignore polling failures
      }

      try {
        const temp = await api.get<TemperatureInfo>('/api/v2/system/temperature/cpu');
        if (!active.current) return;
        if (temp.is_available && temp.celsius != null) {
          systemTemperature = temp;
          temperatureHistory = appendHistory(temperatureHistory, temp.celsius);
        }
      } catch {
        // Temperature may not be available — that's fine
      }

      if (active.current) {
        pollingTimeout = setTimeout(poll, POLLING_INTERVAL_MS);
      }
    }

    pollingTimeout = setTimeout(poll, POLLING_INTERVAL_MS);
  }

  function stopPollingFallback(): void {
    if (pollingTimeout) {
      clearTimeout(pollingTimeout);
      pollingTimeout = null;
    }
  }

  // Connect to metrics SSE stream for live updates
  function connectMetricsStream(): void {
    metricsSSE = new ReconnectingEventSource(
      '/api/v2/system/metrics/stream?metrics=cpu.total,memory.used_percent,cpu.temperature',
      { max_retry_time: 30000 }
    );

    metricsSSE.addEventListener('metrics', (event: Event) => {
      try {
        // eslint-disable-next-line no-undef
        const messageEvent = event as MessageEvent;
        const metrics = JSON.parse(messageEvent.data) as Record<string, MetricPoint>;

        if (metrics['cpu.total']) {
          cpuHistory = appendHistory(cpuHistory, metrics['cpu.total'].value);
        }
        if (metrics['memory.used_percent']) {
          memoryHistory = appendHistory(memoryHistory, metrics['memory.used_percent'].value);
        }
        if (metrics['cpu.temperature']) {
          temperatureHistory = appendHistory(temperatureHistory, metrics['cpu.temperature'].value);
        }
      } catch {
        logger.debug('Failed to parse metrics SSE event');
      }
    });
  }

  function disconnectMetricsStream(): void {
    if (metricsSSE) {
      metricsSSE.close();
      metricsSSE = null;
    }
  }

  // Load system information
  async function loadSystemInfo(): Promise<void> {
    try {
      systemInfo = await api.get<SystemInfo>('/api/v2/system/info');
    } catch (error: unknown) {
      logger.debug('Failed to load system info', {
        error: error instanceof Error ? error.message : 'Unknown error',
      });
    }
  }

  // Load disk usage
  async function loadDiskUsage(): Promise<void> {
    try {
      diskUsage = await api.get<DiskInfo[]>('/api/v2/system/disks');
    } catch (error: unknown) {
      logger.debug('Failed to load disk usage', {
        error: error instanceof Error ? error.message : 'Unknown error',
      });
    }
  }

  // Load memory usage and CPU from same endpoint
  async function loadMemoryUsage(): Promise<void> {
    try {
      const data = await api.get<ResourcesResponse>('/api/v2/system/resources');
      memoryUsage = {
        total: data.memory_total,
        used: data.memory_used,
        free: data.memory_free,
        available: data.memory_available,
        buffers: data.memory_buffers,
        cached: data.memory_cached,
        usedPercent: data.memory_usage_percent,
      };
      // Seed initial CPU history point so the sparkline isn't empty before SSE/polling starts
      if (data.cpu_usage_percent > 0) {
        cpuHistory = appendHistory(cpuHistory, data.cpu_usage_percent);
      }
    } catch (error: unknown) {
      logger.debug('Failed to load memory usage', {
        error: error instanceof Error ? error.message : 'Unknown error',
      });
    }
  }

  // Load system temperature
  async function loadSystemTemperature(): Promise<void> {
    try {
      systemTemperature = await api.get<TemperatureInfo>('/api/v2/system/temperature/cpu');
    } catch (error: unknown) {
      if (error instanceof ApiError && error.status === 404) {
        systemTemperature = { is_available: false };
        return;
      }
      logger.debug('Failed to load temperature', {
        error: error instanceof Error ? error.message : 'Unknown error',
      });
      systemTemperature = { is_available: false };
    }
  }

  // Load process information
  async function loadProcesses(): Promise<void> {
    try {
      const url = showAllProcesses
        ? '/api/v2/system/processes?all=true'
        : '/api/v2/system/processes';
      processes = await api.get<ProcessInfo[]>(url);
    } catch (error: unknown) {
      logger.debug('Failed to load processes', {
        error: error instanceof Error ? error.message : 'Unknown error',
      });
      processes = [];
    }
  }

  // Load all data
  async function loadAllData(): Promise<void> {
    isLoading = true;
    await Promise.all([
      loadSystemInfo(),
      loadDiskUsage(),
      loadMemoryUsage(),
      loadSystemTemperature(),
      loadProcesses(),
    ]);
    isLoading = false;
  }

  // Initialize on mount, clean up on unmount
  $effect(() => {
    const active = { current: true };

    // Load initial data, then load metrics history (which needs data for fallback seeding)
    loadAllData().then(() => {
      if (active.current) {
        loadMetricsHistory(active);
      }
    });

    return () => {
      active.current = false;
      disconnectMetricsStream();
      stopPollingFallback();
    };
  });
</script>

{#if currentSubpage === 'terminal'}
  <!-- Browser Terminal Page -->
  <div
    class="col-span-12 flex flex-col h-[calc(100dvh-80px)] lg:h-[calc(100dvh-112px)] overflow-hidden"
    role="region"
    aria-label={t('system.sections.terminal')}
  >
    <TerminalPage />
  </div>
{:else if currentSubpage === 'database'}
  <!-- Database Dashboard Page -->
  <div
    class="col-span-12 space-y-4"
    role="region"
    aria-label={t('system.database.dashboard.title')}
  >
    <div class="max-w-5xl mx-auto">
      <DatabaseDashboard />
    </div>
  </div>
{:else}
  <!-- System Overview Page -->
  <div class="col-span-12 space-y-4" role="region" aria-label={t('system.aria.dashboard')}>
    <!-- Top Metric Strip -->
    <MetricStrip
      {cpuPercent}
      cpuCores={systemInfo.num_cpu ?? 0}
      {cpuHistory}
      {memoryPercent}
      memoryUsed={memoryUsage.used ?? 0}
      memoryTotal={memoryUsage.total ?? 0}
      memoryAvailable={memoryUsage.available ?? 0}
      {memoryHistory}
      temperatureAvailable={systemTemperature.is_available}
      temperatureValue={temperatureDisplay}
      temperatureHistory={temperatureHistoryConverted}
      {tempSymbol}
      uptimeSeconds={systemInfo.uptime_seconds ?? 0}
      hostname={systemInfo.hostname ?? ''}
      systemModel={systemInfo.system_model}
    />

    <!-- System Details + Storage -->
    <div class="grid grid-cols-1 lg:grid-cols-3 gap-3">
      <SystemDetailsCard
        osDisplay={systemInfo.os_display ?? ''}
        systemModel={systemInfo.system_model}
        uptimeSeconds={systemInfo.uptime_seconds ?? 0}
        timeZone={systemInfo.time_zone}
        cpuCores={systemInfo.num_cpu ?? 0}
        temperatureAvailable={systemTemperature.is_available}
        temperatureValue={temperatureDisplay}
        {tempSymbol}
      />
      <div class="lg:col-span-2">
        <StorageCard
          disks={diskUsage}
          memory={memoryUsage.total
            ? memoryUsage
            : { total: 0, used: 0, free: 0, available: 0, buffers: 0, cached: 0, usedPercent: 0 }}
        />
      </div>
    </div>

    <!-- Process Table -->
    <SystemProcessTable
      {processes}
      {showAllProcesses}
      processName={BIRDNET_PROCESS_NAME}
      onToggleShowAll={() => {
        showAllProcesses = !showAllProcesses;
        loadProcesses();
      }}
    />

    <!-- Refresh button -->
    <div class="flex justify-center mt-6">
      <button
        class="inline-flex items-center px-4 py-2 rounded-lg font-medium text-sm text-white bg-blue-600 hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
        onclick={loadAllData}
        disabled={isLoading}
        aria-label={t('system.aria.refreshData')}
      >
        <RefreshCw class="size-5 mr-2" />
        {t('system.refreshData')}
      </button>
    </div>
  </div>
{/if}
