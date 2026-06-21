/**
 * Pure transforms for the species ridgeline (joyplot).
 *
 * A ridgeline draws one overlapping area row per species. These helpers compute the per-row
 * vertical layout (baselines plus the shared amplitude that maps a density value to pixels) and
 * pick a species' peak bucket for tooltips. Kept framework-free so they are unit-tested directly
 * rather than through the Svelte component.
 *
 * The component is reused across charts that express a per-species distribution: the who-sings-when
 * hour-of-day ridgeline (#1159) and the confidence distribution (#1162). Density semantics differ
 * (24 hours vs N confidence bins); these helpers are agnostic to what an index means.
 */

/** A full-amplitude ridge spans this many row-steps, so adjacent ridges overlap. */
export const RIDGE_OVERLAP = 2;

/** One species' row in a ridgeline. */
export interface RidgelineSeries {
  /** Stable identity: the D3 key and color-assignment seed (scientific name). */
  scientificName: string;
  /** Server-provided common name; re-localized per visitor locale in the component. */
  commonName: string;
  /** Normalized density along the x-axis (e.g. 24 hourly values that sum to ~1). */
  density: number[];
  /** Raw detection count for the tooltip. */
  total?: number;
}

/** Vertical placement of one ridge. */
export interface RidgelineRow {
  series: RidgelineSeries;
  /** Position in the stack (0 = top). */
  index: number;
  /** y of the row's baseline (the area's y0), in chart-group coordinates. */
  baseline: number;
}

/** Computed layout for an entire ridgeline. */
export interface RidgelineLayout {
  rows: RidgelineRow[];
  /** Pixels that the global-max density occupies above a baseline (the amplitude-scale range max). */
  amplitude: number;
  /** Largest density across all series (the amplitude-scale domain max). */
  maxDensity: number;
}

/** Largest density value across all series, or 0 when there is none. */
export function maxDensity(series: RidgelineSeries[]): number {
  let m = 0;
  for (const s of series) {
    for (const v of s.density) {
      if (v > m) m = v;
    }
  }
  return m;
}

/**
 * Computes the vertical layout for `series.length` rows within `innerHeight`.
 *
 * Baselines are spaced so the top row's full-amplitude peak touches y=0 and the bottom row's
 * baseline sits at innerHeight: with `n` rows and overlap `o`,
 *   rowStep      = innerHeight / (n - 1 + o)
 *   amplitude    = o * rowStep
 *   baseline[i]  = amplitude + i * rowStep
 * so non-adjacent ridges can still overlap (amplitude > rowStep when o > 1) without the tallest
 * ridge clipping above the plot. Returns an empty layout for no rows or a non-positive height.
 */
export function ridgelineLayout(
  series: RidgelineSeries[],
  innerHeight: number,
  overlap: number = RIDGE_OVERLAP
): RidgelineLayout {
  const n = series.length;
  if (n === 0 || innerHeight <= 0) {
    return { rows: [], amplitude: 0, maxDensity: 0 };
  }
  const rowStep = innerHeight / (n - 1 + overlap);
  const amplitude = overlap * rowStep;
  const rows: RidgelineRow[] = [];
  for (const [index, s] of series.entries()) {
    rows.push({ series: s, index, baseline: amplitude + index * rowStep });
  }
  return { rows, amplitude, maxDensity: maxDensity(series) };
}

/**
 * Index of a species' largest density bucket (its peak activity), or -1 when the density is empty
 * or all zero. Used to label the tooltip with the peak time / bin.
 */
export function peakIndex(density: number[]): number {
  let idx = -1;
  let best = 0;
  for (const [i, v] of density.entries()) {
    if (v > best) {
      best = v;
      idx = i;
    }
  }
  return idx;
}
