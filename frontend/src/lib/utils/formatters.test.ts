/**
 * Tests for date and time formatting utilities
 */

import { describe, it, expect } from 'vitest';
import {
  formatDate,
  formatDateTime,
  formatDateForInput,
  formatTime,
  formatUptime,
  formatNumber,
  formatPercentage,
  formatBytes,
  formatRelativeTime,
  formatDuration,
  formatCurrency,
  truncateText,
  parseISODate,
} from './formatters';

describe('formatters', () => {
  describe('formatDate', () => {
    it('formats Date objects correctly', () => {
      const date = new Date('2024-08-25T15:30:00');
      const result = formatDate(date);
      expect(result).toBeTruthy();
      expect(result).toMatch(/\d{1,2}\/\d{1,2}\/\d{4}/); // Basic date format check
    });

    it('formats YYYY-MM-DD strings correctly without timezone shift', () => {
      const result = formatDate('2024-08-25');
      expect(result).toBeTruthy();
      // The exact format depends on locale, but should contain the date
      expect(result).toMatch(/25|2024/);
    });

    it('formats ISO strings correctly', () => {
      const result = formatDate('2024-08-25T15:30:00Z');
      expect(result).toBeTruthy();
    });

    it('formats timestamps correctly', () => {
      const timestamp = new Date('2024-08-25T15:30:00').getTime();
      const result = formatDate(timestamp);
      expect(result).toBeTruthy();
    });

    it('returns empty string for invalid dates', () => {
      expect(formatDate('invalid')).toBe('');
      expect(formatDate('2024-13-45')).toBe('');
      expect(formatDate('')).toBe('');
    });

    it('handles null and undefined', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      expect(formatDate(null as any)).toBe('');
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      expect(formatDate(undefined as any)).toBe('');
    });
  });

  describe('formatDateTime', () => {
    it('formats Date objects correctly', () => {
      const date = new Date('2024-08-25T15:30:00');
      const result = formatDateTime(date);
      expect(result).toBeTruthy();
      expect(result).toMatch(/\d{1,2}\/\d{1,2}\/\d{4}/); // Should contain date
      expect(result).toMatch(/\d{1,2}:\d{2}/); // Should contain time
    });

    it('formats YYYY-MM-DD strings correctly without timezone shift', () => {
      const result = formatDateTime('2024-08-25');
      expect(result).toBeTruthy();
      // Should show noon time due to parseLocalDateString behavior
      expect(result).toMatch(/12:00|noon/i);
    });

    it('returns empty string for invalid dates', () => {
      expect(formatDateTime('invalid')).toBe('');
    });
  });

  describe('formatDateForInput', () => {
    it('formats Date objects to YYYY-MM-DD', () => {
      const date = new Date('2024-08-25T15:30:00');
      const result = formatDateForInput(date);
      expect(result).toBe('2024-08-25');
    });

    it('formats YYYY-MM-DD strings correctly without timezone shift', () => {
      const result = formatDateForInput('2024-08-25');
      expect(result).toBe('2024-08-25');
    });

    it('formats timestamps correctly', () => {
      const timestamp = new Date('2024-08-25T15:30:00').getTime();
      const result = formatDateForInput(timestamp);
      expect(result).toBe('2024-08-25');
    });

    it('returns empty string for null/undefined', () => {
      expect(formatDateForInput(null)).toBe('');
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      expect(formatDateForInput(undefined as any)).toBe('');
    });

    it('returns empty string for invalid dates', () => {
      expect(formatDateForInput('invalid')).toBe('');
      expect(formatDateForInput('')).toBe('');
    });

    it('handles edge cases around timezone boundaries', () => {
      // Test that YYYY-MM-DD strings don't shift due to timezone
      const result1 = formatDateForInput('2024-01-01');
      const result2 = formatDateForInput('2024-12-31');
      expect(result1).toBe('2024-01-01');
      expect(result2).toBe('2024-12-31');
    });
  });

  describe('formatTime', () => {
    it('formats seconds to MM:SS', () => {
      expect(formatTime(65)).toBe('1:05');
      expect(formatTime(125)).toBe('2:05');
    });

    it('formats seconds to HH:MM:SS', () => {
      expect(formatTime(3665)).toBe('1:01:05');
      expect(formatTime(7325)).toBe('2:02:05');
    });

    it('handles zero and negative values', () => {
      expect(formatTime(0)).toBe('0:00');
      expect(formatTime(-10)).toBe('0:00');
    });

    it('handles null and undefined', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      expect(formatTime(null as any)).toBe('0:00');
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      expect(formatTime(undefined as any)).toBe('0:00');
    });
  });

  describe('formatUptime', () => {
    it('formats seconds to human readable', () => {
      expect(formatUptime(61)).toBe('1 minute, 1 second');
      expect(formatUptime(3661)).toBe('1 hour, 1 minute, 1 second');
      expect(formatUptime(90061)).toBe('1 day, 1 hour, 1 minute, 1 second');
    });

    it('handles plurals correctly', () => {
      expect(formatUptime(120)).toBe('2 minutes');
      expect(formatUptime(7200)).toBe('2 hours');
    });

    it('handles zero and negative values', () => {
      expect(formatUptime(0)).toBe('0 seconds');
      expect(formatUptime(-10)).toBe('0 seconds');
    });
  });

  describe('formatNumber', () => {
    it('formats numbers with default decimals', () => {
      expect(formatNumber(1234)).toMatch(/1,?234/);
    });

    it('formats numbers with specified decimals', () => {
      expect(formatNumber(1234.5, 2)).toMatch(/1,?234\.50/);
    });

    it('handles string inputs', () => {
      expect(formatNumber('1234.5', 1)).toMatch(/1,?234\.5/);
    });

    it('returns 0 for invalid inputs', () => {
      expect(formatNumber('invalid')).toBe('0');
      expect(formatNumber(NaN)).toBe('0');
    });
  });

  describe('formatPercentage', () => {
    it('formats percentage with default decimals', () => {
      expect(formatPercentage(45.678)).toBe('45.7%');
    });

    it('formats percentage with specified decimals', () => {
      expect(formatPercentage(45.678, 2)).toBe('45.68%');
    });

    it('handles NaN values', () => {
      expect(formatPercentage(NaN)).toBe('0%');
    });
  });

  describe('formatBytes', () => {
    it('formats bytes to readable sizes', () => {
      expect(formatBytes(0)).toBe('0 Bytes');
      expect(formatBytes(1024)).toBe('1 KB');
      expect(formatBytes(1048576)).toBe('1 MB');
      expect(formatBytes(1073741824)).toBe('1 GB');
    });

    it('handles decimals correctly', () => {
      expect(formatBytes(1536, 1)).toBe('1.5 KB');
    });

    it('handles negative and null values', () => {
      expect(formatBytes(-100)).toBe('');
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      expect(formatBytes(null as any)).toBe('');
    });
  });

  describe('formatRelativeTime', () => {
    it('formats recent times correctly', () => {
      const now = new Date();
      const fiveMinutesAgo = new Date(now.getTime() - 5 * 60 * 1000);

      const result = formatRelativeTime(fiveMinutesAgo);
      expect(result).toMatch(/5 minutes? ago|in 5 minutes?/);
    });

    it('handles YYYY-MM-DD strings without timezone shift', () => {
      const today = new Date().toISOString().split('T')[0];
      const result = formatRelativeTime(today);
      expect(result).toBeTruthy();
      // Should not be "yesterday" due to timezone shift
      expect(result).not.toMatch(/yesterday/i);
    });

    it('returns empty string for invalid dates', () => {
      expect(formatRelativeTime('invalid')).toBe('');
    });
  });

  describe('formatDuration', () => {
    it('formats duration between dates', () => {
      const start = '2024-08-25T10:00:00';
      const end = '2024-08-25T11:30:00';

      const result = formatDuration(start, end);
      expect(result).toBe('1 hour, 30 minutes');
    });

    it('handles YYYY-MM-DD strings correctly', () => {
      const result = formatDuration('2024-08-25', '2024-08-26');
      expect(result).toBe('1 day');
    });

    it('returns empty string for invalid dates', () => {
      expect(formatDuration('invalid', '2024-08-25')).toBe('');
      expect(formatDuration('2024-08-25', 'invalid')).toBe('');
    });
  });

  describe('formatCurrency', () => {
    it('formats currency with defaults', () => {
      const result = formatCurrency(1234.56);
      expect(result).toMatch(/\$1,?234\.56/);
    });

    it('formats currency with different locale', () => {
      const result = formatCurrency(1234.56, 'EUR', 'de-DE');
      expect(result).toMatch(/1\.?234,56\s?€|€\s?1\.?234,56/);
    });
  });

  describe('truncateText', () => {
    it('truncates text correctly', () => {
      expect(truncateText('Hello World', 8)).toBe('Hello...');
    });

    it('returns original text if under limit', () => {
      expect(truncateText('Hello', 10)).toBe('Hello');
    });

    it('uses custom suffix', () => {
      expect(truncateText('Hello World', 8, ' [more]')).toBe('H [more]');
    });

    it('handles empty strings', () => {
      expect(truncateText('', 5)).toBe('');
    });
  });

  describe('parseISODate', () => {
    it('parses YYYY-MM-DD strings correctly', () => {
      const result = parseISODate('2024-08-25');
      expect(result).toBeInstanceOf(Date);
      expect(result?.getFullYear()).toBe(2024);
      expect(result?.getMonth()).toBe(7); // 0-indexed
      expect(result?.getDate()).toBe(25);
    });

    it('parses ISO strings correctly', () => {
      const result = parseISODate('2024-08-25T15:30:00Z');
      expect(result).toBeInstanceOf(Date);
    });

    it('returns null for invalid dates', () => {
      expect(parseISODate('invalid')).toBe(null);
      expect(parseISODate('')).toBe(null);
    });

    it('handles timezone edge cases', () => {
      const result = parseISODate('2024-01-01');
      expect(result?.getDate()).toBe(1); // Should not shift to previous day
    });
  });

  describe('timezone edge cases', () => {
    it('maintains date consistency across timezone boundaries', () => {
      // Test that parsing "2024-01-01" always results in January 1st,
      // regardless of system timezone
      const dateString = '2024-01-01';

      const parsedDate = parseISODate(dateString);
      expect(parsedDate).not.toBeNull();
      const formattedBack = formatDateForInput(parsedDate as Date);

      expect(formattedBack).toBe(dateString);
    });

    it('handles New Year boundaries correctly', () => {
      const dec31 = '2023-12-31';
      const jan1 = '2024-01-01';

      const parsedDec31 = parseISODate(dec31);
      const parsedJan1 = parseISODate(jan1);

      expect(parsedDec31).not.toBeNull();
      expect(parsedJan1).not.toBeNull();
      expect(formatDateForInput(parsedDec31 as Date)).toBe(dec31);
      expect(formatDateForInput(parsedJan1 as Date)).toBe(jan1);
    });
  });
});
