/** Narrowest chart width in pixels, used until the container has been measured. */
export const DETECTION_RATE_CHART_MIN_WIDTH = 96;

/** Default chart height in pixels, sized for a full-width card. */
export const DETECTION_RATE_CHART_HEIGHT = 100;

/**
 * Turns a measured container width into the width the chart draws at.
 * Unmeasured (0), negative and non-finite widths fall back to the minimum.
 */
export function clampChartWidth(
  measured: number,
  minWidth: number = DETECTION_RATE_CHART_MIN_WIDTH
): number {
  if (!Number.isFinite(measured)) return minWidth;
  return Math.max(minWidth, Math.floor(measured));
}
