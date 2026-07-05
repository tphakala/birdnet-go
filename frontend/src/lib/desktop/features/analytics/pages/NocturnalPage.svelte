<script lang="ts">
  import AnalyticsPageShell from '../components/AnalyticsPageShell.svelte';
  import ChartGrid from '../components/ChartGrid.svelte';
  import { chartsForGroup } from '../registry/charts';
</script>

<AnalyticsPageShell titleKey="analytics.hub.tabs.nocturnal" group="nocturnal">
  <ChartGrid charts={chartsForGroup('nocturnal')} />
</AnalyticsPageShell>
