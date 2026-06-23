import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import YearOverYearChart from './YearOverYearChart.svelte';
import { peakCumulative, type YearOverYearData } from './utils/yearOverYear';

// jsdom has no layout engine; assert on element counts/attributes only.

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

// This year ahead then behind over four days; both series monotonic non-decreasing.
const sample: YearOverYearData = {
  currentYear: 2026,
  previousYear: 2025,
  points: [
    { date: '2026-01-01', monthDay: '01-01', thisYear: 2, lastYear: 1, delta: 1 },
    { date: '2026-01-02', monthDay: '01-02', thisYear: 4, lastYear: 3, delta: 1 },
    { date: '2026-01-03', monthDay: '01-03', thisYear: 5, lastYear: 6, delta: -1 },
    { date: '2026-01-04', monthDay: '01-04', thisYear: 7, lastYear: 8, delta: -1 },
  ],
};

const empty: YearOverYearData = { currentYear: 2026, previousYear: 2025, points: [] };

// Both years flat at zero: the component must bail like the empty state.
const allZero: YearOverYearData = {
  currentYear: 2026,
  previousYear: 2025,
  points: [
    { date: '2026-01-01', monthDay: '01-01', thisYear: 0, lastYear: 0, delta: 0 },
    { date: '2026-01-02', monthDay: '01-02', thisYear: 0, lastYear: 0, delta: 0 },
  ],
};

// Format a date `offset` days after a base date as YYYY-MM-DD (local) and its MM-DD key.
function isoDay(base: Date, offset: number): { date: string; monthDay: string } {
  const d = new Date(base);
  d.setDate(d.getDate() + offset);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return { date: `${y}-${m}-${day}`, monthDay: `${m}-${day}` };
}

// A long range (40 days > MAX_DOTS_DAYS) so the component drops the per-point dots but still draws
// the lines and the hover overlay.
const longRange: YearOverYearData = (() => {
  const base = new Date('2026-01-01T00:00:00');
  const points = [];
  let thisCum = 0;
  let lastCum = 0;
  for (let i = 0; i < 40; i++) {
    thisCum += i % 2 === 0 ? 2 : 1;
    lastCum += 1;
    const { date, monthDay } = isoDay(base, i);
    points.push({ date, monthDay, thisYear: thisCum, lastYear: lastCum, delta: thisCum - lastCum });
  }
  return { currentYear: 2026, previousYear: 2025, points };
})();

describe('YearOverYearChart', () => {
  it('renders without throwing for empty data', () => {
    expect(() => render(YearOverYearChart, { props: { data: empty } })).not.toThrow();
  });

  it('renders without throwing when both years are flat at zero', () => {
    expect(() => render(YearOverYearChart, { props: { data: allZero } })).not.toThrow();
  });

  it('renders a single in-range day (zero-width x-domain) without NaN', async () => {
    const singleDay: YearOverYearData = {
      currentYear: 2026,
      previousYear: 2025,
      points: [{ date: '2026-01-01', monthDay: '01-01', thisYear: 3, lastYear: 2, delta: 1 }],
    };
    const { container } = render(YearOverYearChart, { props: { data: singleDay, width: 800 } });
    await Promise.resolve();
    const line = container.querySelector('.yoy-line-this');
    expect(line).toBeTruthy();
    expect(line?.getAttribute('d') ?? '').not.toContain('NaN');
  });

  it('renders the axes, the delta band, and both cumulative lines', async () => {
    const { container } = render(YearOverYearChart, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelector('.x-axis')).toBeTruthy();
    expect(container.querySelector('.y-axis')).toBeTruthy();
    expect(container.querySelector('.yoy-band')).toBeTruthy();
    expect(container.querySelector('.yoy-line-this')).toBeTruthy();
    expect(container.querySelector('.yoy-line-last')).toBeTruthy();
    expect(container.querySelector('.yoy-overlay')).toBeTruthy();
  });

  it('draws one dot per day per series on a short range', async () => {
    const { container } = render(YearOverYearChart, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelectorAll('.yoy-dots-this circle')).toHaveLength(4);
    expect(container.querySelectorAll('.yoy-dots-last circle')).toHaveLength(4);
  });

  it('drops the per-point dots on a wide range but still draws the lines', async () => {
    const { container } = render(YearOverYearChart, { props: { data: longRange, width: 800 } });
    await Promise.resolve();
    expect(container.querySelectorAll('.yoy-dots-this circle')).toHaveLength(0);
    expect(container.querySelector('.yoy-line-this')).toBeTruthy();
    expect(container.querySelector('.yoy-line-last')).toBeTruthy();
  });

  it('does not draw axes when both years are flat at zero', async () => {
    const { container } = render(YearOverYearChart, { props: { data: allZero, width: 800 } });
    await Promise.resolve();
    // ChartCard owns the not-enough-data state; the chart itself bails before drawing.
    expect(container.querySelector('.yoy-line-this')).toBeNull();
  });

  it('renders an accessible legend with one item per series', async () => {
    const { container } = render(YearOverYearChart, { props: { data: sample } });
    await Promise.resolve();
    // <ul>/<li> legend (not aria-hidden), so the series labels reach assistive tech.
    const legend = container.querySelector('ul.yoy-legend');
    expect(legend).toBeTruthy();
    expect(legend?.hasAttribute('aria-hidden')).toBe(false);
    expect(container.querySelectorAll('.yoy-legend-item')).toHaveLength(2);
  });

  it('sets an accessible label on the chart container', () => {
    const { container } = render(YearOverYearChart, {
      props: { data: sample, ariaLabel: 'Year over year' },
    });
    expect(container.querySelector('[aria-label="Year over year"]')).toBeTruthy();
  });

  it('renders a screen-reader summary when there is data', async () => {
    const { getByTestId } = render(YearOverYearChart, { props: { data: sample } });
    await Promise.resolve();
    expect(getByTestId('yoy-summary')).toBeTruthy();
  });

  it('peakCumulative returns the max across both series', () => {
    expect(peakCumulative(sample)).toBe(8);
    expect(peakCumulative(empty)).toBe(0);
    expect(peakCumulative(allZero)).toBe(0);
  });
});
