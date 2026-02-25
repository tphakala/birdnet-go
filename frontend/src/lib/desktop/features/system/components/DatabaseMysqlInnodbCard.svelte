<script lang="ts">
  import { t } from '$lib/i18n';
  import { Gauge, HardDrive, Lock, AlertTriangle, Timer } from '@lucide/svelte';
  import { formatBytesCompact, formatNumber } from '$lib/utils/formatters';
  import type { MySQLDetails } from '$lib/types/database';

  interface Props {
    details: MySQLDetails;
  }

  let { details }: Props = $props();
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <h3
    class="text-xs font-semibold uppercase tracking-wider mb-3 text-slate-400 dark:text-slate-500"
  >
    {t('system.database.dashboard.innodb.title')}
  </h3>
  <div class="space-y-3">
    <!-- InnoDB Buffer Pool -->
    <div class="space-y-2">
      <div class="flex justify-between text-sm">
        <div class="flex items-center gap-2">
          <Gauge class="w-3.5 h-3.5 text-slate-400 dark:text-slate-500" />
          <span class="text-slate-400 dark:text-slate-500"
            >{t('system.database.dashboard.innodb.bufferPoolHitRate')}</span
          >
        </div>
        <span class="font-mono tabular-nums font-medium text-emerald-600 dark:text-emerald-400"
          >{details.buffer_pool_hit_rate.toFixed(1)}%</span
        >
      </div>
      <div class="flex justify-between text-sm">
        <div class="flex items-center gap-2">
          <HardDrive class="w-3.5 h-3.5 text-slate-400 dark:text-slate-500" />
          <span class="text-slate-400 dark:text-slate-500"
            >{t('system.database.dashboard.innodb.bufferPoolSize')}</span
          >
        </div>
        <span class="font-mono tabular-nums font-medium"
          >{formatBytesCompact(details.buffer_pool_size_bytes)}</span
        >
      </div>
    </div>

    <div class="border-t border-[var(--border-100)]"></div>

    <!-- Lock stats -->
    <div class="space-y-2">
      <div class="flex justify-between text-sm">
        <div class="flex items-center gap-2">
          <Lock class="w-3.5 h-3.5 text-slate-400 dark:text-slate-500" />
          <span class="text-slate-400 dark:text-slate-500"
            >{t('system.database.dashboard.innodb.lockWaits')}</span
          >
        </div>
        <span class="font-mono tabular-nums font-medium">{details.lock_waits}</span>
      </div>
      <div class="flex justify-between text-sm">
        <div class="flex items-center gap-2">
          <AlertTriangle class="w-3.5 h-3.5 text-slate-400 dark:text-slate-500" />
          <span class="text-slate-400 dark:text-slate-500"
            >{t('system.database.dashboard.innodb.deadlocks')}</span
          >
        </div>
        <span
          class="font-mono tabular-nums font-medium {details.deadlocks > 0
            ? 'text-red-600 dark:text-red-400'
            : 'text-emerald-600 dark:text-emerald-400'}">{details.deadlocks}</span
        >
      </div>
      <div class="flex justify-between text-sm">
        <div class="flex items-center gap-2">
          <Timer class="w-3.5 h-3.5 text-slate-400 dark:text-slate-500" />
          <span class="text-slate-400 dark:text-slate-500"
            >{t('system.database.dashboard.innodb.avgLockWait')}</span
          >
        </div>
        <span class="font-mono tabular-nums font-medium"
          >{details.avg_lock_wait_ms.toFixed(1)}ms</span
        >
      </div>
      <div class="flex justify-between text-sm">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.dashboard.innodb.tableLocksWaited')}</span
        >
        <span class="font-mono tabular-nums font-medium">{details.table_locks_waited}</span>
      </div>
      <div class="flex justify-between text-sm">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.dashboard.innodb.tableLocksImmediate')}</span
        >
        <span class="font-mono tabular-nums font-medium"
          >{formatNumber(details.table_locks_immediate)}</span
        >
      </div>
    </div>
  </div>
</div>
