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
  - Dynamic temperature thermometer (color changes with temperature)
  - Animated wind indicator (speed changes with wind strength)
  - Responsive visibility based on container width
  - Horizontal layout with smart metric prioritization
  - Accessible labels and descriptions
  
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

  // Temperature color calculation
  const tempColor = $derived(() => {
    if (temperature === undefined) return '#94a3b8'; // gray-400
    if (temperature <= 0) return '#3b82f6'; // blue-500 - freezing
    if (temperature <= 10) return '#06b6d4'; // cyan-500 - cold
    if (temperature <= 20) return '#10b981'; // emerald-500 - comfortable
    if (temperature <= 30) return '#f59e0b'; // amber-500 - warm
    return '#ef4444'; // red-500 - hot
  });

  // Wind animation speed calculation
  const windAnimationDuration = $derived(() => {
    if (windSpeed === undefined || windSpeed === 0) return '0s';
    // Faster animation for stronger winds
    const duration = Math.max(0.5, 3 - windSpeed * 0.1);
    return `${duration}s`;
  });

  // Wind strength indicator
  const windStrength = $derived(() => {
    if (windSpeed === undefined) return 'none';
    if (windSpeed < 1) return 'calm';
    if (windSpeed < 5) return 'light';
    if (windSpeed < 10) return 'moderate';
    if (windSpeed < 15) return 'strong';
    return 'severe';
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
    weatherIconMap[weatherCode()] || {
      day: '❓',
      night: '❓',
      description: weatherDescription || 'Unknown',
    }
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
        class={cn('wm-weather-icon inline-block flex-shrink-0', emojiSizeClasses[size])}
        aria-label={weatherDesc}
      >
        {weatherEmoji}
      </span>

      <!-- Weather Description - conditionally visible -->
      {#if showWeatherDescription}
        <span class={cn(textSizeClasses[size], 'text-base-content/70 whitespace-nowrap')}>
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
          <svg
            class={cn(sizeClasses[size], 'flex-shrink-0')}
            viewBox="0 0 24 24"
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
            aria-label={`Temperature: ${temperature.toFixed(1)}°C`}
          >
            <g>
              <!-- Thermometer body -->
              <rect
                x="9"
                y="3"
                width="6"
                height="11"
                rx="3"
                fill="#e2e8f0"
                stroke="#64748b"
                stroke-width="1.5"
              />
              <!-- Mercury/liquid -->
              <rect
                x="11"
                y={14 - Math.min(10, Math.max(0, ((temperature + 10) / 40) * 10))}
                width="2"
                height={Math.min(10, Math.max(0, ((temperature + 10) / 40) * 10))}
                fill={tempColor()}
                rx="1"
              />
              <!-- Bulb -->
              <circle
                cx="12"
                cy="18"
                r="3.5"
                fill={tempColor()}
                stroke="#64748b"
                stroke-width="1.5"
              />
              <!-- Temperature marks -->
              <line x1="8" y1="6" x2="9" y2="6" stroke="#64748b" stroke-width="0.5" />
              <line x1="8" y1="9" x2="9" y2="9" stroke="#64748b" stroke-width="0.5" />
              <line x1="8" y1="12" x2="9" y2="12" stroke="#64748b" stroke-width="0.5" />
            </g>
          </svg>
        {/if}
        <span class={cn(textSizeClasses[size], 'text-base-content/70 whitespace-nowrap')}>
          {temperature.toFixed(1)}{temperatureUnit()}
        </span>
      </div>
    {/if}

    <!-- Wind Speed Group -->
    {#if windSpeed !== undefined && showWindSpeedGroup}
      <div class="wm-wind-group flex items-center gap-1 flex-shrink-0">
        <!-- Wind Speed Icon -->
        {#if SHOW_WINDSPEED_ICON}
          <svg
            class={cn(sizeClasses[size], 'flex-shrink-0')}
            viewBox="0 0 24 24"
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
            aria-label={`Wind speed: ${windSpeed.toFixed(1)} m/s`}
            style={`--wind-duration: ${windAnimationDuration()}`}
          >
            <g>
              <!-- Wind lines with varying opacity based on strength -->
              <path
                d="M3 8h11c1.1 0 2 0.9 2 2s-0.9 2-2 2h-2"
                stroke="#64748b"
                stroke-width="1.5"
                stroke-linecap="round"
                opacity={windStrength() === 'calm' ? '0.3' : '0.8'}
                style:animation="windBlow {windAnimationDuration()} ease-in-out infinite"
              />
              <path
                d="M3 12h15c1.7 0 3 1.3 3 3s-1.3 3-3 3h-3"
                stroke="#64748b"
                stroke-width="1.5"
                stroke-linecap="round"
                opacity={windStrength() === 'calm'
                  ? '0.3'
                  : windStrength() === 'light'
                    ? '0.6'
                    : '1'}
                style:animation="windBlow {windAnimationDuration()} ease-in-out infinite"
                style:animation-delay="0.1s"
              />
              <path
                d="M3 16h10c0.6 0 1 0.4 1 1s-0.4 1-1 1h-1"
                stroke="#64748b"
                stroke-width="1.5"
                stroke-linecap="round"
                opacity={windStrength() === 'calm'
                  ? '0.2'
                  : windStrength() === 'light'
                    ? '0.4'
                    : '0.7'}
                style:animation="windBlow {windAnimationDuration()} ease-in-out infinite"
                style:animation-delay="0.2s"
              />
              {#if windStrength() === 'strong' || windStrength() === 'severe'}
                <!-- Extra wind line for strong winds -->
                <path
                  d="M3 4h8c0.6 0 1 0.4 1 1s-0.4 1-1 1h-1"
                  stroke="#94a3b8"
                  stroke-width="1.5"
                  stroke-linecap="round"
                  opacity="0.6"
                  style:animation="windBlow {windAnimationDuration()} ease-in-out infinite"
                  style:animation-delay="0.3s"
                />
              {/if}
            </g>
          </svg>
        {/if}
        <span class={cn(textSizeClasses[size], 'text-base-content/70 whitespace-nowrap')}>
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
  @keyframes windBlow {
    0%,
    100% {
      transform: translateX(0);
    }

    50% {
      transform: translateX(2px);
    }
  }

  /* Container debugging (remove in production) */
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
</style>
