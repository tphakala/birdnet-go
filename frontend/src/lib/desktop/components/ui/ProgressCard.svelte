<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { safeArrayAccess } from '$lib/utils/security';

  interface ProgressItem {
    label: string;
    used: number;
    total: number;
    usagePercent: number;
    mountpoint?: string;
    // Memory-specific fields
    buffers?: number;
    cached?: number;
    available?: number;
    free?: number;
  }

  interface Props {
    title: string;
    items: ProgressItem[];
    isLoading?: boolean;
    error?: string | null;
    showDetails?: boolean;
    emptyMessage?: string;
    className?: string;
  }

  let {
    title,
    items,
    isLoading = false,
    error = null,
    showDetails = false,
    emptyMessage = 'No data available',
    className = '',
  }: Props = $props();

  // PERFORMANCE OPTIMIZATION: Pure utility functions outside reactive context
  // These functions only depend on their parameters, not component state
  // Moving them outside prevents recreation on every component render
  function formatStorage(bytes: number): string {
    if (!bytes) return '0 B';
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (
      Math.round((bytes / Math.pow(1024, i)) * 100) / 100 + ' ' + (safeArrayAccess(sizes, i) ?? 'B')
    );
  }

  function getProgressColor(percentage: number): string {
    if (percentage > 90) return 'bg-error';
    if (percentage > 70) return 'bg-warning';
    return 'bg-success';
  }

  function getBufferColor(baseColor: string): string {
    // Return a lighter/muted version of the base color for buffers
    switch (baseColor) {
      case 'bg-error':
        return 'bg-error/50';
      case 'bg-warning':
        return 'bg-warning/50';
      case 'bg-success':
        return 'bg-success/50';
      default:
        return 'bg-info/50';
    }
  }

  // PERFORMANCE OPTIMIZATION: Cache dynamic heading ID generation with $derived
  // Avoids string processing on every render when title hasn't changed
  let headingId = $derived(`${title.toLowerCase().replace(/\s+/g, '-')}-heading`);
</script>

<div class={cn('card bg-base-100 shadow-sm', className)}>
  <div class="card-body card-padding">
    <h2 class="card-title" id={headingId}>{title}</h2>
    <div class="divider"></div>

    <!-- Loading state -->
    {#if isLoading}
      <div class="py-4">
        <div class="flex flex-col gap-2">
          <div class="skeleton h-4 w-full mb-2"></div>
          <div class="skeleton h-4 w-4/5 mb-2"></div>
          <div class="skeleton h-4 w-3/4"></div>
        </div>
      </div>
    {/if}

    <!-- Error state -->
    {#if error && !isLoading}
      <div class="alert alert-error" role="alert">{error}</div>
    {/if}

    <!-- Data loaded state -->
    {#if !isLoading && !error && items.length > 0}
      <div class="space-y-4" aria-labelledby={headingId}>
        {#each items as item}
          <div>
            <!-- Title and usage - optimized for card width -->
            <div class="flex flex-col lg:flex-row lg:justify-between mb-2 gap-1">
              <span class="font-medium">{item.mountpoint || item.label}</span>
              <span class="text-base-content/80 lg:text-base-content">
                {formatStorage(item.used)} / {formatStorage(item.total)}
              </span>
            </div>
            <div
              class="w-full bg-base-200 rounded-full h-2 relative overflow-hidden"
              role="progressbar"
              aria-valuenow={item.usagePercent}
              aria-valuemin="0"
              aria-valuemax="100"
              aria-valuetext="{Math.round(item.usagePercent)}% used"
            >
              {#if item.buffers !== undefined || item.cached !== undefined}
                {@const buffCachePercent =
                  (((item.buffers ?? 0) + (item.cached ?? 0)) / item.total) * 100}
                {@const totalPercent = item.usagePercent + buffCachePercent}
                {@const baseColor = getProgressColor(item.usagePercent)}
                <!-- Total allocation including buffers/cache -->
                <div
                  class="h-2 absolute inset-y-0 left-0 {getBufferColor(baseColor)}"
                  style:width="{Math.min(totalPercent, 100)}%"
                ></div>
                <!-- Used memory (excluding buffers/cache) -->
                <div
                  class="h-2 absolute inset-y-0 left-0 {baseColor}"
                  style:width="{item.usagePercent}%"
                ></div>
              {:else}
                <!-- Simple progress bar for non-memory items -->
                <div
                  class="h-2 rounded-full {getProgressColor(item.usagePercent)}"
                  style:width="{item.usagePercent}%"
                ></div>
              {/if}
            </div>
            <!-- Usage percentages - clear layout for tablets/desktop -->
            <div class="text-xs lg:text-right mt-1">
              {#if item.buffers !== undefined || item.cached !== undefined}
                {@const buffCachePercent =
                  (((item.buffers ?? 0) + (item.cached ?? 0)) / item.total) * 100}
                <div class="space-y-0.5">
                  <div>{Math.round(item.usagePercent)}% used</div>
                  {#if buffCachePercent > 0}
                    <div class="text-base-content/70">
                      {Math.round(buffCachePercent)}% buff/cache
                    </div>
                  {/if}
                </div>
              {:else}
                {Math.round(item.usagePercent)}% used
              {/if}
            </div>
          </div>
        {/each}

        <!-- Memory Details - single column for clarity on tablets/desktop -->
        {#if showDetails && items.length === 1}
          {@const item = items[0]}
          <div class="divider my-3"></div>
          <div class="space-y-2 text-sm">
            <!-- Free memory -->
            <div class="flex justify-between items-center">
              <span class="text-base-content/60 min-w-[80px]">Free:</span>
              <span class="font-semibold text-right"
                >{formatStorage(item.free ?? item.total - item.used)}</span
              >
            </div>
            <!-- Available memory -->
            <div class="flex justify-between items-center">
              <span class="text-base-content/60 min-w-[80px]">Available:</span>
              <span class="font-semibold text-right"
                >{formatStorage(item.available ?? item.total - item.used)}</span
              >
            </div>
            <!-- Buff/Cache combined (if available) -->
            {#if (item.buffers !== undefined || item.cached !== undefined) && (item.buffers ?? 0) + (item.cached ?? 0) > 0}
              <div class="flex justify-between items-center">
                <span class="text-base-content/60 min-w-[80px]">Buff/Cache:</span>
                <span class="font-semibold text-right"
                  >{formatStorage((item.buffers ?? 0) + (item.cached ?? 0))}</span
                >
              </div>
            {/if}
          </div>
        {/if}
      </div>
    {/if}

    <!-- No data state -->
    {#if !isLoading && !error && items.length === 0}
      <div class="text-center py-4 text-base-content/70">{emptyMessage}</div>
    {/if}
  </div>
</div>
