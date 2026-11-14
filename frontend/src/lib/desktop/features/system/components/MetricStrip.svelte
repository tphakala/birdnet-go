<script lang="ts">
  import { t } from '$lib/i18n';
  import { Cpu, MemoryStick, Thermometer, Brain } from '@lucide/svelte';
  import { formatBytesCompact } from '$lib/utils/formatters';
  import Sparkline from './Sparkline.svelte';
  import Skeleton from '$lib/desktop/components/ui/Skeleton.svelte';
  import SkeletonStatus from '$lib/desktop/components/ui/SkeletonStatus.svelte';
  import type { ModelMetrics } from '$lib/desktop/features/system/types';

  interface Props {
    /** Null when there is no reading: no history and the resources could not load. */
    cpuPercent: number | null;
    /** Null when the system info could not load; the tile then shows a dash. */
    cpuCores: number | null;
    cpuHistory: number[];
    /** Null when there is no reading: no history and the resources could not load. */
    memoryPercent: number | null;
    /** Null when the resources could not load; the tile then shows dashes, never zeros. */
    memoryUsed: number | null;
    memoryTotal: number | null;
    memoryAvailable: number | null;
    memoryHistory: number[];
    temperatureAvailable: boolean;
    temperatureValue: number;
    temperatureHistory: number[];
    tempSymbol: string;
    models: ModelMetrics[];
    birdnetOverlap: number;
    /** Each tile shows a skeleton until the first answer its values depend on. */
    cpuLoading?: boolean;
    memoryLoading?: boolean;
    temperatureLoading?: boolean;
    inferenceLoading?: boolean;
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
    models,
    birdnetOverlap,
    cpuLoading = false,
    memoryLoading = false,
    temperatureLoading = false,
    inferenceLoading = false,
  }: Props = $props();

  const sparklineColorCpu = '#3b82f6';
  const sparklineColorMemory = '#8b5cf6';
  const sparklineColorTemperature = '#f97316';

  // The inference footer lists one entry per model and wraps once a few models are listed
  // (three on a typical tile). It always reserves two lines, loading and loaded, so the tile
  // row keeps its height whether one model or several are listed. `lh` is one line of the
  // footer's own line height; 0.125rem is its row gap.
  const INFERENCE_FOOTER_CLASS = 'min-h-[calc(2lh+0.125rem)]';

  let hasTempHistory = $derived(temperatureHistory.length > 0);
  let tempMin = $derived(hasTempHistory ? Math.min(...temperatureHistory) : temperatureValue);
  let tempMax = $derived(hasTempHistory ? Math.max(...temperatureHistory) : temperatureValue);

  let modelsWithData = $derived(models.filter(m => m.history.length > 0));
  let hasInferenceData = $derived(modelsWithData.length > 0);

  function getThresholdMs(model: ModelMetrics): number {
    let seconds = model.chunkSeconds;
    if (model.id.startsWith('BirdNET')) {
      seconds = model.chunkSeconds - birdnetOverlap;
    }
    return Math.max(seconds, 0.001) * 1000;
  }

  type StatusLevel = 'ok' | 'warning' | 'critical';

  function getModelStatus(model: ModelMetrics): StatusLevel | null {
    if (model.history.length === 0) return null;
    const avgMs = model.history[model.history.length - 1];
    const threshold = getThresholdMs(model);
    const ratio = avgMs / threshold;
    if (ratio >= 1.0) return 'critical';
    if (ratio >= 0.7) return 'warning';
    return 'ok';
  }

  let worstStatus = $derived.by((): StatusLevel | null => {
    const priority: Record<StatusLevel, number> = { ok: 0, warning: 1, critical: 2 };
    let worst: StatusLevel | null = null;
    for (const m of models) {
      const s = getModelStatus(m);
      if (s == null) continue;
      // eslint-disable-next-line security/detect-object-injection -- priority is a typed Record<StatusLevel, number>; s and worst are StatusLevel values, not user input
      if (worst == null || priority[s] > priority[worst]) worst = s;
    }
    return worst;
  });

  let inferenceDatasets = $derived(modelsWithData.map(m => ({ data: m.history, color: m.color })));

  // Prefer BirdNET for the header display; fall back to first model with data
  let primaryAvgMs = $derived.by(() => {
    if (modelsWithData.length === 0) return 0;
    const birdnet = modelsWithData.find(m => m.id.startsWith('BirdNET'));
    const primary = birdnet ?? modelsWithData[0];
    return primary.history[primary.history.length - 1];
  });
