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
  import { Thermometer, Wind } from '@lucide/svelte';
  import { safeGet } from '$lib/utils/security';
  import {
    convertTemperature,
    convertWindSpeed,
    getTemperatureSymbol,
    getWindSpeedUnit,
    type TemperatureUnit,
  } from '$lib/utils/formatters';
  import {
    WEATHER_ICON_MAP,
    UNKNOWN_WEATHER_INFO,
    getEffectiveWeatherCode,
    isNightTime,
    translateWeatherCondition,
    getWindOpacityClass,
  } from '$lib/utils/weather';

  interface Props {
    weatherIcon?: string;
    weatherDescription?: string;
    timeOfDay?: 'day' | 'night';
    temperature?: number;
    windSpeed?: number;
    windGust?: number;
    units?: TemperatureUnit;
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

  // Extract base weather code using shared utility, with fallback to description-based derivation
  const weatherCode = $derived(getEffectiveWeatherCode(weatherIcon, weatherDescription));

  // Determine if it's night time using shared utility
  const isNight = $derived(isNightTime(weatherIcon, timeOfDay));

  // Get weather info from shared mapping
  const weatherInfo = $derived(
    safeGet(WEATHER_ICON_MAP, weatherCode, {
      ...UNKNOWN_WEATHER_INFO,
      description: weatherDescription || UNKNOWN_WEATHER_INFO.description,
    })
  );

  const weatherEmoji = $derived(isNight ? weatherInfo.night : weatherInfo.day);

  // Get localized weather description using shared utility
  const weatherDesc = $derived(
    translateWeatherCondition(weatherDescription || weatherInfo.description)
  );

  // Get appropriate wind icon opacity based on wind speed using shared utility
  const windOpacity = $derived(getWindOpacityClass(windSpeed));

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
    <div class="wm-weather-group flex items-center gap-1 shrink-0">
      <!-- Weather Icon - always visible when showWeatherIcon is true -->
      <span
        class={cn('wm-weather-icon inline-block shrink-0', safeGet(emojiSizeClasses, size, ''))}
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
    {#if displayTemperature !== undefined && showTemperatureGroup}
      <div class="wm-temperature-group flex items-center gap-1 shrink-0">
        <!-- Temperature Icon -->
        {#if SHOW_TEMPERATURE_ICON}
          <Thermometer
            class={cn(safeGet(sizeClasses, size, ''), 'shrink-0')}
            aria-label={`Temperature: ${displayTemperature.toFixed(1)}${temperatureUnit}`}
          />
        {/if}
        <span
          class={cn(safeGet(textSizeClasses, size, ''), 'text-base-content/70 whitespace-nowrap')}
        >
          {displayTemperature.toFixed(1)}{temperatureUnit}
        </span>
      </div>
    {/if}

    <!-- Wind Speed Group -->
    {#if displayWindSpeed !== undefined && showWindSpeedGroup}
      <div class="wm-wind-group flex items-center gap-1 shrink-0">
        <!-- Wind Speed Icon -->
        {#if SHOW_WINDSPEED_ICON}
          <Wind
            class={cn(safeGet(sizeClasses, size, ''), windOpacity, 'shrink-0')}
            aria-label={`Wind speed: ${displayWindSpeed.toFixed(0)} ${windSpeedUnit}`}
          />
        {/if}
        <span
          class={cn(safeGet(textSizeClasses, size, ''), 'text-base-content/70 whitespace-nowrap')}
        >
          {displayWindSpeed.toFixed(0)}{displayWindGust !== undefined &&
          displayWindGust > displayWindSpeed
            ? `(${displayWindGust.toFixed(0)})`
            : ''}
          {windSpeedUnit}
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

  /* No icon sizing overrides needed - Lucide icons accept classes directly */
</style>
