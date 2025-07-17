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
      const response = (await fetchWithCSRF(
        `/api/v2/weather/detection/${id}`
      )) as globalThis.Response;

      if (!response.ok) {
        throw new Error('Weather data not available');
      }

      const data = (await response.json()) as WeatherData;
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

  let previousDetectionId = $state<string | undefined>(undefined);

  // Fetch when detectionId changes
  $effect(() => {
    if (detectionId && autoFetch && detectionId !== previousDetectionId) {
      previousDetectionId = detectionId;
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
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-5 w-5 mr-2"
          viewBox="0 0 20 20"
          fill="currentColor"
        >
          <path
            fill-rule="evenodd"
            d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z"
            clip-rule="evenodd"
          />
        </svg>
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
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-5 w-5 mr-2 text-blue-500"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M3 15a4 4 0 004 4h9a5 5 0 10-.1-9.999 5.002 5.002 0 10-9.78 2.096A4.001 4.001 0 003 15z"
            />
          </svg>
          <div>
            <div class="text-base-content/70">Temperature</div>
            <div class="font-medium">{formatTemperature(weather.hourly?.temperature)}</div>
          </div>
        </div>

        <!-- Weather condition -->
        <div class="flex items-center">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-5 w-5 mr-2 text-blue-500"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z"
            />
          </svg>
          <div>
            <div class="text-base-content/70">Weather</div>
            <div class="font-medium">{weather.hourly?.weatherMain || 'N/A'}</div>
          </div>
        </div>

        <!-- Wind speed -->
        <div class="flex items-center">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-5 w-5 mr-2 text-blue-500"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M5 3v4M3 5h4M6 17v4m-2-2h4m5-16l2.286 6.857L21 12l-5.714 2.143L13 21l-2.286-6.857L5 12l5.714-2.143L13 3z"
            />
          </svg>
          <div>
            <div class="text-base-content/70">Wind</div>
            <div class="font-medium">{formatWindSpeed(weather.hourly?.windSpeed)}</div>
          </div>
        </div>

        <!-- Humidity -->
        <div class="flex items-center">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-5 w-5 mr-2 text-blue-500"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M3.055 11H5a2 2 0 012 2v1a2 2 0 002 2 2 2 0 012 2v2.945M8 3.935V5.5A2.5 2.5 0 0010.5 8h.5a2 2 0 012 2 2 2 0 104 0 2 2 0 012-2h1.064M15 20.488V18a2 2 0 012-2h3.064M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
            />
          </svg>
          <div>
            <div class="text-base-content/70">Humidity</div>
            <div class="font-medium">{formatPercentage(weather.hourly?.humidity)}</div>
          </div>
        </div>

        {#if !compact && weather.hourly?.pressure !== undefined}
          <!-- Pressure (non-compact mode) -->
          <div class="flex items-center">
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-5 w-5 mr-2 text-blue-500"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              aria-hidden="true"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z"
              />
            </svg>
            <div>
              <div class="text-base-content/70">Pressure</div>
              <div class="font-medium">{weather.hourly.pressure} hPa</div>
            </div>
          </div>
        {/if}

        {#if !compact && weather.hourly?.clouds !== undefined}
          <!-- Cloud cover (non-compact mode) -->
          <div class="flex items-center">
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-5 w-5 mr-2 text-blue-500"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              aria-hidden="true"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M3 15a4 4 0 004 4h9a5 5 0 10-.1-9.999 5.002 5.002 0 10-9.78 2.096A4.001 4.001 0 003 15z"
              />
            </svg>
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
