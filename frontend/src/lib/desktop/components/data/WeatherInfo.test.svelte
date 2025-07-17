<script lang="ts">
  import WeatherInfo from './WeatherInfo.svelte';

  interface Props {
    detectionId?: string;
    weatherData?: any;
    compact?: boolean;
    showTitle?: boolean;
    autoFetch?: boolean;
    className?: string;
    titleClassName?: string;
    gridClassName?: string;
    onError?: (error: Error) => void;
    onLoad?: (data: any) => void;
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
