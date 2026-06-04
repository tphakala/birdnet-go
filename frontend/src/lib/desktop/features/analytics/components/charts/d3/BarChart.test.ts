import { describe, it, expect, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import BarChart from './BarChart.svelte';

// jsdom has no layout engine (no getBBox / clientWidth), so these tests assert
// on element counts, attributes and data binding rather than pixel geometry.
// This matches the style of utils/__tests__/axes.test.ts.

afterEach(() => {
  cleanup();
});

describe('BarChart', () => {
  const sampleData = [
    { label: 'Robin', value: 12 },
    { label: 'Sparrow', value: 7 },
    { label: 'Crow', value: 3 },
  ];

  it('renders without throwing for empty data', () => {
    expect(() => render(BarChart, { props: { data: [] } })).not.toThrow();
  });

  it('renders one rect per data point (vertical default)', async () => {
    const { container } = render(BarChart, { props: { data: sampleData } });
    // Allow the $effect-driven render to run.
    await Promise.resolve();
    const bars = container.querySelectorAll('rect.bar');
    expect(bars).toHaveLength(sampleData.length);
  });

  it('renders one rect per data point (horizontal)', async () => {
    const { container } = render(BarChart, {
      props: { data: sampleData, orientation: 'horizontal' },
    });
    await Promise.resolve();
    const bars = container.querySelectorAll('rect.bar');
    expect(bars).toHaveLength(sampleData.length);
  });

  it('renders the value axis label when provided', async () => {
    const { container } = render(BarChart, {
      props: { data: sampleData, valueAxisLabel: 'Number of Detections' },
    });
    await Promise.resolve();
    const labels = Array.from(container.querySelectorAll('.axis-label')).map(n => n.textContent);
    expect(labels).toContain('Number of Detections');
  });

  it('applies per-bar colors when provided', async () => {
    const { container } = render(BarChart, {
      props: {
        data: [
          { label: 'A', value: 1, color: 'rgb(255, 0, 0)' },
          { label: 'B', value: 2, color: 'rgb(0, 255, 0)' },
        ],
      },
    });
    await Promise.resolve();
    const bars = container.querySelectorAll('rect.bar');
    expect(bars).toHaveLength(2);
    // jsdom normalizes color strings; just assert each bar received a fill.
    bars.forEach(bar => {
      expect((bar as SVGRectElement).style.fill).not.toBe('');
    });
  });

  it('sets an accessible label on the chart container', () => {
    const { container } = render(BarChart, {
      props: { data: sampleData, ariaLabel: 'Top species chart' },
    });
    const labelled = container.querySelector('[aria-label="Top species chart"]');
    expect(labelled).toBeTruthy();
  });
});
