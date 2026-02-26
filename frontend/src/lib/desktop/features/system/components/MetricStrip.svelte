<script lang="ts">
  import { t } from '$lib/i18n';
  import { Cpu, MemoryStick, Thermometer, Server } from '@lucide/svelte';
  import { formatBytesCompact, formatUptimeCompact } from '$lib/utils/formatters';
  import Sparkline from './Sparkline.svelte';

  interface Props {
    cpuPercent: number;
    cpuCores: number;
    cpuHistory: number[];
    memoryPercent: number;
    memoryUsed: number;
    memoryTotal: number;
    memoryAvailable: number;
    memoryHistory: number[];
    temperatureAvailable: boolean;
    temperatureValue: number;
    temperatureHistory: number[];
    tempSymbol: string;
    uptimeSeconds: number;
    hostname: string;
    systemModel?: string;
  }

  let {
    cpuPercent,
    cpuCores,
    cpuHistory,
    memoryPercent,
    memoryUsed,
    memoryTotal,
    memoryAvailable,
    memoryHistory,
    temperatureAvailable,
    temperatureValue,
    temperatureHistory,
    tempSymbol,
    uptimeSeconds,
    hostname,
    systemModel,
  }: Props = $props();

  const sparklineColorCpu = '#3b82f6';
  const sparklineColorMemory = '#8b5cf6';
  const sparklineColorTemperature = '#f97316';

  let hasTempHistory = $derived(temperatureHistory.length > 0);
  let tempMin = $derived(hasTempHistory ? Math.min(...temperatureHistory) : temperatureValue);
  let tempMax = $derived(hasTempHistory ? Math.max(...temperatureHistory) : temperatureValue);
  let shortModel = $derived(systemModel?.split(' ').slice(0, 3).join(' ') ?? '');
</script>

<div class="grid grid-cols-2 lg:grid-cols-4 gap-3">
  <!-- CPU -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-blue-500/10">
          <Cpu class="w-4 h-4 text-blue-500" />
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.metrics.cpu')}</span
        >
      </div>
      <span class="font-mono tabular-nums text-lg font-semibold">{cpuPercent.toFixed(1)}%</span>
    </div>
    <div class="flex-1 min-h-[28px]">
      <Sparkline data={cpuHistory} color={sparklineColorCpu} />
    </div>
    <div class="flex justify-between mt-2 text-[10px] text-slate-500 dark:text-slate-400">
      <span>{cpuCores} {t('system.metrics.cores')}</span>
      <span>{cpuHistory.length} {t('system.metrics.samples')}</span>
    </div>
  </div>

  <!-- Memory -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-violet-500/10">
          <MemoryStick class="w-4 h-4 text-violet-500" />
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.metrics.memory')}</span
        >
      </div>
      <span class="font-mono tabular-nums text-lg font-semibold">{memoryPercent.toFixed(1)}%</span>
    </div>
    <div class="flex-1 min-h-[28px]">
      <Sparkline data={memoryHistory} color={sparklineColorMemory} />
    </div>
    <div class="flex justify-between mt-2 text-[10px] text-slate-500 dark:text-slate-400">
      <span>{formatBytesCompact(memoryUsed)} / {formatBytesCompact(memoryTotal)}</span>
      <span>{formatBytesCompact(memoryAvailable)} {t('system.metrics.available')}</span>
    </div>
  </div>

  <!-- Temperature -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-orange-500/10">
          <Thermometer class="w-4 h-4 text-orange-500" />
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.metrics.temperature')}</span
        >
      </div>
      <span class="font-mono tabular-nums text-lg font-semibold">
        {#if temperatureAvailable}
          {temperatureValue.toFixed(1)}{tempSymbol}
        {:else}
          —
        {/if}
      </span>
    </div>
    {#if temperatureAvailable}
      <div class="flex-1 min-h-[28px]">
        <Sparkline data={temperatureHistory} color={sparklineColorTemperature} />
      </div>
      <div class="flex justify-between mt-2 text-[10px] text-slate-500 dark:text-slate-400">
        <span>{t('system.metrics.min')} {tempMin.toFixed(1)}{tempSymbol}</span>
        <span>{t('system.metrics.max')} {tempMax.toFixed(1)}{tempSymbol}</span>
      </div>
    {:else}
      <div class="flex-1 min-h-[28px] flex items-center">
        <span class="text-xs text-slate-500 dark:text-slate-400"
          >{t('system.metrics.tempUnavailable')}</span
        >
      </div>
    {/if}
  </div>

  <!-- System Status -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-emerald-500/10">
          <Server class="w-4 h-4 text-emerald-500" />
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.metrics.system')}</span
        >
      </div>
      <div class="flex items-center gap-1">
        <span class="w-2 h-2 rounded-full bg-emerald-500 animate-pulse"></span>
        <span class="text-xs text-emerald-600 dark:text-emerald-400 font-medium"
          >{t('system.metrics.online')}</span
        >
      </div>
    </div>
    <div class="space-y-2 text-sm flex-1">
      <div class="flex justify-between">
        <span class="text-slate-500 dark:text-slate-400">{t('system.metrics.uptime')}</span>
        <span class="font-mono tabular-nums font-medium">{formatUptimeCompact(uptimeSeconds)}</span>
      </div>
      <div class="flex justify-between">
        <span class="text-slate-500 dark:text-slate-400">{t('system.metrics.host')}</span>
        <span class="font-medium truncate ml-2">{hostname}</span>
      </div>
      {#if shortModel}
        <div class="flex justify-between">
          <span class="text-slate-500 dark:text-slate-400">{t('system.metrics.model')}</span>
          <span class="font-medium truncate ml-2 text-xs">{shortModel}</span>
        </div>
      {/if}
    </div>
  </div>
</div>
