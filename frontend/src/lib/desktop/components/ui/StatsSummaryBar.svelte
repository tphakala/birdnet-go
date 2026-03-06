<!--
  StatsSummaryBar Component

  Purpose: Displays a horizontal row of icon + count + label stats
  separated by vertical dividers.

  @component
-->
<script lang="ts">
  import type { Component } from 'svelte';

  interface StatItem {
    icon: Component;
    iconClass?: string;
    count: number | string;
    label: string;
  }

  interface Props {
    stats: StatItem[];
    class?: string;
  }

  let { stats, class: className = '' }: Props = $props();
</script>

<div class="flex items-center gap-4 text-sm {className}">
  {#each stats as stat, index (stat.label)}
    {#if index > 0}
      <span class="w-px h-3 bg-base-300"></span>
    {/if}
    <div class="flex items-center gap-1.5">
      <stat.icon class="size-3.5 {stat.iconClass ?? 'text-base-content/40'}" />
      <span class="text-base-content/60">
        <span class="font-semibold text-base-content tabular-nums">{stat.count}</span>
        {stat.label}
      </span>
    </div>
  {/each}
</div>
