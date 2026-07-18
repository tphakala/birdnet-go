import { describe, it, expect } from 'vitest';
import { scaleTime } from 'd3-scale';

import { boundaryDateTicks } from './axes';

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
