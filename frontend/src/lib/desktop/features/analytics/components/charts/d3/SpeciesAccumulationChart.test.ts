import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import SpeciesAccumulationChart from './SpeciesAccumulationChart.svelte';
import { finalCumulative, type AccumulationData } from './utils/accumulation';

// jsdom has no layout engine; assert on element counts/attributes only.

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

// A short rising curve: cumulative climbs 1 -> 1 -> 2 -> 3 over four days.
const sample: AccumulationData = {
  points: [
    { date: '2026-03-01', cumulativeSpecies: 1, newSpecies: 1 },
    { date: '2026-03-02', cumulativeSpecies: 1, newSpecies: 0 },
    { date: '2026-03-03', cumulativeSpecies: 2, newSpecies: 1 },
    { date: '2026-03-04', cumulativeSpecies: 3, newSpecies: 1 },
  ],
};

const empty: AccumulationData = { points: [] };

// All-zero curve (no species accumulated): the component must bail like the empty state.
const allZero: AccumulationData = {
  points: [
    { date: '2026-03-01', cumulativeSpecies: 0, newSpecies: 0 },
    { date: '2026-03-02', cumulativeSpecies: 0, newSpecies: 0 },
  ],
};

// Format a date `offset` days after a base date as YYYY-MM-DD (local), so a long fixture spans real
// consecutive calendar days across a month boundary rather than invalid dates like 2026-03-40.
function isoDay(base: Date, offset: number): string {
  const d = new Date(base);
  d.setDate(d.getDate() + offset);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

// A long range (40 days > MAX_DOTS_DAYS) so the component drops the per-point dots but still draws
// the line.
const longRange: AccumulationData = (() => {
  const base = new Date('2026-03-01T00:00:00');
  const points = [];
  let cum = 0;
  for (let i = 0; i < 40; i++) {
    const isNew = i % 3 === 0 ? 1 : 0;
    cum += isNew;
    points.push({ date: isoDay(base, i), cumulativeSpecies: cum, newSpecies: isNew });
  }
  return { points };
})();

describe('SpeciesAccumulationChart', () => {
  it('renders without throwing for empty data', () => {
    expect(() => render(SpeciesAccumulationChart, { props: { data: empty } })).not.toThrow();
  });

  it('renders without throwing when no species have accumulated', () => {
    expect(() => render(SpeciesAccumulationChart, { props: { data: allZero } })).not.toThrow();
  });

  it('renders a single in-range day (zero-width x-domain) without NaN', async () => {
    // One day with 2+ first-seen species passes minDataPoints (keyed on the species count) but yields
    // a single point, so minDate === maxDate. The padded domain must keep the scale well-defined.
    const singleDay: AccumulationData = {
      points: [{ date: '2026-03-01', cumulativeSpecies: 3, newSpecies: 3 }],
    };
    const { container } = render(SpeciesAccumulationChart, {
      props: { data: singleDay, width: 800 },
    });
    await Promise.resolve();
    const line = container.querySelector('.accumulation-line');
    expect(line).toBeTruthy();
    // A NaN domain produces an "MNaN,..." path; assert the rendered path carries no NaN.
    expect(line?.getAttribute('d') ?? '').not.toContain('NaN');
  });

  it('renders the axes, the area, the line, and the total-species reference line', async () => {
    const { container } = render(SpeciesAccumulationChart, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelector('.x-axis')).toBeTruthy();
    expect(container.querySelector('.y-axis')).toBeTruthy();
    expect(container.querySelector('.accumulation-area')).toBeTruthy();
    expect(container.querySelector('.accumulation-line')).toBeTruthy();
    expect(container.querySelector('.accumulation-asymptote')).toBeTruthy();
  });

  it('draws one dot per day on a short range', async () => {
    const { container } = render(SpeciesAccumulationChart, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelectorAll('.accumulation-dots circle')).toHaveLength(4);
  });

  it('drops the per-point dots on a wide range but still draws the line', async () => {
    const { container } = render(SpeciesAccumulationChart, {
      props: { data: longRange, width: 800 },
    });
    await Promise.resolve();
    expect(container.querySelectorAll('.accumulation-dots circle')).toHaveLength(0);
    expect(container.querySelector('.accumulation-line')).toBeTruthy();
  });

  it('draws the cumulative line as a single continuous run (monotonic, no breaks)', async () => {
    const { container } = render(SpeciesAccumulationChart, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    const path = container.querySelector('.accumulation-line');
    expect(path).toBeTruthy();
    const moveCommands = (path?.getAttribute('d')?.match(/M/g) ?? []).length;
    expect(moveCommands).toBe(1);
  });

  it('does not draw axes when no species have accumulated', async () => {
    const { container } = render(SpeciesAccumulationChart, {
      props: { data: allZero, width: 800 },
    });
    await Promise.resolve();
    // ChartCard owns the not-enough-data state; the chart itself bails before drawing.
    expect(container.querySelector('.accumulation-line')).toBeNull();
  });

  it('sets an accessible label on the chart container', () => {
    const { container } = render(SpeciesAccumulationChart, {
      props: { data: sample, ariaLabel: 'Species accumulation' },
    });
    expect(container.querySelector('[aria-label="Species accumulation"]')).toBeTruthy();
  });

  it('renders a screen-reader summary when there is data', async () => {
    const { getByTestId } = render(SpeciesAccumulationChart, { props: { data: sample } });
    await Promise.resolve();
    expect(getByTestId('accumulation-summary')).toBeTruthy();
  });

  it('finalCumulative returns the asymptote (max) value', () => {
    expect(finalCumulative(sample)).toBe(3);
    expect(finalCumulative(empty)).toBe(0);
    expect(finalCumulative(allZero)).toBe(0);
  });
});
