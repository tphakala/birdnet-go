import { afterEach, describe, expect, it } from 'vitest';
import { t } from '$lib/i18n';
import { cleanup, render, screen } from '@testing-library/svelte';
import ConditionsCard from './ConditionsCard.svelte';
import type { DetectionDetailData, DetectionWeather } from './detailTypes';

const detection: DetectionDetailData = {
  id: 7,
  date: '2026-11-14',
  time: '02:57:03',
  timestamp: '2026-11-14T02:57:03+02:00',
  beginTime: '2026-11-14T02:57:00+02:00',
  endTime: '2026-11-14T02:57:03+02:00',
  speciesCode: 'eurwoo',
  scientificName: 'Scolopax rusticola',
  commonName: 'Eurasian Woodcock',
  confidence: 0.87,
  verified: 'unverified',
  locked: false,
};

function renderCard(weather: DetectionWeather) {
  return render(ConditionsCard, { props: { detection, weather, speciesName: 'Woodcock' } });
}

/** The value text of the tile whose term contains the label key. */
function tileValue(label: string): string {
  const term = screen.getByText(label, { exact: false });
  return term.closest('.dd-tile')?.querySelector('.dd-tile-value')?.textContent.trim() ?? '';
}

describe('ConditionsCard weather tiles', () => {
  afterEach(() => cleanup());

  // Regression: the weather API leaves out zero values, so a dry or calm hour showed
  // dashes as if nothing had been recorded.
  it('shows zero for precipitation and wind the API left out', () => {
    renderCard({ hourly: { time: '03:00', temperature: 2, humidity: 80 } });

    expect(tileValue('detections.detail.conditions.precipitation')).toBe('0.0 mm');
    expect(tileValue('detections.detail.conditions.windFrom')).toBe('0.0 m/s');
  });

  // The API leaves out clouds of 0 too, but the Wunderground provider never sets clouds
  // (internal/weather/provider_wunderground.go), so a recorded hour shows 0 % for it.
  it('shows 0 for cloud cover of a recorded hour when the API left it out', () => {
    renderCard({ hourly: { time: '03:00', temperature: 2, clouds: 40 } });
    expect(tileValue('detections.weather.labels.cloudCover')).toBe('40%');
  });

  it('shows 0 % cloud cover for a recorded hour that left it out', () => {
    renderCard({ hourly: { time: '03:00', temperature: 2 } });

    expect(tileValue('detections.weather.labels.cloudCover')).toBe('0%');
  });

  it('shows dashes for the empty hourly object the API sends when it has no data', () => {
    renderCard({ hourly: { time: '', temperature: 0, feels_like: 0 } });

    expect(tileValue('detections.detail.conditions.precipitation')).toBe('-');
    expect(tileValue('detections.weather.labels.wind')).toBe('-');
    expect(tileValue('detections.weather.labels.cloudCover')).toBe('-');
    expect(tileValue('detections.detail.conditions.feelsLike')).toBe('-');
    // The weather line must not read 0 degrees.
    expect(document.querySelector('.dd-weather-temp')?.textContent.trim()).toBe('-');
    expect(screen.getByRole('img', { name: 'not-available' })).toBeInTheDocument();
  });

  it('gives no wind direction when only the detection carries a wind speed', () => {
    const withWind: DetectionDetailData = {
      ...detection,
      weather: { weatherIcon: '', windSpeed: 3, units: 'metric' },
    };
    render(ConditionsCard, {
      props: { detection: withWind, weather: null, speciesName: 'Woodcock' },
    });

    expect(tileValue('detections.weather.labels.wind')).toBe('3.0 m/s');
    expect(document.querySelector('.dd-tile-icon[style]')).toBeNull();
  });

  it('shows the wind from north for a recorded hour', () => {
    renderCard({ hourly: { time: '03:00', wind_speed: 3 } });

    expect(t).toHaveBeenCalledWith('detections.detail.compass.n');
  });

  it('names the compass point of the wind direction through t()', () => {
    renderCard({ hourly: { time: '03:00', wind_speed: 3, wind_deg: 292 } });

    expect(t).toHaveBeenCalledWith('detections.detail.compass.wnw');
  });

  it('shows dashes when there is no observation for the hour', () => {
    renderCard({});

    expect(tileValue('detections.detail.conditions.precipitation')).toBe('-');
    expect(tileValue('detections.weather.labels.cloudCover')).toBe('-');
  });

  // The arrow icon rests pointing up, so a wind from 0 degrees (north) blows south: down.
  it('points the wind arrow the way a north wind blows (south)', () => {
    const { container } = renderCard({ hourly: { time: '03:00', wind_speed: 3 } });

    const icon = container.querySelector<HTMLElement>('.dd-tile-icon[style]');
    expect(icon?.style.transform).toBe('rotate(180deg)');
  });

  it('points the wind arrow east for a west wind', () => {
    const { container } = renderCard({ hourly: { time: '03:00', wind_speed: 3, wind_deg: 270 } });

    const icon = container.querySelector<HTMLElement>('.dd-tile-icon[style]');
    expect(icon?.style.transform).toBe('rotate(450deg)');
  });

  // The stored weather icon has no d or n suffix, so without sun times the day or night
  // signal is the time of day the API or the detection carries.
  it('shows the night icon for a stored code without suffix and a night time of day', () => {
    renderCard({
      hourly: { time: '03:00', temperature: 2, weather_icon: '01' },
      time_of_day: 'night',
    });

    expect(screen.getByRole('img', { name: 'clear-night' })).toBeInTheDocument();
  });

  it('shows the day icon for a stored code without suffix and a day time of day', () => {
    renderCard({
      hourly: { time: '03:00', temperature: 2, weather_icon: '01' },
      time_of_day: 'day',
    });

    expect(screen.getByRole('img', { name: 'clear-day' })).toBeInTheDocument();
  });

  it('falls back to the detection time of day when the weather has none', () => {
    render(ConditionsCard, {
      props: {
        detection: { ...detection, timeOfDay: 'night' },
        weather: { hourly: { time: '03:00', temperature: 2, weather_icon: '01' } },
        speciesName: 'Woodcock',
      },
    });

    expect(screen.getByRole('img', { name: 'clear-night' })).toBeInTheDocument();
  });
});

describe('ConditionsCard loading', () => {
  afterEach(() => cleanup());

  // Regression: the card showed placeholder bars with no visible loading label.
  it('labels its placeholders while the weather loads', () => {
    render(ConditionsCard, {
      props: { detection, weather: null, weatherLoading: true, speciesName: 'Woodcock' },
    });

    expect(screen.getByRole('status')).toHaveTextContent('common.loading');
  });

  it('shows one loading label and a sun placeholder while only the sun is loading', () => {
    render(ConditionsCard, {
      props: {
        detection,
        weather: { hourly: { time: '03:00', temperature: 2 } },
        sun: null,
        weatherLoading: true,
        speciesName: 'Woodcock',
      },
    });

    expect(screen.getAllByText('common.loading')).toHaveLength(1);
    const legend = document.querySelector('.dd-sky-legend');
    expect(legend?.querySelectorAll('[data-skeleton]')).toHaveLength(2);
    expect(legend?.textContent).not.toContain('-');
  });

  it('shows no loading label once the weather answered', () => {
    renderCard({ hourly: { time: '03:00' } });

    expect(screen.queryByText('common.loading')).toBeNull();
  });
});
