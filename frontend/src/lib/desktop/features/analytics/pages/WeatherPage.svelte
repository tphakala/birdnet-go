<script lang="ts">
  import { CloudSun } from '@lucide/svelte';
  import AnalyticsComingSoon from '../components/AnalyticsComingSoon.svelte';
</script>

<AnalyticsComingSoon
  titleKey="analytics.hub.tabs.weather"
  icon={CloudSun}
  descriptionKey="analytics.comingSoon.weather.description"
  featureKeys={[
    'analytics.comingSoon.weather.feature1',
    'analytics.comingSoon.weather.feature2',
    'analytics.comingSoon.weather.feature3',
  ]}
/>
