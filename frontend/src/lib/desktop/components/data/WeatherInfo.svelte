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
  import { t } from '$lib/i18n';
  import type { Snippet } from 'svelte';
  import { Thermometer, Sun, Wind, Droplets, Gauge, Cloud, XCircle } from '@lucide/svelte';
  import { formatTemperature, formatWindSpeed, type TemperatureUnit } from '$lib/utils/formatters';

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
    units?: TemperatureUnit;
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
    units = 'metric',
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

  // State for fetched weather data (when using detectionId)
  let fetchedWeather = $state<WeatherData | null>(null);
  let loading = $state(false);
  let error = $state<string | null>(null);

  // Display value: prefer prop over fetched data (no $effect needed)
  const weather = $derived(weatherData ?? fetchedWeather);

  // Fetch weather data
  async function fetchWeatherInfo(id: string) {
    if (!id) return;

    loading = true;
    error = null;

    try {
      const data = await fetchWithCSRF<WeatherData>(`/api/v2/weather/detection/${id}`);
      fetchedWeather = data;
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

  // Helper function to format percentage
  function formatPercentage(value: number | undefined): string {
    if (value === undefined) return 'N/A';
    return `${value}%`;
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
    fetchedWeather = data;
    error = null;
    loading = false;
  }
</script>

<div class={cn('weather-info', className)}>
  {#if showTitle}
    <h3 class={cn('text-lg font-semibold text-base-content mb-3', titleClassName)}>
      {t('detections.weather.title')}
    </h3>
  {/if}

  {#if loading}
    <!-- Loading state -->
    <div class="py-4 flex justify-center" role="status" aria-live="polite">
      <span class="loading loading-spinner loading-md text-primary"></span>
      <span class="sr-only">{t('detections.weather.loading')}</span>
    </div>
  {:else if error}
    <!-- Error state -->
    <div class="py-4" role="alert">
      <div class="text-error flex items-center">
        <XCircle class="size-5 mr-2" />
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
          <Thermometer class="size-5 mr-2" />
          <div>
            <div class="text-base-content/70">{t('detections.weather.labels.temperature')}</div>
            <div class="font-medium">{formatTemperature(weather.hourly?.temperature, units)}</div>
          </div>
        </div>

        <!-- Weather condition -->
        <div class="flex items-center">
          <Sun class="size-5 mr-2" />
          <div>
            <div class="text-base-content/70">{t('detections.weather.labels.weather')}</div>
            <div class="font-medium">{weather.hourly?.weatherMain || 'N/A'}</div>
          </div>
        </div>

        <!-- Wind speed -->
        <div class="flex items-center">
          <Wind class="size-5 mr-2" />
          <div>
            <div class="text-base-content/70">{t('detections.weather.labels.wind')}</div>
            <div class="font-medium">{formatWindSpeed(weather.hourly?.windSpeed, units)}</div>
          </div>
        </div>

        <!-- Humidity -->
        <div class="flex items-center">
          <Droplets class="size-5 mr-2" />
          <div>
            <div class="text-base-content/70">{t('detections.weather.labels.humidity')}</div>
            <div class="font-medium">{formatPercentage(weather.hourly?.humidity)}</div>
          </div>
        </div>

        {#if !compact && weather.hourly?.pressure !== undefined}
          <!-- Pressure (non-compact mode) -->
          <div class="flex items-center">
            <Gauge class="size-5 mr-2" />
            <div>
              <div class="text-base-content/70">{t('detections.weather.labels.pressure')}</div>
              <div class="font-medium">
                {weather.hourly.pressure}
                {t('detections.weather.units.pressure')}
              </div>
            </div>
          </div>
        {/if}

        {#if !compact && weather.hourly?.clouds !== undefined}
          <!-- Cloud cover (non-compact mode) -->
          <div class="flex items-center">
            <Cloud class="size-5 mr-2" />
            <div>
              <div class="text-base-content/70">{t('detections.weather.labels.cloudCover')}</div>
              <div class="font-medium">{formatPercentage(weather.hourly.clouds)}</div>
            </div>
          </div>
        {/if}
      </div>
    {/if}
  {:else}
    <!-- No data state -->
    <div class="py-4 text-center text-base-content/60">
      {t('detections.weather.noDataAvailable')}
    </div>
  {/if}
</div>
