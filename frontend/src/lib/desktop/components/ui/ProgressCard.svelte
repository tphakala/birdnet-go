<script lang="ts">
  import { cn } from '$lib/utils/cn';

  interface ProgressItem {
    label: string;
    used: number;
    total: number;
    usagePercent: number;
    mountpoint?: string;
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

  function formatStorage(bytes: number): string {
    if (!bytes) return '0 B';
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return Math.round((bytes / Math.pow(1024, i)) * 100) / 100 + ' ' + sizes[i];
  }

  function getProgressColor(percentage: number): string {
    if (percentage > 90) return 'bg-error';
    if (percentage > 70) return 'bg-warning';
    return 'bg-success';
  }
</script>

<div class={cn('card bg-base-100 shadow-sm', className)}>
  <div class="card-body card-padding">
    <h2 class="card-title" id={`${title.toLowerCase().replace(/\s+/g, '-')}-heading`}>{title}</h2>
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
      <div
        class="space-y-4"
        aria-labelledby={`${title.toLowerCase().replace(/\s+/g, '-')}-heading`}
      >
        {#each items as item}
          <div>
            <div class="flex justify-between mb-1">
              <span class="font-medium">{item.mountpoint || item.label}</span>
              <span>{formatStorage(item.used)} / {formatStorage(item.total)}</span>
            </div>
            <div
              class="w-full bg-base-200 rounded-full h-2"
              role="progressbar"
              aria-valuenow={item.usagePercent}
              aria-valuemin="0"
              aria-valuemax="100"
              aria-valuetext="{Math.round(item.usagePercent)}% used"
            >
              <div
                class="h-2 rounded-full {getProgressColor(item.usagePercent)}"
                style:width="{item.usagePercent}%"
              ></div>
            </div>
            <div class="text-xs text-right mt-1">{Math.round(item.usagePercent)}% used</div>
          </div>
        {/each}

        <!-- Memory Details for memory usage -->
        {#if showDetails && items.length === 1}
          {@const item = items[0]}
          <div class="grid grid-cols-2 gap-2 text-sm">
            <div class="flex justify-between">
              <span class="text-base-content/70">Free:</span>
              <span>{formatStorage(item.total - item.used)}</span>
            </div>
            <div class="flex justify-between">
              <span class="text-base-content/70">Available:</span>
              <span>{formatStorage(item.total - item.used)}</span>
            </div>
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
