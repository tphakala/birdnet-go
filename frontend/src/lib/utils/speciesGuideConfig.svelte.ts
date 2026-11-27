/**
 * Reactive wrapper around resolveSpeciesGuideConfig.
 *
 * Three components need the same thing — SpeciesDetailModal, DetectionDetail and
 * CurrentlyHearingCard — and each previously carried its own copy of the same effect
 * (state, stale flag, .then assignment, teardown) plus its own restatement of the
 * `?? false` / `?? true` fallbacks. Keeping the fallbacks in one place matters: they
 * encode which sections default on, so three drifting copies is a real hazard.
 *
 * Lives in a `.svelte.ts` module so it can use runes. The store value is passed in as a
 * getter rather than read here, because `$store` auto-subscription is a component-level
 * feature; invoking the getter inside the effect keeps the subscription tracked by the
 * calling component.
 */
import {
  resolveSpeciesGuideConfig,
  sameSpeciesGuideUIConfig,
  type SpeciesGuideUIConfig,
} from './speciesGuideConfig';
import type { SpeciesGuideSettings } from '$lib/stores/settings';

/** Reactive view of the guide gating flags, with defaults applied while unresolved. */
export interface SpeciesGuideConfigState {
  /** False until the config resolves, so the guide UI never flashes in before gating. */
  readonly enabled: boolean;
  readonly showNotes: boolean;
  readonly showSimilarSpecies: boolean;
  readonly showTaxonomy: boolean;
}

/**
 * createSpeciesGuideConfig wires the guide config into a component's lifecycle.
 * Call during component initialisation (it registers an `$effect`).
 *
 * @param getFromStore reads `$speciesGuideStoreSettings` in the caller's reactive
 *   scope; when the authenticated settings load has populated it, it wins (and reflects
 *   live edits on the settings page). When null (guest, or settings not yet loaded),
 *   the public dashboard-settings endpoint is used instead.
 *
 *   Must be `$speciesGuideStoreSettings` and not `$dashboardSettings?.speciesGuide` —
 *   the latter is a seeded, always-truthy object, so it would suppress the public
 *   fallback and gate guests off permanently.
 */
export function createSpeciesGuideConfig(
  getFromStore: () => Partial<SpeciesGuideSettings> | null | undefined
): SpeciesGuideConfigState {
  let config = $state<SpeciesGuideUIConfig | null>(null);

  $effect(() => {
    const fromStore = getFromStore();
    let stale = false;
    void resolveSpeciesGuideConfig(fromStore).then(resolved => {
      // Drop a late resolution once the inputs have moved on, so a slow public fetch
      // cannot overwrite a newer store-derived value.
      if (stale) return;
      // Assign only on a real change. resolveSpeciesGuideConfig allocates a fresh
      // object every call, so an unrelated settings emission would otherwise rewrite
      // this $state with an equal-but-not-identical value and invalidate every
      // downstream derived for nothing.
      if (!sameSpeciesGuideUIConfig(config, resolved)) config = resolved;
    });
    return () => {
      stale = true;
    };
  });

  return {
    // Fail closed on `enabled` (hide the guide until we know it is on) and open on the
    // section flags (they default on in the backend), matching toSpeciesGuideUIConfig.
    get enabled() {
      return config?.enabled ?? false;
    },
    get showNotes() {
      return config?.showNotes ?? true;
    },
    get showSimilarSpecies() {
      return config?.showSimilarSpecies ?? true;
    },
    get showTaxonomy() {
      return config?.showTaxonomy ?? true;
    },
  };
}
