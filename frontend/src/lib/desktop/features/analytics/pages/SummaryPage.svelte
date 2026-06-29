<script lang="ts">
  import AnalyticsPageShell from '../components/AnalyticsPageShell.svelte';
  import AnalyticsOverview from '../components/AnalyticsOverview.svelte';
  import { analyticsControls } from '../registry/analyticsControls.svelte';
</script>

<AnalyticsPageShell titleKey="analytics.hub.tabs.summary" group="overview">
  <AnalyticsOverview params={analyticsControls.params} />
</AnalyticsPageShell>
