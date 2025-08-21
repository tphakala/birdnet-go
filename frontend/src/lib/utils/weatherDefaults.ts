/**
 * Shared weather provider default configurations
 * This module provides centralized defaults for weather integrations
 * to ensure consistency across UI, stores, and backend payload construction
 */

import type {
  OpenWeatherSettings,
  WundergroundSettings,
  WeatherSettings,
} from '$lib/stores/settings';

/**
 * Default configuration for OpenWeather provider
 */
export const openWeatherDefaults: OpenWeatherSettings = {
  enabled: false,
  apiKey: '',
  endpoint: 'https://api.openweathermap.org/data/2.5/weather',
  units: 'metric',
  language: 'en',
};

/**
 * Default configuration for Weather Underground provider
 */
export const wundergroundDefaults: WundergroundSettings = {
  enabled: false,
  apiKey: '',
  stationId: '',
  endpoint: 'https://api.weather.com/v2/pws/observations/current',
  units: 'm', // m=metric, e=imperial, h=UK hybrid
};

/**
 * Complete default weather configuration
 */
export const weatherDefaults: WeatherSettings = {
  provider: 'yrno' as const,
  pollInterval: 60,
  debug: false,
  openWeather: openWeatherDefaults,
  wunderground: wundergroundDefaults,
};

/**
 * Helper to get provider-specific defaults
 */
export function getProviderDefaults(
  provider: WeatherSettings['provider']
): OpenWeatherSettings | WundergroundSettings | null {
  switch (provider) {
    case 'openweather':
      return openWeatherDefaults;
    case 'wunderground':
      return wundergroundDefaults;
    case 'none':
    case 'yrno':
      return null;
  }
}
