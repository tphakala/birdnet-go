// analyticsControls.svelte.ts
//
// Module-level Svelte 5 store: single owner of analytics URL filter state
// (range/start/end/species/source) and single writer of browser history for the
// analytics routes. Per-view chart group is derived from the route by each page,
// NOT stored here. Filter state persists across analytics pages.
import { navigation } from '$lib/stores/navigation.svelte';
import { createSpeciesId, type Species, type SpeciesFrequency } from '$lib/types/species';
import { t } from '$lib/i18n';
import { getLogger } from '$lib/utils/logger';
import { buildAppUrl } from '$lib/utils/urlHelpers';

import {
  parseAnalyticsParams,
  serializeAnalyticsParams,
  resolveDateRange,
  formatDateForAPI,
} from './analyticsParams';
import type { AnalyticsParams, AudioSourceOption } from './types';

const logger = getLogger('analytics-controls');
const SPECIES_SUMMARY_LIMIT = 50;
const AUTO_SELECT_TOP = 5;

// Lifted verbatim from Analytics.svelte.
interface SpeciesSummaryResponse {
  scientific_name?: string;
  common_name?: string;
  count?: number;
}

// Lifted verbatim from Analytics.svelte.
interface SourcesResponse {
  sources?: unknown;
}

// Lifted verbatim from Analytics.svelte (lines 150-155).
function classifyFrequency(count: number): SpeciesFrequency {
  if (count > 100) return 'very-common';
  if (count > 50) return 'common';
  if (count > 10) return 'uncommon';
  return 'rare';
}

// Lifted verbatim from Analytics.svelte (lines 253-272).
// Coerce the /analytics/sources payload ({ sources: [{ id, name, count }] }) defensively into the
// control bar's option shape, dropping rows without a usable id and falling back name -> id.
function coerceSources(data: unknown): AudioSourceOption[] {
  if (!data || typeof data !== 'object' || Array.isArray(data)) return [];
  const raw = (data as SourcesResponse).sources;
  if (!Array.isArray(raw)) return [];
  return raw
    .map(item => {
      if (!item || typeof item !== 'object') return null;
      const s = item as { id?: unknown; name?: unknown; count?: unknown };
      // The backend serialises id as a string; also accept a finite number defensively so a future
      // numeric-id payload does not silently drop sources.
      let id = '';
      if (typeof s.id === 'string') id = s.id;
      else if (typeof s.id === 'number' && Number.isFinite(s.id)) id = String(s.id);
      if (!id) return null;
      const name = typeof s.name === 'string' && s.name ? s.name : id;
      const count = typeof s.count === 'number' && Number.isFinite(s.count) ? s.count : 0;
      return { id, name, count };
    })
    .filter((s): s is AudioSourceOption => s !== null);
}

/**
 * Shape of the analytics control store returned by createAnalyticsControls().
 * The store is the single writer of analytics browser history; consumers read
 * state and call mutating methods - they never write URLs directly.
 */
export interface AnalyticsControls {
  readonly params: AnalyticsParams;
  /** Serialized query string (no leading ?) reflecting the current params. */
  readonly queryString: string;
  /** Scientific name -> common name map built from the species summary fetch. */
  readonly speciesNames: Map<string, string>;
  readonly availableSpecies: Species[];
  readonly loadingSpecies: boolean;
  readonly availableSources: AudioSourceOption[];
  readonly loadingSources: boolean;
  /** Merge partial params, resolve dates, and push/replace the URL. */
  applyParams(partial: Partial<AnalyticsParams>, mode?: 'push' | 'replace'): void;
  /** Fetch species for the current date range (cached by range key). */
  ensureSpecies(): void;
  /** Fetch audio sources once per session. */
  ensureSources(): void;
  /** Re-parse filter state from window.location.search (called on popstate). */
  syncFromUrl(): void;
  /** Register the single popstate listener; returns a cleanup function. */
  init(): () => void;
}

function currentSearch(): string {
  return typeof window !== 'undefined' ? window.location.search : '';
}

function rangeKey(p: AnalyticsParams): string {
  return `${formatDateForAPI(p.startDate)}|${formatDateForAPI(p.endDate)}`;
}

