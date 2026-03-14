/**
 * Weather utilities shared across weather components
 */

import { t } from '$lib/i18n';
import { safeGet } from '$lib/utils/security';

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
    '12': { day: '🌨️', night: '🌨️', description: 'Sleet' },
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
  /** Light wind: <3 m/s - shown with reduced opacity */
  LIGHT: 3,
  /** Moderate wind: 3-7 m/s - shown with medium opacity (8+ is strong) */
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
  const info = safeGet(WEATHER_ICON_MAP, weatherCode, UNKNOWN_WEATHER_INFO);
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

/**
 * Yr.no weather symbol to standardized icon code mapping
 * Used as fallback when weatherIcon is "unknown" but description contains the raw symbol
 * Based on https://nrkno.github.io/yr-weather-symbols/
 */
const YR_NO_SYMBOL_TO_ICON: Record<string, string> = {
  // Clear sky
  clearsky_day: '01',
  clearsky_night: '01',
  clearsky_polartwilight: '01',
  // Fair (few clouds)
  fair_day: '02',
  fair_night: '02',
  fair_polartwilight: '02',
  // Partly cloudy
  partlycloudy_day: '03',
  partlycloudy_night: '03',
  partlycloudy_polartwilight: '03',
  // Cloudy
  cloudy: '04',
  // Fog
  fog: '50',
  // Rain showers (light/normal/heavy)
  lightrainshowers_day: '09',
  lightrainshowers_night: '09',
  lightrainshowers_polartwilight: '09',
  rainshowers_day: '09',
  rainshowers_night: '09',
  rainshowers_polartwilight: '09',
  heavyrainshowers_day: '09',
  heavyrainshowers_night: '09',
  heavyrainshowers_polartwilight: '09',
  // Rain (light/normal/heavy)
  lightrain: '10',
  rain: '10',
  heavyrain: '10',
  // Thunderstorm variants
  lightrainshowersandthunder_day: '11',
  lightrainshowersandthunder_night: '11',
  lightrainshowersandthunder_polartwilight: '11',
  rainshowersandthunder_day: '11',
  rainshowersandthunder_night: '11',
  rainshowersandthunder_polartwilight: '11',
  heavyrainshowersandthunder_day: '11',
  heavyrainshowersandthunder_night: '11',
  heavyrainshowersandthunder_polartwilight: '11',
  lightrainandthunder: '11',
  rainandthunder: '11',
  heavyrainandthunder: '11',
  // Sleet showers
  lightsleetshowers_day: '12',
  lightsleetshowers_night: '12',
  lightsleetshowers_polartwilight: '12',
  sleetshowers_day: '12',
  sleetshowers_night: '12',
  sleetshowers_polartwilight: '12',
  heavysleetshowers_day: '12',
  heavysleetshowers_night: '12',
  heavysleetshowers_polartwilight: '12',
  // Sleet
  lightsleet: '12',
  sleet: '12',
  heavysleet: '12',
  // Sleet and thunder (including yr.no typo "lightssleet")
  lightssleetshowersandthunder_day: '11',
  lightssleetshowersandthunder_night: '11',
  lightssleetshowersandthunder_polartwilight: '11',
  sleetshowersandthunder_day: '11',
  sleetshowersandthunder_night: '11',
  sleetshowersandthunder_polartwilight: '11',
  heavysleetshowersandthunder_day: '11',
  heavysleetshowersandthunder_night: '11',
  heavysleetshowersandthunder_polartwilight: '11',
  lightsleetandthunder: '11',
  sleetandthunder: '11',
  heavysleetandthunder: '11',
  // Snow showers
  lightsnowshowers_day: '13',
  lightsnowshowers_night: '13',
  lightsnowshowers_polartwilight: '13',
  snowshowers_day: '13',
  snowshowers_night: '13',
  snowshowers_polartwilight: '13',
  heavysnowshowers_day: '13',
  heavysnowshowers_night: '13',
  heavysnowshowers_polartwilight: '13',
  // Snow
  lightsnow: '13',
  snow: '13',
  heavysnow: '13',
  // Snow and thunder (including yr.no typo "lightssnow")
  lightssnowshowersandthunder_day: '11',
  lightssnowshowersandthunder_night: '11',
  lightssnowshowersandthunder_polartwilight: '11',
  snowshowersandthunder_day: '11',
  snowshowersandthunder_night: '11',
  snowshowersandthunder_polartwilight: '11',
  heavysnowshowersandthunder_day: '11',
  heavysnowshowersandthunder_night: '11',
  heavysnowshowersandthunder_polartwilight: '11',
  lightsnowandthunder: '11',
  snowandthunder: '11',
  heavysnowandthunder: '11',
};

/**
 * Derive icon code from yr.no weather description
 * Used when weatherIcon is "unknown" but description contains the raw yr.no symbol
 *
 * @param description - Raw yr.no symbol code (e.g., 'partlycloudy_night')
 * @returns Standardized icon code (e.g., '03') or empty string if not found
 */
