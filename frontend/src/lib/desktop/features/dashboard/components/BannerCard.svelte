<!--
  BannerCard - Composable dashboard banner with toggleable sub-elements.
  Displays custom image, title, description, location map, and weather.
  In edit mode, provides WYSIWYG inline editing for title and description,
  plus preview of enabled sub-elements. Toggle controls are in the cogwheel settings dropdown.
  @component
-->
<script lang="ts">
  import { Cloud, RefreshCw, Sunrise, Sunset } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { BannerConfig } from '$lib/stores/settings';
  import type { LatestWeatherResponse } from '$lib/types/detection.types';
  import BannerLocationMap from './BannerLocationMap.svelte';
  import WeatherSvgIcon from '$lib/desktop/components/ui/WeatherSvgIcon.svelte';
  import { birdnetSettings } from '$lib/stores/settings';
  import {
    getBasmiliusIconName,
    getMoonPhaseI18nKey,
    translateWeatherCondition,
  } from '$lib/utils/weather';
  import {
    type TemperatureUnit,
    convertTemperature,
    getTemperatureSymbol,
    convertWindSpeed,
    getWindSpeedUnit,
  } from '$lib/utils/formatters';
  import { buildAppUrl } from '$lib/utils/urlHelpers';

  interface Props {
    config: BannerConfig;
    editMode?: boolean;
    onUpdate?: (_config: BannerConfig) => void;
  }

  let { config, editMode = false, onUpdate }: Props = $props();

  let birdnet = $derived($birdnetSettings);
  let latitude = $derived(birdnet?.latitude ?? 0);
  let longitude = $derived(birdnet?.longitude ?? 0);
  let hasLocation = $derived(latitude !== 0 || longitude !== 0);

  let hasAnyContent = $derived(
    editMode ||
      config.title ||
      config.description ||
      (config.showImage && config.imagePath) ||
      (config.showLocationMap && hasLocation) ||
      config.showWeather
  );

  // Weather state
  let weatherData = $state<LatestWeatherResponse | null>(null);
  let weatherLoading = $state(false);
  let weatherError = $state(false);
  let temperatureUnit = $state<TemperatureUnit>('metric');

  async function fetchWeather(signal?: AbortSignal) {
    if (!config.showWeather || editMode) return;
    weatherLoading = true;
    weatherError = false;
    try {
      const [weatherResp, configResp] = await Promise.all([
        fetch(buildAppUrl('/api/v2/weather/latest'), { signal }),
        fetch(buildAppUrl('/api/v2/settings/dashboard'), { signal }),
      ]);
      if (!weatherResp.ok) throw new Error('Failed to fetch weather');
      weatherData = await weatherResp.json();
      if (configResp.ok) {
        const dashConfig = await configResp.json();
        temperatureUnit = dashConfig.temperatureUnit === 'fahrenheit' ? 'imperial' : 'metric';
      }
    } catch (e: unknown) {
      if (e instanceof Error && e.name === 'AbortError') return;
      weatherError = true;
    } finally {
      weatherLoading = false;
    }
  }

  // Refresh weather every 10 minutes
  const WEATHER_REFRESH_MS = 10 * 60_000;

  $effect(() => {
    if (config.showWeather && !editMode) {
      const controller = new AbortController();
      fetchWeather(controller.signal);
      const id = setInterval(() => fetchWeather(controller.signal), WEATHER_REFRESH_MS);
      return () => {
        controller.abort();
        clearInterval(id);
      };
    }
  });

  // Local time clock (updates every minute)
  let now = $state(new Date());
  $effect(() => {
    const id = setInterval(() => (now = new Date()), 60_000);
    return () => clearInterval(id);
  });

  let is24h = $derived(config.timeFormat !== '12h');

  function formatTime(date: Date): string {
    return date.toLocaleTimeString([], {
      hour: '2-digit',
      minute: '2-digit',
      hour12: !is24h,
    });
  }

  function formatSunTime(isoString: string): string | null {
    const date = new Date(isoString);
    // Guard against epoch-zero (database has no sunrise/sunset data)
    if (date.getFullYear() < 2000) return null;
    return date.toLocaleTimeString([], {
      hour: '2-digit',
      minute: '2-digit',
      hour12: !is24h,
    });
  }

  let timezone = $derived(Intl.DateTimeFormat().resolvedOptions().timeZone.replace(/_/g, ' '));

  function update(partial: Partial<BannerConfig>) {
    onUpdate?.({ ...config, ...partial });
  }

  function inputValue(e: Event): string {
    return (e.target as HTMLInputElement).value;
  }

  function textareaValue(e: Event): string {
    return (e.target as HTMLTextAreaElement).value;
  }
</script>

