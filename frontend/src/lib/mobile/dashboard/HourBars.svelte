<!--
  HourBars.svelte - Hourly detection bars for one species, hours 0..maxHour.

  Bars are scaled to the species' own busiest hour. Hours outside sunrise..sunset are
  dimmed when sun times are known. Decorative: callers put the facts in text.
-->
<script lang="ts">
  interface Props {
    counts: number[];
    maxHour: number;
    /** Chart height in px. */
    height: number;
    sunriseHour?: number | null;
    sunsetHour?: number | null;
  }

  let { counts, maxHour, height, sunriseHour = null, sunsetHour = null }: Props = $props();

  /** Smallest visible bar, so a single detection is never a sliver. */
  const MIN_BAR_PERCENT = 8;

  const hours = $derived(counts.slice(0, maxHour + 1));
  const peak = $derived(Math.max(1, ...hours));

  function isNight(hour: number): boolean {
    if (sunriseHour === null || sunsetHour === null) return false;
    return hour < sunriseHour || hour >= sunsetHour;
  }
</script>

<div
  class="hour-bars"
  aria-hidden="true"
  style:height="{height}px"
  style:grid-template-columns="repeat({hours.length}, 1fr)"
>
  {#each hours as count, hour (hour)}
    <span
      class="hour-bar"
      class:night={isNight(hour)}
      style:height="{count > 0 ? Math.max(MIN_BAR_PERCENT, (count / peak) * 100) : 0}%"
    ></span>
  {/each}
</div>

<style>
  .hour-bars {
    display: grid;
    width: 100%;
    align-items: end;
    column-gap: 1px;
  }

  .hour-bar {
    border-radius: 1px 1px 0 0;
    background-color: var(--color-primary);
  }

  .hour-bar.night {
    opacity: 0.45;
  }
</style>
