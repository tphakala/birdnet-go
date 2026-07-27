<script lang="ts">
  import ChartCard from './ChartCard.svelte';
  import { analyticsControls } from '../registry/analyticsControls.svelte';
  import type { ChartDef } from '../registry/types';

  interface Props {
    charts: ChartDef[];
  }

  let { charts }: Props = $props();
</script>

<div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
  {#each charts as chart (chart.id)}
    <div
      data-chart-id={chart.id}
      class={chart.size === 'normal' ? 'lg:col-span-1' : 'lg:col-span-2'}
    >
      <ChartCard
        {chart}
        params={analyticsControls.params}
        speciesNames={analyticsControls.speciesNames}
        speciesLoading={analyticsControls.loadingSpecies}
        onParamsChange={partial => analyticsControls.applyParams(partial, 'push')}
      />
    </div>
  {/each}
</div>
