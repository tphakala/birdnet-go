<script lang="ts">
  import { onMount } from 'svelte';
  import SystemInfoCard from '$lib/desktop/components/ui/SystemInfoCard.svelte';
  import ProgressCard from '$lib/desktop/components/ui/ProgressCard.svelte';
  import ProcessTable from '$lib/desktop/components/ui/ProcessTable.svelte';
  import { actionIcons } from '$lib/utils/icons';

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

  // Helper computed properties
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

  // Transform memory data for ProgressCard
  let memoryProgressItems = $derived(
    memoryUsage.data.total
      ? [
          {
            label: 'RAM Usage',
            used: memoryUsage.data.used,
            total: memoryUsage.data.total,
            usagePercent: memoryUsage.data.usedPercent,
          },
        ]
      : []
  );

  // Load system information
  async function loadSystemInfo(): Promise<void> {
    systemInfo.loading = true;
    systemInfo.error = null;

    try {
      const response = await fetch('/api/v2/system/info', {
        headers: {
          'X-CSRF-Token': (document.querySelector('meta[name="csrf-token"]') as any)?.content || '',
        },
      });

      if (!response.ok) {
        throw new Error(`Server responded with ${response.status}`);
      }

      systemInfo.data = await response.json();
    } catch (error: unknown) {
      // Handle system info fetch error silently
      systemInfo.error = `Failed to load system information: ${error instanceof Error ? error.message : 'Unknown error'}`;
    } finally {
      systemInfo.loading = false;
    }
  }

  // Load disk usage
  async function loadDiskUsage(): Promise<void> {
    diskUsage.loading = true;
    diskUsage.error = null;

    try {
      const response = await fetch('/api/v2/system/disks', {
        headers: {
          'X-CSRF-Token': (document.querySelector('meta[name="csrf-token"]') as any)?.content || '',
        },
      });

      if (!response.ok) {
        throw new Error(`Server responded with ${response.status}`);
      }

      diskUsage.data = await response.json();
    } catch (error: unknown) {
      // Handle disk usage fetch error silently
      diskUsage.error = `Failed to load disk usage: ${error instanceof Error ? error.message : 'Unknown error'}`;
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
      const response = await fetch('/api/v2/system/resources', {
        headers: {
          'X-CSRF-Token': (document.querySelector('meta[name="csrf-token"]') as any)?.content || '',
        },
      });

      if (!response.ok) {
        throw new Error(`Server responded with ${response.status}`);
      }

      const data = await response.json();
      // Map the API response to our UI data model
      memoryUsage.data = {
        total: data.memory_total,
        used: data.memory_used,
        free: data.memory_free,
        available: data.memory_free,
        usedPercent: data.memory_usage_percent,
      };
    } catch (error: unknown) {
      // Handle memory usage fetch error silently
      memoryUsage.error = `Failed to load memory usage: ${error instanceof Error ? error.message : 'Unknown error'}`;
    } finally {
      memoryUsage.loading = false;
    }
  }

  // Load system temperature
  async function loadSystemTemperature(): Promise<void> {
    systemTemperature.loading = true;
    systemTemperature.error = null;

    try {
      const response = await fetch('/api/v2/system/temperature/cpu', {
        headers: {
          'X-CSRF-Token': (document.querySelector('meta[name="csrf-token"]') as any)?.content || '',
        },
      });

      if (!response.ok) {
        if (response.status === 404) {
          systemTemperature.data = { is_available: false };
          return;
        }
        throw new Error(`Server responded with ${response.status}`);
      }

      systemTemperature.data = await response.json();
    } catch (error: unknown) {
      // Handle temperature fetch error silently
      systemTemperature.error = `Failed to load temperature: ${error instanceof Error ? error.message : 'Unknown error'}`;
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
      const response = await fetch(url, {
        headers: {
          'X-CSRF-Token': (document.querySelector('meta[name="csrf-token"]') as any)?.content || '',
        },
      });

      if (!response.ok) {
        throw new Error(`Server responded with ${response.status}`);
      }

      processes.data = await response.json();
    } catch (error: unknown) {
      // Handle processes fetch error silently
      processes.error = `Failed to load process information: ${error instanceof Error ? error.message : 'Unknown error'}`;
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

  // Load data on mount
  onMount(() => {
    loadAllData();
  });
</script>

<div class="col-span-12 space-y-4" role="region" aria-label="System Dashboard">
  <div class="gap-6 system-cards-grid">
    <!-- System Information Card -->
    <SystemInfoCard
      title="System Information"
      systemInfo={systemInfo.data}
      temperatureInfo={systemTemperature.data}
      isLoading={systemInfo.loading}
      error={systemInfo.error}
      temperatureLoading={systemTemperature.loading}
      temperatureError={systemTemperature.error}
    />

    <!-- Disk Usage Card -->
    <ProgressCard
      title="Disk Usage"
      items={diskProgressItems}
      isLoading={diskUsage.loading}
      error={diskUsage.error}
      emptyMessage="No disk information available"
    />

    <!-- Memory Usage Card -->
    <ProgressCard
      title="Memory Usage"
      items={memoryProgressItems}
      isLoading={memoryUsage.loading}
      error={memoryUsage.error}
      showDetails={true}
    />
  </div>

  <!-- Process Information Card -->
  <ProcessTable
    title="Process Information"
    processes={processes.data}
    {showAllProcesses}
    isLoading={processes.loading}
    error={processes.error}
    onToggleShowAll={loadProcesses}
    className="mt-6"
  />

  <!-- Refresh button -->
  <div class="flex justify-center mt-6">
    <button
      class="btn btn-primary"
      onclick={loadAllData}
      disabled={isAnyLoading}
      aria-label="Refresh system data"
    >
      {#if isAnyLoading}
        <span class="loading loading-spinner loading-sm mr-2" aria-hidden="true"></span>
      {:else}
        <span class="mr-2" aria-hidden="true">
          {@html actionIcons.refresh}
        </span>
      {/if}
      Refresh Data
    </button>
  </div>
</div>

<style>
  .system-cards-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .system-cards-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (min-width: 1024px) {
    .system-cards-grid {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }
</style>
