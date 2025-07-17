<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';

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
    emptyMessage = 'No data available',
    showEmpty = false,
  }: Props = $props();
</script>

<div class={cn('card bg-base-100 shadow-sm', className)}>
  <div class="card-body p-4 md:p-6">
    <h2 class="card-title">{title}</h2>

    {#if isLoading}
      <div class="flex justify-center items-center p-8">
        <span class="loading loading-spinner loading-lg text-primary"></span>
      </div>
    {:else if showEmpty}
      <div class="text-center py-4 text-base-content/50">
        {emptyMessage}
      </div>
    {:else}
      <div class={cn('chart-container', chartHeight)}>
        <canvas id={chartId} class="w-full h-full"></canvas>
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
</style>
