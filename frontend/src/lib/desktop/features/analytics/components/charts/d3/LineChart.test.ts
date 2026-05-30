import { describe, it, expect, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import LineChart from './LineChart.svelte';

// jsdom has no layout engine, so assert on element counts/attributes only.

afterEach(() => {
  cleanup();
});

function makeSeries(id: string, values: number[]) {
  return {
    id,
    label: id,
    data: values.map((value, i) => ({
      date: new Date(2024, 0, i + 1),
      value,
    })),
  };
}

describe('LineChart', () => {
  it('renders without throwing for empty series', () => {
    expect(() => render(LineChart, { props: { series: [] } })).not.toThrow();
  });

  it('renders one line path per series', async () => {
    const series = [makeSeries('a', [1, 2, 3]), makeSeries('b', [3, 2, 1])];
    const { container } = render(LineChart, { props: { series } });
    await Promise.resolve();
    const lines = container.querySelectorAll('path.line-series');
    expect(lines).toHaveLength(2);
  });

  it('renders hover points for the data of a single series', async () => {
    const series = [makeSeries('a', [1, 2, 3, 4])];
    const { container } = render(LineChart, { props: { series } });
    await Promise.resolve();
    const points = container.querySelectorAll('circle.line-point');
    expect(points).toHaveLength(4);
  });

  it('renders the value axis label when provided', async () => {
    const series = [makeSeries('a', [1, 2, 3])];
    const { container } = render(LineChart, {
      props: { series, valueAxisLabel: 'Number of Detections' },
    });
    await Promise.resolve();
    const labels = Array.from(container.querySelectorAll('.axis-label')).map(n => n.textContent);
    expect(labels).toContain('Number of Detections');
  });

  it('shows a legend for multiple series by default', async () => {
    const series = [makeSeries('a', [1, 2]), makeSeries('b', [2, 1])];
    const { container } = render(LineChart, { props: { series } });
    await Promise.resolve();
    const legend = container.querySelector('.legend');
    expect(legend).toBeTruthy();
  });

  it('omits the legend for a single series by default', async () => {
    const series = [makeSeries('a', [1, 2, 3])];
    const { container } = render(LineChart, { props: { series } });
    await Promise.resolve();
    const legend = container.querySelector('.legend');
    expect(legend).toBeFalsy();
  });

  it('sets an accessible label on the chart container', () => {
    const series = [makeSeries('a', [1])];
    const { container } = render(LineChart, {
      props: { series, ariaLabel: 'Detection trend chart' },
    });
    const labelled = container.querySelector('[aria-label="Detection trend chart"]');
    expect(labelled).toBeTruthy();
  });
});
