<!--
  HourAxis.svelte - Hour labels under (or above) an HourBars chart covering hours 0..maxHour.
  Fills its container; the last label is right-aligned so it never overflows.
-->
<script lang="ts">
  import { axisTicks } from './phoneSummary';

  let { maxHour }: { maxHour: number } = $props();

  const ticks = $derived(axisTicks(maxHour));
</script>

<span class="hour-axis" aria-hidden="true">
  {#each ticks as hour, i (hour)}
    {@const isLast = i === ticks.length - 1}
    <span
      style:left={isLast ? undefined : `${(hour / (maxHour + 1)) * 100}%`}
      style:right={isLast ? '0' : undefined}>{hour}</span
    >
  {/each}
</span>

<style>
  .hour-axis {
    position: relative;
    display: block;
    width: 100%;
    height: 1rem;
    font-size: 0.625rem;
  }

  .hour-axis span {
    position: absolute;
    top: 0;
  }
</style>
