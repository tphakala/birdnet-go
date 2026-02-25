<script lang="ts">
  import { t } from '$lib/i18n';
  import { Zap, Timer, Activity, Database } from '@lucide/svelte';
  import { formatBytesCompact, formatNumber } from '$lib/utils/formatters';
  import Sparkline from './Sparkline.svelte';
  import type { PerformanceStats } from '$lib/types/database';

  interface Props {
    performance: PerformanceStats;
    engine: string;
    status: string;
    sizeBytes: number;
    totalDetections: number;
    journalMode?: string; // SQLite only
    readLatencyHistory: number[];
    writeLatencyHistory: number[];
    queriesPerSecHistory: number[];
  }

  let {
    performance,
    engine,
    status,
    sizeBytes,
    totalDetections,
    journalMode,
    readLatencyHistory = [],
    writeLatencyHistory = [],
    queriesPerSecHistory = [],
  }: Props = $props();

  let lastQps = $derived(
    queriesPerSecHistory.length > 0
      ? queriesPerSecHistory[queriesPerSecHistory.length - 1]
      : performance.queries_per_sec
  );

  let engineLabel = $derived.by(() => {
    if (engine === 'sqlite') {
      return journalMode ? `SQLite (${journalMode.toUpperCase()})` : 'SQLite';
    }
    return 'MySQL';
  });
</script>

<div class="grid grid-cols-2 lg:grid-cols-4 gap-3">
  <!-- Read Latency -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-blue-500/10">
          <Zap class="w-4 h-4 text-blue-500" />
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.database.dashboard.metrics.readLatency')}</span
        >
      </div>
      <span class="font-mono tabular-nums text-lg font-semibold"
        >{performance.read_latency_avg_ms.toFixed(1)}ms</span
      >
    </div>
    <div class="flex-1 min-h-[28px]">
      <Sparkline data={readLatencyHistory} color="#3b82f6" />
    </div>
    <div class="flex justify-between mt-2 text-[10px] text-slate-400 dark:text-slate-500">
      <span
        >{t('system.database.dashboard.metrics.avg')}
        {performance.read_latency_avg_ms.toFixed(1)}ms</span
      >
      <span
        >{t('system.database.dashboard.metrics.max')}
        {performance.read_latency_max_ms.toFixed(1)}ms</span
      >
    </div>
  </div>

  <!-- Write Latency -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-violet-500/10">
          <Timer class="w-4 h-4 text-violet-500" />
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.database.dashboard.metrics.writeLatency')}</span
        >
      </div>
      <span class="font-mono tabular-nums text-lg font-semibold"
        >{performance.write_latency_avg_ms.toFixed(1)}ms</span
      >
    </div>
    <div class="flex-1 min-h-[28px]">
      <Sparkline data={writeLatencyHistory} color="#8b5cf6" />
    </div>
    <div class="flex justify-between mt-2 text-[10px] text-slate-400 dark:text-slate-500">
      <span
        >{t('system.database.dashboard.metrics.avg')}
        {performance.write_latency_avg_ms.toFixed(1)}ms</span
      >
      <span
        >{t('system.database.dashboard.metrics.max')}
        {performance.write_latency_max_ms.toFixed(1)}ms</span
      >
    </div>
  </div>

  <!-- Queries/sec -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-emerald-500/10">
          <Activity class="w-4 h-4 text-emerald-500" />
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.database.dashboard.metrics.queriesPerSec')}</span
        >
      </div>
      <span class="font-mono tabular-nums text-lg font-semibold">{lastQps.toFixed(0)}</span>
    </div>
    <div class="flex-1 min-h-[28px]">
      <Sparkline data={queriesPerSecHistory} color="#22c55e" />
    </div>
    <div class="flex justify-between mt-2 text-[10px] text-slate-400 dark:text-slate-500">
      <span
        >{t('system.database.dashboard.metrics.lastHour', {
          count: formatNumber(performance.queries_last_hour),
        })}</span
      >
      <span
        >{t('system.database.dashboard.metrics.slowQueries', {
          count: performance.slow_query_count,
        })}</span
      >
    </div>
  </div>

  <!-- Database Status -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div
          class="p-1.5 rounded-lg {engine === 'mysql' ? 'bg-orange-500/10' : 'bg-emerald-500/10'}"
        >
          <Database class="w-4 h-4 {engine === 'mysql' ? 'text-orange-500' : 'text-emerald-500'}" />
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.database.dashboard.metrics.database')}</span
        >
      </div>
      <div class="flex items-center gap-1">
        {#if status === 'connected'}
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
        <span class="font-medium">{engineLabel}</span>
      </div>
      <div class="flex justify-between">
        <span class="text-slate-400 dark:text-slate-500">{t('system.database.stats.size')}</span>
        <span class="font-mono tabular-nums font-medium">{formatBytesCompact(sizeBytes)}</span>
      </div>
      <div class="flex justify-between">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.stats.detections')}</span
        >
        <span class="font-mono tabular-nums font-medium">{formatNumber(totalDetections)}</span>
      </div>
    </div>
  </div>
</div>
