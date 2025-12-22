<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { Terminal, ChevronUp, ChevronDown, ChevronsUpDown } from '@lucide/svelte';
  import { safeArrayAccess } from '$lib/utils/security';
  import { t } from '$lib/i18n';

  interface ProcessInfo {
    pid: number;
    name: string;
    status: string;
    cpu: number;
    memory: number;
    uptime: number;
  }

  type SortColumn = 'name' | 'status' | 'cpu' | 'memory' | 'uptime';
  type SortDirection = 'asc' | 'desc';

  interface Props {
    title: string;
    processes: ProcessInfo[];
    showAllProcesses?: boolean;
    isLoading?: boolean;
    error?: string | null;
    onToggleShowAll?: () => void;
    className?: string;
  }

  let {
    title,
    processes,
    showAllProcesses = false,
    isLoading = false,
    error = null,
    onToggleShowAll,
    className = '',
  }: Props = $props();

  // Sort state
  let sortColumn = $state<SortColumn>('name');
  let sortDirection = $state<SortDirection>('asc');

  // Handle column header click to toggle sort
  function handleSort(column: SortColumn) {
    if (sortColumn === column) {
      // Toggle direction if clicking same column
      sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      // New column: set to ascending by default
      sortColumn = column;
      sortDirection = 'asc';
    }
  }

  // Sorted processes
  let sortedProcesses = $derived.by(() => {
    if (!processes || processes.length === 0) return [];

    return [...processes].sort((a, b) => {
      let comparison = 0;

      switch (sortColumn) {
        case 'name': {
          const nameA = a.name === 'main' ? 'BirdNET-Go' : a.name;
          const nameB = b.name === 'main' ? 'BirdNET-Go' : b.name;
          comparison = nameA.localeCompare(nameB);
          break;
        }
        case 'status':
          comparison = a.status.localeCompare(b.status);
          break;
        case 'cpu':
          comparison = a.cpu - b.cpu;
          break;
        case 'memory':
          comparison = a.memory - b.memory;
          break;
        case 'uptime':
          comparison = a.uptime - b.uptime;
          break;
      }

      return sortDirection === 'asc' ? comparison : -comparison;
    });
  });

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

  function getStatusBadgeClass(status: string): string {
    switch (status) {
      case 'running':
        return 'badge-success';
      case 'sleeping':
      case 'sleep':
        return 'badge-warning';
      case 'zombie':
        return 'badge-error';
      case 'idle':
        return 'badge-info';
      default:
        return 'badge-secondary';
    }
  }

  // PERFORMANCE OPTIMIZATION: Cache process display names with $derived
  // Avoid string processing in template for BirdNET-Go name transformation
  let processDisplayNames = $derived(
    processes.reduce(
      (acc, process) => {
        acc[process.pid] = process.name === 'main' ? 'BirdNET-Go' : process.name;
        return acc;
      },
      {} as Record<number, string>
    )
  );

  function handleToggleChange() {
    if (onToggleShowAll) {
      onToggleShowAll();
    }
  }
</script>

