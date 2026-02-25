<script lang="ts">
  import { Download, Clock, ChevronUp, ChevronDown, ChevronsUpDown, Loader2 } from '@lucide/svelte';
  import { formatBytesCompact, formatDateTime } from '$lib/utils/formatters';

  interface BackupJob {
    job_id: string;
    db_type: string;
    status: string;
    progress: number;
    bytes_written: number;
    total_bytes: number;
    started_at: string;
    completed_at?: string;
    error?: string;
    download_url?: string;
  }

  interface Props {
    backups: BackupJob[];
    onCreateBackup: () => void;
    isCreating?: boolean;
    disabled?: boolean;
  }

  let { backups = [], onCreateBackup, isCreating = false, disabled = false }: Props = $props();

  let sortColumn = $state<string>('date');
  let sortDirection = $state<'asc' | 'desc'>('desc');

  type ColumnDef = { key: string; label: string };
  const columns: ColumnDef[] = [
    { key: 'date', label: 'Date' },
    { key: 'type', label: 'Database' },
    { key: 'size', label: 'Size' },
    { key: 'status', label: 'Status' },
  ];

  const statusOrder: Record<string, number> = {
    failed: 0,
    in_progress: 1,
    pending: 2,
    completed: 3,
  };

  let safeBackups = $derived(Array.isArray(backups) ? backups : []);

  let sortedBackups = $derived.by(() => {
    return [...safeBackups].sort((a, b) => {
      let cmp = 0;
      switch (sortColumn) {
        case 'date':
          cmp = new Date(a.started_at).getTime() - new Date(b.started_at).getTime();
          break;
        case 'type':
          cmp = a.db_type.localeCompare(b.db_type);
          break;
        case 'size':
          cmp = a.bytes_written - b.bytes_written;
          break;
        case 'status':
          cmp = (statusOrder[a.status] ?? 99) - (statusOrder[b.status] ?? 99);
          break;
      }
      return sortDirection === 'asc' ? cmp : -cmp;
    });
  });

  function toggleSort(col: string): void {
    if (sortColumn === col) {
      sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      sortColumn = col;
      sortDirection = col === 'type' ? 'asc' : 'desc';
    }
  }

  function statusClasses(status: string): string {
    switch (status) {
      case 'pending':
        return 'bg-slate-500/10 text-slate-600 dark:text-slate-400';
      case 'in_progress':
        return 'bg-blue-500/10 text-blue-600 dark:text-blue-400';
      case 'completed':
        return 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400';
      case 'failed':
        return 'bg-red-500/10 text-red-600 dark:text-red-400';
      default:
        return 'bg-slate-500/10 text-slate-600 dark:text-slate-400';
    }
  }

  function dbTypeClasses(dbType: string): string {
    return dbType === 'legacy'
      ? 'bg-blue-500/10 text-blue-600 dark:text-blue-400'
      : 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400';
  }
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <div class="flex items-center justify-between mb-3">
    <div class="flex items-center gap-2">
      <h3 class="text-xs font-semibold uppercase tracking-wider text-slate-400 dark:text-slate-500">
        Backup History
      </h3>
      <span
        class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] bg-slate-500/10 text-slate-400 dark:text-slate-500"
      >
        {backups.length}
      </span>
    </div>
    <button
      class="flex items-center gap-2 px-3 py-1.5 text-xs font-medium rounded-lg transition-colors cursor-pointer border hover:bg-black/5 dark:hover:bg-white/5 disabled:opacity-50 disabled:cursor-not-allowed"
      style:border-color="var(--border-100)"
      style:color="var(--text-secondary, var(--slate-400))"
      onclick={onCreateBackup}
      disabled={isCreating || disabled}
    >
      {#if isCreating}
        <Loader2 class="w-3.5 h-3.5 animate-spin" />
        Creating...
      {:else}
        <Download class="w-3.5 h-3.5" />
        Create Backup
      {/if}
    </button>
  </div>

  {#if backups.length === 0}
    <div class="py-8 text-center text-sm text-slate-400 dark:text-slate-500">No backups yet</div>
  {:else}
    <div class="overflow-x-auto">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-[var(--border-100)]">
            {#each columns as col (col.key)}
              <th
                class="text-left py-2 px-3 text-xs font-medium cursor-pointer select-none hover:text-blue-500 transition-colors text-slate-400 dark:text-slate-500"
                onclick={() => toggleSort(col.key)}
              >
                <div class="flex items-center gap-1">
                  {col.label}
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
            <th class="text-right py-2 px-3 text-xs font-medium text-slate-400 dark:text-slate-500">
              Actions
            </th>
          </tr>
        </thead>
        <tbody>
          {#each sortedBackups as backup (backup.job_id)}
            <tr
              class="border-b border-[var(--border-100)] last:border-b-0 hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition-colors"
            >
              <td class="py-2 px-3">
                <div class="flex items-center gap-2">
                  <Clock class="w-3.5 h-3.5 flex-shrink-0 text-slate-400 dark:text-slate-500" />
                  <span class="text-sm tabular-nums">{formatDateTime(backup.started_at)}</span>
                </div>
              </td>
              <td class="py-2 px-3">
                <span
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] {dbTypeClasses(
                    backup.db_type
                  )}">{backup.db_type}</span
                >
              </td>
              <td class="py-2 px-3">
                <span class="font-mono tabular-nums text-xs font-medium"
                  >{formatBytesCompact(backup.bytes_written)}</span
                >
              </td>
              <td class="py-2 px-3">
                <span
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] {statusClasses(
                    backup.status
                  )}">{backup.status}</span
                >
              </td>
              <td class="py-2 px-3 text-right">
                {#if backup.status === 'completed' && backup.download_url}
                  <a
                    href={backup.download_url}
                    class="inline-flex items-center gap-1 px-2 py-1 text-xs font-medium rounded transition-colors hover:bg-blue-500/10 text-blue-600 dark:text-blue-400"
                    download
                  >
                    <Download class="w-3 h-3" />
                    Download
                  </a>
                {:else if backup.status === 'in_progress'}
                  <span
                    class="inline-flex items-center gap-1 text-xs tabular-nums text-blue-600 dark:text-blue-400"
                  >
                    <Loader2 class="w-3 h-3 animate-spin" />
                    {backup.progress}%
                  </span>
                {:else if backup.status === 'failed'}
                  <span class="text-xs text-red-600 dark:text-red-400" title={backup.error ?? ''}>
                    Failed
                  </span>
                {:else}
                  <span class="text-xs text-slate-400 dark:text-slate-500">&mdash;</span>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
