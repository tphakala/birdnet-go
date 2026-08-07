/**
 * rarity.svelte.ts
 *
 * Lazy, session-scoped cache of geomodel occurrence probabilities for the
 * configured location and current week. Used to flag "rare" detections in the
 * dashboard and detection lists without an extra per-row backend call.
 *
 * Contract:
 * - One fetch per session against GET /api/v2/range/species/scores (which returns
 *   ALL geomodel species with raw scores, threshold 0). Concurrent callers dedupe
 *   on the in-flight promise.
 * - The endpoint is primary-model only, so species with no geomodel coverage
 *   (e.g. bats, Perch) are absent from the map. isRareSpecies() therefore returns
 *   false for them: they have no occurrence probability and must never be flagged.
 * - "Rare" is defined relative to a caller-supplied threshold (the configurable
 *   Dashboard.Rarity.Threshold), consistent with the rarity shown on the detail page.
 *
 * Usage:
 *   import { loadRarityScores, isRareSpecies, getOccurrence } from '$lib/stores/rarity.svelte';
 */

import { api } from '$lib/utils/api';
import { getLogger } from '$lib/utils/logger';

const logger = getLogger('rarity');

/** One species entry from GET /api/v2/range/species/scores. */
interface RangeSpeciesScore {
  scientificName?: string;
  score?: number;
}

/** Response shape of GET /api/v2/range/species/scores (only fields we use). */
interface RangeScoresResponse {
  species?: RangeSpeciesScore[];
}

// Module-level reactive state, populated once per session.
let scores = $state<Map<string, number>>(new Map());
let loaded = $state(false);
let inFlight: Promise<void> | null = null;

/** Normalize a scientific name for case/whitespace-insensitive lookup. */
function normalize(scientificName: string): string {
  return scientificName.trim().toLowerCase();
}

/**
 * Fetch geomodel occurrence scores once and cache them. Safe to call repeatedly;
 * concurrent calls share a single in-flight request and it no-ops once loaded.
 */
export async function loadRarityScores(): Promise<void> {
  if (loaded) return;
  if (inFlight) return inFlight;

  inFlight = (async () => {
    try {
      // names=false skips per-species common-name resolution (the dominant cost
      // when fetching all geomodel species); we only need scientificName -> score.
      const data = await api.get<RangeScoresResponse>('/api/v2/range/species/scores?names=false');
      const map = new Map<string, number>();
      for (const entry of data.species ?? []) {
        if (entry.scientificName && typeof entry.score === 'number') {
          map.set(normalize(entry.scientificName), entry.score);
        }
      }
      scores = map;
      loaded = true;
    } catch (error) {
      // Best-effort: the indicator simply stays hidden. Failures are expected
      // (offline, unauthenticated/public mode, geomodel not ready), so warn
      // rather than error to avoid log noise.
      logger.warn('failed to load rarity scores', error);
    } finally {
      inFlight = null;
    }
  })();

  return inFlight;
}

/**
 * Returns the geomodel occurrence probability (0-1) for a species, or undefined
 * when the species is not in the geomodel (no coverage) or scores are not loaded.
 */
export function getOccurrence(scientificName: string | undefined): number | undefined {
  if (!scientificName) return undefined;
  return scores.get(normalize(scientificName));
}

/**
 * Returns true when the species' occurrence probability is at or below the
 * threshold. Species without geomodel coverage return false (never flagged rare).
 */
export function isRareSpecies(scientificName: string | undefined, threshold: number): boolean {
  const occurrence = getOccurrence(scientificName);
  return occurrence !== undefined && occurrence <= threshold;
}

/** Whether the occurrence-score cache has finished loading. */
export function rarityScoresLoaded(): boolean {
  return loaded;
}

/**
 * Clear the cached occurrence scores so the next loadRarityScores() refetches.
 * Called when the configured location changes on a settings save (the scores are
 * location-specific and the endpoint carries no location marker to key on), and
 * used by tests to start from a clean state.
 */
export function resetRarityScores(): void {
  scores = new Map();
  loaded = false;
  inFlight = null;
}
