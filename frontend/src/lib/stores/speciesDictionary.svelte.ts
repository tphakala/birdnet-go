/**
 * speciesDictionary.svelte.ts
 *
 * Per-visitor species-name dictionary store.
 *
 * Fetches the backend-generated, per-locale dictionary mapping scientific names
 * to localized common names and exposes forward and reverse lookup maps.
 *
 * Contracts:
 * - One fetch per (locale, version) pair. The cache key includes the dataset
 *   version so a backend deployment that bumps the version automatically
 *   invalidates stale entries.
 * - Language-switch race guard: if the active locale changes while a fetch is
 *   in flight, the stale result is discarded and maps always reflect the most
 *   recently requested locale.
 * - The reverse map retains ALL scientific names per normalized common name
 *   (unlike the settings-page speciesNames.ts which deletes on conflict), so
 *   an ambiguous common name yields multiple candidate species for search.
 *
 * Usage:
 *   import { loadDictionary, localizeScientific, resolveCommonToScientific, searchScientificByCommon } from './speciesDictionary.svelte';
 */

import { api } from '$lib/utils/api';
import { getLocale } from '$lib/i18n/store.svelte';
import { getSpeciesDictVersion } from '$lib/stores/appState.svelte';
import { normalizeForLookup } from '$lib/utils/speciesNames';
import { loggers } from '$lib/utils/logger';
import type { Locale } from '$lib/i18n/config';

const logger = loggers.ui;

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** The raw JSON shape returned by GET /api/v2/species/dictionary/<locale> */
type DictionaryResponse = Record<string, string>;

/** The two maps derived from one locale's dictionary. */
interface DictionaryMaps {
  /** scientific name (verbatim, as returned by the backend) -> localized common name */
  forward: Map<string, string>;
  /**
   * NFC-folded common name -> array of scientific names.
   * Multiple entries exist when two scientific names share the same normalized
   * common name. De-duplicated within each array.
   */
  reverse: Map<string, string[]>;
}

/** Reactive state exposed to consumers. */
interface DictionaryState {
  /** The locale these maps were built for. */
  locale: Locale;
  /** Forward map: scientific name -> localized common name. */
  forward: Map<string, string>;
  /** Reverse map: NFC-normalized common name -> scientific names. */
  reverse: Map<string, string[]>;
}

// ---------------------------------------------------------------------------
// Internal cache
// ---------------------------------------------------------------------------

/**
 * Cache entry: maps keyed by the cache token (locale + version) at the time
 * they were fetched.
 */
interface CacheEntry {
  maps: DictionaryMaps;
  /** The dataset version that was current when this entry was fetched. */
  version: string;
}

/** locale -> CacheEntry */
const cache = new Map<string, CacheEntry>();

// ---------------------------------------------------------------------------
// Reactive state (Svelte 5 runes)
// ---------------------------------------------------------------------------

const EMPTY_MAPS: DictionaryMaps = {
  forward: new Map(),
  reverse: new Map(),
};

/** Reactive current state, always reflecting the latest successfully loaded locale. */
let current = $state<DictionaryState>({
  locale: getLocale(),
  forward: EMPTY_MAPS.forward,
  reverse: EMPTY_MAPS.reverse,
});

/** Monotonically increasing counter used to detect superseded fetches. */
let fetchSeq = 0;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Build forward and reverse maps from the raw backend dictionary object.
 * The reverse map keeps ALL scientific names per normalized common name.
 */
function buildMaps(dict: DictionaryResponse): DictionaryMaps {
  const forward = new Map<string, string>();
  const reverse = new Map<string, string[]>();

  for (const [scientific, common] of Object.entries(dict)) {
    // Forward: verbatim scientific name -> common name
    forward.set(scientific, common);

    // Reverse: NFC-normalized common name -> array of scientific names
    const key = normalizeForLookup(common);
    const existing = reverse.get(key);
    if (existing === undefined) {
      reverse.set(key, [scientific]);
    } else if (!existing.includes(scientific)) {
      existing.push(scientific);
    }
  }

  return { forward, reverse };
}

/**
 * Compute the URL for the dictionary endpoint.
 * Appends ?v=<version> only when the version is non-empty.
 */