export function deriveIconFromDescription(description: string | undefined | null): string {
  if (!description || typeof description !== 'string') return '';
  return safeGet(YR_NO_SYMBOL_TO_ICON, description, '');
}

/**
 * Get effective weather icon code, with fallback to description-based derivation
 * Handles legacy "unknown" icon values by attempting to derive from description
 *
 * @param weatherIcon - The stored weather icon code
 * @param description - Raw weather description (yr.no symbol)
 * @returns Valid icon code or empty string
 */
export function getEffectiveWeatherCode(
  weatherIcon: string | undefined | null,
  description?: string | null
): string {
  // First try to extract from weatherIcon
  const extracted = extractWeatherCode(weatherIcon);
  if (extracted) return extracted;

  // If weatherIcon is "unknown" or invalid, try to derive from description
  if (description) {
    return deriveIconFromDescription(description);
  }

  return '';
}

/**
 * Maps standardized weather codes to Basmilius icon names.
 * Used by WeatherSvgIcon component in the banner card.
 */
export const WEATHER_CODE_TO_BASMILIUS: Record<string, { day: string; night: string }> = {
  '01': { day: 'clear-day', night: 'clear-night' },
  '02': { day: 'partly-cloudy-day', night: 'partly-cloudy-night' },
  '03': { day: 'partly-cloudy-day', night: 'partly-cloudy-night' },
  '04': { day: 'overcast-day', night: 'overcast-night' },
  '09': { day: 'partly-cloudy-day-rain', night: 'partly-cloudy-night-rain' },
  '10': { day: 'rain', night: 'rain' },
  '11': { day: 'thunderstorms-day-rain', night: 'thunderstorms-night-rain' },
  '12': { day: 'sleet', night: 'sleet' },
  '13': { day: 'snow', night: 'snow' },
  '50': { day: 'fog-day', night: 'fog-night' },
};

/**
 * Returns the Basmilius icon name for a given weather code and time of day.
 */
export function getBasmiliusIconName(
  weatherIcon: string,
  description?: string,
  timeOfDay?: string
): string {
  const code = getEffectiveWeatherCode(weatherIcon, description);
  const isNight = timeOfDay === 'night' || weatherIcon.endsWith('n');
  const mapping = safeGet(WEATHER_CODE_TO_BASMILIUS, code);
  if (!mapping) return 'not-available';
  return isNight ? mapping.night : mapping.day;
}

/**
 * Birding condition level based on wind speed.
 */
export type BirdingConditionLevel = 'excellent' | 'moderate' | 'poor';

/**
 * Returns birding condition level based on wind speed (m/s).
 * Uses existing WIND_SPEED_THRESHOLDS.
 */
export function getBirdingConditionLevel(windSpeed: number): BirdingConditionLevel {
  if (windSpeed < WIND_SPEED_THRESHOLDS.LIGHT) return 'excellent';
  if (windSpeed < WIND_SPEED_THRESHOLDS.MODERATE) return 'moderate';
  return 'poor';
}

/**
 * Returns the CSS color class for a birding condition level.
 */
export function getBirdingConditionColor(level: BirdingConditionLevel): string {
  switch (level) {
    case 'excellent':
      return 'text-green-500';
    case 'moderate':
      return 'text-amber-500';
    case 'poor':
      return 'text-red-500';
  }
}

/**
 * Moon phase emoji mapping for detection card badges.
 */
export const MOON_PHASE_EMOJIS: Record<string, string> = {
  'New Moon': '🌑',
  'Waxing Crescent': '🌒',
  'First Quarter': '🌓',
  'Waxing Gibbous': '🌔',
  'Full Moon': '🌕',
  'Waning Gibbous': '🌖',
  'Last Quarter': '🌗',
  'Waning Crescent': '🌘',
};

/**
 * Returns the emoji for a given moon phase name.
 */
export function getMoonPhaseEmoji(phaseName: string): string {
  return safeGet(MOON_PHASE_EMOJIS, phaseName) ?? '🌑';
}

/**
 * Maps moon phase names from API to i18n keys.
 */
const MOON_PHASE_I18N_KEYS: Record<string, string> = {
  'New Moon': 'newMoon',
  'Waxing Crescent': 'waxingCrescent',
  'First Quarter': 'firstQuarter',
  'Waxing Gibbous': 'waxingGibbous',
  'Full Moon': 'fullMoon',
  'Waning Gibbous': 'waningGibbous',
  'Last Quarter': 'lastQuarter',
  'Waning Crescent': 'waningCrescent',
};

/**
 * Returns the i18n key suffix for a given moon phase name.
 */
export function getMoonPhaseI18nKey(phaseName: string): string {
  return safeGet(MOON_PHASE_I18N_KEYS, phaseName) ?? 'newMoon';
}
