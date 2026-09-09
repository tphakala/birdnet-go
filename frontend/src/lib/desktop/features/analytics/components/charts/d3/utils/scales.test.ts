import { describe, it, expect } from 'vitest';

import { createLinearScale } from './scales';

describe('createLinearScale', () => {
  it('rounds the domain outward by default', () => {
    // The default suits value axes, where rounding a max up to a tidy number is desirable.
    const scale = createLinearScale({ domain: [0, 23], range: [0, 1000] });
    expect(scale.domain()).toEqual([0, 24]);
  });

  it('leaves an exact domain untouched when nice is false', () => {
    // Regression: an hour-of-day axis is exactly 0..23. Rounding it to 0..24 pushed the final hour
    // to ~96% of the width, so every series stopped short of the plot edge, and it added a tick at
    // hour 24 that the hour formatter clamped to a misplaced "23:00" label.
    const scale = createLinearScale({ domain: [0, 23], range: [0, 1000], nice: false });
    expect(scale.domain()).toEqual([0, 23]);
    expect(scale(23)).toBe(1000);
    expect(scale(0)).toBe(0);
  });

  it('still validates the domain when nice is disabled', () => {
    expect(() =>
      // @ts-expect-error - exercising the runtime guard with a malformed domain
      createLinearScale({ domain: ['a', 'b'], range: [0, 1000], nice: false })
    ).toThrow(TypeError);
  });
});
