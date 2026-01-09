/**
 * Weather utilities shared across weather components
 */

import { t } from '$lib/i18n';

/**
 * Weather icon code to emoji mapping
 * Maps OpenWeatherMap icon codes (first 2 digits) to day/night emojis
 */
export const WEATHER_ICON_MAP: Record<string, { day: string; night: string; description: string }> =
  {
    '01': { day: '☀️', night: '🌙', description: 'Clear sky' },
    '02': { day: '⛅', night: '☁️', description: 'Few clouds' },
    '03': { day: '⛅', night: '☁️', description: 'Scattered clouds' },
    '04': { day: '⛅', night: '☁️', description: 'Broken clouds' },
    '09': { day: '🌧️', night: '🌧️', description: 'Shower rain' },
    '10': { day: '🌦️', night: '🌧️', description: 'Rain' },
    '11': { day: '⛈️', night: '⛈️', description: 'Thunderstorm' },
    '13': { day: '❄️', night: '❄️', description: 'Snow' },
    '50': { day: '🌫️', night: '🌫️', description: 'Mist' },
  };

/**
 * Default fallback for unknown weather conditions
 */
export const UNKNOWN_WEATHER_INFO = {
  day: '❓',
  night: '❓',
  description: 'Unknown',
};

/**
 * Wind speed thresholds for visual opacity indicators (in m/s)
 */
export const WIND_SPEED_THRESHOLDS = {
  /** Light wind: 0-3 m/s - shown with reduced opacity */
  LIGHT: 3,
  /** Moderate wind: 3-8 m/s - shown with medium opacity */
  MODERATE: 8,
} as const;

/**
 * Extract the base weather code (2 digits) from an OpenWeatherMap icon code
 * @param weatherIcon - Full icon code (e.g., '01d', '09n')
 * @returns Base code (e.g., '01', '09') or empty string if invalid
 */
export function extractWeatherCode(weatherIcon: string | undefined | null): string {
  if (!weatherIcon || typeof weatherIcon !== 'string') return '';
  const match = weatherIcon.match(/^(\d{2})[dn]?$/);
  return match ? match[1] : '';
}

/**
 * Determine if it's night time based on icon code or explicit time of day
 * @param weatherIcon - Weather icon code (e.g., '01n')
 * @param timeOfDay - Explicit time of day override
 */
export function isNightTime(
  weatherIcon: string | undefined | null,
  timeOfDay: 'day' | 'night' = 'day'
): boolean {
  return timeOfDay === 'night' || (weatherIcon?.endsWith('n') ?? false);
}

/**
 * Get weather emoji based on weather code and time of day
 * @param weatherCode - Base weather code (e.g., '01')
 * @param isNight - Whether it's night time
 */
export function getWeatherEmoji(weatherCode: string, isNight: boolean): string {
  const info = WEATHER_ICON_MAP[weatherCode] ?? UNKNOWN_WEATHER_INFO;
  return isNight ? info.night : info.day;
}

/**
 * Translate weather conditions with i18n fallbacks
 * Tries multiple key variations before falling back to the original string
 *
 * @param condition - Weather condition string (e.g., 'Clear sky', 'Few clouds')
 * @returns Translated condition or original if no translation found
 */
export function translateWeatherCondition(condition: string | undefined): string {
  if (!condition) return '';

  // Normalize the condition string for i18n key lookup
  const normalized = condition.toLowerCase().replace(/ /g, '_');

  // Try different key variations in order of preference
  const keys = [
    `detections.weather.conditions.${normalized}`,
    `detections.weather.conditions.${condition.toLowerCase()}`,
    'detections.weather.conditions.unknown',
  ];

  // Return first successful translation or fall back to original
  for (const key of keys) {
    const translation = t(key);
    if (translation !== key) {
      return translation;
    }
  }

  return condition;
}

/**
 * Get wind opacity class based on wind speed
 * Used to visually indicate wind intensity
 *
 * @param windSpeed - Wind speed in m/s
 * @returns Tailwind opacity class or empty string for full opacity
 */
export function getWindOpacityClass(windSpeed: number | undefined): string {
  if (windSpeed === undefined) return '';
  if (windSpeed < WIND_SPEED_THRESHOLDS.LIGHT) return 'opacity-50';
  if (windSpeed < WIND_SPEED_THRESHOLDS.MODERATE) return 'opacity-75';
  return ''; // Strong wind: full opacity
}
