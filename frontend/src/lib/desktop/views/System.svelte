<script lang="ts">
  import SystemInfoCard from '$lib/desktop/components/ui/SystemInfoCard.svelte';
  import ProgressCard from '$lib/desktop/components/ui/ProgressCard.svelte';
  import ProcessTable from '$lib/desktop/components/ui/ProcessTable.svelte';
  import { DatabaseManagement } from '$lib/desktop/components/database';
  import TerminalPage from '$lib/desktop/features/system/TerminalPage.svelte';
  import { t } from '$lib/i18n';
  import { RefreshCw } from '@lucide/svelte';
  import { api, ApiError } from '$lib/utils/api';
  import { navigation } from '$lib/stores/navigation.svelte';

  // Determine which subpage to show
  let currentSubpage = $derived.by(() => {
    const path = navigation.currentPath;
    if (path === '/ui/system/database') return 'database';
    if (path === '/ui/system/terminal') return 'terminal';
    return 'overview';
  });

  // SPINNER CONTROL: Set to false to disable loading spinners (reduces flickering)
  // Change back to true to re-enable spinners for testing
  const ENABLE_LOADING_SPINNERS = false;

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

  interface ApiState<T> {
    loading: boolean;
    error: string | null;
    data: T;
  }

  // System information state
  let systemInfo = $state<ApiState<SystemInfo>>({
    loading: true,
    error: null,
    data: {} as SystemInfo,
  });

  // Disk usage state
  let diskUsage = $state<ApiState<DiskInfo[]>>({
    loading: true,
    error: null,
    data: [],
  });

  // Memory usage state
  let memoryUsage = $state<ApiState<MemoryInfo>>({
    loading: true,
    error: null,
    data: {} as MemoryInfo,
  });

  // System temperature state
  let systemTemperature = $state<ApiState<TemperatureInfo>>({
    loading: true,
    error: null,
    data: { is_available: false },
  });

  // Process information state
  let processes = $state<ApiState<ProcessInfo[]>>({
    loading: true,
    error: null,
    data: [],
  });

  // Toggle for showing all processes
  let showAllProcesses = $state<boolean>(false);

  // PERFORMANCE OPTIMIZATION: Reactive computed properties using $derived
  // $derived automatically tracks dependencies and only recalculates when they change
  // This is more efficient than manual state tracking or effects
  let isAnyLoading = $derived(
    systemInfo.loading ||
      diskUsage.loading ||
      memoryUsage.loading ||
      processes.loading ||
      systemTemperature.loading
  );

  // Transform disk data for ProgressCard
  let diskProgressItems = $derived(
    diskUsage.data.map(disk => ({
      label: disk.mountpoint,
      used: disk.used,
      total: disk.total,
      usagePercent: disk.usage_percent,
      mountpoint: disk.mountpoint,
    }))
  );

  // Transform memory data for ProgressCard with reactive label
  let memoryProgressItems = $derived.by(() => {
    if (!memoryUsage.data.total) return [];
    return [
      {
        label: t('system.memoryUsage.ramUsage'),
        used: memoryUsage.data.used,
        total: memoryUsage.data.total,
        usagePercent: memoryUsage.data.usedPercent,
        free: memoryUsage.data.free,
        available: memoryUsage.data.available,
        buffers: memoryUsage.data.buffers,
        cached: memoryUsage.data.cached,
      },
    ];
  });

  // Load system information
  async function loadSystemInfo(): Promise<void> {
    systemInfo.loading = true;
    systemInfo.error = null;

    try {
      systemInfo.data = await api.get<SystemInfo>('/api/v2/system/info');
    } catch (error: unknown) {
      // Handle system info fetch error silently
      systemInfo.error = t('system.errors.systemInfo', {
        error: error instanceof Error ? error.message : 'Unknown error',
      });
    } finally {
      systemInfo.loading = false;
    }
  }

  // Load disk usage
  async function loadDiskUsage(): Promise<void> {
    diskUsage.loading = true;
    diskUsage.error = null;

    try {
      diskUsage.data = await api.get<DiskInfo[]>('/api/v2/system/disks');
    } catch (error: unknown) {
      // Handle disk usage fetch error silently
      diskUsage.error = t('system.errors.diskUsage', {
        error: error instanceof Error ? error.message : 'Unknown error',
      });
      diskUsage.data = [];
    } finally {
      diskUsage.loading = false;
    }
  }

  // Load memory usage
  async function loadMemoryUsage(): Promise<void> {
    memoryUsage.loading = true;
    memoryUsage.error = null;

    try {
      interface ResourcesResponse {
        memory_total: number;
        memory_used: number;
        memory_free: number;
        memory_available: number;
        memory_buffers: number;
        memory_cached: number;
        memory_usage_percent: number;
      }
      const data = await api.get<ResourcesResponse>('/api/v2/system/resources');
      // Map the API response to our UI data model
      memoryUsage.data = {
        total: data.memory_total,
        used: data.memory_used,
        free: data.memory_free,
        available: data.memory_available,
        buffers: data.memory_buffers,
        cached: data.memory_cached,
        usedPercent: data.memory_usage_percent,
      };
    } catch (error: unknown) {
      // Handle memory usage fetch error silently
      memoryUsage.error = t('system.errors.memoryUsage', {
        error: error instanceof Error ? error.message : 'Unknown error',
      });
    } finally {
      memoryUsage.loading = false;
    }
  }

  // Load system temperature
  async function loadSystemTemperature(): Promise<void> {
    systemTemperature.loading = true;
    systemTemperature.error = null;

    try {
      systemTemperature.data = await api.get<TemperatureInfo>('/api/v2/system/temperature/cpu');
    } catch (error: unknown) {
      // Handle 404 as "temperature not available" (not an error)
      if (error instanceof ApiError && error.status === 404) {
        systemTemperature.data = { is_available: false };
        return;
      }
      // Handle temperature fetch error silently
      systemTemperature.error = t('system.errors.temperature', {
        error: error instanceof Error ? error.message : 'Unknown error',
      });
      systemTemperature.data = { is_available: false };
    } finally {
      systemTemperature.loading = false;
    }
  }

  // Load process information
  async function loadProcesses(): Promise<void> {
    processes.loading = true;
    processes.error = null;

    try {
      const url = showAllProcesses
        ? '/api/v2/system/processes?all=true'
        : '/api/v2/system/processes';
      processes.data = await api.get<ProcessInfo[]>(url);
    } catch (error: unknown) {
      // Handle processes fetch error silently
      processes.error = t('system.errors.processes', {
        error: error instanceof Error ? error.message : 'Unknown error',
      });
      processes.data = [];
    } finally {
      processes.loading = false;
    }
  }

  // Load all data
  async function loadAllData(): Promise<void> {
    await Promise.all([
      loadSystemInfo(),
      loadDiskUsage(),
      loadMemoryUsage(),
      loadSystemTemperature(),
      loadProcesses(),
    ]);
  }

  // PERFORMANCE OPTIMIZATION: Use Svelte 5 $effect instead of legacy onMount
  // $effect runs after component mount and only when dependencies change
  // This is the modern Svelte 5 pattern for side effects
  $effect(() => {
    loadAllData();
  });