export function createAnalyticsControls(): AnalyticsControls {
  let params = $state<AnalyticsParams>(parseAnalyticsParams(currentSearch()));
  let availableSpecies = $state<Species[]>([]);
  let loadingSpecies = $state(false);
  let availableSources = $state<AudioSourceOption[]>([]);
  let loadingSources = $state(false);

  let speciesController: AbortController | null = null;
  let sourcesController: AbortController | null = null;
  let speciesKey = ''; // rangeKey the current availableSpecies were fetched for
  let sourcesRequested = false;
  let popstateBound = false;

  const speciesNames = $derived(
    new Map(availableSpecies.map(s => [s.scientificName ?? s.id, s.commonName]))
  );

  function writeUrl(mode: 'push' | 'replace'): void {
    const qs = serializeAnalyticsParams(params);
    const target = navigation.currentPath + (qs ? `?${qs}` : '');
    if (mode === 'replace') navigation.redirect(target);
    else navigation.navigate(target);
  }

  function applyParams(partial: Partial<AnalyticsParams>, mode: 'push' | 'replace' = 'push'): void {
    const next: AnalyticsParams = { ...params, ...partial };
    const [startDate, endDate] = resolveDateRange(next.range, next.start, next.end);
    next.startDate = startDate;
    next.endDate = endDate;
    params = next;
    writeUrl(mode);
  }

  function syncFromUrl(): void {
    params = parseAnalyticsParams(currentSearch());
  }

  function maybeAutoSelectSpecies(): void {
    if (params.species.length > 0) return;
    if (availableSpecies.length === 0) return;
    const top = availableSpecies
      .slice(0, Math.min(availableSpecies.length, AUTO_SELECT_TOP))
      .map(s => s.scientificName ?? s.id);
    applyParams({ species: top }, 'replace');
  }

  // Adapted from Analytics.svelte fetchAvailableSpecies (lines 171-220).
  // The untrack(() => params) snapshot was dropped: this function is called
  // imperatively (not inside an effect), so reading params directly is safe.
  async function fetchAvailableSpecies(): Promise<void> {
    speciesController?.abort();
    const ac = new AbortController();
    speciesController = ac;
    loadingSpecies = true;

    try {
      const search = new URLSearchParams({
        start_date: formatDateForAPI(params.startDate),
        end_date: formatDateForAPI(params.endDate),
        limit: String(SPECIES_SUMMARY_LIMIT),
      });
      const response = await fetch(buildAppUrl(`/api/v2/analytics/species/summary?${search}`), {
        signal: ac.signal,
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}: ${response.statusText}`);

      const data: unknown = await response.json();
      // Invariant: id === scientificName for real rows.
      availableSpecies = Array.isArray(data)
        ? (data as SpeciesSummaryResponse[]).map((item, index) => {
            const count = item.count ?? 0;
            return {
              id: createSpeciesId(item.scientific_name ?? `species-${index}`),
              commonName: item.common_name ?? t('common.unknown'),
              scientificName: item.scientific_name ?? t('common.unknown'),
              frequency: classifyFrequency(count),
              category: t('analytics.advanced.categories.birds'),
              description: t('analytics.advanced.detections', { count }),
              count,
            };
          })
        : [];

      maybeAutoSelectSpecies();
    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') return;
      logger.error('Failed to fetch available species', err);
      availableSpecies = [];
    } finally {
      if (!ac.signal.aborted) loadingSpecies = false;
    }
  }

  // Adapted from Analytics.svelte fetchAvailableSources (lines 274-296).
  async function fetchAvailableSources(): Promise<void> {
    sourcesController?.abort();
    const ac = new AbortController();
    sourcesController = ac;
    loadingSources = true;

    try {
      // The list is all-history (not range-scoped) so the dropdown stays stable as the date range
      // changes; the per-source charts still re-fetch their own data per range.
      const response = await fetch(buildAppUrl('/api/v2/analytics/sources'), { signal: ac.signal });
      if (!response.ok) throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      availableSources = coerceSources(await response.json());
    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') return;
      logger.error('Failed to fetch available sources', err);
      availableSources = [];
      // Clear the latch so the next time a source-aware tab becomes active the list is retried
      // rather than staying empty for the rest of the session after a transient failure.
      sourcesRequested = false;
    } finally {
      if (!ac.signal.aborted) loadingSources = false;
    }
  }

  function ensureSpecies(): void {
    const key = rangeKey(params);
    if (key === speciesKey && (availableSpecies.length > 0 || loadingSpecies)) return;
    speciesKey = key;
    void fetchAvailableSpecies();
  }

  function ensureSources(): void {
    if (sourcesRequested) return;
    sourcesRequested = true;
    void fetchAvailableSources();
  }

  function init(): () => void {
    if (typeof window === 'undefined' || popstateBound) return () => {};
    const onPop = () => syncFromUrl();
    window.addEventListener('popstate', onPop);
    popstateBound = true;
    return () => {
      window.removeEventListener('popstate', onPop);
      popstateBound = false;
    };
  }

  return {
    get params() {
      return params;
    },
    get queryString() {
      return serializeAnalyticsParams(params);
    },
    get speciesNames() {
      return speciesNames;
    },
    get availableSpecies() {
      return availableSpecies;
    },
    get loadingSpecies() {
      return loadingSpecies;
    },
    get availableSources() {
      return availableSources;
    },
    get loadingSources() {
      return loadingSources;
    },
    applyParams,
    ensureSpecies,
    ensureSources,
    syncFromUrl,
    init,
  };
}

// Module-level singleton: import this in components and pages.
export const analyticsControls = createAnalyticsControls();
