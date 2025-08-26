/**
 * Date and time formatting utilities
 */

import { safeArrayAccess } from './security';
import { parseLocalDateString } from './date';

/**
 * Format date to locale string
 */
export function formatDate(date: Date | string | number | null | undefined): string {
  if (date === null || date === undefined) return '';

  const d = typeof date === 'string' ? parseLocalDateString(date) : new Date(date);
  if (!d || isNaN(d.getTime())) return '';

  return d.toLocaleDateString();
}

/**
 * Format date and time to locale string
 */
export function formatDateTime(date: Date | string | number | null | undefined): string {
  if (date === null || date === undefined) return '';

  const d = typeof date === 'string' ? parseLocalDateString(date) : new Date(date);
  if (!d || isNaN(d.getTime())) return '';

  return d.toLocaleString();
}

/**
 * Format date for HTML input (YYYY-MM-DD)
 */
export function formatDateForInput(date: Date | string | number | null): string {
  if (!date) return '';

  const d = typeof date === 'string' ? parseLocalDateString(date) : new Date(date);
  if (!d || isNaN(d.getTime())) return '';

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
  if (date === null || date === undefined) return '';

  const d = typeof date === 'string' ? parseLocalDateString(date) : new Date(date);
  if (!d || isNaN(d.getTime())) return '';

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
  const startDate = typeof start === 'string' ? parseLocalDateString(start) : new Date(start);
  const endDate = typeof end === 'string' ? parseLocalDateString(end) : new Date(end);

  if (!startDate || !endDate || isNaN(startDate.getTime()) || isNaN(endDate.getTime())) return '';

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
