import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import SeasonalHeatmap from './SeasonalHeatmap.svelte';
import type { HeatmapData } from './utils/heatmap';

// jsdom has no layout engine; assert on element counts/attributes only.

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

const sample: HeatmapData = {
  dates: ['2026-03-01', '2026-03-02'],
  slotResolutionMinutes: 15,
  cells: {
    dateIndex: [0, 0, 1],
    slot: [0, 1, 95],
    count: [3, 2, 7],
  },
};

const empty: HeatmapData = {
  dates: ['2026-03-01'],
  slotResolutionMinutes: 15,
  cells: { dateIndex: [], slot: [], count: [] },
};

describe('SeasonalHeatmap', () => {
  it('renders without throwing for empty cells', () => {
    expect(() => render(SeasonalHeatmap, { props: { data: empty } })).not.toThrow();
  });

  it('renders one rect per cell at a wide width', async () => {
    const { container } = render(SeasonalHeatmap, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelectorAll('rect.heatmap-cell')).toHaveLength(3);
  });

  it('folds sub-hour slots into hourly rows on narrow widths', async () => {
    // Two cells in hour 0 (slots 0 and 1 at 15-min resolution) on the same date collapse to one
    // cell when the chart switches to its 24-row hourly layout.
    const { container } = render(SeasonalHeatmap, { props: { data: sample, width: 300 } });
    await Promise.resolve();
    const cells = container.querySelectorAll('rect.heatmap-cell');
    // date 0 hour 0 (merged) + date 1 hour 23 = 2 cells, vs 3 at full resolution.
    expect(cells).toHaveLength(2);
  });

  it('renders x and y axis groups', async () => {
    const { container } = render(SeasonalHeatmap, { props: { data: sample } });
    await Promise.resolve();
    expect(container.querySelector('.x-axis')).toBeTruthy();
    expect(container.querySelector('.y-axis')).toBeTruthy();
  });

  it('encodes higher counts as higher opacity', async () => {
    const { container } = render(SeasonalHeatmap, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    const opacities = Array.from(container.querySelectorAll('rect.heatmap-cell'), r =>
      parseFloat((r as SVGRectElement).style.opacity)
    );
    // The max-count cell (7) must be fully opaque; the lowest (2) must be fainter.
    expect(Math.max(...opacities)).toBeCloseTo(1, 5);
    expect(Math.min(...opacities)).toBeLessThan(1);
  });

  it('sets an accessible label on the chart container', () => {
    const { container } = render(SeasonalHeatmap, {
      props: { data: sample, ariaLabel: 'Seasonal heatmap' },
    });
    expect(container.querySelector('[aria-label="Seasonal heatmap"]')).toBeTruthy();
  });

  it('renders a screen-reader summary when there is data', async () => {
    const { getByTestId } = render(SeasonalHeatmap, { props: { data: sample } });
    await Promise.resolve();
    expect(getByTestId('heatmap-summary')).toBeTruthy();
  });
});
