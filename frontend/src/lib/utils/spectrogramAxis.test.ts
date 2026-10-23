import { describe, it, expect } from 'vitest';
import {
  batFrequencyAxis,
  BAT_MAX_TICKS,
  BAT_TOP_TICK_MAX_FRACTION,
  HZ_PER_KHZ,
  NYQUIST_DIVISOR,
} from './spectrogramAxis';

describe('batFrequencyAxis', () => {
  it.each([
    { rate: 192_000, nyquist: 96, ticks: [80, 60, 40, 20] },
    { rate: 256_000, nyquist: 128, ticks: [120, 100, 80, 60, 40, 20] },
    { rate: 384_000, nyquist: 192, ticks: [160, 120, 80, 40] },
    { rate: 500_000, nyquist: 250, ticks: [200, 160, 120, 80, 40] },
    { rate: 1_000_000, nyquist: 500, ticks: [400, 300, 200, 100] },
  ])('labels a $rate Hz clip from 0 to $nyquist kHz', ({ rate, nyquist, ticks }) => {
    expect(batFrequencyAxis(rate)).toEqual({ nyquistKHz: nyquist, ticksKHz: ticks });
  });

  it.each([undefined, null, 0, -192_000, Number.NaN, Number.POSITIVE_INFINITY])(
    'returns null for an unknown rate (%s)',
    rate => {
      expect(batFrequencyAxis(rate)).toBeNull();
    }
  );

  it('keeps the axis but draws only the ticks that fit for low rates', () => {
    expect(batFrequencyAxis(48_000)).toEqual({ nyquistKHz: 24, ticksKHz: [20, 10] });
    expect(batFrequencyAxis(32_000)).toEqual({ nyquistKHz: 16, ticksKHz: [10] });
    // Below the smallest step nothing fits: the axis has no ticks and draws nothing.
    expect(batFrequencyAxis(16_000)).toEqual({ nyquistKHz: 8, ticksKHz: [] });
    expect(batFrequencyAxis(1)).toEqual({
      nyquistKHz: 1 / (HZ_PER_KHZ * NYQUIST_DIVISOR),
      ticksKHz: [],
    });
  });

  it('keeps every tick positive, descending, inside the cap and at most the tick limit', () => {
    for (let rate = 96_000; rate <= 1_000_000; rate += 1000) {
      const axis = batFrequencyAxis(rate);
      expect(axis).not.toBeNull();
      if (!axis) continue;
      const { nyquistKHz, ticksKHz } = axis;
      expect(nyquistKHz).toBeCloseTo(rate / (HZ_PER_KHZ * NYQUIST_DIVISOR));
      expect(ticksKHz.length).toBeGreaterThanOrEqual(1);
      expect(ticksKHz.length).toBeLessThanOrEqual(BAT_MAX_TICKS);
      expect(ticksKHz).toEqual([...ticksKHz].sort((a, b) => b - a));
      expect(new Set(ticksKHz).size).toBe(ticksKHz.length);
      for (const tick of ticksKHz) {
        expect(tick).toBeGreaterThan(0);
        expect(tick).toBeLessThanOrEqual(nyquistKHz * BAT_TOP_TICK_MAX_FRACTION);
      }
    }
  });
});
