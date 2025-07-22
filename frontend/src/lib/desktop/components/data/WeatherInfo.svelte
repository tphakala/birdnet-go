<!--
  WeatherInfo.svelte
  
  A comprehensive weather information component that fetches and displays current weather conditions.
  Provides detailed meteorological data including temperature, humidity, wind, and atmospheric pressure.
  
  Usage:
  - Detection detail views with environmental context
  - Weather dashboards and displays
  - Environmental data visualization
  - Historical weather information
  
  Features:
  - Fetches real-time weather data via API
  - Displays hourly and daily weather information
  - Multiple weather parameters (temperature, humidity, wind, pressure)
  - Loading and error state handling
  - Responsive design with customizable layout
  - API integration with CSRF protection
  
  Props:
  - date: string - Date for weather data retrieval
  - className?: string - Additional CSS classes
  - children?: Snippet - Custom content slot
  
  Weather Data:
  - Hourly: temperature, conditions, wind speed, humidity, pressure, clouds
  - Daily: min/max temperatures, weather conditions
  - Automatic API fetching based on date
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { fetchWithCSRF } from '$lib/utils/api';
  import type { Snippet } from 'svelte';
  import { weatherIcons, alertIconsSvg } from '$lib/utils/icons'; // Centralized icons - see icons.ts

  interface WeatherData {
    hourly?: {
      temperature?: number;
      weatherMain?: string;
      windSpeed?: number;
      humidity?: number;
      pressure?: number;
      clouds?: number;
    };
    daily?: {
      temperatureMin?: number;
      temperatureMax?: number;
      weatherMain?: string;
    };
  }

  interface Props {
    detectionId?: string;
    weatherData?: WeatherData;
    compact?: boolean;
    showTitle?: boolean;
    autoFetch?: boolean;
    className?: string;
    titleClassName?: string;
    gridClassName?: string;
    onError?: (_error: Error) => void;
    onLoad?: (_data: WeatherData) => void;
    children?: Snippet<[WeatherData]>;
  }

  let {
    detectionId,
    weatherData,
    compact = false,
    showTitle = true,
    autoFetch = true,
    className = '',
    titleClassName = '',
    gridClassName = '',
    onError,
    onLoad,
    children,
  }: Props = $props();

  let weather = $state<WeatherData | null>(weatherData || null);
  let loading = $state(false);
  let error = $state<string | null>(null);

  // Fetch weather data
  async function fetchWeatherInfo(id: string) {
    if (!id) return;

    loading = true;
    error = null;

    try {
      const data = await fetchWithCSRF<WeatherData>(`/api/v2/weather/detection/${id}`);
      weather = data;
      onLoad?.(data);
    } catch (err) {
      const errorMessage = err instanceof Error ? err.message : 'Failed to load weather data';
      error = errorMessage;
      // Error fetching weather data
      onError?.(err instanceof Error ? err : new Error(errorMessage));
    } finally {
      loading = false;
    }
  }

  // Helper function to format temperature
  function formatTemperature(temp: number | undefined): string {
    if (temp === undefined) return 'N/A';
    return `${temp}°C`;
  }

  // Helper function to format percentage
  function formatPercentage(value: number | undefined): string {
    if (value === undefined) return 'N/A';
    return `${value}%`;
  }

  // Helper function to format wind speed
  function formatWindSpeed(speed: number | undefined): string {
    if (speed === undefined) return 'N/A';
    return `${speed} km/h`;
  }

  // Fetch when detectionId changes
  $effect(() => {
    if (detectionId && autoFetch) {
      fetchWeatherInfo(detectionId);
    }
  });

  // Export fetch function for manual control
  export function refresh() {
    if (detectionId) {
      fetchWeatherInfo(detectionId);
    }
  }

  export function setWeatherData(data: WeatherData) {
    weather = data;
    error = null;
    loading = false;
  }
</script>

<div class={cn('weather-info', className)}>
  {#if showTitle}
    <h3 class={cn('text-lg font-semibold text-base-content mb-3', titleClassName)}>
      Weather Information
    </h3>
  {/if}

  {#if loading}
    <!-- Loading state -->
    <div class="py-4 flex justify-center" role="status" aria-live="polite">
      <span class="loading loading-spinner loading-md text-primary"></span>
      <span class="sr-only">Loading weather information...</span>
    </div>
  {:else if error}
    <!-- Error state -->
    <div class="py-4" role="alert">
      <div class="text-error flex items-center">
        <div class="h-5 w-5 mr-2">
          {@html alertIconsSvg.error}
        </div>
        <span>{error}</span>
      </div>
    </div>
  {:else if weather}
    <!-- Weather data display -->
    {#if children}
      {@render children(weather)}
    {:else}
      <div
        class={cn(
          'grid gap-2 text-sm',
          compact ? 'grid-cols-2 sm:grid-cols-4' : 'grid-cols-2',
          gridClassName
        )}
        aria-live="polite"
      >
        <!-- Temperature -->
        <div class="flex items-center">
          {@html weatherIcons.temperature}
          <div>
            <div class="text-base-content/70">Temperature</div>
            <div class="font-medium">{formatTemperature(weather.hourly?.temperature)}</div>
          </div>
        </div>

        <!-- Weather condition -->
        <div class="flex items-center">
          {@html weatherIcons.sun}
          <div>
            <div class="text-base-content/70">Weather</div>
            <div class="font-medium">{weather.hourly?.weatherMain || 'N/A'}</div>
          </div>
        </div>

        <!-- Wind speed -->
        <div class="flex items-center">
          {@html weatherIcons.wind}
          <div>
            <div class="text-base-content/70">Wind</div>
            <div class="font-medium">{formatWindSpeed(weather.hourly?.windSpeed)}</div>
          </div>
        </div>

        <!-- Humidity -->
        <div class="flex items-center">
          {@html weatherIcons.humidity}
          <div>
            <div class="text-base-content/70">Humidity</div>
            <div class="font-medium">{formatPercentage(weather.hourly?.humidity)}</div>
          </div>
        </div>

        {#if !compact && weather.hourly?.pressure !== undefined}
          <!-- Pressure (non-compact mode) -->
          <div class="flex items-center">
            {@html weatherIcons.pressure}
            <div>
              <div class="text-base-content/70">Pressure</div>
              <div class="font-medium">{weather.hourly.pressure} hPa</div>
            </div>
          </div>
        {/if}

        {#if !compact && weather.hourly?.clouds !== undefined}
          <!-- Cloud cover (non-compact mode) -->
          <div class="flex items-center">
            {@html weatherIcons.cloudCover}
            <div>
              <div class="text-base-content/70">Cloud Cover</div>
              <div class="font-medium">{formatPercentage(weather.hourly.clouds)}</div>
            </div>
          </div>
        {/if}
      </div>
    {/if}
  {:else}
    <!-- No data state -->
    <div class="py-4 text-center text-base-content/50">No weather data available</div>
  {/if}
</div>
