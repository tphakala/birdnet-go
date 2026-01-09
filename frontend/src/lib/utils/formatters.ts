/**
 * Date and time formatting utilities
 */

import { safeArrayAccess } from './security';
import { parseLocalDateString } from './date';

/**
 * Convert various input types into a valid Date object
 * @param value - Date, string, number, null, or undefined
 * @returns Valid Date object or null if invalid/null/undefined
 */
function toValidDate(value: Date | string | number | null | undefined): Date | null {
  if (value === null || value === undefined) return null;

  const d = typeof value === 'string' ? parseLocalDateString(value) : new Date(value);
  if (!d || isNaN(d.getTime())) return null;

  return d;
}

/**
 * Format date to locale string
 */
export function formatDate(date: Date | string | number | null | undefined): string {
  const d = toValidDate(date);
  if (!d) return '';

  return d.toLocaleDateString();
}

/**
 * Format date and time to locale string
 */
export function formatDateTime(date: Date | string | number | null | undefined): string {
  const d = toValidDate(date);
  if (!d) return '';

  return d.toLocaleString();
}

/**
 * Format date for HTML input (YYYY-MM-DD)
 */
export function formatDateForInput(date: Date | string | number | null): string {
  const d = toValidDate(date);
  if (!d) return '';

  const year = d.getFullYear();
  const month = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');

  return `${year}-${month}-${day}`;
}

/**
 * Format time in seconds to MM:SS or HH:MM:SS
 */
export function formatTime(seconds: number): string {
  if (!seconds || seconds < 0) return '0:00';

  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const secs = Math.floor(seconds % 60);

  if (hours > 0) {
    return `${hours}:${String(minutes).padStart(2, '0')}:${String(secs).padStart(2, '0')}`;
  }

  return `${minutes}:${String(secs).padStart(2, '0')}`;
}

/**
 * Format uptime in seconds to human readable string
 */
export function formatUptime(seconds: number): string {
  if (!seconds || seconds < 0) return '0 seconds';

  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const secs = Math.floor(seconds % 60);

  const parts: string[] = [];

  if (days > 0) parts.push(`${days} day${days !== 1 ? 's' : ''}`);
  if (hours > 0) parts.push(`${hours} hour${hours !== 1 ? 's' : ''}`);
  if (minutes > 0) parts.push(`${minutes} minute${minutes !== 1 ? 's' : ''}`);
  if (secs > 0 || parts.length === 0) parts.push(`${secs} second${secs !== 1 ? 's' : ''}`);

  return parts.join(', ');
}

/**
 * Format number with thousand separators
 */
export function formatNumber(num: number | string, decimals: number = 0): string {
  const n = typeof num === 'string' ? parseFloat(num) : num;
  if (isNaN(n)) return '0';

  return n.toLocaleString(undefined, {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  });
}

/**
 * Format percentage value
 */
export function formatPercentage(value: number, decimals: number = 1): string {
  if (isNaN(value)) return '0%';

  return `${value.toFixed(decimals)}%`;
}

/**
 * Format bytes to human readable size
 */
