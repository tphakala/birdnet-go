/**
 * Date utility functions for consistent date handling across the application
 */

/**
 * Format a Date object as YYYY-MM-DD string in the local timezone
 * This avoids timezone conversion issues that occur with toISOString()
 *
 * @param date - The date to format (defaults to current date)
 * @returns Date string in YYYY-MM-DD format
 */
export function getLocalDateString(date: Date = new Date()): string {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

/**
 * Parse a date string (YYYY-MM-DD format) into a Date object safely
 *
 * IMPORTANT: This function specifically handles the JavaScript date parsing quirk where
 * new Date("YYYY-MM-DD") creates a date at midnight UTC, which can appear as the
 * previous day for users in timezones west of UTC.
 *
 * To avoid this timezone shift, YYYY-MM-DD strings are parsed at NOON local time,
 * ensuring the date remains consistent regardless of the user's timezone.
 *
 * @param dateString - Date string in YYYY-MM-DD format, ISO 8601, or Date object
 * @returns Date object representing the date in local timezone, or null if invalid
 *
 * @example
 * // YYYY-MM-DD format - parsed at noon to avoid timezone issues
 * parseLocalDateString('2025-08-25') // Returns Date at noon on Aug 25, 2025
 *
 * // ISO 8601 with time - parsed normally for precise timestamps
 * parseLocalDateString('2025-08-25T10:30:00Z') // Returns the exact date/time
 *
 * // Date object - returned as cloned copy
 * parseLocalDateString(new Date()) // Returns a cloned Date object
 */
export function parseLocalDateString(dateString: string | Date | null | undefined): Date | null {
  if (!dateString) return null;

  // If already a Date object, return a cloned copy to prevent mutation
  if (dateString instanceof Date) {
    return isNaN(dateString.getTime()) ? null : new Date(dateString.getTime());
  }

  // Handle YYYY-MM-DD format specifically to avoid timezone issues
  if (/^\d{4}-\d{2}-\d{2}$/.test(dateString)) {
    // Parse at noon local time to avoid timezone boundary issues
    const date = new Date(dateString + 'T12:00:00');
    return isNaN(date.getTime()) ? null : date;
  }

  // For other formats (ISO 8601 with time), parse normally
  const date = new Date(dateString);
  return isNaN(date.getTime()) ? null : date;
}

/**
 * Check if a date string represents today in the local timezone
 *
 * @param dateString - Date string in YYYY-MM-DD format
 * @returns True if the date is today
 */
export function isToday(dateString: string): boolean {
  return dateString === getLocalDateString();
}

/**
 * Check if a date string represents a future date in the local timezone
 *
 * @param dateString - Date string in YYYY-MM-DD format
 * @returns True if the date is in the future
 */
export function isFutureDate(dateString: string): boolean {
  return dateString > getLocalDateString();
}

/**
 * Parse a time string (HH:MM:SS) and extract the hour component
 *
 * @param timeString - Time string in HH:MM:SS format
 * @returns Hour as a number (0-23)
 * @throws Error if the time string is invalid
 */
export function parseHour(timeString: string): number {
  if (!timeString || typeof timeString !== 'string') {
    throw new Error('Invalid time string: expected non-empty string');
  }

  const parts = timeString.split(':');
  if (parts.length < 1) {
    throw new Error(`Invalid time format: "${timeString}". Expected HH:MM:SS or HH:MM`);
  }

  const hour = parseInt(parts[0], 10);
  if (isNaN(hour) || hour < 0 || hour > 23) {
    throw new Error(`Invalid hour value: "${parts[0]}". Expected 0-23`);
  }

  return hour;
}

/**
 * Format a Date object as HH:MM:SS string in the local timezone
 *
 * @param date - The date to format (defaults to current date)
 * @param includeSeconds - Whether to include seconds (defaults to true)
 * @returns Time string in HH:MM:SS or HH:MM format
 */
export function getLocalTimeString(
  date: Date = new Date(),
  includeSeconds: boolean = true
): string {
  const hours = String(date.getHours()).padStart(2, '0');
  const minutes = String(date.getMinutes()).padStart(2, '0');

  if (!includeSeconds) {
    return `${hours}:${minutes}`;
  }

  const seconds = String(date.getSeconds()).padStart(2, '0');
  return `${hours}:${minutes}:${seconds}`;
}

/**
 * Parse a time string (HH:MM:SS or HH:MM) and return hours, minutes, and seconds
 *
 * @param timeString - Time string in HH:MM:SS or HH:MM format
 * @returns Object with hours, minutes, and seconds (seconds default to 0 if not provided)
 * @throws Error if the time string is invalid
 */
export function parseTime(timeString: string): { hours: number; minutes: number; seconds: number } {
  if (!timeString || typeof timeString !== 'string') {
    throw new Error('Invalid time string: expected non-empty string');
  }

  const parts = timeString.split(':');
  if (parts.length < 2 || parts.length > 3) {
    throw new Error(`Invalid time format: "${timeString}". Expected HH:MM or HH:MM:SS`);
  }

  const hours = parseInt(parts[0], 10);
  const minutes = parseInt(parts[1], 10);
  const seconds = parts.length === 3 ? parseInt(parts[2], 10) : 0;

  if (isNaN(hours) || hours < 0 || hours > 23) {
    throw new Error(`Invalid hour value: "${parts[0]}". Expected 0-23`);
  }
  if (isNaN(minutes) || minutes < 0 || minutes > 59) {
    throw new Error(`Invalid minute value: "${parts[1]}". Expected 0-59`);
  }
  if (isNaN(seconds) || seconds < 0 || seconds > 59) {
    throw new Error(`Invalid second value: "${parts[2] || '0'}". Expected 0-59`);
  }

  return { hours, minutes, seconds };
}

/**
 * Format a Date object as a local date-time string (YYYY-MM-DD HH:MM:SS)
 * This avoids timezone conversion issues and provides consistent formatting
 *
 * @param date - The date to format
 * @param includeSeconds - Whether to include seconds in the time (defaults to true)
 * @returns Date-time string in local timezone
 */
export function formatLocalDateTime(date: Date, includeSeconds: boolean = true): string {
  const dateString = getLocalDateString(date);
  const timeString = getLocalTimeString(date, includeSeconds);
  return `${dateString} ${timeString}`;
}

/**
 * Get the previous day as a YYYY-MM-DD string
 *
 * @param dateString - Date string in YYYY-MM-DD format
 * @returns Previous day in YYYY-MM-DD format
 * @throws Error if the date string is invalid
 *
 * @example
 * getPreviousDay('2025-08-25') // Returns '2025-08-24'
 * getPreviousDay('2025-01-01') // Returns '2024-12-31'
 */
export function getPreviousDay(dateString: string): string {
  const date = parseLocalDateString(dateString);
  if (!date) throw new Error(`Invalid date string: ${dateString}`);
  date.setDate(date.getDate() - 1);
  return getLocalDateString(date);
}

/**
 * Get the next day as a YYYY-MM-DD string
 *
 * @param dateString - Date string in YYYY-MM-DD format
 * @returns Next day in YYYY-MM-DD format
 * @throws Error if the date string is invalid
 *
 * @example
 * getNextDay('2025-08-25') // Returns '2025-08-26'
 * getNextDay('2024-12-31') // Returns '2025-01-01'
 */
export function getNextDay(dateString: string): string {
  const date = parseLocalDateString(dateString);
  if (!date) throw new Error(`Invalid date string: ${dateString}`);
  date.setDate(date.getDate() + 1);
  return getLocalDateString(date);
}

/**
 * Add or subtract days from a date string
 *
 * @param dateString - Date string in YYYY-MM-DD format
 * @param days - Number of days to add (positive) or subtract (negative)
 * @returns New date in YYYY-MM-DD format
 * @throws Error if the date string is invalid
 *
 * @example
 * addDays('2025-08-25', 7) // Returns '2025-09-01'
 * addDays('2025-08-25', -7) // Returns '2025-08-18'
 */
export function addDays(dateString: string, days: number): string {
  // Validate that days is an integer
  if (typeof days !== 'number' || !Number.isFinite(days) || !Number.isInteger(days)) {
    throw new TypeError('days must be an integer');
  }

  const date = parseLocalDateString(dateString);
  if (!date) throw new Error(`Invalid date string: ${dateString}`);
  date.setDate(date.getDate() + days);
  return getLocalDateString(date);
}
