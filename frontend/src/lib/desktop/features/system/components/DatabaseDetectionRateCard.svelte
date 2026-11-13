<script lang="ts">
  import { t } from '$lib/i18n';
  import { Download, Database } from '@lucide/svelte';
  import DetectionRateChart from './DetectionRateChart.svelte';
  import type { HourlyCount } from '$lib/types/database';
  import { splitLocationSegments } from './locationBreaks';

  interface Props {
    data: HourlyCount[];
    engine: 'sqlite' | 'mysql';
    mysqlHost?: string;
    onBackup?: () => void;
  }

  let { data = [], engine, mysqlHost, onBackup }: Props = $props();

  let counts = $derived(data.map(d => d.count));
  let minCount = $derived(counts.length > 0 ? Math.min(...counts) : 0);
  let avgCount = $derived(
    counts.length > 0 ? Math.round(counts.reduce((a, b) => a + b, 0) / counts.length) : 0
  );
  let maxCount = $derived(counts.length > 0 ? Math.max(...counts) : 0);
  let locationSegments = $derived(splitLocationSegments(mysqlHost ?? ''));
</script>

<div
  class="@container bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm"
>
  <div class="flex flex-col gap-4 @xl:flex-row">
    <div class="min-w-0 flex-1">
      <h3 class="text-xs font-semibold uppercase tracking-wider mb-3 text-muted">
        {t('system.database.dashboard.detectionRate.title')}
      </h3>
      <div class="mb-2">
        <DetectionRateChart {data} color="#f59e0b" />
      </div>
      <div class="grid grid-cols-3 text-[10px] font-mono tabular-nums text-muted">
        <span
          >{t('system.database.dashboard.detectionRate.min')}
          {minCount}{t('system.database.dashboard.detectionRate.perHour')}</span
        >
        <span class="text-center"
          >{t('system.database.dashboard.detectionRate.avg')}
          {avgCount}{t('system.database.dashboard.detectionRate.perHour')}</span
        >
        <span class="text-right"
          >{t('system.database.dashboard.detectionRate.max')}
          {maxCount}{t('system.database.dashboard.detectionRate.perHour')}</span
        >
      </div>
    </div>

    {#if (engine === 'sqlite' && onBackup) || engine === 'mysql'}
      <div
        class="border-t border-[var(--border-100)] pt-3 @xl:w-56 @xl:shrink-0 @xl:border-t-0 @xl:border-l @xl:pt-0 @xl:pl-4 @xl:flex @xl:flex-col @xl:justify-center"
      >
        {#if engine === 'sqlite'}
          <!-- SQLite: Create Backup button (only shown when handler is provided) -->
          <button
            class="w-full flex items-center justify-center gap-2 px-3 py-2 text-xs font-medium rounded-lg transition-colors cursor-pointer border border-[var(--border-100)] text-muted hover:bg-black/5 dark:hover:bg-white/5"
            onclick={onBackup}
          >
            <Download class="w-3.5 h-3.5" />
            {t('system.database.dashboard.detectionRate.createBackup')}
          </button>
        {:else}
          <!-- MySQL: host, port and database name plus backup note -->
          <div class="flex items-center gap-2 text-sm">
            <Database class="w-3.5 h-3.5 shrink-0 text-muted" />
            <span class="text-muted">{t('system.database.dashboard.detectionRate.mysqlHost')}</span>
          </div>
          <div class="mt-1 min-w-0 font-mono text-xs font-medium wrap-anywhere">
            {#each locationSegments as segment, index (index)}{#if index > 0}<wbr
                />{/if}{segment}{/each}
          </div>
          <p class="mt-2 text-[10px] text-muted">
            {t('system.database.dashboard.detectionRate.mysqlBackupNote')}
          </p>
        {/if}
      </div>
    {/if}
  </div>
</div>
