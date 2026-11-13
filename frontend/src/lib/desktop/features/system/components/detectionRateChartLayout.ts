/** Narrowest chart width in pixels, used while the container is unmeasured or narrower than this. */
export const DETECTION_RATE_CHART_MIN_WIDTH = 96;

/** Default chart height in pixels, sized for a full-width card. */
export const DETECTION_RATE_CHART_HEIGHT = 100;

/**
 * Turns a measured container width into the width the chart draws at.
 * Widths below the minimum, including unmeasured (0), negative and non-finite ones, are raised to it.
 */
export function clampChartWidth(measured: number): number {
  if (!Number.isFinite(measured)) return DETECTION_RATE_CHART_MIN_WIDTH;
  return Math.max(DETECTION_RATE_CHART_MIN_WIDTH, Math.floor(measured));
}
