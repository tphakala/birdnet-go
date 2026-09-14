<!--
  WeatherIcon Component

  Purpose: Renders weather provider icons based on provider type.
  This component provides a type-safe way to display weather provider icons.

  Props:
  - provider: The weather provider key (none, yrno, openweather, wunderground)
  - className: Optional CSS classes for sizing/styling

  Note: Uses {@html} for SVG rendering - safe because icons are static build-time
  assets, not user-generated content.

  @component
-->
<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils/cn.js';

  // Import all weather provider icons as raw SVG strings
  import NoneIcon from '$lib/assets/icons/weather/none.svg?raw';
  import YrnoIcon from '$lib/assets/icons/weather/yrno.svg?raw';
  import OpenWeatherIcon from '$lib/assets/icons/weather/openweather.svg?raw';
  import WundergroundIcon from '$lib/assets/icons/weather/wunderground.svg?raw';

  // Weather provider type definition
  export type WeatherProvider = 'none' | 'yrno' | 'openweather' | 'wunderground';

  interface Props extends HTMLAttributes<HTMLElement> {
    provider: WeatherProvider;
    className?: string;
  }

  let { provider, className = '', ...rest }: Props = $props();

  // Map provider keys to their SVG content
  const providerIcons: Record<WeatherProvider, string> = {
    none: NoneIcon,
    yrno: YrnoIcon,
    openweather: OpenWeatherIcon,
    wunderground: WundergroundIcon,
  };

  // Runtime type guard to satisfy static analysis (object injection sink warning)
  const isWeatherProvider = (v: unknown): v is WeatherProvider =>
    typeof v === 'string' && v in providerIcons;

  // Get the icon for the current provider with runtime validation
  // eslint-disable-next-line security/detect-object-injection -- Validated by isWeatherProvider type guard
  let iconSvg = $derived(isWeatherProvider(provider) ? providerIcons[provider] : NoneIcon);
</script>

<span
  class={cn('size-5 shrink-0 [&>svg]:size-full [&>svg]:block', className)}
  aria-hidden="true"
  {...rest}
>
  {@html iconSvg}
</span>
