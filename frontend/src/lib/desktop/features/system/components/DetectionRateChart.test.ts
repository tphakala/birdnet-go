import { describe, it, expect, afterEach, vi } from 'vitest';
import { cleanup } from '@testing-library/svelte';
import { renderTyped } from '../../../../../test/render-helpers';
import DetectionRateChart from './DetectionRateChart.svelte';

const MEASURED_WIDTH = 640;

describe('DetectionRateChart sizing', () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('draws at the minimum width and the fixed height while the container is unmeasured', () => {
    const { container } = renderTyped(DetectionRateChart, { props: { data: [] } });
    const svg = container.querySelector('svg');

    expect(svg?.getAttribute('width')).toBe('96');
    expect(svg?.getAttribute('height')).toBe('100');
    expect(svg?.getAttribute('viewBox')).toBe('0 0 96 100');
  });

  it('draws at the measured container width', () => {
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(MEASURED_WIDTH);
    const { container } = renderTyped(DetectionRateChart, { props: { data: [] } });
    const svg = container.querySelector('svg');

    expect(svg?.getAttribute('width')).toBe(String(MEASURED_WIDTH));
    expect(svg?.getAttribute('height')).toBe('100');
    expect(svg?.getAttribute('viewBox')).toBe(`0 0 ${MEASURED_WIDTH} 100`);
  });

  it('spreads the bars across the measured container width', () => {
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(MEASURED_WIDTH);
    const data = [
      { hour: '2026-02-24T10:00:00Z', count: 1 },
      { hour: '2026-02-24T11:00:00Z', count: 2 },
      { hour: '2026-02-24T12:00:00Z', count: 3 },
    ];
    const { container } = renderTyped(DetectionRateChart, { props: { data } });
    const bars = Array.from(container.querySelectorAll('rect'));
    const last = bars[bars.length - 1];
    const lastEnd = Number(last.getAttribute('x')) + Number(last.getAttribute('width'));

    expect(bars).toHaveLength(data.length);
    expect(lastEnd).toBeGreaterThan(MEASURED_WIDTH * 0.9);
    expect(lastEnd).toBeLessThanOrEqual(MEASURED_WIDTH);
  });
});
