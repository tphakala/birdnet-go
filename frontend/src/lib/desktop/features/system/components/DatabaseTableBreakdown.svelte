<script lang="ts">
  import { t } from '$lib/i18n';
  import { ChevronDown, ChevronUp, ChevronsUpDown } from '@lucide/svelte';
  import { formatBytesCompact, formatNumber } from '$lib/utils/formatters';
  import type { TableStats } from '$lib/types/database';

  interface Props {
    tables: TableStats[];
    showEngine?: boolean; // true for MySQL
  }

  let { tables = [], showEngine = false }: Props = $props();

  let sortColumn = $state<string>('size_bytes');
  let sortDirection = $state<'asc' | 'desc'>('desc');

  let totalSize = $derived(tables.reduce((s, tbl) => s + tbl.size_bytes, 0));

  let sortedTables = $derived.by(() => {
    return [...tables].sort((a, b) => {
      let cmp = 0;
      switch (sortColumn) {
        case 'name':
          cmp = a.name.localeCompare(b.name);
          break;
        case 'row_count':
          cmp = a.row_count - b.row_count;
          break;
        case 'size_bytes':
          cmp = a.size_bytes - b.size_bytes;
          break;
        case 'engine':
          cmp = (a.engine ?? '').localeCompare(b.engine ?? '');
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
      sortDirection = col === 'name' || col === 'engine' ? 'asc' : 'desc';
    }
  }

  type ColumnDef = { key: string; label: string };
  let columns = $derived.by((): ColumnDef[] => {
    const cols: ColumnDef[] = [{ key: 'name', label: t('system.database.dashboard.tables.table') }];
    if (showEngine)
      cols.push({ key: 'engine', label: t('system.database.dashboard.tables.engine') });
    cols.push(
      { key: 'row_count', label: t('system.database.dashboard.tables.rows') },
      { key: 'size_bytes', label: t('system.database.dashboard.tables.size') }
    );
    return cols;
  });
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <div class="flex items-center justify-between mb-3">
    <div class="flex items-center gap-2">
      <h3 class="text-xs font-semibold uppercase tracking-wider text-slate-400 dark:text-slate-500">
        {t('system.database.dashboard.tables.title')}
      </h3>
      <span
        class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] bg-slate-500/10 text-slate-400 dark:text-slate-500"
      >
        {tables.length}
      </span>
    </div>
    <span class="text-xs font-mono tabular-nums text-slate-400 dark:text-slate-500">
      {t('system.database.dashboard.tables.total')}: {formatBytesCompact(totalSize)}
    </span>
  </div>

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
          <th class="text-left py-2 px-3 text-xs font-medium text-slate-400 dark:text-slate-500">
            {t('system.database.dashboard.tables.usage')}
          </th>
        </tr>
      </thead>
      <tbody>
        {#each sortedTables as table (table.name)}
          {@const pct = totalSize > 0 ? (table.size_bytes / totalSize) * 100 : 0}
          <tr
            class="border-b border-[var(--border-100)] last:border-b-0 hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition-colors"
          >
            <td class="py-2 px-3">
              <span class="font-mono text-xs font-medium">{table.name}</span>
            </td>
            {#if showEngine}
              <td class="py-2 px-3">
                <span
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] bg-orange-500/10 text-orange-600 dark:text-orange-400"
                  >{table.engine}</span
                >
              </td>
            {/if}
            <td class="py-2 px-3">
              <span class="font-mono tabular-nums text-xs">{formatNumber(table.row_count)}</span>
            </td>
            <td class="py-2 px-3">
              <span class="font-mono tabular-nums text-xs font-medium"
                >{formatBytesCompact(table.size_bytes)}</span
              >
            </td>
            <td class="py-2 px-3">
              <div class="flex items-center gap-2">
                <div class="w-16 h-1.5 rounded-full overflow-hidden bg-slate-100 dark:bg-slate-800">
                  <div
                    class="h-full rounded-full {showEngine
                      ? 'bg-orange-500'
                      : 'bg-blue-500'} transition-all duration-300"
                    style:width="{pct}%"
                  ></div>
                </div>
                <span class="font-mono tabular-nums text-[10px] text-slate-400 dark:text-slate-500"
                  >{pct.toFixed(1)}%</span
                >
              </div>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>
