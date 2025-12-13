<!--
  WeatherBadge.svelte

  A compact badge displaying weather icon and temperature.
  Designed for overlay on spectrogram cards.

  Props:
  - weatherIcon: string - Weather code identifier
  - temperature?: number - Temperature value
  - units?: string - Temperature unit system
  - timeOfDay?: string - Day/night context for icon
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { safeGet } from '$lib/utils/security';

  interface Props {
    weatherIcon: string;
    temperature?: number;
    units?: string;
    timeOfDay?: string;
    className?: string;
  }

  let { weatherIcon, temperature, units, timeOfDay = 'day', className = '' }: Props = $props();

  // Weather icon mapping
  const weatherIconMap: Record<string, { day: string; night: string; description: string }> = {
    '01': { day: '☀️', night: '🌙', description: 'Clear sky' },
    '02': { day: '⛅', night: '☁️', description: 'Few clouds' },
    '03': { day: '☁️', night: '☁️', description: 'Scattered clouds' },
    '04': { day: '☁️', night: '☁️', description: 'Broken clouds' },
    '09': { day: '🌧️', night: '🌧️', description: 'Shower rain' },
    '10': { day: '🌦️', night: '🌧️', description: 'Rain' },
    '11': { day: '⛈️', night: '⛈️', description: 'Thunderstorm' },
    '13': { day: '❄️', night: '❄️', description: 'Snow' },
    '50': { day: '🌫️', night: '🌫️', description: 'Mist' },
  };

  // Extract icon code
  const iconCode = $derived.by(() => {
    if (!weatherIcon || typeof weatherIcon !== 'string') return '';
    const match = weatherIcon.match(/^(\d{2})[dn]?$/);
    return match ? match[1] : '';
  });

  const isNight = $derived(timeOfDay === 'night' || weatherIcon?.endsWith('n'));
  const iconData = $derived(
    safeGet(weatherIconMap, iconCode, { day: '❓', night: '❓', description: 'Unknown' })
  );
  const icon = $derived(isNight ? iconData.night : iconData.day);

  // Format temperature
  function formatTemperature(temp: number | undefined, unit: string | undefined): string {
    if (temp === undefined) return '';
    const rounded = Math.round(temp);
    if (unit === 'imperial') return `${rounded}°F`;
    return `${rounded}°C`;
  }

  const formattedTemp = $derived(formatTemperature(temperature, units));
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
