<!--
  WeatherDetails.svelte
  
  A detailed weather component optimized for modal dialogs and spacious layouts.
  Displays weather metrics in a vertical stack with icons always visible.
  
  Usage:
  - Modal dialogs (ReviewModal)
  - Detailed detection views
  - Large information panels
  
  Features:
  - Vertical stacked layout
  - Always shows weather, temperature, and wind icons
  - Optimized for large display contexts
  - No responsive hiding - all metrics always visible
  - Clean, readable typography
  - Uses centralized icon system from icons.ts
  
  Props:
  - weatherIcon?: string - Weather icon code (e.g., '01d', '09n')
  - weatherDescription?: string - Weather description text
  - timeOfDay?: 'day' | 'night' - Time of day for icon display
  - temperature?: number - Temperature in Celsius
  - windSpeed?: number - Wind speed in m/s
  - windGust?: number - Wind gust speed in m/s
  - units?: 'metric' | 'imperial' | 'standard' - Unit system
  - size?: 'md' | 'lg' | 'xl' - Component size variant
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import { Thermometer, Wind } from '@lucide/svelte';
  import { safeGet } from '$lib/utils/security';
  import {
    convertTemperature,
    convertWindSpeed,
    getTemperatureSymbol,
    getWindSpeedUnit,
    type TemperatureUnit,
  } from '$lib/utils/formatters';

  interface Props {
    weatherIcon?: string;
    weatherDescription?: string;
    timeOfDay?: 'day' | 'night';
    temperature?: number;
    windSpeed?: number;
    windGust?: number;
    units?: TemperatureUnit;
    size?: 'md' | 'lg' | 'xl';
    className?: string;
    loading?: boolean;
    error?: string | null;
  }

  let {
    weatherIcon,
    weatherDescription,
    timeOfDay = 'day',
    temperature,
    windSpeed,
    windGust,
    units = 'metric',
    size = 'lg',
    className = '',
    loading = false,
    error = null,
  }: Props = $props();

  // Get the appropriate unit label based on the units setting
  const temperatureUnit = $derived(getTemperatureSymbol(units));

  // Convert temperature from Celsius (internal storage) to display unit
  const displayTemperature = $derived.by(() => {
    if (temperature === undefined) return undefined;
    return convertTemperature(temperature, units);
  });

  // Convert wind speed from m/s (internal storage) to display unit
  const displayWindSpeed = $derived.by(() => {
    if (windSpeed === undefined) return undefined;
    return convertWindSpeed(windSpeed, units);
  });

  // Convert wind gust from m/s (internal storage) to display unit
  const displayWindGust = $derived.by(() => {
    if (windGust === undefined) return undefined;
    return convertWindSpeed(windGust, units);
  });

  const windSpeedUnit = $derived(getWindSpeedUnit(units));

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
  const weatherCode = $derived.by(() => {
    if (!weatherIcon || typeof weatherIcon !== 'string') return '';
    const match = weatherIcon.match(/^(\d{2})[dn]?$/);
    return match ? match[1] : '';
  });

  // Determine if it's night time
  const isNight = $derived(timeOfDay === 'night' || weatherIcon?.endsWith('n'));

  // Get weather emoji and description
  const weatherInfo = $derived(
    safeGet(weatherIconMap, weatherCode, {
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

  // Get appropriate wind icon opacity based on wind speed
  const getWindOpacity = $derived.by(() => {
    if (windSpeed === undefined) return '';
    if (windSpeed < 3) return 'opacity-50'; // Light wind: 0-3 m/s
    if (windSpeed < 8) return 'opacity-75'; // Moderate wind: 3-8 m/s
    return ''; // Strong wind: 8+ m/s - full opacity
  });

  // Size classes
  const iconSizeClasses = {
    md: 'h-5 w-5',
    lg: 'h-6 w-6',
    xl: 'h-8 w-8',
  };

  const textSizeClasses = {
    md: 'text-sm',
    lg: 'text-base',
    xl: 'text-lg',
  };

  const emojiSizeClasses = {
    md: 'text-lg',
    lg: 'text-xl',
    xl: 'text-2xl',
  };

  const gapClasses = {
    md: 'gap-2',
    lg: 'gap-3',
    xl: 'gap-4',
  };
</script>

<div class={cn('wd-container flex flex-col', safeGet(gapClasses, size, ''), className)}>
  <!-- Loading State -->
  {#if loading}
    <div class="animate-pulse space-y-2">
      <div class="flex items-center gap-2">
        <div class={cn('rounded-full bg-base-300', safeGet(iconSizeClasses, size, ''))}></div>
        <div class="h-4 bg-base-300 rounded-sm w-24"></div>
      </div>
      <div class="flex items-center gap-2">
        <div class={cn('rounded-sm bg-base-300', safeGet(iconSizeClasses, size, ''))}></div>
        <div class="h-4 bg-base-300 rounded-sm w-16"></div>
      </div>
      <div class="flex items-center gap-2">
        <div class={cn('rounded-sm bg-base-300', safeGet(iconSizeClasses, size, ''))}></div>
        <div class="h-4 bg-base-300 rounded-sm w-20"></div>
      </div>
    </div>
    <!-- Error State -->
  {:else if error}
    <div class="text-error text-sm">
      {error}
    </div>
    <!-- Weather Condition with Icon and Description -->
  {:else if weatherIcon}
    <div class="wd-weather-row flex items-center gap-2">
      <span
        class={cn('wd-weather-icon', safeGet(emojiSizeClasses, size, ''))}
        aria-label={weatherDesc}
      >
        {weatherEmoji}
      </span>
      <span class={cn(safeGet(textSizeClasses, size, ''), 'text-base-content font-medium')}>
        {weatherDesc}
      </span>
    </div>
  {/if}

  <!-- Temperature with Thermometer Icon -->
  {#if displayTemperature !== undefined}
    <div class="wd-temperature-row flex items-center gap-2">
      <Thermometer
        class={cn(safeGet(iconSizeClasses, size, ''), 'shrink-0')}
        aria-label={`Temperature: ${displayTemperature.toFixed(1)}${temperatureUnit}`}
      />
      <span class={cn(safeGet(textSizeClasses, size, ''), 'text-base-content')}>
        {displayTemperature.toFixed(1)}{temperatureUnit}
      </span>
    </div>
  {/if}

  <!-- Wind Speed with Wind Icon -->
  {#if displayWindSpeed !== undefined}
    <div class="wd-wind-row flex items-center gap-2">
      <Wind
        class={cn(safeGet(iconSizeClasses, size, ''), getWindOpacity, 'shrink-0')}
        aria-label={`Wind speed: ${displayWindSpeed.toFixed(0)} ${windSpeedUnit}`}
      />
      <span class={cn(safeGet(textSizeClasses, size, ''), 'text-base-content')}>
        {displayWindSpeed.toFixed(0)}{displayWindGust !== undefined &&
        displayWindGust > displayWindSpeed
          ? `(${displayWindGust.toFixed(0)})`
          : ''}
        {windSpeedUnit}
      </span>
    </div>
  {:else}
    <!-- No Data State -->
    <div class={cn(safeGet(textSizeClasses, size, ''), 'text-base-content/40 italic')}>
      {t('detections.weather.noData')}
    </div>
  {/if}
</div>

<style>
  /* Component-specific styling */
  .wd-container {
    min-width: 0; /* Prevent flex item from growing beyond container */
  }

  .wd-weather-row,
  .wd-temperature-row,
  .wd-wind-row {
    min-width: 0; /* Prevent overflow */
    align-items: center;
  }

  /* Ensure text doesn't wrap in compact displays */
  .wd-weather-row span,
  .wd-temperature-row span,
  .wd-wind-row span {
    white-space: nowrap;
  }

  /* No icon sizing overrides needed - Lucide icons accept classes directly */
</style>
