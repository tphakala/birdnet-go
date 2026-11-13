import { describe, it, expect } from 'vitest';
import { clampChartWidth, DETECTION_RATE_CHART_MIN_WIDTH } from './detectionRateChartLayout';

describe('clampChartWidth', () => {
  it('uses the measured width when it is above the minimum', () => {
    expect(clampChartWidth(640)).toBe(640);
  });

  it('rounds fractional widths down to whole pixels', () => {
    expect(clampChartWidth(640.9)).toBe(640);
  });

  it('falls back to the minimum before the container is measured', () => {
    expect(clampChartWidth(0)).toBe(DETECTION_RATE_CHART_MIN_WIDTH);
  });

  it('raises measured widths below the minimum and keeps the minimum and the next width', () => {
    expect(clampChartWidth(DETECTION_RATE_CHART_MIN_WIDTH / 2)).toBe(
      DETECTION_RATE_CHART_MIN_WIDTH
    );
    expect(clampChartWidth(DETECTION_RATE_CHART_MIN_WIDTH - 1)).toBe(
      DETECTION_RATE_CHART_MIN_WIDTH
    );
    expect(clampChartWidth(DETECTION_RATE_CHART_MIN_WIDTH)).toBe(DETECTION_RATE_CHART_MIN_WIDTH);
    expect(clampChartWidth(DETECTION_RATE_CHART_MIN_WIDTH + 1)).toBe(
      DETECTION_RATE_CHART_MIN_WIDTH + 1
    );
  });

  it('falls back to the minimum for negative and non-finite widths', () => {
    expect(clampChartWidth(-20)).toBe(DETECTION_RATE_CHART_MIN_WIDTH);
    expect(clampChartWidth(Number.NaN)).toBe(DETECTION_RATE_CHART_MIN_WIDTH);
    expect(clampChartWidth(Number.POSITIVE_INFINITY)).toBe(DETECTION_RATE_CHART_MIN_WIDTH);
  });
});
