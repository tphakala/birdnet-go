import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import {
  getLocalDateString,
  isToday,
  isFutureDate,
  parseHour,
  getLocalTimeString,
  parseTime,
} from './date';

describe('Date Utilities', () => {
  // Store original Date constructor
  const OriginalDate = global.Date;

  beforeEach(() => {
    // Reset to real Date
    global.Date = OriginalDate;
  });

  afterEach(() => {
    // Ensure Date is restored
    global.Date = OriginalDate;
    vi.clearAllMocks();
  });

  describe('getLocalDateString', () => {
    it('should format current date correctly', () => {
      const mockDate = new Date('2024-01-15T12:00:00');
      vi.setSystemTime(mockDate);

      expect(getLocalDateString()).toBe('2024-01-15');
    });

    it('should format provided date correctly', () => {
      const date = new Date('2024-12-31T23:59:59');
      expect(getLocalDateString(date)).toBe('2024-12-31');
    });

    it('should pad single digit months and days', () => {
      const date = new Date('2024-01-05T00:00:00');
      expect(getLocalDateString(date)).toBe('2024-01-05');
    });

    it('should handle leap year dates', () => {
      const date = new Date('2024-02-29T12:00:00');
      expect(getLocalDateString(date)).toBe('2024-02-29');
    });

    it('should handle year boundaries', () => {
      const date = new Date('2023-12-31T23:59:59');
      expect(getLocalDateString(date)).toBe('2023-12-31');

      const nextDay = new Date('2024-01-01T00:00:00');
      expect(getLocalDateString(nextDay)).toBe('2024-01-01');
    });
  });

  describe('isToday', () => {
    it("should return true for today's date", () => {
      const mockDate = new Date('2024-01-15T12:00:00');
      vi.setSystemTime(mockDate);

      expect(isToday('2024-01-15')).toBe(true);
    });

    it('should return false for past dates', () => {
      const mockDate = new Date('2024-01-15T12:00:00');
      vi.setSystemTime(mockDate);

      expect(isToday('2024-01-14')).toBe(false);
    });

    it('should return false for future dates', () => {
      const mockDate = new Date('2024-01-15T12:00:00');
      vi.setSystemTime(mockDate);

      expect(isToday('2024-01-16')).toBe(false);
    });

    it('should handle timezone edge cases', () => {
      // Test at 23:59:59 local time
      const mockDate = new Date('2024-01-15T23:59:59');
      vi.setSystemTime(mockDate);

      expect(isToday('2024-01-15')).toBe(true);
      expect(isToday('2024-01-16')).toBe(false);
    });
  });

  describe('isFutureDate', () => {
    it('should return true for future dates', () => {
      const mockDate = new Date('2024-01-15T12:00:00');
      vi.setSystemTime(mockDate);

      expect(isFutureDate('2024-01-16')).toBe(true);
      expect(isFutureDate('2024-12-31')).toBe(true);
    });

    it('should return false for today', () => {
      const mockDate = new Date('2024-01-15T12:00:00');
      vi.setSystemTime(mockDate);

      expect(isFutureDate('2024-01-15')).toBe(false);
    });

    it('should return false for past dates', () => {
      const mockDate = new Date('2024-01-15T12:00:00');
      vi.setSystemTime(mockDate);

      expect(isFutureDate('2024-01-14')).toBe(false);
      expect(isFutureDate('2023-12-31')).toBe(false);
    });
  });

  describe('parseHour', () => {
    it('should parse valid time strings', () => {
      expect(parseHour('14:30:45')).toBe(14);
      expect(parseHour('00:00:00')).toBe(0);
      expect(parseHour('23:59:59')).toBe(23);
      expect(parseHour('09:15:30')).toBe(9);
    });

    it('should parse time strings without seconds', () => {
      expect(parseHour('14:30')).toBe(14);
      expect(parseHour('00:00')).toBe(0);
    });

    it('should parse hour-only strings', () => {
      expect(parseHour('14')).toBe(14);
      expect(parseHour('00')).toBe(0);
      expect(parseHour('23')).toBe(23);
    });

    it('should throw error for invalid input', () => {
      expect(() => parseHour('')).toThrow('Invalid time string: expected non-empty string');
      expect(() => parseHour(null as unknown as string)).toThrow(
        'Invalid time string: expected non-empty string'
      );
      expect(() => parseHour(undefined as unknown as string)).toThrow(
        'Invalid time string: expected non-empty string'
      );
    });

    it('should throw error for invalid hour values', () => {
      expect(() => parseHour('24:00:00')).toThrow('Invalid hour value: "24". Expected 0-23');
      expect(() => parseHour('-1:00:00')).toThrow('Invalid hour value: "-1". Expected 0-23');
      expect(() => parseHour('abc:00:00')).toThrow('Invalid hour value: "abc". Expected 0-23');
    });
  });

  describe('getLocalTimeString', () => {
    it('should format time with seconds by default', () => {
      const date = new Date('2024-01-15T14:30:45');
      expect(getLocalTimeString(date)).toBe('14:30:45');
    });

    it('should format time without seconds when specified', () => {
      const date = new Date('2024-01-15T14:30:45');
      expect(getLocalTimeString(date, false)).toBe('14:30');
    });

    it('should pad single digits', () => {
      const date = new Date('2024-01-15T09:05:03');
      expect(getLocalTimeString(date)).toBe('09:05:03');
      expect(getLocalTimeString(date, false)).toBe('09:05');
    });

    it('should handle midnight', () => {
      const date = new Date('2024-01-15T00:00:00');
      expect(getLocalTimeString(date)).toBe('00:00:00');
    });

    it('should handle end of day', () => {
      const date = new Date('2024-01-15T23:59:59');
      expect(getLocalTimeString(date)).toBe('23:59:59');
    });

    it('should use current time when no date provided', () => {
      const mockDate = new Date('2024-01-15T14:30:45');
      vi.setSystemTime(mockDate);

      expect(getLocalTimeString()).toBe('14:30:45');
    });
  });

  describe('parseTime', () => {
    it('should parse time with seconds', () => {
      expect(parseTime('14:30:45')).toEqual({ hours: 14, minutes: 30, seconds: 45 });
      expect(parseTime('00:00:00')).toEqual({ hours: 0, minutes: 0, seconds: 0 });
      expect(parseTime('23:59:59')).toEqual({ hours: 23, minutes: 59, seconds: 59 });
    });

    it('should parse time without seconds', () => {
      expect(parseTime('14:30')).toEqual({ hours: 14, minutes: 30, seconds: 0 });
      expect(parseTime('00:00')).toEqual({ hours: 0, minutes: 0, seconds: 0 });
      expect(parseTime('23:59')).toEqual({ hours: 23, minutes: 59, seconds: 0 });
    });

    it('should throw error for invalid input', () => {
      expect(() => parseTime('')).toThrow('Invalid time string: expected non-empty string');
      expect(() => parseTime(null as unknown as string)).toThrow(
        'Invalid time string: expected non-empty string'
      );
      expect(() => parseTime(undefined as unknown as string)).toThrow(
        'Invalid time string: expected non-empty string'
      );
    });

    it('should throw error for invalid format', () => {
      expect(() => parseTime('14')).toThrow(
        'Invalid time format: "14". Expected HH:MM or HH:MM:SS'
      );
      expect(() => parseTime('14:30:45:00')).toThrow(
        'Invalid time format: "14:30:45:00". Expected HH:MM or HH:MM:SS'
      );
    });

    it('should throw error for invalid values', () => {
      expect(() => parseTime('24:00:00')).toThrow('Invalid hour value: "24". Expected 0-23');
      expect(() => parseTime('23:60:00')).toThrow('Invalid minute value: "60". Expected 0-59');
      expect(() => parseTime('23:59:60')).toThrow('Invalid second value: "60". Expected 0-59');
      expect(() => parseTime('abc:def:ghi')).toThrow('Invalid hour value: "abc". Expected 0-23');
    });

    it('should handle edge cases', () => {
      expect(parseTime('00:00:00')).toEqual({ hours: 0, minutes: 0, seconds: 0 });
      expect(parseTime('23:59:59')).toEqual({ hours: 23, minutes: 59, seconds: 59 });
      expect(parseTime('12:00')).toEqual({ hours: 12, minutes: 0, seconds: 0 });
    });
  });

  describe('Timezone Edge Cases', () => {
    it('should handle DST transitions', () => {
      // Mock a date during DST transition (varies by timezone)
      // This is a conceptual test - actual DST dates vary by location
      const springForward = new Date('2024-03-10T02:00:00'); // US DST starts
      const fallBack = new Date('2024-11-03T02:00:00'); // US DST ends

      expect(getLocalDateString(springForward)).toBe('2024-03-10');
      expect(getLocalDateString(fallBack)).toBe('2024-11-03');
    });

    it('should maintain consistency across timezone boundaries', () => {
      // Test that our functions work consistently regardless of the system timezone
      // by checking that date strings are formatted based on local date components
      const testDate = new Date(2024, 0, 15, 23, 59, 59); // Jan 15, 2024, 23:59:59 local
      expect(getLocalDateString(testDate)).toBe('2024-01-15');

      // One second later
      const nextDay = new Date(2024, 0, 16, 0, 0, 0); // Jan 16, 2024, 00:00:00 local
      expect(getLocalDateString(nextDay)).toBe('2024-01-16');
    });

    it('should handle dates near midnight correctly', () => {
      const beforeMidnight = new Date('2024-01-15T23:59:59');
      const afterMidnight = new Date('2024-01-16T00:00:01');

      expect(getLocalDateString(beforeMidnight)).toBe('2024-01-15');
      expect(getLocalDateString(afterMidnight)).toBe('2024-01-16');

      // Time formatting
      expect(getLocalTimeString(beforeMidnight)).toBe('23:59:59');
      expect(getLocalTimeString(afterMidnight)).toBe('00:00:01');
    });

    it('should handle month boundaries', () => {
      const endOfMonth = new Date('2024-01-31T23:59:59');
      const startOfNextMonth = new Date('2024-02-01T00:00:00');

      expect(getLocalDateString(endOfMonth)).toBe('2024-01-31');
      expect(getLocalDateString(startOfNextMonth)).toBe('2024-02-01');
    });

    it('should handle year boundaries', () => {
      const endOfYear = new Date('2023-12-31T23:59:59');
      const startOfNewYear = new Date('2024-01-01T00:00:00');

      expect(getLocalDateString(endOfYear)).toBe('2023-12-31');
      expect(getLocalDateString(startOfNewYear)).toBe('2024-01-01');
    });
  });
});
