/**
 * All-time summary of one species for the detection detail page.
 *
 * The summary endpoint returns every species in one response and takes seconds
 * on a large database, so the rows are kept for a few minutes and shared by
 * every detail page opened in that time (stepping through detections would
 * otherwise repeat the request on each step).
 */
import { buildAppUrl } from '$lib/utils/urlHelpers';
import { isSpeciesSummary } from './detailGuards';
import type { SpeciesSummary } from './detailTypes';

const CACHE_TTL_MS = 300_000;

let cached: { at: number; rows: unknown[] } | null = null;
let inFlight: Promise<unknown[]> | null = null;

async function fetchRows(): Promise<unknown[]> {
  const response = await fetch(buildAppUrl('/api/v2/analytics/species/summary'));
  if (!response.ok) {
    throw new Error(`species summary request failed: ${response.status}`);
  }
  const data: unknown = await response.json();
  return Array.isArray(data) ? data : [];
}

// Counts the times the kept rows were dropped, so a request that was out at the time does
// not store rows read before the change.
let generation = 0;

/**
 * Drops the kept rows and lets a request already out run on without keeping its answer.
 * A change of a detection's species moves one detection between two species' totals, so
 * rows kept or being read before it are out of date for both.
 */
export function forgetSpeciesSummary(): void {
  generation += 1;
  cached = null;
  inFlight = null;
}

/** The summary row of a species, or null when the species has none. Rejects when the request fails. */
export async function loadSpeciesSummary(scientificName: string): Promise<SpeciesSummary | null> {
  let rows = cached && Date.now() - cached.at <= CACHE_TTL_MS ? cached.rows : null;
  if (!rows) {
    const startedAt = generation;
    const request = (inFlight ??= fetchRows().finally(() => {
      if (inFlight === request) inFlight = null;
    }));
    rows = await request;
    if (startedAt === generation) cached = { at: Date.now(), rows };
  }
  // Only the row asked for is checked: the list holds every species of the station.
  const row = rows.find(
    candidate => isSpeciesSummary(candidate) && candidate.scientific_name === scientificName
  );
  return isSpeciesSummary(row) ? row : null;
}
