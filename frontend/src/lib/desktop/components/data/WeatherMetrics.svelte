<!--
  WeatherMetrics.svelte
  
  A responsive weather metrics component that displays weather icon, temperature, and wind speed.
  Automatically adjusts visible metrics based on available container width.
  
  Usage:
  - Weather data display in detection rows
  - Environmental condition visualization
  - Responsive weather status indicators
  
  Features:
  - Integrated weather icon display
  - Responsive visibility based on container width
  - Horizontal layout with smart metric prioritization
  - Accessible labels and descriptions
  - Uses centralized icon system from icons.ts
  
  Props:
  - weatherIcon?: string - Weather icon code (e.g., '01d', '09n')
  - weatherDescription?: string - Weather description text
  - timeOfDay?: 'day' | 'night' - Time of day for icon display
  - temperature?: number - Temperature in Celsius
  - windSpeed?: number - Wind speed in m/s
  - size?: 'sm' | 'md' | 'lg' - Icon size variant
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import { weatherIcons } from '$lib/utils/icons';
  import { safeGet } from '$lib/utils/security';

  interface Props {
    weatherIcon?: string;
    weatherDescription?: string;
    timeOfDay?: 'day' | 'night';
    temperature?: number;
    windSpeed?: number;
    windGust?: number;
    units?: 'metric' | 'imperial' | 'standard';
    size?: 'sm' | 'md' | 'lg';
    className?: string;
  }

  let {
    weatherIcon,
    weatherDescription,
    timeOfDay = 'day',
    temperature,
    windSpeed,
    windGust,
    units = 'metric',
    size = 'sm',
    className = '',
  }: Props = $props();

  // Toggle constants for temperature and wind speed icons
  const SHOW_TEMPERATURE_ICON = false;
  const SHOW_WINDSPEED_ICON = false;

  // Container element reference for width observation
  let containerElement: HTMLDivElement | null = $state(null);
  let containerWidth = $state(0);

  // Observe container width
  $effect(() => {
    if (!containerElement) return;

    const resizeObserver = new globalThis.ResizeObserver(entries => {
      for (const entry of entries) {
        containerWidth = entry.contentRect.width;
      }
    });

    resizeObserver.observe(containerElement);

    return () => {
      resizeObserver.disconnect();
    };
  });

  // Responsive visibility thresholds - progressive enhancement
  // With two-line layout, we need less horizontal space per element
  const showWeatherIcon = $derived(containerWidth === 0 || containerWidth >= 30); // Always show weather icon unless very narrow
  const showWeatherDescription = $derived(containerWidth === 0 || containerWidth >= 110); // Hide description on narrow
  const showTemperatureGroup = $derived(containerWidth === 0 || containerWidth >= 30); // Always show temperature
  const showWindSpeedGroup = $derived(containerWidth === 0 || containerWidth >= 100); // Hide wind speed on narrow

  // Get the appropriate unit labels based on the units setting
  const temperatureUnit = $derived(() => {
    switch (units) {
      case 'imperial':
        return '°F';
      case 'standard':
        return 'K';
      default:
        return '°C';
    }
  });

  const windSpeedUnit = $derived(() => {
    return units === 'imperial' ? 'mph' : 'm/s';
  });

  // Weather icon mapping
  const weatherIconMap: Record<string, { day: string; night: string; description: string }> = {
    '01': { day: '☀️', night: '🌙', description: 'Clear sky' },
    '02': { day: '⛅', night: '☁️', description: 'Few clouds' },
    '03': { day: '⛅', night: '☁️', description: 'Scattered clouds' },
    '04': { day: '⛅', night: '☁️', description: 'Broken clouds' },
    '09': { day: '🌧️', night: '🌧️', description: 'Shower rain' },
    '10': { day: '🌦️', night: '🌧️', description: 'Rain' },
    '11': { day: '⛈️', night: '⛈️', description: 'Thunderstorm' },
    '13': { day: '❄️', night: '❄️', description: 'Snow' },
    '50': { day: '🌫️', night: '🌫️', description: 'Mist' },
  };

  // Extract base weather code
  const weatherCode = $derived(() => {
    if (!weatherIcon || typeof weatherIcon !== 'string') return '';
    const match = weatherIcon.match(/^(\d{2})[dn]?$/);
    return match ? match[1] : '';
  });

  // Determine if it's night time
  const isNight = $derived(timeOfDay === 'night' || weatherIcon?.endsWith('n'));

  // Get weather emoji and description
  const weatherInfo = $derived(
    safeGet(weatherIconMap, weatherCode(), {
      day: '❓',
      night: '❓',
      description: weatherDescription || 'Unknown',
    })
  );

  const weatherEmoji = $derived(isNight ? weatherInfo.night : weatherInfo.day);

  // Helper function to translate weather conditions with fallbacks
  function translateWeatherCondition(condition: string | undefined): string {
    if (!condition) return '';

    // Normalize the condition string
    const normalized = condition.toLowerCase().replace(/ /g, '_');

    // Try different key variations
    const keys = [
      `detections.weather.conditions.${normalized}`,
      `detections.weather.conditions.${condition.toLowerCase()}`,
      'detections.weather.conditions.unknown',
    ];

    // Return first successful translation or original
    for (const key of keys) {
      const translation = t(key);
      if (translation !== key) {
        return translation;
      }
    }

    return condition;
  }

  // Get localized weather description
  const weatherDesc = $derived(
    translateWeatherCondition(weatherDescription || weatherInfo.description)
  );

  // Get appropriate wind icon based on wind speed
  const getWindIcon = $derived(() => {
    if (windSpeed === undefined) return safeGet(weatherIcons, 'wind', '');
    if (windSpeed < 3) return safeGet(weatherIcons, 'windLight', ''); // Light wind: 0-3 m/s
    if (windSpeed < 8) return safeGet(weatherIcons, 'windModerate', ''); // Moderate wind: 3-8 m/s
    return safeGet(weatherIcons, 'windStrong', ''); // Strong wind: 8+ m/s
  });

  // Size classes
  const sizeClasses = {
    sm: 'h-5 w-5',
    md: 'h-6 w-6',
    lg: 'h-8 w-8',
  };

  const textSizeClasses = {
    sm: 'text-xs',
    md: 'text-sm',
    lg: 'text-base',
  };

  const emojiSizeClasses = {
    sm: 'text-base',
    md: 'text-lg',
    lg: 'text-xl',
  };
