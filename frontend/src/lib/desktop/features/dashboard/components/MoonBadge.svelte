<!--
  MoonBadge - Compact moon phase emoji badge for detection cards.
  Displayed alongside the existing WeatherBadge.
  @component
-->
<script lang="ts">
  import { getMoonPhaseEmoji } from '$lib/utils/weather';

  interface Props {
    moonPhaseName: string;
    className?: string;
  }

  let { moonPhaseName, className = '' }: Props = $props();

  let emoji = $derived(getMoonPhaseEmoji(moonPhaseName));
</script>

<div
  class="flex items-center justify-center rounded-full bg-black/50 px-1.5 py-0.5 backdrop-blur-sm {className}"
  title={moonPhaseName}
  role="img"
  aria-label={moonPhaseName}
>
  <span class="text-sm leading-none">{emoji}</span>
</div>
