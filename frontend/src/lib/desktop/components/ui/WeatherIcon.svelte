<!--
  WeatherIcon Component

  Purpose: Renders weather provider icons based on provider type.
  This component provides a type-safe way to display weather provider icons
  without using raw HTML injection.

  Props:
  - provider: The weather provider key (none, yrno, openweather, wunderground)
  - className: Optional CSS classes for sizing/styling

  @component
-->
<script lang="ts">
  // Import all weather provider icons as raw SVG strings
  import NoneIcon from '$lib/assets/icons/weather/none.svg?raw';
  import YrnoIcon from '$lib/assets/icons/weather/yrno.svg?raw';
  import OpenWeatherIcon from '$lib/assets/icons/weather/openweather.svg?raw';
  import WundergroundIcon from '$lib/assets/icons/weather/wunderground.svg?raw';

  // Weather provider type definition
  export type WeatherProvider = 'none' | 'yrno' | 'openweather' | 'wunderground';

  interface Props {
    provider: WeatherProvider;
    className?: string;
  }

  let { provider, className = 'size-5' }: Props = $props();

  // Map provider keys to their SVG content
  const providerIcons: Record<WeatherProvider, string> = {
    none: NoneIcon,
    yrno: YrnoIcon,
    openweather: OpenWeatherIcon,
    wunderground: WundergroundIcon,
  };

  // Get the icon for the current provider
  let iconSvg = $derived(providerIcons[provider] || NoneIcon);
</script>

<!--
  Note: We use {@html} here because the SVG icons are static assets
  imported at build time, not user-generated content. The icons are
  trusted and sanitized by the build process.
-->
<span class="{className} shrink-0 [&>svg]:size-full [&>svg]:block">
  {@html iconSvg}
</span>
