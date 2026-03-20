<!--
  WeatherSvgIcon - Renders animated SVG weather icons from @bybas/weather-icons.
  Uses Vite ?raw imports to inline SVGs for animation support.
  @component
-->
<script lang="ts">
  // Static imports for all weather condition icons (fill style)
  // Weather conditions - day
  import clearDay from '@bybas/weather-icons/production/fill/all/clear-day.svg?raw';
  import partlyCloudyDay from '@bybas/weather-icons/production/fill/all/partly-cloudy-day.svg?raw';
  import overcastDay from '@bybas/weather-icons/production/fill/all/overcast-day.svg?raw';
  import partlyCloudyDayRain from '@bybas/weather-icons/production/fill/all/partly-cloudy-day-rain.svg?raw';
  import thunderstormsDayRain from '@bybas/weather-icons/production/fill/all/thunderstorms-day-rain.svg?raw';
  import fogDay from '@bybas/weather-icons/production/fill/all/fog-day.svg?raw';

  // Weather conditions - night
  import clearNight from '@bybas/weather-icons/production/fill/all/clear-night.svg?raw';
  import partlyCloudyNight from '@bybas/weather-icons/production/fill/all/partly-cloudy-night.svg?raw';
  import overcastNight from '@bybas/weather-icons/production/fill/all/overcast-night.svg?raw';
  import partlyCloudyNightRain from '@bybas/weather-icons/production/fill/all/partly-cloudy-night-rain.svg?raw';
  import thunderstormsNightRain from '@bybas/weather-icons/production/fill/all/thunderstorms-night-rain.svg?raw';
  import fogNight from '@bybas/weather-icons/production/fill/all/fog-night.svg?raw';

  // Weather conditions - day/night agnostic (same icon regardless)
  import rainIcon from '@bybas/weather-icons/production/fill/all/rain.svg?raw';
  import sleetIcon from '@bybas/weather-icons/production/fill/all/sleet.svg?raw';
  import snowIcon from '@bybas/weather-icons/production/fill/all/snow.svg?raw';

  // Moon phases
  import moonNew from '@bybas/weather-icons/production/fill/all/moon-new.svg?raw';
  import moonWaxingCrescent from '@bybas/weather-icons/production/fill/all/moon-waxing-crescent.svg?raw';
  import moonFirstQuarter from '@bybas/weather-icons/production/fill/all/moon-first-quarter.svg?raw';
  import moonWaxingGibbous from '@bybas/weather-icons/production/fill/all/moon-waxing-gibbous.svg?raw';
  import moonFull from '@bybas/weather-icons/production/fill/all/moon-full.svg?raw';
  import moonWaningGibbous from '@bybas/weather-icons/production/fill/all/moon-waning-gibbous.svg?raw';
  import moonLastQuarter from '@bybas/weather-icons/production/fill/all/moon-last-quarter.svg?raw';
  import moonWaningCrescent from '@bybas/weather-icons/production/fill/all/moon-waning-crescent.svg?raw';

  // Wind
  import wind from '@bybas/weather-icons/production/fill/all/wind.svg?raw';

  // Not available fallback
  import notAvailable from '@bybas/weather-icons/production/fill/all/not-available.svg?raw';

  const iconMap: Record<string, string> = {
    // Weather - day
    'clear-day': clearDay,
    'partly-cloudy-day': partlyCloudyDay,
    'overcast-day': overcastDay,
    'partly-cloudy-day-rain': partlyCloudyDayRain,
    rain: rainIcon,
    'thunderstorms-day-rain': thunderstormsDayRain,
    sleet: sleetIcon,
    snow: snowIcon,
    'fog-day': fogDay,
    // Weather - night
    'clear-night': clearNight,
    'partly-cloudy-night': partlyCloudyNight,
    'overcast-night': overcastNight,
    'partly-cloudy-night-rain': partlyCloudyNightRain,
    'thunderstorms-night-rain': thunderstormsNightRain,
    'fog-night': fogNight,
    // Moon phases
    'moon-new': moonNew,
    'moon-waxing-crescent': moonWaxingCrescent,
    'moon-first-quarter': moonFirstQuarter,
    'moon-waxing-gibbous': moonWaxingGibbous,
    'moon-full': moonFull,
    'moon-waning-gibbous': moonWaningGibbous,
    'moon-last-quarter': moonLastQuarter,
    'moon-waning-crescent': moonWaningCrescent,
    // Wind
    wind: wind,
    // Fallback
    'not-available': notAvailable,
  };

  interface Props {
    icon: string;
    size?: number;
    className?: string;
    title?: string;
  }

  let { icon, size = 32, className = '', title = '' }: Props = $props();

  // eslint-disable-next-line security/detect-object-injection -- icon is a string prop used as key in a static icon map
  let svgContent = $derived(iconMap[icon] ?? iconMap['not-available']);
</script>

<div
  class="inline-flex items-center justify-center {className}"
  style:width="{size}px"
  style:height="{size}px"
  role="img"
  aria-label={title || icon}
>
  {@html svgContent}
</div>

<style>
  div :global(svg) {
    width: 100%;
    height: 100%;
  }
</style>
