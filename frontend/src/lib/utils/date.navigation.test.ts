/**
 * Tests for date navigation utility functions
 */

import { describe, it, expect } from 'vitest';
import { getPreviousDay, getNextDay, addDays } from './date';

describe('Date Navigation Utilities', () => {
  describe('getPreviousDay', () => {
    it('gets previous day correctly', () => {
      expect(getPreviousDay('2025-08-25')).toBe('2025-08-24');
      expect(getPreviousDay('2025-08-10')).toBe('2025-08-09');
    });

    it('handles month boundaries', () => {
      expect(getPreviousDay('2025-09-01')).toBe('2025-08-31');
      expect(getPreviousDay('2025-03-01')).toBe('2025-02-28'); // Non-leap year
      expect(getPreviousDay('2024-03-01')).toBe('2024-02-29'); // Leap year
    });

    it('handles year boundaries', () => {
      expect(getPreviousDay('2025-01-01')).toBe('2024-12-31');
      expect(getPreviousDay('2000-01-01')).toBe('1999-12-31');
    });

    it('throws on invalid date', () => {
      expect(() => getPreviousDay('invalid')).toThrow(/^Invalid date string/);
      expect(() => getPreviousDay('')).toThrow(/^Invalid date string/);
    });
  });

  describe('getNextDay', () => {
    it('gets next day correctly', () => {
      expect(getNextDay('2025-08-25')).toBe('2025-08-26');
      expect(getNextDay('2025-08-10')).toBe('2025-08-11');
    });

    it('handles month boundaries', () => {
      expect(getNextDay('2025-08-31')).toBe('2025-09-01');
      expect(getNextDay('2025-02-28')).toBe('2025-03-01'); // Non-leap year
      expect(getNextDay('2024-02-28')).toBe('2024-02-29'); // Leap year
      expect(getNextDay('2024-02-29')).toBe('2024-03-01'); // Leap year Feb 29
    });

    it('handles year boundaries', () => {
      expect(getNextDay('2024-12-31')).toBe('2025-01-01');
      expect(getNextDay('1999-12-31')).toBe('2000-01-01');
    });

    it('throws on invalid date', () => {
      expect(() => getNextDay('invalid')).toThrow(/^Invalid date string/);
      expect(() => getNextDay('')).toThrow(/^Invalid date string/);
    });
  });

  describe('addDays', () => {
    it('adds positive days correctly', () => {
      expect(addDays('2025-08-25', 1)).toBe('2025-08-26');
      expect(addDays('2025-08-25', 7)).toBe('2025-09-01');
      expect(addDays('2025-08-25', 30)).toBe('2025-09-24');
      expect(addDays('2025-08-25', 365)).toBe('2026-08-25');
    });

    it('subtracts negative days correctly', () => {
      expect(addDays('2025-08-25', -1)).toBe('2025-08-24');
      expect(addDays('2025-08-25', -7)).toBe('2025-08-18');
      expect(addDays('2025-08-25', -30)).toBe('2025-07-26');
      expect(addDays('2025-08-25', -365)).toBe('2024-08-25');
    });

    it('handles zero days', () => {
      expect(addDays('2025-08-25', 0)).toBe('2025-08-25');
    });

    it('handles month boundaries', () => {
      expect(addDays('2025-08-31', 1)).toBe('2025-09-01');
      expect(addDays('2025-09-01', -1)).toBe('2025-08-31');
      expect(addDays('2025-02-28', 1)).toBe('2025-03-01'); // Non-leap year
      expect(addDays('2024-02-28', 1)).toBe('2024-02-29'); // Leap year
    });

    it('handles year boundaries', () => {
      expect(addDays('2024-12-25', 10)).toBe('2025-01-04');
      expect(addDays('2025-01-05', -10)).toBe('2024-12-26');
      expect(addDays('2024-12-31', 1)).toBe('2025-01-01');
      expect(addDays('2025-01-01', -1)).toBe('2024-12-31');
    });

    it('handles large jumps correctly', () => {
      expect(addDays('2025-01-01', 1000)).toBe('2027-09-28');
      expect(addDays('2025-01-01', -1000)).toBe('2022-04-07');
    });

    it('throws on invalid date', () => {
      expect(() => addDays('invalid', 1)).toThrow(/^Invalid date string/);
      expect(() => addDays('', 1)).toThrow(/^Invalid date string/);
    });
  });

  describe('navigation consistency', () => {
    it('getPreviousDay and getNextDay are inverses', () => {
      const date = '2025-08-25';
      expect(getNextDay(getPreviousDay(date))).toBe(date);
      expect(getPreviousDay(getNextDay(date))).toBe(date);
    });

    it('addDays with opposite values cancel out', () => {
      const date = '2025-08-25';
      expect(addDays(addDays(date, 10), -10)).toBe(date);
      expect(addDays(addDays(date, -7), 7)).toBe(date);
    });

    it('getPreviousDay equals addDays with -1', () => {
      const date = '2025-08-25';
      expect(getPreviousDay(date)).toBe(addDays(date, -1));
    });

    it('getNextDay equals addDays with 1', () => {
      const date = '2025-08-25';
      expect(getNextDay(date)).toBe(addDays(date, 1));
    });
  });
});
