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
 */
export function parseHour(timeString: string): number {
  return parseInt(timeString.split(':')[0], 10);
}