<!--
  WeatherIcon.svelte
  
  A component that displays weather condition icons with day/night variations.
  Maps weather codes to appropriate emoji icons based on time of day context.
  
  Usage:
  - Detection records with weather data
  - Weather information displays
  - Environmental context indicators
  - Historical weather visualization
  
  Features:
  - Day/night icon variations
  - Size variants (sm, md, lg)
  - Weather code mapping
  - Comprehensive weather conditions
  - Accessible descriptions
  
  Props:
  - weatherIcon: string - Weather code identifier
  - timeOfDay?: string - Time context for icon selection
  - size?: 'sm' | 'md' | 'lg' - Icon size variant
  - className?: string - Additional CSS classes
  
  Weather Codes:
  - 01: Clear sky
  - 02-04: Various cloud conditions
  - 09-10: Rain conditions
  - 11: Thunderstorm
  - 13: Snow
  - 50: Mist/fog
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';

  interface Props {
    weatherIcon: string;
    timeOfDay?: string;
    size?: 'sm' | 'md' | 'lg';
    className?: string;
  }

  let { weatherIcon, timeOfDay = 'day', size = 'md', className = '' }: Props = $props();

  // Map weather codes to icons
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

  // Extract icon code from weatherIcon string (e.g., "01d" -> "01") with validation
  const iconCode = $derived(
    (() => {
      if (!weatherIcon || typeof weatherIcon !== 'string') {
        return '';
      }

      // Use regex to safely extract two-digit code from the beginning
      const match = weatherIcon.match(/^(\d{2})[dn]?$/);
      return match ? match[1] : '';
    })()
  );
  const isNight = $derived(timeOfDay === 'night' || weatherIcon?.endsWith('n'));

  const iconData = $derived(
    weatherIconMap[iconCode] || { day: '❓', night: '❓', description: 'Unknown' }
  );
  const icon = $derived(isNight ? iconData.night : iconData.day);
  const description = $derived(iconData.description);

  // Size classes
  const sizeClasses = {
    sm: 'text-base',
    md: 'text-lg',
    lg: 'text-xl',
  };
</script>

<span
  class={cn('inline-block', sizeClasses[size], className)}
  title={description}
  aria-label={description}
>
  {icon}
</span>
