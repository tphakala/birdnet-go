<!--
  BannerCard - Composable dashboard banner with toggleable sub-elements.
  Displays custom image, title, description, location map, and weather.
  In edit mode, provides WYSIWYG inline editing for title and description,
  plus preview of enabled sub-elements. Toggle controls are in the cogwheel settings dropdown.
  @component
-->
<script lang="ts">
  import { Cloud, RefreshCw } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { BannerConfig } from '$lib/stores/settings';
  import type { LatestWeatherResponse } from '$lib/types/detection.types';
  import BannerLocationMap from './BannerLocationMap.svelte';
  import WeatherSvgIcon from '$lib/desktop/components/ui/WeatherSvgIcon.svelte';
  import { birdnetSettings } from '$lib/stores/settings';
  import {
    getBasmiliusIconName,
    getBirdingConditionLevel,
    getBirdingConditionColor,
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
        fetch('/api/v2/weather/latest', { signal }),
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

  $effect(() => {
    if (config.showWeather && !editMode) {
      const controller = new AbortController();
      fetchWeather(controller.signal);
      return () => controller.abort();
    }
  });

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

{#if hasAnyContent}
  <div
    class="flex h-full flex-col overflow-hidden rounded-2xl bg-[var(--color-base-100)] shadow-xs"
  >
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
      <!-- Normal mode: horizontal layout -->
      <div class="flex flex-1 p-6">
        <div class="flex flex-1 flex-col gap-6 md:flex-row">
          {#if config.showImage && config.imagePath}
            <div class="shrink-0">
              <img
                src={config.imagePath}
                alt={config.title || t('dashboard.editMode.stationBanner')}
                class="h-auto w-full rounded-xl object-cover md:w-48"
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

            {#if config.showWeather}
              {#if weatherLoading}
                <div
                  class="mt-3 flex items-center gap-1.5 text-sm text-[var(--color-base-content)]/50"
                >
                  <RefreshCw class="size-4 animate-spin" />
                  <span>{t('detections.weather.loading')}</span>
                </div>
              {:else if weatherError || !weatherData?.hourly}
                <div
                  class="mt-3 flex items-center gap-1.5 text-sm text-[var(--color-base-content)]/50"
                >
                  <span>{t('detections.weather.noDataAvailable')}</span>
                </div>
              {:else}
                <div class="mt-4 flex flex-wrap items-start gap-6">
                  <!-- Group 1: Weather Condition -->
                  <div class="flex items-center gap-3">
                    <WeatherSvgIcon
                      icon={getBasmiliusIconName(
                        weatherData.hourly.weather_icon ?? '',
                        weatherData.hourly.weather_desc
                      )}
                      size={40}
                      title={weatherData.hourly.weather_desc ?? ''}
                    />
                    <div>
                      <div class="text-lg font-semibold text-[var(--color-base-content)]">
                        {Math.round(
                          convertTemperature(weatherData.hourly.temperature ?? 0, temperatureUnit)
                        )}{getTemperatureSymbol(temperatureUnit)}
                        <span class="text-sm font-normal text-[var(--color-base-content)]/60">
                          / {t('detections.weather.labels.temperature')}
                          {Math.round(
                            convertTemperature(weatherData.hourly.feels_like ?? 0, temperatureUnit)
                          )}{getTemperatureSymbol(temperatureUnit)}
                        </span>
                      </div>
                      <div class="text-sm text-[var(--color-base-content)]/60">
                        {translateWeatherCondition(
                          weatherData.hourly.weather_desc ?? weatherData.hourly.weather_main ?? ''
                        )}
                      </div>
                    </div>
                  </div>

                  <!-- Group 2: Wind & Birding Conditions -->
                  {#if weatherData.hourly.wind_speed !== undefined}
                    {@const windSpeed = weatherData.hourly.wind_speed}
                    {@const birdingLevel = getBirdingConditionLevel(windSpeed)}
                    <div class="flex items-center gap-3">
                      <WeatherSvgIcon
                        icon="wind"
                        size={32}
                        title={t('detections.weather.labels.wind')}
                      />
                      <div>
                        <div class="text-sm font-medium text-[var(--color-base-content)]">
                          {convertWindSpeed(windSpeed, temperatureUnit).toFixed(1)}
                          {getWindSpeedUnit(temperatureUnit)}
                          {#if weatherData.hourly.wind_gust && weatherData.hourly.wind_gust > windSpeed}
                            <span class="text-[var(--color-base-content)]/50">
                              ({convertWindSpeed(
                                weatherData.hourly.wind_gust,
                                temperatureUnit
                              ).toFixed(1)})
                            </span>
                          {/if}
                        </div>
                        <div class="text-xs {getBirdingConditionColor(birdingLevel)}">
                          {t(`weather.birding.${birdingLevel}`)}
                        </div>
                      </div>
                    </div>
                  {/if}

                  <!-- Group 3: Moon Phase -->
                  {#if weatherData.moon}
                    <div class="flex items-center gap-3">
                      <WeatherSvgIcon
                        icon={weatherData.moon.iconName}
                        size={32}
                        title={t(`weather.moon.${getMoonPhaseI18nKey(weatherData.moon.phaseName)}`)}
                      />
                      <div>
                        <div class="text-sm font-medium text-[var(--color-base-content)]">
                          {t(`weather.moon.${getMoonPhaseI18nKey(weatherData.moon.phaseName)}`)}
                        </div>
                        <div class="text-xs text-[var(--color-base-content)]/50">
                          {Math.round(weatherData.moon.illumination)}%
                        </div>
                      </div>
                    </div>
                  {/if}

                  <!-- Refresh button -->
                  <button
                    class="ml-auto self-center rounded-lg p-1.5 text-[var(--color-base-content)]/40 transition-colors hover:bg-[var(--color-base-200)] hover:text-[var(--color-base-content)]/70"
                    onclick={() => fetchWeather()}
                    aria-label={t('common.refresh')}
                  >
                    <RefreshCw class="size-4" />
                  </button>
                </div>
              {/if}
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
    {/if}
  </div>
{/if}
