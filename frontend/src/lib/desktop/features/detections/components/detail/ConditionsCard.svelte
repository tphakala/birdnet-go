<!--
  ConditionsCard - when in the day the detection happened and what the weather was.

  The sky strip is a 24 hour axis in station time, shaded for night, civil
  twilight and daylight, with the sun's path drawn as an arc and a marker at the
  detection time. The species' usual activity by hour sits along the ground of
  the same strip, so a detection can be read against both daylight and the
  species' habits. The weather at the time and the moon phase follow.

  The card has the same parts for every detection: two legend lines, the weather
  line, six weather tiles (a value that was not recorded shows a dash) and a
  note. Only the sky strip changes size: it takes whatever height the row has
  left, so the card fills its column without an empty tail.
-->
<script lang="ts">
  import {
    Cloud,
    CloudRain,
    Droplets,
    Gauge,
    Navigation,
    Sunrise,
    Sunset,
    Thermometer,
    Wind,
  } from '@lucide/svelte';
  import WeatherSvgIcon from '$lib/desktop/components/ui/WeatherSvgIcon.svelte';
  import { t } from '$lib/i18n';
  import {
    convertTemperature,
    convertWindSpeed,
    getTemperatureSymbol,
    getWindSpeedUnit,
    type TemperatureUnit,
  } from '$lib/utils/formatters';
  import {
    getBasmiliusIconName,
    getMoonPhaseI18nKey,
    translateWeatherCondition,
  } from '$lib/utils/weather';
  import DetailCard from './DetailCard.svelte';
  import {
    MINUTES_PER_DAY,
    compassPoint,
    dayPercent,
    wallClockMinutes,
    wallClockTime,
  } from './detailTime';
  import type {
    DetectionDetailData,
    DetectionWeather,
    HourlyBucket,
    SunTimes,
  } from './detailTypes';

  interface Props {
    detection: DetectionDetailData;
    weather?: DetectionWeather | null;
    sun?: SunTimes | null;
    /** The species' detections per hour of day, for the activity bars. */
    hourlyActivity?: HourlyBucket[] | null;
    speciesName: string;
  }

  let {
    detection,
    weather = null,
    sun = null,
    hourlyActivity = null,
    speciesName,
  }: Props = $props();

  const HOURS_PER_DAY = 24;
  const PERCENT = 100;
  const HALF_TURN_DEGREES = 180;
  const AXIS_HOURS = [0, 6, 12, 18, 24];
  const MINUTES_PER_HOUR = 60;
  const MISSING = '-';
  // Share of the strip height where the horizon line sits, measured from the top.
  const HORIZON_PERCENT = 62;
  // How far the sun marker travels above and below the horizon, in percent of strip height.
  const DAY_ARC_HEIGHT_PERCENT = 46;
  const NIGHT_ARC_DEPTH_PERCENT = 24;
  // Past this point of the day axis the time label sits left of the marker so it stays inside.
  const LABEL_FLIP_PERCENT = 80;
  // Smallest bar height, in percent, for an hour that has any detections.
  const MIN_BAR_PERCENT = 8;
  // Height of the busiest hour's bar, in percent of the ground band.
  const ACTIVITY_MAX_PERCENT = 82;

  let units = $derived<TemperatureUnit>(detection.weather?.units ?? 'metric');
  let hourly = $derived(weather?.hourly);

  // Sun events as minutes of the station's day. The sun endpoint is preferred;
  // the weather provider's sunrise and sunset are the fallback.
  let sunriseTime = $derived(sun?.sunrise ?? weather?.daily?.sunrise);
  let sunsetTime = $derived(sun?.sunset ?? weather?.daily?.sunset);
  let sunrise = $derived(wallClockMinutes(sunriseTime));
  let sunset = $derived(wallClockMinutes(sunsetTime));
  let dawn = $derived(wallClockMinutes(sun?.civil_dawn) ?? sunrise);
  let dusk = $derived(wallClockMinutes(sun?.civil_dusk) ?? sunset);
  let moment = $derived(wallClockMinutes(detection.timestamp ?? `T${detection.time}`));
  let hasSun = $derived(sunrise !== null && sunset !== null && sunrise < sunset);

  // Background gradient: night, twilight ramp, day, twilight ramp, night.
  let skyGradient = $derived.by(() => {
    if (!hasSun || sunrise === null || sunset === null || dawn === null || dusk === null) {
      return 'linear-gradient(to right, var(--sky-night), var(--sky-night))';
    }
    const stops = [
      `var(--sky-night) 0%`,
      `var(--sky-night) ${dayPercent(dawn)}%`,
      `var(--sky-twilight) ${dayPercent((dawn + sunrise) / 2)}%`,
      `var(--sky-day) ${dayPercent(sunrise)}%`,
      `var(--sky-day) ${dayPercent(sunset)}%`,
      `var(--sky-twilight) ${dayPercent((sunset + dusk) / 2)}%`,
      `var(--sky-night) ${dayPercent(dusk)}%`,
      `var(--sky-night) 100%`,
    ];
    return `linear-gradient(to right, ${stops.join(', ')})`;
  });

  // Height of the sun marker relative to the horizon. The day arc is half an
  // ellipse from sunrise to sunset; the night arc is its shallower mirror from
  // sunset round midnight to the next sunrise.
  let sunIsUp = $derived(
    hasSun && moment !== null && sunrise !== null && sunset !== null
      ? moment >= sunrise && moment <= sunset
      : false
  );

  let markerTop = $derived.by(() => {
    if (!hasSun || moment === null || sunrise === null || sunset === null) {
      return HORIZON_PERCENT;
    }
    if (sunIsUp) {
      const half = (sunset - sunrise) / 2;
      const offset = (moment - (sunrise + half)) / half;
      return HORIZON_PERCENT - DAY_ARC_HEIGHT_PERCENT * Math.sqrt(Math.max(0, 1 - offset * offset));
    }
    const nightLength = MINUTES_PER_DAY - (sunset - sunrise);
    const half = nightLength / 2;
    const sinceSunset = moment >= sunset ? moment - sunset : moment + MINUTES_PER_DAY - sunset;
    const offset = (sinceSunset - half) / half;
    return HORIZON_PERCENT + NIGHT_ARC_DEPTH_PERCENT * Math.sqrt(Math.max(0, 1 - offset * offset));
  });

  // Activity bars, scaled to the busiest hour.
  let activity = $derived.by(() => {
    if (!hourlyActivity || hourlyActivity.length === 0) return null;
    const counts = Array.from({ length: HOURS_PER_DAY }, () => 0);
    for (const bucket of hourlyActivity) {
      if (bucket.hour >= 0 && bucket.hour < HOURS_PER_DAY) {
        counts[bucket.hour] = bucket.count;
      }
    }
    const peak = Math.max(...counts);
    if (peak === 0) return null;
    return counts.map((count, hour) => ({ hour, count, share: count / peak }));
  });

  let detectionHour = $derived(moment === null ? -1 : Math.floor(moment / MINUTES_PER_HOUR));

  let temperature = $derived(hourly?.temperature ?? detection.weather?.temperature);
  let windSpeed = $derived(hourly?.wind_speed ?? detection.weather?.windSpeed);
  let windGust = $derived(hourly?.wind_gust ?? detection.weather?.windGust);
  let humidity = $derived(hourly?.humidity ?? detection.weather?.humidity);
  let description = $derived(hourly?.weather_desc ?? detection.weather?.description);
  let iconCode = $derived(hourly?.weather_icon ?? detection.weather?.weatherIcon ?? '');
  let hasWeather = $derived(temperature !== undefined || Boolean(description));

  let conditionIcon = $derived(
    hasWeather
      ? getBasmiliusIconName(iconCode, description, sunIsUp ? 'day' : 'night')
      : 'not-available'
  );

  function degrees(value: number): string {
    return `${Math.round(convertTemperature(value, units))}${getTemperatureSymbol(units)}`;
  }

  function speed(value: number): string {
    return convertWindSpeed(value, units).toFixed(1);
  }

  // Wind as "2.5 (5.3) m/s": speed, then the gust in brackets, as the rest of the app writes it.
  let windValue = $derived.by(() => {
    if (windSpeed === undefined) return MISSING;
    const gust = windGust !== undefined ? ` (${speed(windGust)})` : '';
    return `${speed(windSpeed)}${gust} ${getWindSpeedUnit(units)}`;
  });

  let windLabel = $derived(
    hourly?.wind_deg !== undefined
      ? t('detections.detail.conditions.windFrom', { direction: compassPoint(hourly.wind_deg) })
      : t('detections.weather.labels.wind')
  );

  let moonName = $derived(
    detection.weather?.moonPhaseName
      ? t(`weather.moon.${getMoonPhaseI18nKey(detection.weather.moonPhaseName)}`)
      : ''
  );
  let moonIcon = $derived(
    detection.weather?.moonPhaseName
      ? `moon-${detection.weather.moonPhaseName
          .toLowerCase()
          .replace(/\s+moon$/, '')
          .replace(/\s+/g, '-')}`
      : ''
  );
