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
});
