<script lang="ts">
  import AnalyticsPageShell from '../components/AnalyticsPageShell.svelte';
  import ChartGrid from '../components/ChartGrid.svelte';
  import { chartsForGroup } from '../registry/charts';
</script>

<AnalyticsPageShell titleKey="analytics.hub.tabs.quality" group="quality">
  <ChartGrid charts={chartsForGroup('quality')} />
</AnalyticsPageShell>
