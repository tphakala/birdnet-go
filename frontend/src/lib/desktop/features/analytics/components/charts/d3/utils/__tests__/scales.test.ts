import { describe, it, expect } from 'vitest';
import { createTimeScale } from '../scales';

const MS_PER_DAY = 86400000;

describe('createTimeScale', () => {
  it('passes a normal (non-collapsed) domain through unchanged', () => {
    const start = new Date('2026-01-01T00:00:00');
    const end = new Date('2026-01-08T00:00:00');
    const scale = createTimeScale({ domain: [start, end], range: [0, 100] });

    const domain = scale.domain();
    expect(domain[0].getTime()).toBe(start.getTime());
    expect(domain[1].getTime()).toBe(end.getTime());
    expect(scale(start)).toBe(0);
    expect(scale(end)).toBe(100);
  });

  it('pads a collapsed (single-day) domain so it is not zero-width', () => {
    const day = new Date('2026-05-30T12:00:00');
    const scale = createTimeScale({ domain: [day, day], range: [0, 100] });

    const domain = scale.domain();
    // Endpoints must differ after padding (one day on each side).
    expect(domain[1].getTime()).toBeGreaterThan(domain[0].getTime());
    expect(domain[0].getTime()).toBe(day.getTime() - MS_PER_DAY);
    expect(domain[1].getTime()).toBe(day.getTime() + MS_PER_DAY);

    // The original day maps to a finite, mid-range value (not NaN).
    const x = scale(day);
    expect(Number.isFinite(x)).toBe(true);
    expect(x).toBeCloseTo(50, 5);
  });
});
