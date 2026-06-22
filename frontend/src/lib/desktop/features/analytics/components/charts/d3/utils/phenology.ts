// Arrival/departure phenology data + pure helpers.
//
// The backend (GET /api/v2/analytics/species/phenology) returns one row per species (the top-N by
// detection volume within the selected range): the first and last false-positive-excluded detection
// as station-local YYYY-MM-DD dates, plus the in-range detection count. The chart draws one
// horizontal residency bar per species (a Gantt) so you can read seasonal arrival/departure timing:
// "when did the swifts arrive, when did they go quiet?".

import { parseLocalDateString } from '$lib/utils/date';

/** Raw API row from the phenology endpoint, before common-name enrichment. */
export interface PhenologyDatum {
  /** Species scientific name (the stable key). */
  scientificName: string;
  /** First in-range detection, station-local YYYY-MM-DD. */
  firstSeen: string;
  /** Last in-range detection, station-local YYYY-MM-DD. */
  lastSeen: string;
  /** In-range detection count (false positives excluded). */
  count: number;
}

/** One residency row the chart renders: a {@link PhenologyDatum} plus the resolved common name. */
export interface PhenologyRow extends PhenologyDatum {
  /** Localized common name, resolved client-side (falls back to the scientific name). */
  commonName: string;
}

/** Chart input: the residency rows, in arrival order (server-sorted by first-seen). */
export interface PhenologyData {
  rows: PhenologyRow[];
}

/** Milliseconds in a calendar day; used only for an inclusive day-count diff of two local midnights. */
const MS_PER_DAY = 86_400_000;

/**
 * Inclusive residency span in days between two YYYY-MM-DD dates (e.g. a species seen only on one day
 * spans 1 day; first-seen 2026-05-01 to last-seen 2026-05-10 spans 10). Both dates parse as local
 * midnight, so a DST day is 23h or 25h; Math.round absorbs that before adding the inclusive +1.
 * Returns 0 for an unparseable or reversed pair, so a malformed payload never yields a negative span.
 */
export function residencyDays(firstSeen: string, lastSeen: string): number {
  const first = parseLocalDateString(firstSeen);
  const last = parseLocalDateString(lastSeen);
  if (!first || !last || isNaN(first.getTime()) || isNaN(last.getTime())) return 0;
  const span = Math.round((last.getTime() - first.getTime()) / MS_PER_DAY) + 1;
  return span > 0 ? span : 0;
}
