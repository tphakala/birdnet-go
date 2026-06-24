import { describe, it, expect } from 'vitest';
import {
  hourlyTotal,
  maxHourly,
  peakHour,
  minuteToAngle,
  polarToCartesian,
  formatMinuteOfDay,
  arcAngles,
  dayRegionSegments,
  MINUTES_PER_DAY,
  type NocturnalClockData,
} from '../nocturnal';

function clock(hourly: number[], sun: NocturnalClockData['sun'] = null): NocturnalClockData {
  return { hourly, sun };
}

describe('nocturnal utils', () => {
  describe('hourlyTotal', () => {
    it('sums all hourly counts', () => {
      expect(hourlyTotal(clock([1, 2, 3, 4]))).toBe(10);
    });

    it('ignores non-finite values', () => {
      expect(hourlyTotal(clock([1, NaN, 2, Infinity]))).toBe(3);
    });

    it('is 0 for an empty array', () => {
      expect(hourlyTotal(clock([]))).toBe(0);
    });
  });

  describe('maxHourly', () => {
    it('returns the largest count', () => {
      expect(maxHourly(clock([0, 5, 2, 9, 1]))).toBe(9);
    });

    it('is 0 when there is no activity', () => {
      expect(maxHourly(clock([0, 0, 0]))).toBe(0);
    });

    it('ignores counts beyond 24 hours', () => {
      const arr = new Array<number>(26).fill(1);
      arr[25] = 999; // beyond hour 23, must be ignored
      expect(maxHourly(clock(arr))).toBe(1);
    });
  });

  describe('peakHour', () => {
    it('returns the hour index of the maximum', () => {
      expect(peakHour(clock([0, 0, 7, 3]))).toBe(2);
    });

    it('returns the earliest hour on a tie', () => {
      expect(peakHour(clock([4, 4, 1]))).toBe(0);
    });

    it('is null when there is no activity', () => {
      expect(peakHour(clock([0, 0, 0]))).toBeNull();
    });
  });

  describe('minuteToAngle', () => {
    it('maps midnight to 0 (top of the dial)', () => {
      expect(minuteToAngle(0)).toBe(0);
    });

    it('maps noon to PI (bottom of the dial)', () => {
      expect(minuteToAngle(MINUTES_PER_DAY / 2)).toBeCloseTo(Math.PI, 10);
    });

    it('maps 06:00 to PI/2 (clockwise quarter turn)', () => {
      expect(minuteToAngle(6 * 60)).toBeCloseTo(Math.PI / 2, 10);
    });
  });

  describe('polarToCartesian', () => {
    it('places angle 0 at the top (midnight)', () => {
      const p = polarToCartesian(100, 100, 50, 0);
      expect(p.x).toBeCloseTo(100, 10);
      expect(p.y).toBeCloseTo(50, 10);
    });

    it('places a quarter turn to the right (06:00)', () => {
      const p = polarToCartesian(100, 100, 50, Math.PI / 2);
      expect(p.x).toBeCloseTo(150, 10);
      expect(p.y).toBeCloseTo(100, 10);
    });
  });

  describe('formatMinuteOfDay', () => {
    it('formats a minute-of-day as zero-padded HH:MM', () => {
      expect(formatMinuteOfDay(372)).toBe('06:12');
      expect(formatMinuteOfDay(0)).toBe('00:00');
      expect(formatMinuteOfDay(MINUTES_PER_DAY - 1)).toBe('23:59');
    });

    it('wraps a full day back to 00:00', () => {
      expect(formatMinuteOfDay(MINUTES_PER_DAY)).toBe('00:00');
    });
  });

  describe('arcAngles', () => {
    it('returns increasing angles for a normal same-day span', () => {
      const { startAngle, endAngle } = arcAngles(6 * 60, 18 * 60); // 06:00 -> 18:00
      expect(startAngle).toBeCloseTo(minuteToAngle(6 * 60), 10);
      expect(endAngle).toBeCloseTo(minuteToAngle(18 * 60), 10);
      expect(endAngle).toBeGreaterThan(startAngle);
    });

    it('wraps the end forward over midnight when it precedes the start', () => {
      // sunrise 23:00 (1380), sunset 02:00 (120) -> day spans midnight; end must wrap past start.
      const { startAngle, endAngle } = arcAngles(1380, 120);
      expect(endAngle).toBeGreaterThan(startAngle);
      // The swept arc stays under a full turn (a real daytime span never covers the whole dial).
      expect(endAngle - startAngle).toBeLessThan(2 * Math.PI);
      expect(endAngle).toBeCloseTo(minuteToAngle(120) + 2 * Math.PI, 10);
    });

    it('wraps equal start/end to a full turn rather than a zero-width arc', () => {
      const { startAngle, endAngle } = arcAngles(600, 600);
      expect(endAngle - startAngle).toBeCloseTo(2 * Math.PI, 10);
    });
  });

  describe('dayRegionSegments', () => {
    it('returns a single segment for a normal same-day span', () => {
      const segs = dayRegionSegments(6 * 60, 18 * 60, 1440); // width=1440 -> 1px per minute
      expect(segs).toHaveLength(1);
      expect(segs[0].x).toBeCloseTo(360, 10);
      expect(segs[0].width).toBeCloseTo(720, 10);
    });

    it('splits into two segments when the span wraps past midnight', () => {
      // sunrise 23:00 (1380), sunset 02:00 (120) at width 1440 -> [0,120] and [1380,1440].
      const segs = dayRegionSegments(1380, 120, 1440);
      expect(segs).toHaveLength(2);
      expect(segs[0]).toEqual({ x: 0, width: 120 });
      expect(segs[1]).toEqual({ x: 1380, width: 60 });
    });
  });
});
