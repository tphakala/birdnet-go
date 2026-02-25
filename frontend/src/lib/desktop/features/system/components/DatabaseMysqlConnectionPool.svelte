<script lang="ts">
  import { t } from '$lib/i18n';
  import { Users, AlertTriangle, Network } from '@lucide/svelte';
  import { formatNumber } from '$lib/utils/formatters';
  import type { MySQLDetails } from '$lib/types/database';

  interface Props {
    details: MySQLDetails;
  }

  let { details }: Props = $props();

  let poolUsagePct = $derived(
    details.max_connections > 0 ? (details.active_connections / details.max_connections) * 100 : 0
  );
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <h3
    class="text-xs font-semibold uppercase tracking-wider mb-3 text-slate-400 dark:text-slate-500"
  >
    {t('system.database.dashboard.connectionPool.title')}
  </h3>
  <div class="space-y-3">
    <!-- Pool usage bar -->
    <div>
      <div class="flex items-center justify-between mb-1.5">
        <span class="text-sm font-medium"
          >{t('system.database.dashboard.connectionPool.poolUsage')}</span
        >
        <span class="font-mono tabular-nums text-sm">
          <span class="font-semibold">{details.active_connections}</span>
          <span class="text-slate-400 dark:text-slate-500"> / {details.max_connections}</span>
        </span>
      </div>
      <div
        class="h-2 rounded-full overflow-hidden bg-slate-100 dark:bg-slate-800"
        role="progressbar"
        aria-label={t('system.database.dashboard.connectionPool.poolUsage')}
        aria-valuenow={Math.round(poolUsagePct)}
        aria-valuemin={0}
        aria-valuemax={100}
      >
        <div
          class="h-full rounded-full bg-blue-500 transition-all duration-300"
          style:width="{poolUsagePct}%"
        ></div>
      </div>
      <div
        class="flex justify-between mt-1 text-[10px] font-mono tabular-nums text-slate-400 dark:text-slate-500"
      >
        <span
          >{details.active_connections}
          {t('system.database.dashboard.connectionPool.active')}, {details.idle_connections}
          {t('system.database.dashboard.connectionPool.idle')}</span
        >
        <span>{details.threads_idle} {t('system.database.dashboard.connectionPool.waiting')}</span>
      </div>
    </div>

    <div class="border-t border-[var(--border-100)]"></div>

    <div class="space-y-2">
      <div class="flex justify-between text-sm">
        <div class="flex items-center gap-2">
          <Users class="w-3.5 h-3.5 text-slate-400 dark:text-slate-500" />
          <span class="text-slate-400 dark:text-slate-500"
            >{t('system.database.dashboard.connectionPool.totalCreated')}</span
          >
        </div>
        <span class="font-mono tabular-nums font-medium">{formatNumber(details.total_created)}</span
        >
      </div>
      <div class="flex justify-between text-sm">
        <div class="flex items-center gap-2">
          <AlertTriangle class="w-3.5 h-3.5 text-slate-400 dark:text-slate-500" />
          <span class="text-slate-400 dark:text-slate-500"
            >{t('system.database.dashboard.connectionPool.connErrors')}</span
          >
        </div>
        <span
          class="font-mono tabular-nums font-medium {details.connection_errors > 0
            ? 'text-amber-600 dark:text-amber-400'
            : ''}">{details.connection_errors}</span
        >
      </div>
      <div class="flex justify-between text-sm">
        <div class="flex items-center gap-2">
          <Network class="w-3.5 h-3.5 text-slate-400 dark:text-slate-500" />
          <span class="text-slate-400 dark:text-slate-500"
            >{t('system.database.dashboard.connectionPool.threads')}</span
          >
        </div>
        <span class="font-mono tabular-nums font-medium"
          >{details.threads_running}
          {t('system.database.dashboard.connectionPool.running')}, {details.threads_cached}
          {t('system.database.dashboard.connectionPool.cached')}</span
        >
      </div>
    </div>
  </div>
</div>
