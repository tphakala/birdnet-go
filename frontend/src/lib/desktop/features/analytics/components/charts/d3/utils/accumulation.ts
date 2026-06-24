// Species accumulation curve data + pure helpers.
//
// The backend (GET /api/v2/analytics/species/accumulation) returns one entry per calendar day in the
// requested range. `cumulativeSpecies` is the running count of distinct species first detected within
// the range on or before that day; `newSpecies` is how many first appeared that day. "First seen" is
// bounded to the selected window, not lifetime, so the curve shows how fast the species list fills up
// over the period and flattens toward an asymptote as the common species are exhausted.

export interface AccumulationPoint {
  /** Station-local calendar day, YYYY-MM-DD. */
  date: string;
  /** Running count of distinct species first seen in-period on or before this day. */
  cumulativeSpecies: number;
  /** Species first seen on this day (within the selected period). */
  newSpecies: number;
}

export interface AccumulationData {
  points: AccumulationPoint[];
}

/**
 * Final cumulative species count (the curve's asymptote value). Drives the registry's
 * not-enough-data check: a dense per-day payload would otherwise always look like plenty of data, so
 * the gate keys on how many distinct species actually accumulated. The series is monotonic
 * non-decreasing by construction, but to stay robust against a malformed payload this returns the
 * maximum cumulative value rather than blindly trusting the last element. An empty series is 0.
 */
export function finalCumulative(data: AccumulationData): number {
  let max = 0;
  for (const p of data.points) {
    if (p.cumulativeSpecies > max) max = p.cumulativeSpecies;
  }
  return max;
}