<div class={cn('card bg-base-100 shadow-xs', className)}>
  <div class="card-body card-padding">
    <div class="flex justify-between items-center mb-2">
      <h2 class="card-title" id="process-info-heading">{title}</h2>

      <!-- Enhanced toggle for showing all processes -->
      <div class="flex items-center gap-2 bg-base-200 px-3 py-1.5 rounded-lg shadow-xs">
        <span class="text-sm font-medium">{t('system.processInfo.table.showAllProcesses')}</span>
        <input
          type="checkbox"
          class="toggle toggle-sm toggle-primary"
          checked={showAllProcesses}
          onchange={handleToggleChange}
          aria-label={t('system.processInfo.table.showAllProcesses')}
        />
      </div>
    </div>
    <div class="divider mt-0"></div>

    <!-- Loading state -->
    {#if isLoading}
      <div class="py-4">
        <div class="flex justify-center">
          <span class="loading loading-spinner loading-lg" aria-hidden="true"></span>
          <span class="sr-only">{t('system.processInfo.loading')}</span>
        </div>
      </div>
    {/if}

    <!-- Error state -->
    {#if error && !isLoading}
      <div class="alert alert-error" role="alert">{error}</div>
    {/if}

    <!-- Data loaded state -->
    {#if !isLoading && !error}
      <div class="overflow-x-auto" aria-labelledby="process-info-heading">
        <table class="table table-zebra w-full">
          <thead>
            <tr class="bg-base-200">
              <th scope="col">
                <button
                  type="button"
                  class="flex items-center gap-1 hover:text-primary transition-colors cursor-pointer w-full"
                  onclick={() => handleSort('name')}
                  aria-label="Sort by process name"
                >
                  {t('system.processInfo.table.process')}
                  {#if sortColumn === 'name'}
                    {#if sortDirection === 'asc'}
                      <ChevronUp class="size-4" />
                    {:else}
                      <ChevronDown class="size-4" />
                    {/if}
                  {:else}
                    <ChevronsUpDown class="size-4 opacity-30" />
                  {/if}
                </button>
              </th>
              <th scope="col">
                <button
                  type="button"
                  class="flex items-center gap-1 hover:text-primary transition-colors cursor-pointer w-full"
                  onclick={() => handleSort('status')}
                  aria-label="Sort by status"
                >
                  {t('system.processInfo.table.status')}
                  {#if sortColumn === 'status'}
                    {#if sortDirection === 'asc'}
                      <ChevronUp class="size-4" />
                    {:else}
                      <ChevronDown class="size-4" />
                    {/if}
                  {:else}
                    <ChevronsUpDown class="size-4 opacity-30" />
                  {/if}
                </button>
              </th>
              <th scope="col">
                <button
                  type="button"
                  class="flex items-center gap-1 hover:text-primary transition-colors cursor-pointer w-full"
                  onclick={() => handleSort('cpu')}
                  aria-label="Sort by CPU usage"
                >
                  {t('system.processInfo.table.cpu')}
                  {#if sortColumn === 'cpu'}
                    {#if sortDirection === 'asc'}
                      <ChevronUp class="size-4" />
                    {:else}
                      <ChevronDown class="size-4" />
                    {/if}
                  {:else}
                    <ChevronsUpDown class="size-4 opacity-30" />
                  {/if}
                </button>
              </th>
              <th scope="col">
                <button
                  type="button"
                  class="flex items-center gap-1 hover:text-primary transition-colors cursor-pointer w-full"
                  onclick={() => handleSort('memory')}
                  aria-label="Sort by memory usage"
                >
                  {t('system.processInfo.table.memory')}
                  {#if sortColumn === 'memory'}
                    {#if sortDirection === 'asc'}
                      <ChevronUp class="size-4" />
                    {:else}
                      <ChevronDown class="size-4" />
                    {/if}
                  {:else}
                    <ChevronsUpDown class="size-4 opacity-30" />
                  {/if}
                </button>
              </th>
              <th scope="col">
                <button
                  type="button"
                  class="flex items-center gap-1 hover:text-primary transition-colors cursor-pointer w-full"
                  onclick={() => handleSort('uptime')}
                  aria-label="Sort by uptime"
                >
                  {t('system.processInfo.table.uptime')}
                  {#if sortColumn === 'uptime'}
                    {#if sortDirection === 'asc'}
                      <ChevronUp class="size-4" />
                    {:else}
                      <ChevronDown class="size-4" />
                    {/if}
                  {:else}
                    <ChevronsUpDown class="size-4 opacity-30" />
                  {/if}
                </button>
              </th>
            </tr>
          </thead>
          <tbody>
            {#if sortedProcesses.length === 0}
              <tr>
                <td
                  colspan="5"
                  class="text-center py-6 opacity-70"
                  style:color="var(--color-base-content)"
                >
                  {t('system.processInfo.noProcesses')}
                </td>
              </tr>
            {:else}
              {#each sortedProcesses as process (process.pid)}
                <tr class="hover:bg-base-200/50 transition-colors duration-150">
                  <td>
                    <div class="flex items-start gap-2">
                      <div class="p-1.5 bg-primary/10 rounded-md text-primary">
                        <Terminal class="size-4" />
                      </div>
                      <div>
                        <div class="font-medium">
                          {processDisplayNames[process.pid]}
                        </div>
                        <div class="text-xs opacity-60" style:color="var(--color-base-content)">
                          PID: {process.pid}
                        </div>
                      </div>
                    </div>
                  </td>
                  <td>
                    <span class="badge badge-sm {getStatusBadgeClass(process.status)}">
                      {process.status}
                    </span>
                  </td>
                  <td>
                    <div class="flex items-center gap-2">
                      <div
                        class="w-16 h-2 bg-base-200 rounded-full overflow-hidden"
                        role="progressbar"
                        aria-valuenow={Math.min(Math.round(process.cpu), 100)}
                        aria-valuemin="0"
                        aria-valuemax="100"
                        aria-valuetext="{Math.round(process.cpu)}% CPU usage"
                      >
                        <div
                          class="h-full rounded-full bg-primary"
                          style:width="{Math.min(Math.round(process.cpu), 100)}%"
                        ></div>
                      </div>
                      <span class="text-sm">{Math.round(process.cpu)}%</span>
                    </div>
                  </td>
                  <td>
                    <span class="text-sm font-medium">{formatStorage(process.memory)}</span>
                  </td>
                  <td>
                    <span class="text-sm">{formatUptime(process.uptime)}</span>
                  </td>
                </tr>
              {/each}
            {/if}
          </tbody>
        </table>
      </div>
    {/if}
  </div>
</div>
