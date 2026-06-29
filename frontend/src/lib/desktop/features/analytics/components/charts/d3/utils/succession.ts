/**
 * Pure transforms for the acoustic succession streamgraph.
 *
 * A streamgraph stacks each top-N species' raw hour-of-day detection counts (24 buckets) with a
 * wiggle offset, so band width is detection volume and the bands show the diel acoustic handover
 * (dawn-chorus species -> daytime -> dusk -> night). These helpers are framework-free so they are
 * unit-tested directly rather than through the Svelte component; the d3.stack layout stays in the
 * component.
 */
import { color as d3color } from 'd3-color';

/** Hour-of-day buckets in a species' counts (0..23). */
export const SUCCESSION_HOURS = 24;

/** One species' row in the streamgraph. */
export interface SuccessionSeries {
  /** Stable identity: the D3 stack key and color-assignment seed (scientific name). */
  scientificName: string;
  /** Server-provided common name; re-localized per visitor locale in the component. */
  commonName: string;
  /** Raw hour-of-day detection counts (24 values; index = station-local hour). */
  counts: number[];
  /** Total detection count for the tooltip (defaults to the sum of counts). */
  total?: number;
}

/**
 * Picks black or white text for a label drawn on top of a band of the given fill color, by the
 * fill's relative luminance, so an inline band label stays legible across the whole species palette
 * regardless of theme. Falls back to a safe dark color when the fill cannot be parsed (e.g. an oklch
 * theme token d3-color does not understand). The 0.6 threshold biases toward dark text because the
 * translucent bands read lighter over the card background.
 */
export function readableTextColor(fill: string): string {
  const c = d3color(fill)?.rgb();
  if (!c || Number.isNaN(c.r)) return '#111827';
  const luminance = (0.299 * c.r + 0.587 * c.g + 0.114 * c.b) / 255;
  return luminance > 0.6 ? '#111827' : '#f9fafb';
}
