import { describe, it, expect } from 'vitest';
import { scaleTime } from 'd3-scale';

import { boundaryDateTicks, hourAxisTickValues } from './axes';

describe('hourAxisTickValues', () => {
  it('always ends on the final hour so the edge is labelled', () => {
    const ticks = hourAxisTickValues(23, 3);
    expect(ticks[0]).toBe(0);
    expect(ticks[ticks.length - 1]).toBe(23);
    // d3's own ticks would stop at 21 and leave the last hour unlabelled at the plot edge.
    expect(ticks).toEqual([0, 3, 6, 9, 12, 15, 18, 21, 23]);
  });

  it('drops a regular tick that would collide with the final hour', () => {
    // With a step of 4, 20 sits 3 hours from 23 (>= half a step) and survives; a tick at 22 would
    // not. Verified via a step whose last multiple lands adjacent to the final hour.
    expect(hourAxisTickValues(23, 4)).toEqual([0, 4, 8, 12, 16, 20, 23]);
    // Step 2 would put a tick at 22, only 1 hour from 23 (< half a step), so it is dropped.
    expect(hourAxisTickValues(23, 2)).not.toContain(22);
  });

  it('never emits a tick beyond the final hour', () => {
    for (const step of [1, 2, 3, 4, 6]) {
      const ticks = hourAxisTickValues(23, step);
      expect(Math.max(...ticks)).toBe(23);
    }
  });
});

describe('boundaryDateTicks', () => {
  it('always includes the exact domain start and end', () => {
    const start = new Date(2024, 5, 18); // spans a month → weekly nice ticks
    const end = new Date(2024, 6, 18);
    const scale = scaleTime().domain([start, end]).range([0, 1000]);

    const ticks = boundaryDateTicks(scale, 8);

    expect(ticks[0]).toEqual(start);
    expect(ticks[ticks.length - 1]).toEqual(end);
  });

  it('drops nice ticks that hug an endpoint so labels do not overlap', () => {
    const start = new Date(2024, 5, 18);
    const end = new Date(2024, 6, 18);
    const scale = scaleTime().domain([start, end]).range([0, 1000]);

    // A large gap threshold removes every interior nice tick, leaving only the
    // two forced endpoints.
    const ticks = boundaryDateTicks(scale, 8, 10_000);

    expect(ticks).toHaveLength(2);
    expect(ticks[0]).toEqual(start);
    expect(ticks[1]).toEqual(end);
  });

  it('collapses a zero-width domain to a single tick', () => {
    const day = new Date(2024, 5, 18);
    const scale = scaleTime().domain([day, day]).range([0, 1000]);

    expect(boundaryDateTicks(scale, 8)).toEqual([day]);
  });
});