export function formatBytes(bytes: number, decimals: number = 2): string {
  if (bytes === 0) return '0 Bytes';
  if (!bytes || bytes < 0) return '';

  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB'];

  const i = Math.floor(Math.log(bytes) / Math.log(k));

  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${safeArrayAccess(sizes, i) ?? 'Bytes'}`;
}

/**
 * Format relative time (e.g., "2 hours ago", "in 3 days")
 */
export function formatRelativeTime(date: Date | string | number | null | undefined): string {
  const d = toValidDate(date);
  if (!d) return '';

  const now = new Date();
  const diffInSeconds = Math.floor((now.getTime() - d.getTime()) / 1000);

  const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });

  if (Math.abs(diffInSeconds) < 60) {
    return rtf.format(-diffInSeconds, 'second');
  } else if (Math.abs(diffInSeconds) < 3600) {
    return rtf.format(-Math.floor(diffInSeconds / 60), 'minute');
  } else if (Math.abs(diffInSeconds) < 86400) {
    return rtf.format(-Math.floor(diffInSeconds / 3600), 'hour');
  } else if (Math.abs(diffInSeconds) < 2592000) {
    return rtf.format(-Math.floor(diffInSeconds / 86400), 'day');
  } else if (Math.abs(diffInSeconds) < 31536000) {
    return rtf.format(-Math.floor(diffInSeconds / 2592000), 'month');
  } else {
    return rtf.format(-Math.floor(diffInSeconds / 31536000), 'year');
  }
}

/**
 * Format duration between two dates
 */
export function formatDuration(start: Date | string | number, end: Date | string | number): string {
  const startDate = toValidDate(start);
  const endDate = toValidDate(end);

  if (!startDate || !endDate) return '';

  const diffInSeconds = Math.abs(endDate.getTime() - startDate.getTime()) / 1000;

  return formatUptime(diffInSeconds);
}

/**
 * Format currency value
 */
export function formatCurrency(
  value: number,
  currency: string = 'USD',
  locale: string = 'en-US'
): string {
  return new Intl.NumberFormat(locale, {
    style: 'currency',
    currency: currency,
  }).format(value);
}

/**
 * Truncate text with ellipsis
 */
export function truncateText(text: string, maxLength: number, suffix: string = '...'): string {
  if (!text || text.length <= maxLength) return text;

  return text.substring(0, maxLength - suffix.length) + suffix;
}

/**
 * Format file size from bytes
 */
export function formatFileSize(bytes: number): string {
  return formatBytes(bytes);
}

/**
 * Parse and format ISO date string to local date
 */
export function parseISODate(isoString: string): Date | null {
  return parseLocalDateString(isoString);
}

/**
 * Temperature unit types
 */
export type TemperatureUnit = 'metric' | 'imperial' | 'standard';

/**
 * Convert temperature from Celsius to the specified unit system.
 * All temperatures are stored internally in Celsius.
 *
 * @param celsius - Temperature in Celsius
 * @param unit - Target unit system: 'metric' (Celsius), 'imperial' (Fahrenheit), 'standard' (Kelvin)
 * @returns Temperature converted to the target unit
 */
export function convertTemperature(celsius: number, unit: TemperatureUnit): number {
  switch (unit) {
    case 'imperial':
      // Celsius to Fahrenheit: C * 9/5 + 32
      return celsius * (9 / 5) + 32;
    case 'standard':
      // Celsius to Kelvin: C + 273.15
      return celsius + 273.15;
    case 'metric':
      // Celsius, no conversion needed
      return celsius;
  }
}

/**
 * Get the temperature unit symbol for display.
 *
 * @param unit - Unit system: 'metric', 'imperial', or 'standard'
 * @returns Unit symbol (°C, °F, or K)
 */
export function getTemperatureSymbol(unit: TemperatureUnit): string {
  switch (unit) {
    case 'imperial':
      return '°F';
    case 'standard':
      return 'K';
    case 'metric':
      return '°C';
  }
}

/**
 * Format temperature for display with conversion and unit symbol.
 * All temperatures are stored internally in Celsius and converted for display.
 *
 * @param celsius - Temperature in Celsius (as stored in database)
 * @param unit - Display unit system: 'metric', 'imperial', or 'standard'
 * @param decimals - Number of decimal places (default: 1)
 * @returns Formatted temperature string with unit (e.g., "72.5°F")
 */
export function formatTemperature(
  celsius: number | undefined | null,
  unit: TemperatureUnit = 'metric',
  decimals: number = 1
): string {
  if (celsius === undefined || celsius === null || isNaN(celsius)) {
    return 'N/A';
  }

  const converted = convertTemperature(celsius, unit);
  const symbol = getTemperatureSymbol(unit);

  return `${converted.toFixed(decimals)}${symbol}`;
}

/**
 * Format temperature for compact display (rounded, no decimals).
 * Useful for badges and small UI elements.
 *
 * @param celsius - Temperature in Celsius
 * @param unit - Display unit system
 * @returns Formatted temperature string (e.g., "73°F")
 */
export function formatTemperatureCompact(
  celsius: number | undefined | null,
  unit: TemperatureUnit = 'metric'
): string {
  if (celsius === undefined || celsius === null || isNaN(celsius)) {
    return '';
  }

  const converted = convertTemperature(celsius, unit);
  const symbol = getTemperatureSymbol(unit);

  return `${Math.round(converted)}${symbol}`;
}

/**
 * Wind speed conversion constant: 1 m/s = 2.23694 mph
 */
const MS_TO_MPH = 2.23694;

/**
 * Convert wind speed from m/s to the specified unit system.
 * All wind speeds are stored internally in meters per second (m/s).
 *
 * @param metersPerSecond - Wind speed in m/s
 * @param unit - Target unit system: 'metric'/'standard' (m/s), 'imperial' (mph)
 * @returns Wind speed converted to the target unit
 */
export function convertWindSpeed(metersPerSecond: number, unit: TemperatureUnit): number {
  switch (unit) {
    case 'imperial':
      // m/s to mph: m/s * 2.23694
      return metersPerSecond * MS_TO_MPH;
    case 'metric':
    case 'standard':
      // m/s, no conversion needed
      return metersPerSecond;
  }
}

/**
 * Get the wind speed unit label for display.
 *
 * @param unit - Unit system: 'metric', 'imperial', or 'standard'
 * @returns Unit label (m/s or mph)
 */
export function getWindSpeedUnit(unit: TemperatureUnit): string {
  switch (unit) {
    case 'imperial':
      return 'mph';
    case 'metric':
    case 'standard':
      return 'm/s';
  }
}

/**
 * Format wind speed for display with conversion and unit label.
 * All wind speeds are stored internally in m/s and converted for display.
 *
 * @param metersPerSecond - Wind speed in m/s (as stored in database)
 * @param unit - Display unit system: 'metric', 'imperial', or 'standard'
 * @param decimals - Number of decimal places (default: 0 for whole numbers)
 * @returns Formatted wind speed string with unit (e.g., "11 mph")
 */
export function formatWindSpeed(
  metersPerSecond: number | undefined | null,
  unit: TemperatureUnit = 'metric',
  decimals: number = 0
): string {
  if (metersPerSecond === undefined || metersPerSecond === null || isNaN(metersPerSecond)) {
    return 'N/A';
  }

  const converted = convertWindSpeed(metersPerSecond, unit);
  const unitLabel = getWindSpeedUnit(unit);

  return `${converted.toFixed(decimals)} ${unitLabel}`;
}
