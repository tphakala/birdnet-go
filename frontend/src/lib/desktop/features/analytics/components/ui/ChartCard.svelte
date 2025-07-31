<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import { t } from '$lib/i18n';

  interface Props {
    title: string;
    chartId: string;
    isLoading?: boolean;
    className?: string;
    chartHeight?: string;
    children?: Snippet;
    emptyMessage?: string;
    showEmpty?: boolean;
  }

  let {
    title,
    chartId,
    isLoading = false,
    className = '',
    chartHeight = 'h-80',
    children,
    emptyMessage = t('analytics.charts.noDataAvailable'),
    showEmpty = false,
  }: Props = $props();
</script>

<div class={cn('card bg-base-100 shadow-sm', className)}>
  <div class="card-body p-4 md:p-6">
    <h2 class="card-title">{title}</h2>

    {#if showEmpty && !isLoading}
      <div class="text-center py-4 text-base-content/50">
        {emptyMessage}
      </div>
    {:else}
      <div class="relative" aria-busy={isLoading}>
        <div class={cn('chart-container', chartHeight, isLoading ? 'invisible' : '')}>
          <canvas id={chartId} class="w-full h-full"></canvas>
        </div>
        {#if isLoading}
          <div class="absolute inset-0 flex justify-center items-center">
            <span class="loading loading-spinner loading-lg text-primary"></span>
          </div>
        {/if}
      </div>
    {/if}

    {#if children}
      {@render children()}
    {/if}
  </div>
</div>

<style>
  .chart-container {
    position: relative;
  }

  /* Canvas is always present in DOM but hidden when loading
     This prevents Chart.js from losing canvas reference */
</style>
