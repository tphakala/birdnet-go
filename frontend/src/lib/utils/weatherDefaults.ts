/**
 * Shared weather provider default configurations
 * This module provides centralized defaults for weather integrations
 * to ensure consistency across UI, stores, and backend payload construction
 */

import type {
  OpenWeatherSettings,
  WundergroundSettings,
  PirateWeatherSettings,
  TempestSettings,
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
 * Default configuration for the local Tempest/WeatherFlow UDP provider.
 * Empty listenAddress defaults to ":50222" at the backend (WeatherFlow's
 * fixed, non-configurable broadcast port).
 */
export const tempestDefaults: TempestSettings = {
  listenAddress: '',
  extraFields: {
    illuminance: false,
    uvIndex: false,
    solarRadiation: false,
    lightningDistance: false,
    lightningCount: false,
    windLull: false,
  },
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
  tempest: tempestDefaults,
};

/**
 * Helper to get provider-specific defaults
 */
export function getProviderDefaults(
  provider: WeatherSettings['provider']
): OpenWeatherSettings | WundergroundSettings | PirateWeatherSettings | TempestSettings | null {
  switch (provider) {
    case 'openweather':
      return openWeatherDefaults;
    case 'wunderground':
      return wundergroundDefaults;
    case 'pirateweather':
      return pirateWeatherDefaults;
    case 'tempest':
      return tempestDefaults;
    case 'none':
    case 'yrno':
      return null;
  }
}