</script>

<div
  bind:this={containerElement}
  class={cn('wm-container flex flex-col gap-1 overflow-hidden', className)}
  title={weatherDesc}
>
  <!-- Line 1: Weather Icon + Description -->
  {#if weatherIcon && showWeatherIcon}
    <div class="wm-weather-group flex items-center gap-1 flex-shrink-0">
      <!-- Weather Icon - always visible when showWeatherIcon is true -->
      <span
        class={cn(
          'wm-weather-icon inline-block flex-shrink-0',
          safeGet(emojiSizeClasses, size, '')
        )}
        aria-label={weatherDesc}
      >
        {weatherEmoji}
      </span>

      <!-- Weather Description - conditionally visible -->
      {#if showWeatherDescription}
        <span
          class={cn(safeGet(textSizeClasses, size, ''), 'text-base-content/70 whitespace-nowrap')}
        >
          {weatherDesc}
        </span>
      {/if}
    </div>
  {/if}

  <!-- Line 2: Temperature + Wind Speed -->
  <div class="flex items-center gap-2 overflow-hidden">
    <!-- Temperature Group -->
    {#if temperature !== undefined && showTemperatureGroup}
      <div class="wm-temperature-group flex items-center gap-1 flex-shrink-0">
        <!-- Temperature Icon -->
        {#if SHOW_TEMPERATURE_ICON}
          <div
            class={cn(safeGet(sizeClasses, size, ''), 'flex-shrink-0')}
            aria-label={`Temperature: ${temperature.toFixed(1)}°C`}
          >
            {@html safeGet(weatherIcons, 'temperature', '')}
          </div>
        {/if}
        <span
          class={cn(safeGet(textSizeClasses, size, ''), 'text-base-content/70 whitespace-nowrap')}
        >
          {temperature.toFixed(1)}{temperatureUnit()}
        </span>
      </div>
    {/if}

    <!-- Wind Speed Group -->
    {#if windSpeed !== undefined && showWindSpeedGroup}
      <div class="wm-wind-group flex items-center gap-1 flex-shrink-0">
        <!-- Wind Speed Icon -->
        {#if SHOW_WINDSPEED_ICON}
          <div
            class={cn(safeGet(sizeClasses, size, ''), 'flex-shrink-0')}
            aria-label={`Wind speed: ${windSpeed.toFixed(1)} m/s`}
          >
            {@html getWindIcon()}
          </div>
        {/if}
        <span
          class={cn(safeGet(textSizeClasses, size, ''), 'text-base-content/70 whitespace-nowrap')}
        >
          {windSpeed.toFixed(0)}{windGust !== undefined && windGust > windSpeed
            ? `(${windGust.toFixed(0)})`
            : ''}
          {windSpeedUnit()}
        </span>
      </div>
    {/if}
  </div>
</div>

<style>
  /* Container queries for responsive layout */
  /* These enable the component to adapt its layout based on available container width
     rather than viewport width, making it more flexible in different contexts */
  .wm-container {
    container-type: inline-size;
  }

  /* Progressive disclosure based on container width */
  @container (max-width: 30px) {
    /* Very narrow: hide temperature too */
    .wm-temperature-group {
      display: none;
    }
  }

  @container (max-width: 40px) {
    /* Very narrow: hide weather icon */
    .wm-weather-group {
      display: none;
    }
  }

  @container (max-width: 80px) {
    /* Medium narrow: hide wind speed */
    .wm-wind-group {
      display: none;
    }
  }

  /* Override the centralized icon sizing to match our component needs */
  /* Use more specific selectors to override without !important */
  .wm-temperature-group > div :global(svg.h-5.w-5),
  .wm-wind-group > div :global(svg.h-5.w-5) {
    height: inherit;
    width: inherit;
    margin-right: 0;
  }

  /* Ensure our size classes take precedence */
  .wm-temperature-group > .h-5.w-5,
  .wm-temperature-group > .h-6.w-6,
  .wm-temperature-group > .h-8.w-8,
  .wm-wind-group > .h-5.w-5,
  .wm-wind-group > .h-6.w-6,
  .wm-wind-group > .h-8.w-8 {
    display: block;
  }
</style>
