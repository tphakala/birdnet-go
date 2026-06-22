import { describe, it, expect } from 'vitest';

import { residencyDays } from '../phenology';

describe('residencyDays', () => {
  it('counts a single-day residency as 1 (inclusive)', () => {
    expect(residencyDays('2026-05-01', '2026-05-01')).toBe(1);
  });

  it('counts a multi-day span inclusively', () => {
    expect(residencyDays('2026-05-01', '2026-05-10')).toBe(10);
  });

  it('spans a month boundary inclusively', () => {
    // 2026-02 has 28 days; 2026-02-25 .. 2026-03-02 is 6 days inclusive.
    expect(residencyDays('2026-02-25', '2026-03-02')).toBe(6);
  });

  it('returns 0 for a reversed range', () => {
    expect(residencyDays('2026-05-10', '2026-05-01')).toBe(0);
  });

  it('returns 0 for an unparseable date', () => {
    expect(residencyDays('not-a-date', '2026-05-01')).toBe(0);
    expect(residencyDays('2026-05-01', '')).toBe(0);
  });
});
