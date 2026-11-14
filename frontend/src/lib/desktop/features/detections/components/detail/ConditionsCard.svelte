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
    Navigation2,
    Sunrise,
    Sunset,
    Thermometer,
    Wind,
  } from '@lucide/svelte';
  import Skeleton from '$lib/desktop/components/ui/Skeleton.svelte';
  import SkeletonStatus from '$lib/desktop/components/ui/SkeletonStatus.svelte';
  import WeatherSvgIcon from '$lib/desktop/components/ui/WeatherSvgIcon.svelte';
  import { t } from '$lib/i18n';
  import {
    convertWindSpeed,
    formatTemperatureCompact,
    getWindSpeedUnit,
    type TemperatureUnit,
  } from '$lib/utils/formatters';
  import {
    getBasmiliusIconName,
    getMoonPhaseI18nKey,
    getMoonPhaseIconName,
    translateWeatherCondition,
  } from '$lib/utils/weather';
  import DetailCard from './DetailCard.svelte';
  import { barHeight, formatCount, formatDecimal, PERCENT } from './detailFormat';
  import {
    MINUTES_PER_DAY,
    compassPointKey,
    dayPercent,
    detectionMinute,
    MINUTES_PER_HOUR,
    sunEventTimes,
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
    /** True while the weather or the sun times are still being requested. */
    weatherLoading?: boolean;
    /** True while the hourly activity is still being requested. */
    activityLoading?: boolean;
    speciesName: string;
  }

  let {
    detection,
    weather = null,
    sun = null,
    hourlyActivity = null,
    weatherLoading = false,
    activityLoading = false,
    speciesName,
  }: Props = $props();

  const HOURS_PER_DAY = 24;
  const HALF_TURN_DEGREES = 180;
  // The Navigation2 icon's arrow points up (north) when not rotated.
  const AXIS_HOURS = [0, 6, 12, 18, 24];
  const MISSING = '-';
  // Share of the strip height where the horizon line sits, measured from the top.
  const HORIZON_PERCENT = 62;
  // How far the sun marker travels above and below the horizon, in percent of strip height.
  const DAY_ARC_HEIGHT_PERCENT = 46;
  const NIGHT_ARC_DEPTH_PERCENT = 24;
  // Past this point of the day axis the time label sits left of the marker so it stays inside.
  const LABEL_FLIP_PERCENT = 80;
  // Height of the busiest hour's bar, in percent of the ground band.
  const ACTIVITY_MAX_PERCENT = 82;

  let units = $derived<TemperatureUnit>(detection.weather?.units ?? 'metric');
  let hourly = $derived(weather?.hourly);
  // The loading flag covers the weather and the sun times together; whichever of the two
  // has not arrived yet is still on its way, and shows placeholders instead of dashes.
  let weatherPending = $derived(weatherLoading && weather === null);
  let sunPending = $derived(weatherLoading && sun === null);

  let sunTimes = $derived(sunEventTimes(sun, weather));
  let moment = $derived(detectionMinute(detection));

  // Sun events as minutes of the station's day; null without a usable sunrise and sunset.
  // Without civil twilight times the twilight ramps start at sunrise and sunset.
  let sunSpan = $derived.by(() => {
    const sunrise = wallClockMinutes(sunTimes.sunrise);
    const sunset = wallClockMinutes(sunTimes.sunset);
    if (sunrise === null || sunset === null || sunrise >= sunset) return null;
    return {
      sunrise,
      sunset,
      dawn: wallClockMinutes(sun?.civil_dawn) ?? sunrise,
      dusk: wallClockMinutes(sun?.civil_dusk) ?? sunset,
    };
  });

  // Background gradient: night, twilight ramp, day, twilight ramp, night.
  let skyGradient = $derived.by(() => {
    if (!sunSpan) return 'linear-gradient(to right, var(--sky-night), var(--sky-night))';
    const { sunrise, sunset, dawn, dusk } = sunSpan;
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

  let sunIsUp = $derived(
    sunSpan !== null && moment !== null && moment >= sunSpan.sunrise && moment <= sunSpan.sunset
  );

  /** Height of half an ellipse at `elapsed` minutes into an arc `length` minutes long, 0 to 1. */
  function arcHeight(elapsed: number, length: number): number {
    const half = length / 2;
    const offset = (elapsed - half) / half;
    return Math.sqrt(Math.max(0, 1 - offset * offset));
  }

  // Height of the sun marker relative to the horizon. The day arc is half an
  // ellipse from sunrise to sunset; the night arc is its shallower mirror from
  // sunset round midnight to the next sunrise.
  let markerTop = $derived.by(() => {
    if (!sunSpan || moment === null) return HORIZON_PERCENT;
    const { sunrise, sunset } = sunSpan;
    if (sunIsUp) {
      return (
        HORIZON_PERCENT - DAY_ARC_HEIGHT_PERCENT * arcHeight(moment - sunrise, sunset - sunrise)
      );
    }
    const sinceSunset = moment >= sunset ? moment - sunset : moment + MINUTES_PER_DAY - sunset;
    const nightLength = MINUTES_PER_DAY - (sunset - sunrise);
    return HORIZON_PERCENT + NIGHT_ARC_DEPTH_PERCENT * arcHeight(sinceSunset, nightLength);
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
    return counts.map((count, hour) => ({ hour, count, peak }));
  });

  let detectionHour = $derived(moment === null ? -1 : Math.floor(moment / MINUTES_PER_HOUR));

  // The weather API leaves out values of zero, so with an observation for the hour (the
  // hourly object has a time) a missing precipitation, wind speed or wind direction is zero.
  // An hour without data still comes as an hourly object with no time and a temperature and
  // feels-like of 0, so nothing from it is read then. Cloud cover is zero when left out of a
  // recorded hour; one provider never sets it (internal/weather/provider_wunderground.go),
  // so there a clear sky and a missing value look the same.
  let observed = $derived(Boolean(hourly?.time));
  let temperature = $derived(
    observed
      ? (hourly?.temperature ?? detection.weather?.temperature)
      : detection.weather?.temperature
  );
  let feelsLike = $derived(observed ? hourly?.feels_like : undefined);
  let precipitation = $derived(observed ? (hourly?.precipitation ?? 0) : undefined);
  let clouds = $derived(observed ? (hourly?.clouds ?? 0) : undefined);
  let windSpeed = $derived(observed ? (hourly?.wind_speed ?? 0) : detection.weather?.windSpeed);
  let windGust = $derived(hourly?.wind_gust ?? detection.weather?.windGust);
  let humidity = $derived(hourly?.humidity ?? detection.weather?.humidity);
  let description = $derived(hourly?.weather_desc ?? detection.weather?.description);
  let iconCode = $derived(hourly?.weather_icon ?? detection.weather?.weatherIcon ?? '');
  let hasWeather = $derived(temperature !== undefined || Boolean(description));

  // The stored icon code has no day or night mark, so the day or night comes from the sun
  // times when they are known, else from the time of day the API or the detection carries.
  let dayOrNight = $derived(
    sunSpan ? (sunIsUp ? 'day' : 'night') : (weather?.time_of_day ?? detection.timeOfDay)
  );
  let conditionIcon = $derived(
    hasWeather ? getBasmiliusIconName(iconCode, description, dayOrNight) : 'not-available'
  );

  // Speeds keep one decimal below this and are whole from it up, so the wind and its
  // gust fit their tile with the unit.
  const WHOLE_SPEED_FROM = 10;

  function speed(value: number): string {
    const converted = convertWindSpeed(value, units);
    return converted < WHOLE_SPEED_FROM
      ? formatDecimal(converted)
      : formatCount(Math.round(converted));
  }

  function hourLabel(hour: number): string {
    return `${String(hour).padStart(2, '0')}:00`;
  }

  // Wind as "2.5 (5.3) m/s": speed, then the gust in brackets, as the rest of the app writes it.
  let windValue = $derived.by(() => {
    if (windSpeed === undefined) return MISSING;
    const gust = windGust !== undefined ? ` (${speed(windGust)})` : '';
    return `${speed(windSpeed)}${gust}`;
  });

  interface Tile {
    key: string;
    icon: typeof Wind;
    label: string;
    /** The number, or a dash when it was not recorded. */
    value: string;
    /** Unit after the number, with its own leading space where it has one; kept whole
        when the number has to be cut. */
    unit?: string;
    /** Rotation of the icon in degrees, for the wind arrow. */
    rotate?: number;
    suffix?: string;
  }

  // A direction only for a recorded hour; north is 0, which the API leaves out.
  let windDeg = $derived(observed ? (hourly?.wind_deg ?? 0) : undefined);
  let windUnit = $derived(windSpeed === undefined ? undefined : ` ${getWindSpeedUnit(units)}`);

  // The six tiles, always: a value that was not recorded shows a dash. The wind tile's
  // arrow points the way the wind blows, when the direction is known.
  let tiles = $derived<Tile[]>([
    windDeg !== undefined
      ? {
          key: 'wind',
          icon: Navigation2,
          rotate: windDeg + HALF_TURN_DEGREES,
          label: t('detections.detail.conditions.windFrom', {
            direction: t(`detections.detail.compass.${compassPointKey(windDeg)}`),
          }),
          value: windValue,
          unit: windUnit,
        }
      : {
          key: 'wind',
          icon: Wind,
          label: t('detections.weather.labels.wind'),
          value: windValue,
          unit: windUnit,
        },
    {
      key: 'precipitation',
      icon: CloudRain,
      label: t('detections.detail.conditions.precipitation'),
      value: precipitation !== undefined ? formatDecimal(precipitation) : MISSING,
      unit: precipitation !== undefined ? ' mm' : undefined,
      suffix: precipitation ? hourly?.precipitation_type : undefined,
    },
    {
      key: 'clouds',
      icon: Cloud,
      label: t('detections.detail.conditions.cloudCover'),
      value: clouds !== undefined ? String(clouds) : MISSING,
      unit: clouds !== undefined ? '%' : undefined,
    },
    {
      key: 'humidity',
      icon: Droplets,
      label: t('detections.weather.labels.humidity'),
      value: humidity !== undefined ? String(humidity) : MISSING,
      unit: humidity !== undefined ? '%' : undefined,
    },
    {
      key: 'pressure',
      icon: Gauge,
      label: t('detections.weather.labels.pressure'),
      value: hourly?.pressure ? String(hourly.pressure) : MISSING,
      unit: hourly?.pressure ? ' hPa' : undefined,
    },
    {
      key: 'feelsLike',
      icon: Thermometer,
      label: t('detections.detail.conditions.feelsLike'),
      value: feelsLike !== undefined ? formatTemperatureCompact(feelsLike, units) : MISSING,
    },
  ]);

  let moonName = $derived(
    detection.weather?.moonPhaseName
      ? t(`weather.moon.${getMoonPhaseI18nKey(detection.weather.moonPhaseName)}`)
      : ''
  );
  let moonIcon = $derived(
    detection.weather?.moonPhaseName ? getMoonPhaseIconName(detection.weather.moonPhaseName) : ''
  );
</script>

{#snippet sunEvent(Icon: typeof Sunrise, label: string, time: string | undefined)}
  <span class="dd-sky-legend-item">
    <Icon class="size-3.5" />
    {label}
    {#if !wallClockTime(time) && sunPending}
      <Skeleton inline className="h-3 w-9" />
    {:else}
      <strong>{wallClockTime(time) || MISSING}</strong>
    {/if}
  </span>
{/snippet}

<DetailCard title={t('detections.detail.conditions.title')} fill={true}>
  {#snippet header()}
    <!-- The one visible label while the card's own data loads. -->
    {#if weatherPending || sunPending || (activityLoading && hourlyActivity === null)}
      <SkeletonStatus label={t('common.loading')} className="ml-auto text-xs" />
    {/if}
  {/snippet}

  <div class="dd-conditions">
    <!-- Sky strip: the one part that grows with the card. -->
    <div class="dd-sky-block">
      <div
        class="dd-sky"
        style:background={skyGradient}
        role="group"
        aria-label={t('detections.detail.conditions.skyLabel', {
          time: detection.time,
          sunrise: wallClockTime(sunTimes.sunrise) || '?',
          sunset: wallClockTime(sunTimes.sunset) || '?',
        })}
      >
        {#if sunSpan}
          <!-- Sun path above the horizon between sunrise and sunset -->
          <div
            class="dd-sky-arc"
            style:left="{dayPercent(sunSpan.sunrise)}%"
            style:width="{dayPercent(sunSpan.sunset) - dayPercent(sunSpan.sunrise)}%"
            style:top="{HORIZON_PERCENT - DAY_ARC_HEIGHT_PERCENT}%"
            style:height="{DAY_ARC_HEIGHT_PERCENT * 2}%"
          ></div>
        {/if}
        <div class="dd-sky-horizon" style:top="{HORIZON_PERCENT}%"></div>
        {#if activity}
          <!-- The bars are drawn for the eye; the table below carries their values. -->
          <div class="dd-activity" style:height="{PERCENT - HORIZON_PERCENT}%" aria-hidden="true">
            {#each activity as bar (bar.hour)}
              <span
                class="dd-activity-bar"
                class:dd-activity-bar-now={bar.hour === detectionHour}
                style:height="{barHeight(bar.count, bar.peak, ACTIVITY_MAX_PERCENT)}%"
                title="{hourLabel(bar.hour)}  {bar.count}"
              ></span>
            {/each}
          </div>
          <!-- A table is as tall as its rows whatever its height, so the wrapper hides it. -->
          <div class="sr-only">
            <table>
              <caption>
                {t('detections.detail.conditions.activityLabel', { species: speciesName })}
              </caption>
              <thead>
                <tr>
                  <th scope="col">{t('detections.detail.identity.time')}</th>
                  <th scope="col">{t('analytics.charts.detections')}</th>
                </tr>
              </thead>
              <tbody>
                {#each activity as bar (bar.hour)}
                  <tr>
                    <th scope="row">{hourLabel(bar.hour)}</th>
                    <td>{bar.count}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {:else if activityLoading && hourlyActivity === null}
          <div class="dd-activity-pending" style:height="{PERCENT - HORIZON_PERCENT}%">
            <Skeleton className="h-full w-full rounded-none" />
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
        {@render sunEvent(Sunrise, t('detections.detail.conditions.sunrise'), sunTimes.sunrise)}
        {@render sunEvent(Sunset, t('detections.detail.conditions.sunset'), sunTimes.sunset)}
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
        {#if !hasWeather && weatherPending}
          <span class="dd-weather-temp"><Skeleton inline className="h-6 w-14" /></span>
          <span class="dd-weather-desc"><Skeleton inline className="h-3 w-32" /></span>
        {:else}
          <span class="dd-weather-temp"
            >{temperature !== undefined
              ? formatTemperatureCompact(temperature, units)
              : MISSING}</span
          >
          <span class="dd-weather-desc">
            {description
              ? translateWeatherCondition(description)
              : t('detections.weather.noDataAvailable')}
          </span>
        {/if}
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

    <dl class="dd-tiles">
      {#each tiles as tile (tile.key)}
        <div class="dd-tile">
          <dt class="dd-tile-label">
            <span
              class="dd-tile-icon"
              style:transform={tile.rotate === undefined ? undefined : `rotate(${tile.rotate}deg)`}
              aria-hidden="true"
            >
              <tile.icon class="size-3.5" />
            </span>
            {tile.label}
          </dt>
          <dd class="dd-tile-value">
            {#if tile.value === MISSING && weatherPending}
              <Skeleton inline className="h-3 w-12" />
            {:else}
              <span class="dd-tile-number">{tile.value}</span>{#if tile.unit}<span
                  class="dd-tile-unit">{tile.unit}</span
                >{/if}{#if tile.suffix}<span class="dd-tile-suffix">{tile.suffix}</span>{/if}
            {/if}
          </dd>
        </div>
      {/each}
    </dl>

    <p class="dd-weather-note">
      {#if !hourly?.time && weatherPending}
        <Skeleton inline className="h-2.5 w-40" />
      {:else}
        {hourly?.time
          ? t('detections.detail.conditions.observedAt', { time: hourly.time.slice(0, 5) })
          : t('detections.detail.conditions.notObserved')}
      {/if}
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

  /* While the hourly counts load: a faint pulsing band where the bars will stand. */
  .dd-activity-pending {
    position: absolute;
    right: 0;
    bottom: 0;
    left: 0;
    display: flex;
    align-items: flex-end;
    padding-top: 1.5rem;
    opacity: 0.3;
  }

  .dd-sky-axis {
    position: relative;
    flex: 0 0 auto;
    height: 1rem;
    margin-top: 0.125rem;
    font-size: 0.625rem;
    font-variant-numeric: tabular-nums;
    color: var(--text-subtle);
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
    color: var(--text-subtle);
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
    color: var(--text-subtle);
  }

  .dd-tiles {
    display: grid;
    flex: 0 0 auto;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 0.375rem;
    margin-top: 0.625rem;
  }

  /* In a narrow card the six tiles sit two to a row, so a wind speed with its gust and
     unit fits. Three to a row need a 24.5rem card body (the 1440 px layout has 25rem). */
  @container dd-conditions (width < 24.5rem) {
    .dd-tiles {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  /* The icon sits in the tile's left padding, inside the term, so each tile holds only
     its term and its value. */
  .dd-tile {
    position: relative;
    display: flex;
    flex-direction: column;
    min-width: 0;
    height: 2.875rem;
    padding: 0.375rem 0.5rem 0.375rem 1.75rem;
    border-radius: 0.5rem;
    background-color: var(--color-base-200);
  }

  .dd-tile-icon {
    position: absolute;
    top: 0.4375rem;
    left: 0.5rem;
    display: inline-flex;
    color: var(--text-subtle);
  }

  .dd-tile-label {
    overflow: hidden;
    font-size: 0.6875rem;
    line-height: 1rem;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text-subtle);
  }

  /* One line: the number gives way first, the unit never does. */
  .dd-tile-value {
    display: flex;
    align-items: baseline;
    min-width: 0;
    font-size: 0.875rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    line-height: 1.125rem;
    white-space: nowrap;
  }

  .dd-tile-number {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .dd-tile-unit {
    flex: 0 0 auto;
    white-space: pre;
  }

  .dd-tile-suffix {
    min-width: 0;
    margin-left: 0.25rem;
    overflow: hidden;
    font-size: 0.6875rem;
    font-weight: 400;
    text-overflow: ellipsis;
    color: var(--text-subtle);
  }

  /* Windows contrast themes drop background colours and gradients. The sky's colours
     carry the day and night, and the activity bars and the marker inside it are the data,
     so the whole strip keeps its own paint. */
  @media (forced-colors: active) {
    .dd-sky {
      forced-color-adjust: none;
    }
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
    color: var(--text-subtle);
  }
</style>
