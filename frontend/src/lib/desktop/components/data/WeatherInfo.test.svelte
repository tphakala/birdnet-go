<script lang="ts">
  import WeatherInfo from './WeatherInfo.svelte';

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
    useCustomContent?: boolean;
  }

  let { useCustomContent = false, ...props }: Props = $props();
</script>

<WeatherInfo {...props}>
  {#snippet children(weather)}
    {#if useCustomContent}
      <div class="custom-weather-display">
        Custom: {weather.hourly?.temperature}°C
      </div>
    {/if}
  {/snippet}
</WeatherInfo>
