<script lang="ts">
  import { AudioLines } from '@lucide/svelte';
  import AnalyticsComingSoon from '../components/AnalyticsComingSoon.svelte';
</script>

<AnalyticsComingSoon
  titleKey="analytics.hub.tabs.soundscape"
  icon={AudioLines}
  descriptionKey="analytics.comingSoon.soundscape.description"
  featureKeys={[
    'analytics.comingSoon.soundscape.feature1',
    'analytics.comingSoon.soundscape.feature2',
    'analytics.comingSoon.soundscape.feature3',
  ]}
/>
