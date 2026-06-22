/**
 * excludedSpecies.svelte.ts
 *
 * Single source of truth for the set of species the user has chosen to ignore
 * ("exclude") via the detection action menus.
 *
 * The set is keyed by the SERVER common name (detection.commonName), NOT the
 * localized display name from localizeSpeciesName(). The backend stores and
 * returns exactly this string: POST /api/v2/detections/ignore writes
 * req.common_name into Realtime.Species.Exclude, and
 * GET /api/v2/detections/ignored returns that same list. Keying on anything
 * else (scientificName, a localized name) would silently never match.
 *
 * A SvelteSet (not a plain Set) is required: it is shared by reference across
 * every detection card/row/grid, and SvelteSet.has() reads are tracked, so an
 * in-place add()/delete() re-evaluates every consumer's isExcluded binding.
 * A plain Set shared by reference would leave reference-equality consumers
 * (e.g. DetectionCardGrid) stale.
 *
 * Usage:
 *   import { isExcluded, setExcluded, hydrateExcludedSpecies } from '$lib/stores/excludedSpecies.svelte';
 *   onMount(() => { void hydrateExcludedSpecies(); });
 *   // read:  isExcluded(detection.commonName)
 *   // write: setExcluded(resp.common_name, resp.is_excluded)
 */

import { SvelteSet } from 'svelte/reactivity';
import { api } from '$lib/utils/api';
import { getLogger } from '$lib/utils/logger';

const logger = getLogger('excludedSpecies');

/**
 * Response shape of GET /api/v2/detections/ignored (ExcludedSpeciesResponse).
 * `species` is nullable because Go marshals an empty/nil exclude slice as null.
 */
interface ExcludedSpeciesResponse {
  species: string[] | null;
  count: number;
}

/** Reactive set of excluded common names. Module-level singleton. */
const excluded = new SvelteSet<string>();

/** True once a hydration has completed successfully (so we fetch at most once). */
let hydrated = false;
/** In-flight hydration promise, shared so concurrent callers await one fetch. */
let inflight: Promise<void> | null = null;
/** Monotonic id of the latest hydration run, used to detect a superseded run. */
let runId = 0;

/** Whether the given species (by server common name) is currently excluded. */
export function isExcluded(commonName: string): boolean {
  return excluded.has(commonName);
}

/**
 * Reflect a toggle result in the shared set. Pass the authoritative state from
 * the ignore endpoint's response (resp.is_excluded), never an optimistic guess.
 */
export function setExcluded(commonName: string, excludedNow: boolean): void {
  if (excludedNow) {
    excluded.add(commonName);
  } else {
    excluded.delete(commonName);
  }
}

/**
 * Load the exclude list from the backend into the shared set.
 *
 * Fetches at most once per session unless `force` is true. Concurrent callers
 * share a single in-flight request. A failed fetch is logged and swallowed so
 * the UI keeps working (the set simply stays as it was), and does not mark the
 * store hydrated, so a later call can retry.
 *
 * @param force - Re-fetch even if already hydrated (e.g. after an external change).
 */
export async function hydrateExcludedSpecies(force = false): Promise<void> {
  if (hydrated && !force) return;
  // Coalesce concurrent non-forced callers onto the single in-flight fetch.
  if (inflight && !force) return inflight;
  // A forced refresh waits for any in-flight fetch to settle first, so the two
  // requests do not overlap (and the old one cannot clobber the new result).
  if (inflight) await inflight.catch(() => {});

  const myRunId = ++runId;
  const run = (async () => {
    try {
      const resp = await api.get<ExcludedSpeciesResponse>('/api/v2/detections/ignored');
      const species = Array.isArray(resp.species) ? resp.species : [];
      excluded.clear();
      for (const name of species) {
        if (typeof name === 'string' && name.length > 0) {
          excluded.add(name);
        }
      }
      hydrated = true;
    } catch (err) {
      logger.error('Failed to load excluded species list:', err);
    } finally {
      // Only clear if a newer run has not superseded this one.
      if (runId === myRunId) inflight = null;
    }
  })();
  inflight = run;
  return run;
}

/**
 * Reset all internal state. Exported ONLY for Vitest. Do not call from app code.
 * @internal
 */
export function resetExcludedSpeciesForTest(): void {
  excluded.clear();
  hydrated = false;
  inflight = null;
  runId = 0;
}
