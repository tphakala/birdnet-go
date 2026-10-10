import { describe, expect, it } from 'vitest';
import { fillDailyCounts } from './speciesHistory';

describe('fillDailyCounts', () => {
  it('returns every day in the range with zero for days without detections', () => {
    const points = fillDailyCounts(
      [
        { date: '2026-03-01', count: 4 },
        { date: '2026-03-04', count: 2 },
      ],
      '2026-03-01',
      '2026-03-04'
    );
    expect(points.map(p => p.value)).toEqual([4, 0, 0, 2]);
    expect(points.map(p => p.date.getDate())).toEqual([1, 2, 3, 4]);
  });

  it('returns zeros for a range with no data', () => {
    expect(fillDailyCounts([], '2026-12-31', '2027-01-01').map(p => p.value)).toEqual([0, 0]);
  });
});
