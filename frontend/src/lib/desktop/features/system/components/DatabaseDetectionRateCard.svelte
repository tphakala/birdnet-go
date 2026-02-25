<script lang="ts">
  import { t } from '$lib/i18n';
  import { Download, Database } from '@lucide/svelte';
  import DetectionRateChart from './DetectionRateChart.svelte';
  import type { HourlyCount } from '$lib/types/database';

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
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <h3
    class="text-xs font-semibold uppercase tracking-wider mb-3 text-slate-400 dark:text-slate-500"
  >
    {t('system.database.dashboard.detectionRate.title')}
  </h3>
  <div class="mb-2">
    <DetectionRateChart {data} color="#f59e0b" width={280} height={80} />
  </div>
  <div
    class="grid grid-cols-3 text-[10px] font-mono tabular-nums text-slate-400 dark:text-slate-500"
  >
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

  {#if engine === 'sqlite' && onBackup}
    <!-- SQLite: Create Backup button (only shown when handler is provided) -->
    <div class="mt-4 pt-3 border-t border-[var(--border-100)]">
      <button
        class="w-full flex items-center justify-center gap-2 px-3 py-2 text-xs font-medium rounded-lg transition-colors cursor-pointer border border-[var(--border-100)] text-slate-500 dark:text-slate-400 hover:bg-black/5 dark:hover:bg-white/5"
        onclick={onBackup}
      >
        <Download class="w-3.5 h-3.5" />
        {t('system.database.dashboard.detectionRate.createBackup')}
      </button>
    </div>
  {:else if engine === 'mysql'}
    <!-- MySQL: Host info + backup note -->
    <div class="mt-4 pt-3 border-t border-[var(--border-100)]">
      <div class="flex items-center gap-2 text-sm">
        <Database class="w-3.5 h-3.5 text-slate-400 dark:text-slate-500" />
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.dashboard.detectionRate.mysqlHost')}</span
        >
        <span class="font-mono text-xs font-medium ml-auto">{mysqlHost ?? ''}</span>
      </div>
      <p class="mt-2 text-[10px] text-slate-400 dark:text-slate-500">
        {t('system.database.dashboard.detectionRate.mysqlBackupNote')}
      </p>
    </div>
  {/if}
</div>
