import { describe, it, expect } from 'vitest';
import { clampChartWidth, DETECTION_RATE_CHART_MIN_WIDTH } from './detectionRateChartLayout';

describe('clampChartWidth', () => {
  it('uses the measured width when it is above the minimum', () => {
    expect(clampChartWidth(640)).toBe(640);
  });

  it('rounds fractional widths down so the chart never exceeds its container', () => {
    expect(clampChartWidth(640.9)).toBe(640);
  });

  it('falls back to the minimum before the container is measured', () => {
    expect(clampChartWidth(0)).toBe(DETECTION_RATE_CHART_MIN_WIDTH);
  });

  it('falls back to the minimum for negative and non-finite widths', () => {
    expect(clampChartWidth(-20)).toBe(DETECTION_RATE_CHART_MIN_WIDTH);
    expect(clampChartWidth(Number.NaN)).toBe(DETECTION_RATE_CHART_MIN_WIDTH);
    expect(clampChartWidth(Number.POSITIVE_INFINITY)).toBe(DETECTION_RATE_CHART_MIN_WIDTH);
  });
});