</script>

<!-- Loading tile parts: a bar on the value line, a bar in the sparkline area (5:1, the
     sparkline's view box), and the loading label on the footer line -->
{#snippet loadingValue()}
  <span class="text-lg"><Skeleton inline className="h-5 w-16" /></span>
{/snippet}

{#snippet loadingBody(footerClass: string = '')}
  <div class="flex-1 min-h-[28px]">
    <Skeleton className="h-full w-full aspect-5/1" />
  </div>
  <div class="flex mt-2 text-[10px] {footerClass}">
    <SkeletonStatus label={t('common.loading')} />
  </div>
{/snippet}

<div class="@container">
  <div class="grid grid-cols-2 @4xl:grid-cols-4 gap-3">
    <!-- CPU -->
    <div
      class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
    >
      <div class="flex flex-wrap items-center justify-between gap-x-2 gap-y-1 mb-3">
        <div class="flex items-center gap-2">
          <div class="p-1.5 rounded-lg bg-blue-500/10">
            <Cpu class="w-4 h-4 text-blue-500" />
          </div>
          <span class="text-xs font-medium text-muted">{t('system.metrics.cpu')}</span>
        </div>
        {#if cpuLoading}
          {@render loadingValue()}
        {:else}
          <span class="font-mono tabular-nums text-lg font-semibold"
            >{cpuPercent === null ? '-' : `${cpuPercent.toFixed(1)}%`}</span
          >
        {/if}
      </div>
      {#if cpuLoading}
        {@render loadingBody()}
      {:else}
        <div class="flex-1 min-h-[28px]">
          <Sparkline data={cpuHistory} color={sparklineColorCpu} />
        </div>
        <div class="flex justify-between mt-2 text-[10px] text-muted">
          <span>{cpuCores === null ? '-' : `${cpuCores} ${t('system.metrics.cores')}`}</span>
          <span>{cpuHistory.length} {t('system.metrics.samples')}</span>
        </div>
      {/if}
    </div>

    <!-- Memory -->
    <div
      class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
    >
      <div class="flex flex-wrap items-center justify-between gap-x-2 gap-y-1 mb-3">
        <div class="flex items-center gap-2">
          <div class="p-1.5 rounded-lg bg-violet-500/10">
            <MemoryStick class="w-4 h-4 text-violet-500" />
          </div>
          <span class="text-xs font-medium text-muted">{t('system.metrics.memory')}</span>
        </div>
        {#if memoryLoading}
          {@render loadingValue()}
        {:else}
          <span class="font-mono tabular-nums text-lg font-semibold"
            >{memoryPercent === null ? '-' : `${memoryPercent.toFixed(1)}%`}</span
          >
        {/if}
      </div>
      {#if memoryLoading}
        {@render loadingBody()}
      {:else}
        <div class="flex-1 min-h-[28px]">
          <Sparkline data={memoryHistory} color={sparklineColorMemory} />
        </div>
        <div class="flex justify-between mt-2 text-[10px] text-muted">
          <span
            >{memoryUsed === null || memoryTotal === null
              ? '-'
              : `${formatBytesCompact(memoryUsed)} / ${formatBytesCompact(memoryTotal)}`}</span
          >
          <span
            >{memoryAvailable === null
              ? '-'
              : `${formatBytesCompact(memoryAvailable)} ${t('system.metrics.available')}`}</span
          >
        </div>
      {/if}
    </div>

    <!-- Temperature -->
    <div
      class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
    >
      <div class="flex flex-wrap items-center justify-between gap-x-2 gap-y-1 mb-3">
        <div class="flex items-center gap-2">
          <div class="p-1.5 rounded-lg bg-orange-500/10">
            <Thermometer class="w-4 h-4 text-orange-500" />
          </div>
          <span class="text-xs font-medium text-muted">{t('system.metrics.temperature')}</span>
        </div>
        {#if temperatureLoading}
          {@render loadingValue()}
        {:else}
          <span class="font-mono tabular-nums text-lg font-semibold">
            {#if temperatureAvailable}
              {temperatureValue.toFixed(1)}{tempSymbol}
            {:else}
              -
            {/if}
          </span>
        {/if}
      </div>
      {#if temperatureLoading}
        {@render loadingBody()}
      {:else if temperatureAvailable}
        <div class="flex-1 min-h-[28px]">
          <Sparkline data={temperatureHistory} color={sparklineColorTemperature} />
        </div>
        <div class="flex justify-between mt-2 text-[10px] text-muted">
          <span>{t('system.metrics.min')} {tempMin.toFixed(1)}{tempSymbol}</span>
          <span>{t('system.metrics.max')} {tempMax.toFixed(1)}{tempSymbol}</span>
        </div>
      {:else}
        <div class="flex-1 min-h-[28px] flex items-center">
          <span class="text-xs text-muted">{t('system.metrics.tempUnavailable')}</span>
        </div>
      {/if}
    </div>

    <!-- Inference Time -->
    <div
      class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm flex flex-col"
    >
      <div class="flex flex-wrap items-center justify-between gap-x-2 gap-y-1 mb-3">
        <div class="flex items-center gap-2">
          <div class="p-1.5 rounded-lg bg-teal-500/10">
            <Brain class="w-4 h-4 text-teal-500" />
          </div>
          <span class="text-xs font-medium text-muted">{t('system.metrics.inference')}</span>
        </div>
        <div class="flex items-center gap-2">
          {#if worstStatus === 'ok'}
            <span
              class="text-[10px] font-semibold uppercase px-1.5 py-0.5 rounded badge-status-success"
              >{t('system.metrics.statusOk')}</span
            >
          {:else if worstStatus === 'warning'}
            <span
              class="text-[10px] font-semibold uppercase px-1.5 py-0.5 rounded badge-status-warning"
              >{t('system.metrics.statusWarning')}</span
            >
          {:else if worstStatus === 'critical'}
            <span
              class="text-[10px] font-semibold uppercase px-1.5 py-0.5 rounded badge-status-error"
              >{t('system.metrics.statusCritical')}</span
            >
          {/if}
          {#if inferenceLoading}
            {@render loadingValue()}
          {:else}
            <span class="font-mono tabular-nums text-lg font-semibold">
              {#if hasInferenceData}
                {primaryAvgMs.toFixed(0)}ms
              {:else}
                -
              {/if}
            </span>
          {/if}
        </div>
      </div>
      {#if inferenceLoading}
        {@render loadingBody(INFERENCE_FOOTER_CLASS)}
      {:else if hasInferenceData}
        <div class="flex-1 min-h-[28px]">
          <Sparkline datasets={inferenceDatasets} />
        </div>
        <div
          class="flex flex-wrap content-start gap-x-3 gap-y-0.5 mt-2 text-[10px] text-muted {INFERENCE_FOOTER_CLASS}"
        >
          {#each modelsWithData as m (m.id)}
            <span class="flex items-center gap-1">
              <span class="inline-block w-1.5 h-1.5 rounded-full" style:background={m.color}></span>
              {m.name}
              <span class="font-mono tabular-nums"
                >{m.history[m.history.length - 1].toFixed(0)}ms</span
              >
            </span>
          {/each}
        </div>
      {:else}
        <div class="flex-1 min-h-[28px] flex items-center">
          <span class="text-xs text-muted">{t('system.metrics.noInference')}</span>
        </div>
      {/if}
    </div>
  </div>
</div>
