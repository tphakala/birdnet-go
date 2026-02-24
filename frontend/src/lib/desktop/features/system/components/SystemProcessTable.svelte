<script lang="ts">
  import { t } from '$lib/i18n';
  import { Terminal, ChevronUp, ChevronDown, ChevronsUpDown } from '@lucide/svelte';
  import { formatBytesCompact, formatUptimeCompact } from '$lib/utils/formatters';

  interface ProcessInfo {
    pid: number;
    name: string;
    status: string;
    cpu: number;
    memory: number;
    uptime: number;
  }

  interface Props {
    processes: ProcessInfo[];
    showAllProcesses: boolean;
    processName?: string;
    onToggleShowAll: () => void;
  }

  let {
    processes,
    showAllProcesses,
    processName = 'BirdNET-Go',
    onToggleShowAll,
  }: Props = $props();

  type SortColumn = 'name' | 'status' | 'cpu' | 'memory' | 'uptime';

  let sortColumn = $state<SortColumn>('cpu');
  let sortDirection = $state<'asc' | 'desc'>('desc');

  let sortedProcesses = $derived.by(() => {
    return [...processes].sort((a, b) => {
      let cmp = 0;
      switch (sortColumn) {
        case 'name': {
          const na = a.name === 'main' ? processName : a.name;
          const nb = b.name === 'main' ? processName : b.name;
          cmp = na.localeCompare(nb);
          break;
        }
        case 'status':
          cmp = a.status.localeCompare(b.status);
          break;
        case 'cpu':
          cmp = a.cpu - b.cpu;
          break;
        case 'memory':
          cmp = a.memory - b.memory;
          break;
        case 'uptime':
          cmp = a.uptime - b.uptime;
          break;
      }
      return sortDirection === 'asc' ? cmp : -cmp;
    });
  });

  function toggleSort(col: SortColumn): void {
    if (sortColumn === col) {
      sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      sortColumn = col;
      sortDirection = col === 'name' || col === 'status' ? 'asc' : 'desc';
    }
  }

  function statusBadgeClass(status: string): string {
    switch (status) {
      case 'running':
        return 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400';
      case 'sleeping':
      case 'sleep':
        return 'bg-amber-500/15 text-amber-600 dark:text-amber-400';
      case 'zombie':
        return 'bg-red-500/15 text-red-600 dark:text-red-400';
      default:
        return 'bg-slate-500/15 text-slate-600 dark:text-slate-400';
    }
  }

  const columns: { key: SortColumn; label: string }[] = [
    { key: 'name', label: 'system.processInfo.headers.process' },
    { key: 'status', label: 'system.processInfo.headers.status' },
    { key: 'cpu', label: 'system.processInfo.headers.cpu' },
    { key: 'memory', label: 'system.processInfo.headers.memory' },
    { key: 'uptime', label: 'system.processInfo.headers.uptime' },
  ];
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <div class="flex items-center justify-between mb-3">
    <div class="flex items-center gap-2">
      <h3 class="text-xs font-semibold uppercase tracking-wider text-slate-400 dark:text-slate-500">
        {t('system.processInfo.title')}
      </h3>
      <span
        class="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium bg-slate-500/10 text-slate-400 dark:text-slate-500"
      >
        {processes.length}
      </span>
    </div>
    <div class="flex items-center gap-2 cursor-pointer">
      <span class="text-xs text-slate-400 dark:text-slate-500"
        >{t('system.processInfo.showAll')}</span
      >
      <button
        type="button"
        class="relative w-8 h-4 rounded-full transition-colors cursor-pointer {showAllProcesses
          ? 'bg-blue-500'
          : 'bg-[var(--surface-300)]'}"
        role="switch"
        aria-checked={showAllProcesses}
        aria-label={t('system.processInfo.showAll')}
        onclick={onToggleShowAll}
      >
        <div
          class="absolute top-0.5 w-3 h-3 rounded-full bg-white shadow-sm transition-transform"
          style:transform="translateX({showAllProcesses ? '18px' : '2px'})"
        ></div>
      </button>
    </div>
  </div>

  <div class="overflow-x-auto">
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b border-[var(--border-100)]">
          {#each columns as col (col.key)}
            <th
              class="text-left py-2 px-3 text-xs font-medium cursor-pointer select-none hover:text-blue-500 transition-colors text-slate-400 dark:text-slate-500"
              role="columnheader"
              tabindex="0"
              aria-sort={sortColumn === col.key
                ? sortDirection === 'asc'
                  ? 'ascending'
                  : 'descending'
                : 'none'}
              onclick={() => toggleSort(col.key)}
              onkeydown={(e: KeyboardEvent) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  toggleSort(col.key);
                }
              }}
            >
              <div class="flex items-center gap-1">
                {t(col.label)}
                {#if sortColumn === col.key}
                  {#if sortDirection === 'asc'}
                    <ChevronUp class="w-3 h-3" />
                  {:else}
                    <ChevronDown class="w-3 h-3" />
                  {/if}
                {:else}
                  <ChevronsUpDown class="w-3 h-3 opacity-30" />
                {/if}
              </div>
            </th>
          {/each}
        </tr>
      </thead>
      <tbody>
        {#each sortedProcesses as proc (proc.pid)}
          <tr
            class="border-b last:border-b-0 border-[var(--border-100)]/50 hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition-colors"
          >
            <td class="py-2 px-3">
              <div class="flex items-center gap-2">
                <Terminal class="w-3.5 h-3.5 shrink-0 text-slate-400 dark:text-slate-500" />
                <div>
                  <div class="font-medium text-sm">
                    {proc.name === 'main' ? processName : proc.name}
                  </div>
                  <div
                    class="text-[10px] font-mono tabular-nums text-slate-400 dark:text-slate-500"
                  >
                    {t('system.processInfo.headers.pid')}
                    {proc.pid}
                  </div>
                </div>
              </div>
            </td>
            <td class="py-2 px-3">
              <span
                class="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium {statusBadgeClass(
                  proc.status
                )}"
              >
                {proc.status}
              </span>
            </td>
            <td class="py-2 px-3">
              <div class="flex items-center gap-2">
                <div class="w-12 h-1.5 rounded-full overflow-hidden bg-[var(--surface-300)]">
                  <div
                    class="h-full rounded-full bg-blue-500 transition-[width] duration-600 ease-out"
                    style:width="{Math.min(proc.cpu, 100)}%"
                  ></div>
                </div>
                <span class="font-mono tabular-nums text-xs">{proc.cpu.toFixed(1)}%</span>
              </div>
            </td>
            <td class="py-2 px-3">
              <span class="font-mono tabular-nums text-xs font-medium"
                >{formatBytesCompact(proc.memory)}</span
              >
            </td>
            <td class="py-2 px-3">
              <span class="font-mono tabular-nums text-xs">{formatUptimeCompact(proc.uptime)}</span>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>
