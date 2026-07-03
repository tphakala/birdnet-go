/**
 * Species guide UI gating config, resolvable WITHOUT authentication.
 *
 * The guide/similar endpoints and GET /api/v2/settings/dashboard are
 * deliberately public so unauthenticated guests can use the species guide on
 * instances with auth enabled. Components must therefore not gate the guide UI
 * solely on the settings store ($dashboardSettings), which is populated only by
 * the auth-protected full-settings load. Use resolveSpeciesGuideConfig(): it
 * prefers the already-loaded store value (no extra request for authenticated
 * users) and falls back to one cached fetch of the public dashboard endpoint.
 */
import { api } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';
import type { SpeciesGuideSettings } from '$lib/stores/settings';

const logger = loggers.ui;

/** The gating subset of SpeciesGuideSettings, with *bool defaults applied. */
export interface SpeciesGuideUIConfig {
  enabled: boolean;
  showNotes: boolean;
  showSimilarSpecies: boolean;
}

const DISABLED: SpeciesGuideUIConfig = {
  enabled: false,
  showNotes: false,
  showSimilarSpecies: false,
};

/** Normalize raw speciesGuide settings (store or public endpoint) into UI gates.
 * The show* flags mirror the backend's *bool semantics: absent means true. */
export function toSpeciesGuideUIConfig(
  raw: Partial<SpeciesGuideSettings> | null | undefined
): SpeciesGuideUIConfig {
  if (!raw) return DISABLED;
  return {
    enabled: raw.enabled ?? false,
    showNotes: raw.showNotes ?? true,
    showSimilarSpecies: raw.showSimilarSpecies ?? true,
  };
}

// One public fetch per page load, shared by every consumer (DetectionDetail and
// an open SpeciesDetailModal must not each hit the endpoint).
let publicFetch: Promise<SpeciesGuideUIConfig> | null = null;

/**
 * Resolve the guide UI config for a component that may render for guests.
 *
 * @param fromStore the current `$dashboardSettings?.speciesGuide` value; when
 *   the authenticated settings load has populated it, it wins (and reflects
 *   live edits on the settings page). When absent (guest, or settings not yet
 *   loaded), the public dashboard-settings endpoint is fetched once and cached.
 */
export function resolveSpeciesGuideConfig(
  fromStore: Partial<SpeciesGuideSettings> | null | undefined
): Promise<SpeciesGuideUIConfig> {
  if (fromStore) return Promise.resolve(toSpeciesGuideUIConfig(fromStore));
  publicFetch ??= api
    .get<{ speciesGuide?: Partial<SpeciesGuideSettings> }>('/api/v2/settings/dashboard')
    .then(dash => toSpeciesGuideUIConfig(dash?.speciesGuide))
    .catch((e: unknown) => {
      logger.error('Failed to fetch public dashboard settings for species guide', e, {
        component: 'speciesGuideConfig',
      });
      // Fail closed (guide hidden) but allow a later consumer to retry.
      publicFetch = null;
      return DISABLED;
    });
  return publicFetch;
}

/** Test-only: clear the cached public fetch between tests. */
export function resetSpeciesGuideConfigCacheForTests(): void {
  publicFetch = null;
}
