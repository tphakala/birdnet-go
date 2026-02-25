<script lang="ts">
  import { t } from '$lib/i18n';
  import { Zap } from '@lucide/svelte';
  import { formatBytesCompact, formatNumber } from '$lib/utils/formatters';
  import type { SQLiteDetails } from '$lib/types/database';

  interface Props {
    details: SQLiteDetails;
  }

  let { details }: Props = $props();
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <h3
    class="text-xs font-semibold uppercase tracking-wider mb-3 text-slate-400 dark:text-slate-500"
  >
    {t('system.database.dashboard.locksWal.title')}
  </h3>
  <div class="space-y-3">
    <!-- Lock stats -->
    <div class="space-y-2">
      <div class="flex justify-between text-sm">
        <div class="flex items-center gap-2">
          <Zap class="w-3.5 h-3.5 text-slate-400 dark:text-slate-500" />
          <span class="text-slate-400 dark:text-slate-500"
            >{t('system.database.dashboard.locksWal.busyTimeouts')}</span
          >
        </div>
        <span
          class="font-mono tabular-nums font-medium {details.busy_timeouts > 0
            ? 'text-amber-600 dark:text-amber-400'
            : ''}">{details.busy_timeouts}</span
        >
      </div>
    </div>

    <div class="border-t border-[var(--border-100)]"></div>

    <!-- WAL stats -->
    <div class="space-y-2">
      <div class="flex justify-between text-sm">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.dashboard.locksWal.walSize')}</span
        >
        <span class="font-mono tabular-nums font-medium"
          >{formatBytesCompact(details.wal_size_bytes)}</span
        >
      </div>
      <div class="flex justify-between text-sm">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.dashboard.locksWal.checkpoints')}</span
        >
        <span class="font-mono tabular-nums font-medium"
          >{formatNumber(details.wal_checkpoints)}</span
        >
      </div>
      <div class="flex justify-between text-sm">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.dashboard.locksWal.freelistPages')}</span
        >
        <span class="font-mono tabular-nums font-medium">{details.freelist_pages}</span>
      </div>
      <div class="flex justify-between text-sm">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.dashboard.locksWal.cacheSize')}</span
        >
        <span class="font-mono tabular-nums font-medium"
          >{formatBytesCompact(details.cache_size_bytes)}</span
        >
      </div>
    </div>
  </div>
</div>
