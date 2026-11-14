import { afterEach, describe, expect, it } from 'vitest';
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
    expect(tileValue('detections.weather.labels.wind')).toBe('0.0 m/s');
  });

  // A provider that never reports cloud cover leaves it out like a clear sky, so a
  // missing value stays "not recorded".
  it('shows cloud cover the API left out as not recorded', () => {
    renderCard({ hourly: { time: '03:00', temperature: 2 } });

    expect(tileValue('detections.weather.labels.cloudCover')).toBe('-');
  });

  it('shows dashes for the empty hourly object the API sends when it has no data', () => {
    renderCard({ hourly: { time: '', temperature: 0, feels_like: 0 } });

    expect(tileValue('detections.detail.conditions.precipitation')).toBe('-');
    expect(tileValue('detections.weather.labels.wind')).toBe('-');
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
  });

  it('shows dashes when there is no observation for the hour', () => {
    renderCard({});

    expect(tileValue('detections.detail.conditions.precipitation')).toBe('-');
    expect(tileValue('detections.weather.labels.cloudCover')).toBe('-');
  });

  // Regression: the arrow icon points north-east when not rotated, so it was drawn 45
  // degrees off the way the wind blows.
  it('points the wind arrow the way a north wind blows (south)', () => {
    const { container } = renderCard({ hourly: { time: '03:00', wind_speed: 3 } });

    const icon = container.querySelector<HTMLElement>('.dd-tile-icon[style]');
    expect(icon?.style.transform).toBe('rotate(135deg)');
  });

  // Regression: without sun times the condition icon was always the night one, even for
  // a daytime icon code.
  it('keeps the icon code day or night mark when there are no sun times', () => {
    renderCard({ hourly: { time: '03:00', temperature: 2, weather_icon: '01d' } });

    expect(screen.getByRole('img', { name: 'clear-day' })).toBeInTheDocument();
  });
});