{#snippet sep()}
  <div class="h-4 w-px bg-[var(--color-base-content)]/15"></div>
{/snippet}

<!-- Compact weather: single line for narrow/half-width cards -->
{#snippet weatherCompact()}
  {#if weatherLoading}
    <div class="mt-3 flex items-center gap-1.5 text-sm text-[var(--color-base-content)]/50">
      <RefreshCw class="size-4 animate-spin" />
      <span>{t('detections.weather.loading')}</span>
    </div>
  {:else if weatherError || !weatherData?.hourly}
    <div class="mt-3 flex items-center gap-1.5 text-sm text-[var(--color-base-content)]/50">
      <span>{t('detections.weather.noDataAvailable')}</span>
    </div>
  {:else}
    <div
      class="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-[var(--color-base-content)]"
    >
      <div class="flex items-center gap-1.5">
        <WeatherSvgIcon
          icon={getBasmiliusIconName(
            weatherData.hourly.weather_icon ?? '',
            weatherData.hourly.weather_desc
          )}
          size={28}
          title={weatherData.hourly.weather_desc ?? ''}
        />
        <span class="font-semibold"
          >{Math.round(
            convertTemperature(weatherData.hourly.temperature ?? 0, temperatureUnit)
          )}{getTemperatureSymbol(temperatureUnit)}</span
        >
      </div>
      {#if weatherData.hourly.wind_speed !== undefined}
        {@render sep()}
        <span
          >{convertWindSpeed(weatherData.hourly.wind_speed, temperatureUnit).toFixed(1)}
          {getWindSpeedUnit(temperatureUnit)}</span
        >
      {/if}
      {#if weatherData.moon}
        {@render sep()}
        <WeatherSvgIcon
          icon={weatherData.moon.icon_name}
          size={18}
          title={t(`weather.moon.${getMoonPhaseI18nKey(weatherData.moon.phase_name)}`)}
        />
      {/if}
    </div>
  {/if}
{/snippet}

<!-- Full weather: detailed single line for wide/full-width cards -->
{#snippet weatherFull()}
  {#if weatherLoading}
    <div class="mt-3 flex items-center gap-1.5 text-sm text-[var(--color-base-content)]/50">
      <RefreshCw class="size-4 animate-spin" />
      <span>{t('detections.weather.loading')}</span>
    </div>
  {:else if weatherError || !weatherData?.hourly}
    <div class="mt-3 flex items-center gap-1.5 text-sm text-[var(--color-base-content)]/50">
      <span>{t('detections.weather.noDataAvailable')}</span>
    </div>
  {:else}
    <div
      class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-sm text-[var(--color-base-content)]"
    >
      <span class="font-semibold">{formatTime(now)}</span>
      <span class="text-xs text-[var(--color-base-content)]/40">{timezone}</span>
      {@render sep()}
      <div class="flex items-center gap-1.5">
        <WeatherSvgIcon
          icon={getBasmiliusIconName(
            weatherData.hourly.weather_icon ?? '',
            weatherData.hourly.weather_desc
          )}
          size={36}
          title={weatherData.hourly.weather_desc ?? ''}
        />
        <span>
          <span class="font-semibold"
            >{Math.round(
              convertTemperature(weatherData.hourly.temperature ?? 0, temperatureUnit)
            )}{getTemperatureSymbol(temperatureUnit)}</span
          >
          <span class="text-[var(--color-base-content)]/60"
            >{translateWeatherCondition(
              weatherData.hourly.weather_desc ?? weatherData.hourly.weather_main ?? ''
            )}</span
          >
        </span>
      </div>
      {@render sep()}
      {#if weatherData.hourly.wind_speed !== undefined}
        {@const windSpeed = weatherData.hourly.wind_speed}
        <div class="flex items-center gap-1.5">
          <WeatherSvgIcon icon="wind" size={28} title={t('detections.weather.labels.wind')} />
          <span
            >{convertWindSpeed(windSpeed, temperatureUnit).toFixed(1)}
            {getWindSpeedUnit(
              temperatureUnit
            )}{#if weatherData.hourly.wind_gust && weatherData.hourly.wind_gust > windSpeed}
              <span class="text-[var(--color-base-content)]/50"
                >({convertWindSpeed(weatherData.hourly.wind_gust, temperatureUnit).toFixed(
                  1
                )})</span
              >{/if}</span
          >
        </div>
        {@render sep()}
      {/if}
      {#if weatherData.daily?.sunrise && formatSunTime(weatherData.daily.sunrise)}
        <div class="flex items-center gap-1">
          <Sunrise class="size-4 text-amber-500" />
          <span>{formatSunTime(weatherData.daily.sunrise)}</span>
        </div>
      {/if}
      {#if weatherData.daily?.sunset && formatSunTime(weatherData.daily.sunset)}
        <div class="flex items-center gap-1">
          <Sunset class="size-4 text-orange-400" />
          <span>{formatSunTime(weatherData.daily.sunset)}</span>
        </div>
      {/if}
      {#if (weatherData.daily?.sunrise && formatSunTime(weatherData.daily.sunrise)) || (weatherData.daily?.sunset && formatSunTime(weatherData.daily.sunset))}
        {@render sep()}
      {/if}
      {#if weatherData.moon}
        <div class="flex items-center gap-1.5">
          <WeatherSvgIcon
            icon={weatherData.moon.icon_name}
            size={24}
            title={t(`weather.moon.${getMoonPhaseI18nKey(weatherData.moon.phase_name)}`)}
          />
          <span
            >{t(`weather.moon.${getMoonPhaseI18nKey(weatherData.moon.phase_name)}`)}
            <span class="text-[var(--color-base-content)]/50"
              >{Math.round(weatherData.moon.illumination)}%</span
            ></span
          >
        </div>
      {/if}
    </div>
  {/if}
{/snippet}

{#if hasAnyContent}
  <div class="flex h-full flex-col overflow-clip rounded-2xl bg-[var(--color-base-100)] shadow-xs">
    {#if editMode}
      <!-- Edit mode: horizontal layout matching normal mode (WYSIWYG) -->
      <div class="flex flex-1 p-4">
        <div class="flex flex-1 flex-col gap-4 md:flex-row md:gap-6">
          {#if config.showImage && config.imagePath}
            <div class="shrink-0">
              <img
                src={config.imagePath}
                alt={config.title || t('dashboard.editMode.stationBanner')}
                class="h-auto w-full rounded-xl object-cover md:w-48"
              />
            </div>
          {/if}

          <div class="min-w-0 flex-1 space-y-2">
            <!-- Title (inline WYSIWYG) -->
            <input
              type="text"
              value={config.title}
              placeholder={t('dashboard.banner.titlePlaceholder')}
              aria-label={t('dashboard.banner.titlePlaceholder')}
              class="w-full border-0 border-b-2 border-transparent bg-transparent text-xl font-bold text-[var(--color-base-content)] placeholder:text-[var(--color-base-content)]/30 focus:border-[var(--color-primary)]/50 focus:outline-none"
              oninput={e => update({ title: inputValue(e) })}
            />

            <!-- Description (inline WYSIWYG) -->
            <textarea
              value={config.description}
              placeholder={t('dashboard.banner.descriptionPlaceholder')}
              aria-label={t('dashboard.banner.descriptionPlaceholder')}
              class="w-full resize-y border-0 border-b-2 border-transparent bg-transparent text-sm leading-relaxed text-[var(--color-base-content)]/70 placeholder:text-[var(--color-base-content)]/30 focus:border-[var(--color-primary)]/50 focus:outline-none"
              rows="2"
              oninput={e => update({ description: textareaValue(e) })}
            ></textarea>

            {#if config.showWeather}
              <div
                class="mt-3 flex items-center gap-1.5 text-sm text-[var(--color-base-content)]/50"
              >
                <Cloud class="size-4" />
                <span>{t('dashboard.editMode.weatherPlaceholder')}</span>
              </div>
            {/if}
          </div>

          {#if config.showLocationMap && hasLocation}
            <div class="w-full shrink-0 md:w-64">
              <BannerLocationMap
                {latitude}
                {longitude}
                zoom={config.mapZoom}
                showPin={config.showPin}
                expandable={config.mapExpandable}
                className="h-40"
              />
            </div>
          {/if}
        </div>
      </div>
    {:else}
      <!-- Normal mode: container-query responsive layout -->
      <div class="banner-container flex flex-1 flex-col p-6">
        <div class="flex flex-1 flex-col gap-6 @2xl:flex-row">
          {#if config.showImage && config.imagePath}
            <div class="shrink-0">
              <img
                src={config.imagePath}
                alt={config.title || t('dashboard.editMode.stationBanner')}
                class="h-auto w-full rounded-xl object-cover @2xl:w-48"
              />
            </div>
          {/if}

          <div class="min-w-0 flex-1">
            {#if config.title}
              <h2 class="mb-2 text-xl font-bold text-[var(--color-base-content)]">
                {config.title}
              </h2>
            {/if}

            {#if config.description}
              <p class="text-sm leading-relaxed text-[var(--color-base-content)]/70">
                {config.description}
              </p>
            {/if}

            <!-- Weather inline: visible only on wide cards (≥1024px container) -->
            {#if config.showWeather}
              <div class="hidden @2xl:block">
                {@render weatherFull()}
              </div>
            {/if}
          </div>

          {#if config.showLocationMap && hasLocation}
            <div class="w-full shrink-0 @2xl:w-64">
              <BannerLocationMap
                {latitude}
                {longitude}
                zoom={config.mapZoom}
                showPin={config.showPin}
                expandable={config.mapExpandable}
                className="h-40"
              />
            </div>
          {/if}
        </div>

        <!-- Weather below: visible only on narrow/medium cards (<1024px container) -->
        {#if config.showWeather}
          <div class="@2xl:hidden">
            {@render weatherCompact()}
          </div>
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .banner-container {
    container-type: inline-size;
  }
</style>
