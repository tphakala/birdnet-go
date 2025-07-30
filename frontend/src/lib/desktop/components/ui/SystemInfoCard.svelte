<script lang="ts">
  import { cn } from '$lib/utils/cn';

  interface SystemInfo {
    os_display: string;
    hostname: string;
    uptime_seconds: number;
    num_cpu: number;
    system_model?: string;
    time_zone?: string;
  }

  interface TemperatureInfo {
    is_available: boolean;
    celsius?: number;
  }

  interface Props {
    title: string;
    systemInfo: SystemInfo;
    temperatureInfo?: TemperatureInfo;
    isLoading?: boolean;
    error?: string | null;
    temperatureLoading?: boolean;
    temperatureError?: string | null;
    className?: string;
  }

  let {
    title,
    systemInfo,
    temperatureInfo,
    isLoading = false,
    error = null,
    temperatureLoading = false,
    temperatureError = null,
    className = '',
  }: Props = $props();

  // PERFORMANCE OPTIMIZATION: Pure function for uptime formatting
  // Moved outside reactive context to avoid recreation on every component update
  // This function only depends on its parameter, not component state
  function formatUptime(seconds: number): string {
    if (!seconds) return 'N/A';

    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);

    const parts = [];
    if (days > 0) parts.push(`${days}d`);
    if (hours > 0) parts.push(`${hours}h`);
    if (minutes > 0) parts.push(`${minutes}m`);

    return parts.join(' ') || '< 1m';
  }

  // PERFORMANCE OPTIMIZATION: Cache formatted temperature string with $derived
  // Avoids recalculating temperature format on every render when data hasn't changed
  let formattedTemperature = $derived(
    temperatureInfo?.celsius ? temperatureInfo.celsius.toFixed(1) + '°C' : 'N/A'
  );
</script>

<div class={cn('card bg-base-100 shadow-sm', className)}>
  <div class="card-body card-padding">
    <h2 class="card-title" id="system-info-heading">{title}</h2>
    <div class="divider"></div>

    <!-- Loading state -->
    {#if isLoading}
      <div class="py-4">
        <div class="flex flex-col gap-2">
          <div class="skeleton h-4 w-full mb-2"></div>
          <div class="skeleton h-4 w-3/4 mb-2"></div>
          <div class="skeleton h-4 w-5/6"></div>
        </div>
      </div>
    {/if}

    <!-- Error state -->
    {#if error && !isLoading}
      <div class="alert alert-error" role="alert">{error}</div>
    {/if}

    <!-- Data loaded state -->
    {#if !isLoading && !error}
      <div class="space-y-2" aria-labelledby="system-info-heading">
        <div class="flex justify-between">
          <span class="text-base-content/70">Operating System:</span>
          <span class="font-medium">{systemInfo.os_display || 'N/A'}</span>
        </div>
        <div class="flex justify-between">
          <span class="text-base-content/70">Hostname:</span>
          <span class="font-medium">{systemInfo.hostname || 'N/A'}</span>
        </div>
        <div class="flex justify-between">
          <span class="text-base-content/70">Uptime:</span>
          <span class="font-medium">{formatUptime(systemInfo.uptime_seconds) || 'N/A'}</span>
        </div>
        <div class="flex justify-between">
          <span class="text-base-content/70">CPU Count:</span>
          <span class="font-medium">{systemInfo.num_cpu || 'N/A'}</span>
        </div>

        <!-- CPU Temperature Row (conditional) -->
        {#if temperatureInfo?.is_available && !temperatureLoading && !temperatureError}
          <div class="flex justify-between">
            <span class="text-base-content/70">CPU Temperature:</span>
            <span class="font-medium">{formattedTemperature}</span>
          </div>
        {/if}

        <!-- Loading state for temperature -->
        {#if temperatureLoading}
          <div class="py-1">
            <div class="skeleton h-4 w-1/2"></div>
          </div>
        {/if}

        <!-- Error state for temperature -->
        {#if temperatureError && !temperatureLoading}
          <div class="text-error text-sm" role="alert">{temperatureError}</div>
        {/if}

        <!-- System Model Row -->
        {#if systemInfo.system_model}
          <div class="flex justify-between">
            <span class="text-base-content/70">System Model:</span>
            <span class="font-medium">{systemInfo.system_model || 'N/A'}</span>
          </div>
        {/if}

        <!-- Time Zone Row -->
        {#if systemInfo.time_zone}
          <div class="flex justify-between">
            <span class="text-base-content/70">Time Zone:</span>
            <span class="font-medium">{systemInfo.time_zone || 'N/A'}</span>
          </div>
        {/if}
      </div>
    {/if}
  </div>
</div>
