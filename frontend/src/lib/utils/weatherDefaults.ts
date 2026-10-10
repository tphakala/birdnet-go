/**
 * Shared weather provider default configurations
 * This module provides centralized defaults for weather integrations
 * to ensure consistency across UI, stores, and backend payload construction
 */

import type {
  OpenWeatherSettings,
  WundergroundSettings,
  PirateWeatherSettings,
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
  apiKey: '',
  stationId: '',
  endpoint: 'https://api.weather.com/v2/pws/observations/current',
  units: 'm', // m=metric, e=imperial, h=UK hybrid
};

/**
 * Default configuration for Pirate Weather provider
 */
export const pirateWeatherDefaults: PirateWeatherSettings = {
  apiKey: '',
  endpoint: 'https://api.pirateweather.net/forecast',
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
  pirateWeather: pirateWeatherDefaults,
};

/**
 * Helper to get provider-specific defaults
 */
export function getProviderDefaults(
  provider: WeatherSettings['provider']
): OpenWeatherSettings | WundergroundSettings | PirateWeatherSettings | null {
  switch (provider) {
    case 'openweather':
      return openWeatherDefaults;
    case 'wunderground':
      return wundergroundDefaults;
    case 'pirateweather':
      return pirateWeatherDefaults;
    case 'none':
    case 'yrno':
      return null;
  }
}
