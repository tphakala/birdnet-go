<script lang="ts">
  import { t } from '$lib/i18n';
  import { Cpu, MemoryStick, Thermometer, Brain } from '@lucide/svelte';
  import { formatBytesCompact } from '$lib/utils/formatters';
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
    inferenceAvgMs: number;
    inferenceHistory: number[];
    hasInferenceData: boolean;
    inferenceThresholdMs?: number;
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
    inferenceAvgMs,
    inferenceHistory,
    hasInferenceData,
    inferenceThresholdMs,
  }: Props = $props();

  const sparklineColorCpu = '#3b82f6';
  const sparklineColorMemory = '#8b5cf6';
  const sparklineColorTemperature = '#f97316';
  const sparklineColorInference = '#14b8a6';

  let hasTempHistory = $derived(temperatureHistory.length > 0);
  let tempMin = $derived(hasTempHistory ? Math.min(...temperatureHistory) : temperatureValue);
  let tempMax = $derived(hasTempHistory ? Math.max(...temperatureHistory) : temperatureValue);
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

  <!-- Inference Time -->
  <div
    class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-teal-500/10">
          <Brain class="w-4 h-4 text-teal-500" />
        </div>
        <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
          >{t('system.metrics.inference')}</span
        >
      </div>
      <span class="font-mono tabular-nums text-lg font-semibold">
        {#if hasInferenceData}
          {inferenceAvgMs.toFixed(0)}ms
        {:else}
          —
        {/if}
      </span>
    </div>
    {#if hasInferenceData}
      <div class="flex-1 min-h-[28px]">
        <Sparkline
          data={inferenceHistory}
          color={sparklineColorInference}
          threshold={inferenceThresholdMs}
        />
      </div>
      <div class="flex justify-between mt-2 text-[10px] text-slate-500 dark:text-slate-400">
        <span>{t('system.metrics.avgTime')} {inferenceAvgMs.toFixed(1)}ms</span>
        {#if inferenceThresholdMs != null}
          <span class="text-red-500/60"
            >{t('system.metrics.max')} {inferenceThresholdMs.toFixed(0)}ms</span
          >
        {/if}
        <span>{inferenceHistory.length} {t('system.metrics.samples')}</span>
      </div>
    {:else}
      <div class="flex-1 min-h-[28px] flex items-center">
        <span class="text-xs text-slate-500 dark:text-slate-400"
          >{t('system.metrics.noInference')}</span
        >
      </div>
    {/if}
  </div>
</div>
