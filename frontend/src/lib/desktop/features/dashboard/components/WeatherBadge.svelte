<!--
  WeatherBadge.svelte

  A compact badge displaying weather icon and temperature.
  Designed for overlay on spectrogram cards.

  Props:
  - weatherIcon: string - Weather code identifier
  - description?: string - Raw weather description (yr.no symbol) for fallback
  - temperature?: number - Temperature value
  - units?: string - Temperature unit system
  - timeOfDay?: string - Day/night context for icon
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { safeGet } from '$lib/utils/security';
  import { formatTemperatureCompact, type TemperatureUnit } from '$lib/utils/formatters';
  import {
    WEATHER_ICON_MAP,
    UNKNOWN_WEATHER_INFO,
    getEffectiveWeatherCode,
  } from '$lib/utils/weather';

  interface Props {
    weatherIcon: string;
    description?: string;
    temperature?: number;
    units?: string;
    timeOfDay?: string;
    className?: string;
  }

  let {
    weatherIcon,
    description,
    temperature,
    units,
    timeOfDay = 'day',
    className = '',
  }: Props = $props();

  // Get effective icon code with fallback to description-based derivation
  const iconCode = $derived(getEffectiveWeatherCode(weatherIcon, description));

  const isNight = $derived(timeOfDay === 'night' || weatherIcon?.endsWith('n'));
  const iconData = $derived(safeGet(WEATHER_ICON_MAP, iconCode, UNKNOWN_WEATHER_INFO));
  const icon = $derived(isNight ? iconData.night : iconData.day);

  // Format temperature with conversion from Celsius (internal storage) to display unit
  // Temperature is always stored in Celsius; units prop indicates display preference
  const formattedTemp = $derived(
    formatTemperatureCompact(temperature, (units as TemperatureUnit) || 'metric')
  );
</script>

<div
  class={cn('weather-badge', className)}
  title={iconData.description}
  aria-label="{iconData.description}{formattedTemp ? `, ${formattedTemp}` : ''}"
>
  <span class="weather-icon">{icon}</span>
  {#if formattedTemp}
    <span class="weather-temp">{formattedTemp}</span>
  {/if}
</div>

<style>
  .weather-badge {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    padding: 0.25rem 0.5rem;
    border-radius: 9999px;
    background-color: rgb(0 0 0 / 0.5);
    backdrop-filter: blur(4px);
    box-shadow: 0 2px 4px rgb(0 0 0 / 0.2);
  }

  .weather-icon {
    font-size: 0.875rem;
    line-height: 1;
  }

  .weather-temp {
    font-size: 0.75rem;
    font-weight: 500;
    color: white;
  }
</style>