</script>

{#snippet tile(label: string, value: string, suffix: string)}
  <div class="dd-tile-text">
    <dt class="dd-tile-label">{label}</dt>
    <dd class="dd-tile-value">
      {value}{#if suffix}<span class="dd-tile-suffix">{suffix}</span>{/if}
    </dd>
  </div>
{/snippet}

<DetailCard title={t('detections.detail.conditions.title')} fill={true}>
  <div class="dd-conditions">
    <!-- Sky strip: the one part that grows with the card. -->
    <div class="dd-sky-block">
      <div
        class="dd-sky"
        style:background={skyGradient}
        role="group"
        aria-label={t('detections.detail.conditions.skyLabel', {
          time: detection.time,
          sunrise: wallClockTime(sunriseTime) || '?',
          sunset: wallClockTime(sunsetTime) || '?',
        })}
      >
        {#if hasSun && sunrise !== null && sunset !== null}
          <!-- Sun path above the horizon between sunrise and sunset -->
          <div
            class="dd-sky-arc"
            style:left="{dayPercent(sunrise)}%"
            style:width="{dayPercent(sunset) - dayPercent(sunrise)}%"
            style:top="{HORIZON_PERCENT - DAY_ARC_HEIGHT_PERCENT}%"
            style:height="{DAY_ARC_HEIGHT_PERCENT * 2}%"
          ></div>
        {/if}
        <div class="dd-sky-horizon" style:top="{HORIZON_PERCENT}%"></div>
        {#if activity}
          <div
            class="dd-activity"
            style:height="{PERCENT - HORIZON_PERCENT}%"
            role="img"
            aria-label={t('detections.detail.conditions.activityLabel', { species: speciesName })}
          >
            {#each activity as bar (bar.hour)}
              <span
                class="dd-activity-bar"
                class:dd-activity-bar-now={bar.hour === detectionHour}
                style:height="{Math.max(
                  bar.share * ACTIVITY_MAX_PERCENT,
                  bar.count > 0 ? MIN_BAR_PERCENT : 0
                )}%"
                title="{String(bar.hour).padStart(2, '0')}:00  {bar.count}"
              ></span>
            {/each}
          </div>
        {/if}
        {#if moment !== null}
          <div class="dd-sky-now" style:left="{dayPercent(moment)}%">
            <span class="dd-sky-now-line"></span>
            <span class="dd-sky-now-dot" class:dd-sky-now-dot-day={sunIsUp} style:top="{markerTop}%"
            ></span>
            <span
              class="dd-sky-now-label"
              class:dd-sky-now-label-left={dayPercent(moment) > LABEL_FLIP_PERCENT}
            >
              {detection.time.slice(0, 5)}
            </span>
          </div>
        {/if}
      </div>

      <div class="dd-sky-axis" aria-hidden="true">
        {#each AXIS_HOURS as hour (hour)}
          <span style:left="{(hour / HOURS_PER_DAY) * PERCENT}%"
            >{String(hour).padStart(2, '0')}</span
          >
        {/each}
      </div>

      <!-- Two lines, both always present. -->
      <p class="dd-sky-legend">
        <span class="dd-sky-legend-item">
          <Sunrise class="size-3.5" />
          {t('detections.detail.conditions.sunrise')}
          <strong>{wallClockTime(sunriseTime) || MISSING}</strong>
        </span>
        <span class="dd-sky-legend-item">
          <Sunset class="size-3.5" />
          {t('detections.detail.conditions.sunset')}
          <strong>{wallClockTime(sunsetTime) || MISSING}</strong>
        </span>
      </p>
      <p class="dd-sky-legend dd-sky-legend-activity">
        <span class="dd-sky-legend-swatch" aria-hidden="true"></span>
        <span class="dd-sky-legend-text">
          {t('detections.detail.conditions.activityLegend', { species: speciesName })}
        </span>
      </p>
    </div>

    <!-- Weather at the time -->
    <div class="dd-weather-now">
      <WeatherSvgIcon icon={conditionIcon} size={48} title={description ?? ''} />
      <div class="dd-weather-now-text">
        <span class="dd-weather-temp"
          >{temperature !== undefined ? degrees(temperature) : MISSING}</span
        >
        <span class="dd-weather-desc">
          {description
            ? translateWeatherCondition(description)
            : t('detections.weather.noDataAvailable')}
        </span>
      </div>
      {#if moonName}
        <div class="dd-weather-moon">
          <WeatherSvgIcon icon={moonIcon} size={36} title={moonName} />
          <div class="dd-weather-moon-text">
            <span>{moonName}</span>
            {#if detection.weather?.moonIllumination !== undefined}
              <span class="dd-weather-moon-lit"
                >{t('detections.detail.conditions.moonLit', {
                  percent: Math.round(detection.weather.moonIllumination),
                })}</span
              >
            {/if}
          </div>
        </div>
      {/if}
    </div>

    <!-- Six tiles, always: a value that was not recorded shows a dash. -->
    <dl class="dd-tiles">
      <div class="dd-tile">
        {#if hourly?.wind_deg !== undefined}
          <span
            class="dd-tile-icon"
            style:transform="rotate({hourly.wind_deg + HALF_TURN_DEGREES}deg)"
          >
            <Navigation class="size-3.5" />
          </span>
        {:else}
          <span class="dd-tile-icon"><Wind class="size-3.5" /></span>
        {/if}
        {@render tile(windLabel, windValue, '')}
      </div>
      <div class="dd-tile">
        <span class="dd-tile-icon"><CloudRain class="size-3.5" /></span>
        {@render tile(
          t('detections.detail.conditions.precipitation'),
          hourly?.precipitation !== undefined ? `${hourly.precipitation.toFixed(1)} mm` : MISSING,
          hourly?.precipitation && hourly.precipitation_type ? hourly.precipitation_type : ''
        )}
      </div>
      <div class="dd-tile">
        <span class="dd-tile-icon"><Cloud class="size-3.5" /></span>
        {@render tile(
          t('detections.weather.labels.cloudCover'),
          hourly?.clouds !== undefined ? `${hourly.clouds}%` : MISSING,
          ''
        )}
      </div>
      <div class="dd-tile">
        <span class="dd-tile-icon"><Droplets class="size-3.5" /></span>
        {@render tile(
          t('detections.weather.labels.humidity'),
          humidity !== undefined ? `${humidity}%` : MISSING,
          ''
        )}
      </div>
      <div class="dd-tile">
        <span class="dd-tile-icon"><Gauge class="size-3.5" /></span>
        {@render tile(
          t('detections.weather.labels.pressure'),
          hourly?.pressure ? `${hourly.pressure} hPa` : MISSING,
          ''
        )}
      </div>
      <div class="dd-tile">
        <span class="dd-tile-icon"><Thermometer class="size-3.5" /></span>
        {@render tile(
          t('detections.detail.conditions.feelsLike'),
          hourly?.feels_like !== undefined ? degrees(hourly.feels_like) : MISSING,
          ''
        )}
      </div>
    </dl>

    <p class="dd-weather-note">
      {hourly?.time
        ? t('detections.detail.conditions.observedAt', { time: hourly.time.slice(0, 5) })
        : t('detections.detail.conditions.notObserved')}
    </p>
  </div>
</DetailCard>

<style>
  .dd-conditions {
    display: flex;
    flex: 1;
    flex-direction: column;
    min-height: 0;
    container: dd-conditions / inline-size;
  }

  .dd-sky-block {
    --sky-night: #131a3a;
    --sky-twilight: #c56a55;
    --sky-day: #6fb1e8;

    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-height: 0;
  }

  /* Takes the height the card has left over, never less than the strip needs
     to be read. */
  .dd-sky {
    position: relative;
    flex: 1 1 auto;
    min-height: 5.5rem;
    overflow: hidden;
    border-radius: 0.625rem;
  }

  .dd-sky-arc {
    position: absolute;
    border: 1.5px dashed rgb(255 255 255 / 0.55);
    border-bottom-color: transparent;
    border-radius: 50%;
    clip-path: inset(0 0 50% 0);
  }

  .dd-sky-horizon {
    position: absolute;
    right: 0;
    left: 0;
    height: 38%;
    border-top: 1px solid rgb(255 255 255 / 0.35);
    background: linear-gradient(to bottom, rgb(0 0 0 / 0.28), rgb(0 0 0 / 0.4));
  }

  .dd-sky-now {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 0;
  }

  .dd-sky-now-line {
    position: absolute;
    top: 0;
    bottom: 0;
    left: -0.5px;
    width: 1px;
    background-color: rgb(255 255 255 / 0.85);
  }

  .dd-sky-now-dot {
    position: absolute;
    left: 0;
    width: 0.875rem;
    height: 0.875rem;
    border: 2px solid #fff;
    border-radius: 50%;
    background-color: #c9d2f0;
    box-shadow: 0 0 0 4px rgb(255 255 255 / 0.18);
    transform: translate(-50%, -50%);
  }

  .dd-sky-now-dot-day {
    background-color: #ffd54a;
    box-shadow: 0 0 0 5px rgb(255 213 74 / 0.3);
  }

  .dd-sky-now-label {
    position: absolute;
    top: 0.25rem;
    left: 0.375rem;
    padding: 0 0.3125rem;
    font-size: 0.6875rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    line-height: 1.125rem;
    color: #10142b;
    white-space: nowrap;
    border-radius: 0.25rem;
    background-color: rgb(255 255 255 / 0.92);
  }

  .dd-sky-now-label-left {
    right: 0.375rem;
    left: auto;
  }

  .dd-activity {
    position: absolute;
    right: 0;
    bottom: 0;
    left: 0;
    display: grid;
    grid-template-columns: repeat(24, 1fr);
    align-items: end;
    gap: 2px;
    padding: 0 1px;
  }

  .dd-activity-bar {
    min-height: 0;
    border-radius: 2px 2px 0 0;
    background-color: rgb(255 255 255 / 0.32);
  }

  .dd-activity-bar-now {
    background-color: rgb(255 255 255 / 0.92);
  }

  .dd-sky-axis {
    position: relative;
    flex: 0 0 auto;
    height: 1rem;
    margin-top: 0.125rem;
    font-size: 0.625rem;
    font-variant-numeric: tabular-nums;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-sky-axis span {
    position: absolute;
    top: 0.125rem;
    transform: translateX(-50%);
  }

  .dd-sky-axis span:first-child {
    transform: none;
  }

  .dd-sky-axis span:last-child {
    transform: translateX(-100%);
  }

  .dd-sky-legend {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 1rem;
    height: 1.375rem;
    min-width: 0;
    margin-top: 0.125rem;
    font-size: 0.75rem;
    white-space: nowrap;
    color: color-mix(in srgb, var(--color-base-content) 72%, transparent);
  }

  .dd-sky-legend-activity {
    gap: 0.375rem;
    margin-top: 0;
  }

  .dd-sky-legend-item {
    display: inline-flex;
    align-items: center;
    gap: 0.3125rem;
  }

  .dd-sky-legend-item strong {
    font-variant-numeric: tabular-nums;
    font-weight: 600;
    color: var(--color-base-content);
  }

  .dd-sky-legend-swatch {
    flex: 0 0 auto;
    width: 0.625rem;
    height: 0.625rem;
    border-radius: 2px;
    background-color: color-mix(in srgb, var(--color-base-content) 45%, transparent);
  }

  .dd-sky-legend-text {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .dd-weather-now {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 0.75rem;
    height: 3.5rem;
    margin-top: 0.625rem;
    padding-top: 0.625rem;
    box-sizing: content-box;
    border-top: 1px solid var(--border-100);
  }

  .dd-weather-now-text {
    display: flex;
    flex: 1 1 0;
    flex-direction: column;
    min-width: 0;
  }

  .dd-weather-temp {
    font-size: 1.75rem;
    font-weight: 700;
    line-height: 1.15;
    letter-spacing: -0.02em;
    font-variant-numeric: tabular-nums;
  }

  .dd-weather-desc {
    overflow: hidden;
    font-size: 0.8125rem;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .dd-weather-moon {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 0.375rem;
    font-size: 0.8125rem;
  }

  .dd-weather-moon-text {
    display: flex;
    flex-direction: column;
    white-space: nowrap;
  }

  .dd-weather-moon-lit {
    font-size: 0.6875rem;
    color: color-mix(in srgb, var(--color-base-content) 72%, transparent);
  }

  .dd-tiles {
    display: grid;
    flex: 0 0 auto;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 0.375rem;
    margin-top: 0.625rem;
  }

  /* In a narrow card the six tiles sit two to a row, so their values fit. */
  @container dd-conditions (max-width: 23rem) {
    .dd-tiles {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  .dd-tile {
    display: flex;
    align-items: flex-start;
    gap: 0.375rem;
    min-width: 0;
    height: 2.875rem;
    padding: 0.375rem 0.5rem;
    border-radius: 0.5rem;
    background-color: var(--color-base-200);
  }

  .dd-tile-icon {
    display: inline-flex;
    flex: 0 0 auto;
    margin-top: 0.0625rem;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-tile-text {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .dd-tile-label {
    overflow: hidden;
    font-size: 0.6875rem;
    line-height: 1rem;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: color-mix(in srgb, var(--color-base-content) 72%, transparent);
  }

  .dd-tile-value {
    overflow: hidden;
    font-size: 0.875rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    line-height: 1.125rem;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .dd-tile-suffix {
    margin-left: 0.25rem;
    font-size: 0.6875rem;
    font-weight: 400;
    color: color-mix(in srgb, var(--color-base-content) 72%, transparent);
  }

  .dd-weather-note {
    flex: 0 0 auto;
    height: 1rem;
    margin-top: 0.5rem;
    overflow: hidden;
    font-size: 0.6875rem;
    line-height: 1rem;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }
</style>
