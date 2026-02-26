<script lang="ts">
  import { t } from '$lib/i18n';
  import { Database, Activity, RefreshCw, Loader2, CheckCircle, XCircle } from '@lucide/svelte';
  import { formatBytesCompact, formatNumber } from '$lib/utils/formatters';
  import Sparkline from './Sparkline.svelte';
  import type { DatabaseStats, MigrationStatus } from '$lib/types/migration';

  const SPARKLINE_COLOR = '#3b82f6';

  interface Props {
    legacyStats: DatabaseStats | null;
    v2Stats: DatabaseStats | null;
    migrationStatus: MigrationStatus | null;
    detectionHistory?: number[];
  }

  let { legacyStats, v2Stats, migrationStatus, detectionHistory = [] }: Props = $props();

  let migState = $derived(migrationStatus?.state ?? 'idle');

  const ACTIVE_STATES = [
    'initializing',
    'dual_write',
    'migrating',
    'migrating_predictions',
    'validating',
    'cutover',
  ];
  let isActive = $derived(ACTIVE_STATES.includes(migState));
  let isIdle = $derived(migState === 'idle');
  let isCompleted = $derived(migState === 'completed');
  let isFailed = $derived(migState === 'failed');
  let isValidating = $derived(migState === 'validating');

  let v2TotalRecords = $derived(
    (v2Stats?.total_detections ?? 0) + (migrationStatus?.migrated_records ?? 0)
  );

  let dailyAverage = $derived.by(() => {
    if (detectionHistory.length === 0) return 0;
    const sum = detectionHistory.reduce((a, b) => a + b, 0);
    return Math.round(sum / detectionHistory.length);
  });

  let totalDetections = $derived(legacyStats?.total_detections ?? 0);

  function stateBadgeClass(state: string): string {
    switch (state) {
      case 'idle':
        return 'bg-slate-500/15 text-slate-600 dark:text-slate-400';
      case 'initializing':
      case 'dual_write':
      case 'migrating':
      case 'migrating_predictions':
        return 'bg-blue-500/15 text-blue-600 dark:text-blue-400';
      case 'paused':
        return 'bg-amber-500/15 text-amber-600 dark:text-amber-400';
      case 'validating':
        return 'bg-violet-500/15 text-violet-600 dark:text-violet-400';
      case 'cutover':
        return 'bg-amber-500/15 text-amber-600 dark:text-amber-400';
      case 'completed':
        return 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400';
      case 'failed':
        return 'bg-red-500/15 text-red-600 dark:text-red-400';
      default:
        return 'bg-slate-500/15 text-slate-600 dark:text-slate-400';
    }
  }

  function stateLabel(state: string): string {
    const key = `system.database.migration.status.${state}`;
    const translated = t(key);
    return translated === key
      ? state.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase())
      : translated;
  }
</script>