</script>

{#if currentSubpage === 'terminal'}
  <!-- Browser Terminal Page
       Use explicit dvh-based height: h-full doesn't work here because the ancestor
       grid uses grid-rows-[min-content], so h-full resolves to content height
       instead of the viewport, causing the page to scroll.
       Offset breakdown (mobile / desktop lg):
         Header: p-1+h-12+p-1=56px  /  lg:p-4+h-12+lg:p-4=80px
         Header-wrapper bottom padding: p-3=12px  /  lg:pb-0=0px
         mainContent bottom padding: p-3=12px  /  lg:p-8=32px
         Total: 80px  /  112px -->
  <div
    class="col-span-12 flex flex-col h-[calc(100dvh-80px)] lg:h-[calc(100dvh-112px)] overflow-hidden"
    role="region"
    aria-label={t('system.sections.terminal')}
  >
    <TerminalPage />
  </div>
{:else if currentSubpage === 'database'}
  <!-- Database Management Page -->
  <div class="col-span-12 space-y-4" role="region" aria-label={t('system.database.title')}>
    <div class="max-w-5xl mx-auto">
      <DatabaseManagement />
    </div>
  </div>
{:else}
  <!-- System Overview Page -->
  <div class="col-span-12 space-y-4" role="region" aria-label={t('system.aria.dashboard')}>
    <div class="system-cards-grid">
      <!-- System Information Card -->
      <SystemInfoCard
        title={t('system.systemInfo.title')}
        systemInfo={systemInfo.data}
        temperatureInfo={systemTemperature.data}
        isLoading={systemInfo.loading}
        error={systemInfo.error}
        temperatureLoading={systemTemperature.loading}
        temperatureError={systemTemperature.error}
      />

      <!-- Disk Usage Card -->
      <ProgressCard
        title={t('system.diskUsage.title')}
        items={diskProgressItems}
        isLoading={diskUsage.loading}
        error={diskUsage.error}
        emptyMessage={t('system.diskUsage.emptyMessage')}
      />

      <!-- Memory Usage Card -->
      <ProgressCard
        title={t('system.memoryUsage.title')}
        items={memoryProgressItems}
        isLoading={memoryUsage.loading}
        error={memoryUsage.error}
        showDetails={true}
      />
    </div>

    <!-- Process Information Card -->
    <ProcessTable
      title={t('system.processInfo.title')}
      processes={processes.data}
      {showAllProcesses}
      isLoading={processes.loading}
      error={processes.error}
      onToggleShowAll={() => {
        showAllProcesses = !showAllProcesses;
        loadProcesses();
      }}
      className="mt-6"
    />

    <!-- Refresh button -->
    <div class="flex justify-center mt-6">
      <button
        class="btn btn-primary"
        onclick={loadAllData}
        disabled={isAnyLoading}
        aria-label={t('system.aria.refreshData')}
      >
        {#if ENABLE_LOADING_SPINNERS && isAnyLoading}
          <span class="loading loading-spinner loading-sm mr-2" aria-hidden="true"></span>
        {:else}
          <span class="mr-2" aria-hidden="true">
            <RefreshCw class="size-5" />
          </span>
        {/if}
        {t('system.refreshData')}
      </button>
    </div>
  </div>
{/if}

<style>
  .system-cards-grid {
    display: grid;
    gap: 1.5rem;

    /* Default: Single column for narrow viewports */
    grid-template-columns: 1fr;
  }

  /* Tablets: 2 columns when there's enough space */
  @media (min-width: 768px) {
    .system-cards-grid {
      grid-template-columns: repeat(2, minmax(350px, 1fr));
    }
  }

  /* Desktop: 3 columns only when cards can be adequately wide */
  @media (min-width: 1280px) {
    .system-cards-grid {
      grid-template-columns: repeat(3, minmax(320px, 1fr));
    }
  }

  /* Large desktop: Cap maximum card width for readability */
  @media (min-width: 1920px) {
    .system-cards-grid {
      grid-template-columns: repeat(3, minmax(380px, 480px));
      justify-content: center;
    }
  }
</style>
