/**
 * Date persistence utilities for dashboard date selection
 * Provides hybrid URL + localStorage persistence with configurable expiration
 */

import { getLocalDateString, isFutureDate, parseLocalDateString } from './date';
import { loggers } from './logger';

const logger = loggers.ui;

// Configuration constants
export const DATE_STORAGE_KEY = 'dashboard-selected-date';
export const DATE_URL_PARAM = 'date';
export const DATE_RETENTION_MINUTES = 30; // Configurable retention time (15-30 minutes recommended)
export const DATE_RETENTION_MS = DATE_RETENTION_MINUTES * 60 * 1000;

// TypeScript interfaces
interface StoredDate {
  date: string;
  timestamp: number;
}

interface DatePersistenceConfig {
  storageKey?: string;
  urlParam?: string;
  retentionMs?: number;
}

/**
 * Get date from URL search parameters
 * @param urlParam - The URL parameter name to check (default: 'date')
 * @returns The date string if valid, null otherwise
 */
export function getDateFromURL(urlParam: string = DATE_URL_PARAM): string | null {
  if (typeof window === 'undefined') return null;

  try {
    const params = new URLSearchParams(window.location.search);
    const dateStr = params.get(urlParam);

    if (!dateStr) return null;

    // Validate the date format and ensure it's not in the future
    const parsedDate = parseLocalDateString(dateStr);
    if (!parsedDate || isFutureDate(dateStr)) {
      logger.debug('Invalid or future date in URL', { date: dateStr });
      return null;
    }

    return dateStr;
  } catch (error) {
    logger.error('Error parsing date from URL', error);
    return null;
  }
}

/**
 * Update URL with date parameter without page reload
 * @param date - The date string to set in URL
 * @param urlParam - The URL parameter name to use (default: 'date')
 */
export function updateURLWithDate(date: string, urlParam: string = DATE_URL_PARAM): void {
  if (typeof window === 'undefined') return;

  try {
    const url = new URL(window.location.href);
    const currentDate = getLocalDateString();

    // If the date is today, remove the parameter for cleaner URL
    if (date === currentDate) {
      url.searchParams.delete(urlParam);
    } else {
      url.searchParams.set(urlParam, date);
    }

    // Update URL without reload using History API
    window.history.replaceState(null, '', url.toString());
  } catch (error) {
    logger.error('Error updating URL with date', error);
  }
}

/**
 * Get stored date from localStorage if still within retention period
 * @param storageKey - The localStorage key to use
 * @param retentionMs - The retention period in milliseconds
 * @returns The stored date if valid and recent, null otherwise
 */
export function getStoredDate(
  storageKey: string = DATE_STORAGE_KEY,
  retentionMs: number = DATE_RETENTION_MS
): string | null {
  if (typeof window === 'undefined') return null;

  try {
    const storedValue = window.localStorage.getItem(storageKey);
    if (!storedValue) return null;

    const stored: StoredDate = JSON.parse(storedValue);

    // Validate the stored object structure
    if (!stored.date || typeof stored.date !== 'string' || typeof stored.timestamp !== 'number') {
      logger.debug('Invalid stored date format', { stored });
      return null;
    }

    // Check if within retention period
    const age = Date.now() - stored.timestamp;
    // For zero retention, treat any stored date as expired
    if (retentionMs === 0 || age > retentionMs) {
      logger.debug('Stored date expired', {
        date: stored.date,
        ageMinutes: Math.round(age / 60000),
      });
      // Clean up expired entry
      window.localStorage.removeItem(storageKey);
      return null;
    }

    // Validate the date is not in the future
    if (isFutureDate(stored.date)) {
      logger.debug('Stored date is in the future', { date: stored.date });
      window.localStorage.removeItem(storageKey);
      return null;
    }

    // Validate date format
    const parsedDate = parseLocalDateString(stored.date);
    if (!parsedDate) {
      logger.debug('Invalid stored date format', { date: stored.date });
      window.localStorage.removeItem(storageKey);
      return null;
    }

    return stored.date;
  } catch (error) {
    logger.error('Error reading stored date', error);
    // Clean up corrupted entry
    try {
      window.localStorage.removeItem(storageKey);
    } catch {
      // Ignore cleanup errors
    }
    return null;
  }
}

/**
 * Store date in localStorage with current timestamp
 * @param date - The date string to store
 * @param storageKey - The localStorage key to use
 */
export function setStoredDate(date: string, storageKey: string = DATE_STORAGE_KEY): void {
  if (typeof window === 'undefined') return;

  try {
    const stored: StoredDate = {
      date,
      timestamp: Date.now(),
    };

    window.localStorage.setItem(storageKey, JSON.stringify(stored));
  } catch (error) {
    // Handle storage quota exceeded or other errors
    logger.error('Error storing date', error);
  }
}

/**
 * Clear stored date from localStorage
 * @param storageKey - The localStorage key to clear
 */
export function clearStoredDate(storageKey: string = DATE_STORAGE_KEY): void {
  if (typeof window === 'undefined') return;

  try {
    window.localStorage.removeItem(storageKey);
  } catch (error) {
    logger.error('Error clearing stored date', error);
  }
}

/**
 * Get initial date using priority: URL > Recent Storage > Current Date
 * @param config - Optional configuration for storage and URL parameters
 * @returns The determined date string
 */
export function getInitialDate(config?: DatePersistenceConfig): string {
  const {
    storageKey = DATE_STORAGE_KEY,
    urlParam = DATE_URL_PARAM,
    retentionMs = DATE_RETENTION_MS,
  } = config ?? {};

  // 1. Check URL first (explicit user intent)
  const urlDate = getDateFromURL(urlParam);
  if (urlDate) {
    logger.debug('Using date from URL', { date: urlDate });
    return urlDate;
  }

  // 2. Check recent local storage (smart sticky within retention period)
  const storedDate = getStoredDate(storageKey, retentionMs);
  if (storedDate) {
    logger.debug('Using stored date', { date: storedDate });
    return storedDate;
  }

  // 3. Fallback to current date
  const currentDate = getLocalDateString();
  logger.debug('Using current date (fallback)', { date: currentDate });
  return currentDate;
}

/**
 * Persist date to both URL and localStorage
 * @param date - The date string to persist
 * @param config - Optional configuration for storage and URL parameters
 */
export function persistDate(date: string, config?: DatePersistenceConfig): void {
  const { storageKey = DATE_STORAGE_KEY, urlParam = DATE_URL_PARAM } = config ?? {};

  // Update URL
  updateURLWithDate(date, urlParam);

  // Store in localStorage
  setStoredDate(date, storageKey);
}

/**
 * Create a date persistence manager for use in components
 * Provides a convenient interface for date persistence operations
 */
export function createDatePersistence(config?: DatePersistenceConfig) {
  return {
    getInitial: () => getInitialDate(config),
    persist: (date: string) => persistDate(date, config),
    clear: () => clearStoredDate(config?.storageKey),
    getFromURL: () => getDateFromURL(config?.urlParam),
    getFromStorage: () => getStoredDate(config?.storageKey, config?.retentionMs),
  };
}
