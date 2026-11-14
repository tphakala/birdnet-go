import { describe, expect, it } from 'vitest';
import {
  compassPoint,
  dayPercent,
  nearestSunEvent,
  secondsBetween,
  wallClockDate,
  wallClockDateTime,
  wallClockMinutes,
  wallClockTime,
} from './detailTime';

describe('wall clock fields', () => {
  it('reads the station clock from the timestamp, not the browser zone', () => {
    // The offset is far from any zone the tests run in; the clock fields must
    // still come back as written.
    expect(wallClockTime('2026-11-14T02:57:03+13:45')).toBe('02:57');
    expect(wallClockTime('2026-11-14T02:57:03+13:45', true)).toBe('02:57:03');
    expect(wallClockDate('2026-11-14T02:57:03+13:45')).toBe('2026-11-14');
    expect(wallClockDateTime('2026-11-14T02:57:03+13:45')).toBe('2026-11-14 02:57');
  });

  it('keeps seconds through fractional seconds', () => {
    expect(wallClockTime('2026-11-14T08:26:54.636055395+02:00', true)).toBe('08:26:54');
  });

  it('gives minutes of the day', () => {
    expect(wallClockMinutes('2026-11-14T02:57:00+02:00')).toBe(177);
    expect(wallClockMinutes('2026-11-14T02:57:30+02:00')).toBe(177.5);
    expect(wallClockMinutes('T23:59')).toBe(1439);
  });

  it('answers empty for missing or unparsable input', () => {
    expect(wallClockMinutes(undefined)).toBeNull();
    expect(wallClockMinutes('not a time')).toBeNull();
    expect(wallClockTime(null)).toBe('');
    expect(wallClockDate('14.11.2026')).toBe('');
  });
});

describe('secondsBetween', () => {
  it('gives the length of a capture window', () => {
    expect(secondsBetween('2026-11-14T02:56:56+02:00', '2026-11-14T02:57:23+02:00')).toBe(27);
  });

  it('is null when either end is missing', () => {
    expect(secondsBetween('', '2026-11-14T02:57:23+02:00')).toBeNull();
    expect(secondsBetween('2026-11-14T02:56:56+02:00', 'nonsense')).toBeNull();
  });
});

describe('dayPercent', () => {
  it('maps minutes onto the day axis and clamps', () => {
    expect(dayPercent(0)).toBe(0);
    expect(dayPercent(720)).toBe(50);
    expect(dayPercent(1440)).toBe(100);
    expect(dayPercent(-5)).toBe(0);
    expect(dayPercent(2000)).toBe(100);
  });
});

describe('nearestSunEvent', () => {
  const sunrise = 506; // 08:26
  const sunset = 935; // 15:35

  it('measures an early morning detection against the coming sunrise', () => {
    expect(nearestSunEvent(177, sunrise, sunset)).toEqual({
      event: 'sunrise',
      before: true,
      hours: 5,
      minutes: 29,
    });
  });

  it('measures a late evening detection against the past sunset', () => {
    expect(nearestSunEvent(1407, sunrise, sunset)).toEqual({
      event: 'sunset',
      before: false,
      hours: 7,
      minutes: 52,
    });
  });

  it('measures a detection just after midnight against the previous sunset when it is nearer', () => {
    // 00:10 is 8 h 35 min after the previous evening's sunset and 8 h 16 min before sunrise.
    expect(nearestSunEvent(10, sunrise, sunset)).toEqual({
      event: 'sunrise',
      before: true,
      hours: 8,
      minutes: 16,
    });
  });

  it('reports a daytime detection after sunrise or before sunset, whichever is nearer', () => {
    expect(nearestSunEvent(566, sunrise, sunset)).toMatchObject({
      event: 'sunrise',
      before: false,
    });
    expect(nearestSunEvent(900, sunrise, sunset)).toMatchObject({ event: 'sunset', before: true });
  });

  it('is null without a moment or without sun times', () => {
    expect(nearestSunEvent(null, sunrise, sunset)).toBeNull();
    expect(nearestSunEvent(600, null, null)).toBeNull();
  });
});

describe('compassPoint', () => {
  it('names bearings on the 16-point compass', () => {
    expect(compassPoint(0)).toBe('N');
    expect(compassPoint(290)).toBe('WNW');
    expect(compassPoint(180)).toBe('S');
    expect(compassPoint(359)).toBe('N');
    expect(compassPoint(-90)).toBe('W');
  });
});
