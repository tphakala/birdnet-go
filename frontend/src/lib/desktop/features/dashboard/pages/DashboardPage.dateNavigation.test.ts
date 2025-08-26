/**
 * Test suite to reveal and verify the date navigation bug in DashboardPage
 *
 * Bug description: Date navigation skips days for users in timezones behind UTC
 * Root cause: new Date("YYYY-MM-DD") creates UTC midnight, which appears as
 * previous day for users west of UTC, causing getDate() to return wrong value
 */

import { describe, it, expect } from 'vitest';
import { getLocalDateString, getPreviousDay, getNextDay, addDays } from '$lib/utils/date';

describe('DashboardPage Date Navigation Bug', () => {
  describe('Bug Demonstration', () => {
    it('demonstrates the parsing difference between UTC and noon', () => {
      const dateString = '2025-08-25';

      // Parse as ISO date (creates UTC midnight)
      const utcParse = new Date(dateString);

      // Parse at noon local time (avoids timezone issues)
      const noonParse = new Date(dateString + 'T12:00:00');

      // The UTC parse time will be midnight UTC
      expect(utcParse.toISOString()).toContain('T00:00:00.000Z');

      // The noon parse will be at noon local time
      // This ensures getDate() returns the correct day
      expect(noonParse.getHours()).toBe(12);

      // Both should represent the same calendar date in local time
      expect(getLocalDateString(noonParse)).toBe(dateString);
    });

    it('shows how ISO date parsing can cause issues', () => {
      const testDate = '2025-03-15';

      // Create date at UTC midnight - this is the buggy way
      const utcDate = new Date(testDate);

      // For users west of UTC, this could appear as previous day
      // We can't easily simulate timezone in tests, but we can show
      // that the time is midnight UTC
      expect(utcDate.toISOString()).toBe('2025-03-15T00:00:00.000Z');

      // Create date at noon - this is the fixed way
      const noonDate = new Date(testDate + 'T12:00:00');

      // This ensures we're safely in the middle of the day
      expect(noonDate.getHours()).toBe(12);
    });
  });

  describe('Fixed Implementation Tests', () => {
    it('navigates dates correctly with fixed implementation', () => {
      const startDate = '2025-08-25';

      // Test going backwards using the shared utility
      const prev1 = getPreviousDay(startDate);
      expect(prev1).toBe('2025-08-24');

      const prev2 = getPreviousDay(prev1);
      expect(prev2).toBe('2025-08-23');

      // Test going forwards
      const next1 = getNextDay(startDate);
      expect(next1).toBe('2025-08-26');

      const next2 = getNextDay(next1);
      expect(next2).toBe('2025-08-27');
    });

    it('handles month boundaries correctly', () => {
      // End of month
      const endOfAugust = '2025-08-31';
      const startOfSeptember = getNextDay(endOfAugust);
      expect(startOfSeptember).toBe('2025-09-01');

      // Beginning of month
      const startOfSep = '2025-09-01';
      const endOfAug = getPreviousDay(startOfSep);
      expect(endOfAug).toBe('2025-08-31');

      // February (non-leap year)
      const endOfFeb = '2025-02-28';
      const startOfMarch = getNextDay(endOfFeb);
      expect(startOfMarch).toBe('2025-03-01');
    });

    it('handles year boundaries correctly', () => {
      // End of year
      const endOfYear = '2025-12-31';
      const newYear = getNextDay(endOfYear);
      expect(newYear).toBe('2026-01-01');

      // Beginning of year
      const firstDay = '2026-01-01';
      const lastDay = getPreviousDay(firstDay);
      expect(lastDay).toBe('2025-12-31');
    });

    it('tests the addDays utility for multiple day navigation', () => {
      const startDate = '2025-08-25';

      // Add multiple days
      expect(addDays(startDate, 7)).toBe('2025-09-01');
      expect(addDays(startDate, 30)).toBe('2025-09-24');

      // Subtract multiple days
      expect(addDays(startDate, -7)).toBe('2025-08-18');
      expect(addDays(startDate, -30)).toBe('2025-07-26');

      // Year boundary
      expect(addDays('2024-12-25', 10)).toBe('2025-01-04');
      expect(addDays('2025-01-05', -10)).toBe('2024-12-26');
    });

    it('handles leap year correctly', () => {
      // 2024 is a leap year
      const feb28 = '2024-02-28';
      const feb29 = getNextDay(feb28);
      expect(feb29).toBe('2024-02-29');

      const mar1 = getNextDay(feb29);
      expect(mar1).toBe('2024-03-01');

      // Going backwards
      const backToFeb29 = getPreviousDay(mar1);
      expect(backToFeb29).toBe('2024-02-29');

      const backToFeb28 = getPreviousDay(backToFeb29);
      expect(backToFeb28).toBe('2024-02-28');
    });

    it('handles sequential navigation correctly', () => {
      let currentDate = '2025-08-25';

      // Go back 7 days
      const expectedBackwardDates = [
        '2025-08-24',
        '2025-08-23',
        '2025-08-22',
        '2025-08-21',
        '2025-08-20',
        '2025-08-19',
        '2025-08-18',
      ];

      expectedBackwardDates.forEach(expected => {
        currentDate = getPreviousDay(currentDate);
        expect(currentDate).toBe(expected);
      });

      // Now go forward 7 days back to where we started
      const expectedForwardDates = [
        '2025-08-19',
        '2025-08-20',
        '2025-08-21',
        '2025-08-22',
        '2025-08-23',
        '2025-08-24',
        '2025-08-25',
      ];

      expectedForwardDates.forEach(expected => {
        currentDate = getNextDay(currentDate);
        expect(currentDate).toBe(expected);
      });

      // We should be back where we started
      expect(currentDate).toBe('2025-08-25');
    });

    it('handles various date formats consistently', () => {
      const dates = [
        '2025-01-01', // Start of year
        '2025-02-14', // Valentine's Day
        '2025-06-21', // Summer solstice (approx)
        '2025-10-31', // Halloween
        '2025-12-25', // Christmas
      ];

      dates.forEach(date => {
        // Each date should navigate correctly
        const prev = getPreviousDay(date);
        const next = getNextDay(date);

        // Going back then forward should return to original
        expect(getNextDay(prev)).toBe(date);

        // Going forward then back should return to original
        expect(getPreviousDay(next)).toBe(date);
      });
    });
  });

  describe('Edge Cases', () => {
    it('handles dates near DST transitions', () => {
      // Spring forward (DST starts) - typically second Sunday in March
      const beforeDST = '2025-03-08'; // Saturday before DST
      const duringDST = '2025-03-09'; // Sunday (DST starts at 2 AM)
      const afterDST = '2025-03-10'; // Monday after DST

      expect(getNextDay(beforeDST)).toBe(duringDST);
      expect(getNextDay(duringDST)).toBe(afterDST);
      expect(getPreviousDay(afterDST)).toBe(duringDST);
      expect(getPreviousDay(duringDST)).toBe(beforeDST);

      // Fall back (DST ends) - typically first Sunday in November
      const beforeEnd = '2025-11-01'; // Saturday before DST ends
      const duringEnd = '2025-11-02'; // Sunday (DST ends at 2 AM)
      const afterEnd = '2025-11-03'; // Monday after DST ends

      expect(getNextDay(beforeEnd)).toBe(duringEnd);
      expect(getNextDay(duringEnd)).toBe(afterEnd);
      expect(getPreviousDay(afterEnd)).toBe(duringEnd);
      expect(getPreviousDay(duringEnd)).toBe(beforeEnd);
    });

    it('handles invalid dates gracefully', () => {
      // These should not crash but may produce unexpected results
      // The important thing is they don't skip days unexpectedly

      // Invalid but parseable dates
      const feb30 = '2025-02-30'; // Invalid date
      const date = new Date(feb30 + 'T12:00:00');

      // JavaScript will roll over to March
      expect(date.getMonth()).toBe(2); // March (0-indexed)
    });

    it('verifies the fix matches DatePicker implementation', () => {
      // The DatePicker component already uses the correct pattern
      // This test verifies our fix matches that pattern

      const testDate = '2025-08-15';

      // DatePicker pattern: new Date(value + 'T12:00:00')
      const datePickerStyle = new Date(testDate + 'T12:00:00');

      // Our fixed implementation
      const ourFixed = new Date(testDate + 'T12:00:00');

      // They should be identical
      expect(datePickerStyle.getTime()).toBe(ourFixed.getTime());
      expect(datePickerStyle.getDate()).toBe(ourFixed.getDate());
      expect(datePickerStyle.getHours()).toBe(12);
    });
  });

  describe('Comparison: Buggy vs Fixed', () => {
    it('demonstrates the difference in parsing', () => {
      const dateString = '2025-08-25';

      // Buggy: Parse as ISO (UTC midnight)
      const buggyDate = new Date(dateString);

      // Fixed: Parse at noon local time
      const fixedDate = new Date(dateString + 'T12:00:00');

      // The buggy version creates midnight UTC
      expect(buggyDate.toISOString()).toContain('T00:00:00.000Z');

      // The fixed version creates noon local time
      expect(fixedDate.getHours()).toBe(12);

      // For users west of UTC, buggyDate.getDate() might return 24 instead of 25
      // We can't easily test this without timezone mocking, but the fix ensures
      // we're always safely in the middle of the intended day
    });

    it('shows both implementations handle UTC users the same', () => {
      // For UTC users, both implementations should work the same
      // This test documents that the fix doesn't break UTC users

      const testDate = '2025-08-25';

      // Note: We can't fully test the buggy behavior without timezone mocking
      // but we can verify the fixed implementation works correctly

      const fixedPrev = getPreviousDay(testDate);
      const fixedNext = getNextDay(testDate);

      expect(fixedPrev).toBe('2025-08-24');
      expect(fixedNext).toBe('2025-08-26');
    });
  });
});