<div class="grid grid-cols-2 lg:grid-cols-4 gap-3">
  <!-- Legacy Database -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-amber-500/10">
          <Database class="w-4 h-4 text-amber-500" />
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.database.migration.strip.legacy')}</span
        >
      </div>
      <div class="flex items-center gap-1">
        {#if legacyStats?.connected}
          <span class="w-2 h-2 rounded-full bg-emerald-500 animate-pulse"></span>
          <span class="text-xs text-emerald-600 dark:text-emerald-400 font-medium"
            >{t('system.database.stats.connected')}</span
          >
        {:else}
          <span class="w-2 h-2 rounded-full bg-red-500"></span>
          <span class="text-xs text-red-600 dark:text-red-400 font-medium"
            >{t('system.database.stats.disconnected')}</span
          >
        {/if}
      </div>
    </div>
    <div class="space-y-2 text-sm flex-1">
      <div class="flex justify-between">
        <span class="text-slate-400 dark:text-slate-500">{t('system.database.stats.type')}</span>
        <span class="font-medium uppercase">{legacyStats?.type ?? '---'}</span>
      </div>
      <div class="flex justify-between">
        <span class="text-slate-400 dark:text-slate-500">{t('system.database.stats.size')}</span>
        <span class="font-mono tabular-nums font-medium"
          >{formatBytesCompact(legacyStats?.size_bytes ?? 0)}</span
        >
      </div>
      <div class="flex justify-between">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.stats.detections')}</span
        >
        <span class="font-mono tabular-nums font-medium"
          >{formatNumber(legacyStats?.total_detections ?? 0)}</span
        >
      </div>
    </div>
  </div>

  <!-- V2 (Target) Database -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-emerald-500/10">
          <Database class="w-4 h-4 text-emerald-500" />
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.database.migration.strip.v2Target')}</span
        >
      </div>
      <div class="flex items-center gap-1">
        {#if v2Stats?.connected}
          <span class="w-2 h-2 rounded-full bg-emerald-500 animate-pulse"></span>
          <span class="text-xs text-emerald-600 dark:text-emerald-400 font-medium"
            >{t('system.database.migration.strip.active')}</span
          >
        {:else}
          <span class="w-2 h-2 rounded-full bg-red-500"></span>
          <span class="text-xs text-red-600 dark:text-red-400 font-medium"
            >{t('system.database.stats.disconnected')}</span
          >
        {/if}
      </div>
    </div>
    <div class="space-y-2 text-sm flex-1">
      <div class="flex justify-between">
        <span class="text-slate-400 dark:text-slate-500">{t('system.database.stats.type')}</span>
        <span class="font-medium uppercase">{v2Stats?.type ?? '---'}</span>
      </div>
      <div class="flex justify-between">
        <span class="text-slate-400 dark:text-slate-500">{t('system.database.stats.size')}</span>
        <span class="font-mono tabular-nums font-medium"
          >{formatBytesCompact(v2Stats?.size_bytes ?? 0)}</span
        >
      </div>
      <div class="flex justify-between">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.stats.detections')}</span
        >
        <span class="font-mono tabular-nums font-medium">{formatNumber(v2TotalRecords)}</span>
      </div>
    </div>
  </div>

  <!-- Detections -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-blue-500/10">
          <Activity class="w-4 h-4 text-blue-500" />
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.database.stats.detections')}</span
        >
      </div>
      <span class="font-mono tabular-nums text-lg font-semibold"
        >{formatNumber(totalDetections)}</span
      >
    </div>
    <div class="flex-1 min-h-[28px]">
      <Sparkline data={detectionHistory} color={SPARKLINE_COLOR} />
    </div>
    <div class="flex justify-between mt-2 text-[10px] text-slate-400 dark:text-slate-500">
      <span>{t('system.database.migration.strip.dayAvg', { count: dailyAverage })}</span>
      <span>{t('system.database.migration.strip.dayHistory')}</span>
    </div>
  </div>

  <!-- Migration Status -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-orange-500/10">
          {#if isValidating}
            <Loader2 class="w-4 h-4 text-violet-500 animate-spin" />
          {:else if isActive}
            <RefreshCw class="w-4 h-4 text-orange-500 animate-spin" />
          {:else}
            <RefreshCw class="w-4 h-4 text-orange-500" />
          {/if}
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.database.migration.strip.migration')}</span
        >
      </div>
      <span class="px-2 py-0.5 rounded-full text-[10px] font-medium {stateBadgeClass(migState)}"
        >{stateLabel(migState)}</span
      >
    </div>

    {#if isCompleted}
      <div
        class="flex items-center gap-2 text-sm text-emerald-600 dark:text-emerald-400 font-medium"
      >
        <CheckCircle class="w-4 h-4" />
        <span>{t('system.database.migration.strip.complete')}</span>
      </div>
      <div class="mt-1 text-[10px] tabular-nums text-slate-400 dark:text-slate-500">
        {t('system.database.migration.strip.recordsMigrated', {
          count: formatNumber(migrationStatus?.total_records ?? 0),
        })}
      </div>
    {:else if isIdle}
      <div class="text-sm text-slate-500 dark:text-slate-400">
        {t('system.database.migration.strip.readyToMigrate')}
      </div>
      <div class="mt-1 text-[10px] tabular-nums text-slate-400 dark:text-slate-500">
        {t('system.database.migration.strip.recordsInLegacy', {
          count: formatNumber(legacyStats?.total_detections ?? 0),
        })}
      </div>
    {:else if isValidating}
      <div class="space-y-2">
        <div>
          <div class="h-2 rounded-full overflow-hidden bg-slate-200 dark:bg-slate-700">
            <div class="h-full rounded-full bg-violet-500 animate-pulse" style:width="100%"></div>
          </div>
          <div class="mt-1 text-[10px] text-slate-400 dark:text-slate-500">
            {t('system.database.migration.strip.comparingRecords')}
          </div>
        </div>
      </div>
    {:else if isFailed}
      <div class="flex items-center gap-2 text-sm text-red-600 dark:text-red-400 font-medium">
        <XCircle class="w-4 h-4" />
        <span>{t('system.database.migration.strip.validationFailed')}</span>
      </div>
    {:else}
      <!-- Active / Paused: progress bar -->
      <div class="space-y-2">
        <div>
          <div class="h-2 rounded-full overflow-hidden bg-slate-200 dark:bg-slate-700">
            <div
              class="h-full rounded-full bg-blue-500 transition-all duration-300"
              style:width="{Math.min(migrationStatus?.progress_percent ?? 0, 100).toFixed(1)}%"
            ></div>
          </div>
          <div
            class="flex justify-between mt-1 text-[10px] tabular-nums text-slate-400 dark:text-slate-500"
          >
            <span>{(migrationStatus?.progress_percent ?? 0).toFixed(1)}%</span>
            {#if migrationStatus?.estimated_remaining}
              <span
                >{t('system.database.migration.progress.remaining', {
                  time: migrationStatus.estimated_remaining,
                })}</span
              >
            {/if}
          </div>
        </div>
        <div class="flex justify-between text-sm">
          <span class="text-slate-400 dark:text-slate-500"
            >{t('system.database.migration.strip.rate')}</span
          >
          <span class="font-mono tabular-nums font-medium"
            >{t('system.database.migration.progress.rateValue', {
              rate: formatNumber(Math.round(migrationStatus?.records_per_second ?? 0)),
            })}</span
          >
        </div>
      </div>
    {/if}
  </div>
</div>