function dictUrl(locale: Locale): string {
  const version = getSpeciesDictVersion();
  const base = `/api/v2/species/dictionary/${locale}`;
  return version ? `${base}?v=${encodeURIComponent(version)}` : base;
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/**
 * Load (or return from cache) the species-name dictionary for the given locale.
 *
 * Fetches are guarded against race conditions: if a newer locale is requested
 * before a pending fetch completes, the stale result is discarded.
 *
 * @param locale - The locale to load. Defaults to the current UI locale.
 */
export async function loadDictionary(locale: Locale = getLocale()): Promise<void> {
  const version = getSpeciesDictVersion();
  const cached = cache.get(locale);

  // Cache hit: same (locale, version). Nothing to fetch.
  if (cached?.version === version) {
    // Bump the sequence counter BEFORE assigning current so any in-flight fetch
    // for a different locale sees seq !== fetchSeq and discards its result. Without
    // this, a pending fetch could resolve afterward and clobber current with a
    // stale locale.
    fetchSeq++;
    // Ensure current reflects this locale if it is still the active one.
    if (current.locale !== locale || current.forward !== cached.maps.forward) {
      current = { locale, forward: cached.maps.forward, reverse: cached.maps.reverse };
    }
    return;
  }

  // Claim a sequence number so we can detect superseded fetches.
  const seq = ++fetchSeq;

  logger.debug(`speciesDictionary: fetching ${locale} (seq=${seq}, v=${version || 'none'})`);

  try {
    const dict = await api.get<DictionaryResponse>(dictUrl(locale));

    // Check whether this fetch is still the latest one.
    if (seq !== fetchSeq) {
      logger.debug(`speciesDictionary: discarding stale result for ${locale} (seq=${seq})`);
      return;
    }

    const maps = buildMaps(dict);
    cache.set(locale, { maps, version });
    current = { locale, forward: maps.forward, reverse: maps.reverse };

    logger.debug(`speciesDictionary: loaded ${locale}, ${maps.forward.size} entries (seq=${seq})`);
  } catch (err) {
    // Only log if this fetch is still current (suppress noise from superseded fetches).
    if (seq === fetchSeq) {
      logger.error(`speciesDictionary: failed to load ${locale}`, err);
    }
  }
}

/**
 * Look up the localized common name for a scientific name in the current locale.
 *
 * @param scientificName - The scientific name to look up (verbatim, case-sensitive).
 * @returns The localized common name, or undefined if not found.
 */
export function localizeScientific(scientificName: string): string | undefined {
  return current.forward.get(scientificName);
}

/**
 * Find all scientific names whose NFC-normalized localized common name matches
 * the given text (exact normalized match, not a prefix/substring search).
 *
 * Returns an array because multiple scientific names may share the same common
 * name in a given locale.
 *
 * @param text - The common name to search for. Normalized via NFC + lowercase.
 * @returns Array of matching scientific names (empty when no match).
 */
export function resolveCommonToScientific(text: string): string[] {
  return current.reverse.get(normalizeForLookup(text)) ?? [];
}

/** Minimum query length for substring search (avoids matching everything on 1 char). */
const MIN_SEARCH_LENGTH = 2;

/** Maximum number of scientific names returned (mirrors the backend cap). */
const MAX_SEARCH_RESULTS = 100;

/**
 * Find all scientific names whose NFC-normalized localized common name CONTAINS
 * the given text (substring match, which naturally includes exact matches).
 *
 * Used by the search box so a visitor can type a partial species name in their
 * own UI locale and have it resolved to scientific names the backend accepts.
 *
 * - The query and the stored keys are both NFC-normalized + lowercased via
 *   normalizeForLookup, so composing-keyboard (NFD) input still matches.
 * - Queries shorter than MIN_SEARCH_LENGTH return [] to avoid matching the
 *   entire dictionary on a single character.
 * - Results are de-duplicated and capped at MAX_SEARCH_RESULTS.
 *
 * @param text - The (possibly partial) common name to search for.
 * @returns Array of matching scientific names (empty when no match).
 */
export function searchScientificByCommon(text: string): string[] {
  const needle = normalizeForLookup(text);
  if (needle.length < MIN_SEARCH_LENGTH) return [];

  const seen = new Set<string>();
  const matches: string[] = [];

  for (const [commonKey, scientificNames] of current.reverse) {
    if (!commonKey.includes(needle)) continue;

    for (const scientific of scientificNames) {
      if (seen.has(scientific)) continue;
      seen.add(scientific);
      matches.push(scientific);
      if (matches.length >= MAX_SEARCH_RESULTS) return matches;
    }
  }

  return matches;
}

// ---------------------------------------------------------------------------
// Test helper (not for production use)
// ---------------------------------------------------------------------------

/**
 * Reset all internal state.
 * Exported ONLY for use in Vitest tests. Do not call from application code.
 * @internal
 */
export function resetDictionaryForTest(): void {
  cache.clear();
  fetchSeq = 0;
  current = { locale: getLocale(), forward: EMPTY_MAPS.forward, reverse: EMPTY_MAPS.reverse };
}
