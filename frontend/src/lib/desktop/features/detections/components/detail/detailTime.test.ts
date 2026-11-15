import { describe, expect, it } from 'vitest';
import {
  compassPointKey,
  dayPercent,
  detectionMinute,
  formatCalendarDate,
  formatCalendarDateTime,
  inOffsetOf,
  nearestSunEvent,
  secondsBetween,
  sunEventTimes,
  wallClockDate,
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
    const dateTime = formatCalendarDateTime('2026-11-14T02:57:03+13:45');
    expect(dateTime.endsWith(' 02:57')).toBe(true);
    expect(dateTime).toBe(`${formatCalendarDate('2026-11-14')} 02:57`);
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

  it('measures a detection just after midnight against the coming sunrise when it is nearer', () => {
    // 00:10 is 8 h 35 min after the previous evening's sunset and 8 h 16 min before sunrise.
    expect(nearestSunEvent(10, sunrise, sunset)).toEqual({
      event: 'sunrise',
      before: true,
      hours: 8,
      minutes: 16,
    });
  });

  it("measures a summer night after midnight against the previous evening's sunset", () => {
    // Sunset 21:00, sunrise 06:00: at 01:00 the previous sunset is 4 h back, sunrise 5 h ahead.
    expect(nearestSunEvent(60, 360, 1260)).toEqual({
      event: 'sunset',
      before: false,
      hours: 4,
      minutes: 0,
    });
  });

  it('measures a late evening against the coming sunrise when that is nearer', () => {
    // Sunrise 02:00, sunset 23:30: at 23:50 the next sunrise is 2 h 10 min ahead.
    expect(nearestSunEvent(1430, 120, 1410)).toMatchObject({ event: 'sunset', before: false });
    expect(nearestSunEvent(1430, 120, null)).toEqual({
      event: 'sunrise',
      before: true,
      hours: 2,
      minutes: 10,
    });
  });

  // Regression: with only one sun time known, the event a day earlier was not looked at.
  it("measures against the previous day's sunrise when only the sunrise is known", () => {
    // Sunrise 15:35 (polar): at 00:10 the previous sunrise was 8 h 35 min ago.
    expect(nearestSunEvent(10, 935, null)).toEqual({
      event: 'sunrise',
      before: false,
      hours: 8,
      minutes: 35,
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

describe('compassPointKey', () => {
  it.each([
    [0, 'n'],
    [11, 'n'],
    [12, 'nne'],
    [90, 'e'],
    [180, 's'],
    [290, 'wnw'],
    [359, 'n'],
    [360, 'n'],
    [-90, 'w'],
    [Number.NaN, 'n'],
    [Number.POSITIVE_INFINITY, 'n'],
  ])('names %s degrees %s', (degrees, key) => {
    expect(compassPointKey(degrees)).toBe(key);
  });
});

describe('sunEventTimes', () => {
  it('prefers the sun endpoint and falls back to the weather provider per event', () => {
    const weather = { daily: { sunrise: 'T07:00:00+02:00', sunset: 'T16:00:00+02:00' } };
    expect(sunEventTimes({ date: 'd', sunrise: 'T08:00:00+02:00' }, weather)).toEqual({
      sunrise: 'T08:00:00+02:00',
      sunset: 'T16:00:00+02:00',
    });
    expect(sunEventTimes(null, null)).toEqual({ sunrise: undefined, sunset: undefined });
  });
});

// The sun endpoint writes its times in the zone of the station's coordinates, which can
// differ from the zone of the detection's timestamp: the clock fields of the two then
// count the difference between the offsets as time.
describe('sun times in the detection offset', () => {
  const detectedAt = '2026-11-14T06:30:00Z';

  it('writes a time in the offset of another timestamp', () => {
    expect(inOffsetOf('2026-11-14T08:00:00+02:00', detectedAt)).toBe('2026-11-14T06:00:00Z');
    expect(inOffsetOf('2026-11-14T06:00:00Z', '2026-11-14T08:30:00+02:00')).toBe(
      '2026-11-14T08:00:00+02:00'
    );
    expect(inOffsetOf('2026-11-14T22:30:00-05:00', '2026-11-14T12:00:00+09:30')).toBe(
      '2026-11-15T13:00:00+09:30'
    );
  });

  it('leaves a time as it is when either side has no usable offset', () => {
    expect(inOffsetOf('T08:00:00+02:00', detectedAt)).toBe('T08:00:00+02:00');
    expect(inOffsetOf('2026-11-14T08:00:00+02:00', undefined)).toBe('2026-11-14T08:00:00+02:00');
    expect(inOffsetOf(undefined, detectedAt)).toBeUndefined();
  });

  it('measures a detection 30 minutes after a sunrise written in another offset', () => {
    const times = sunEventTimes(
      { date: '2026-11-14', sunrise: '2026-11-14T08:00:00+02:00' },
      { daily: { sunset: '2026-11-14T17:00:00+02:00' } },
      detectedAt
    );

    expect(times).toEqual({ sunrise: '2026-11-14T06:00:00Z', sunset: '2026-11-14T15:00:00Z' });
    expect(
      nearestSunEvent(
        detectionMinute({ timestamp: detectedAt, time: '06:30:00' }),
        wallClockMinutes(times.sunrise),
        wallClockMinutes(times.sunset)
      )
    ).toEqual({ event: 'sunrise', before: false, hours: 0, minutes: 30 });
  });
});
