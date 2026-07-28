import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('$lib/utils/api', () => ({
  api: { get: vi.fn() },
}));

import { get } from 'svelte/store';
import { api } from '$lib/utils/api';
import {
  dashboardSettings,
  settingsActions,
  settingsStore,
  speciesGuideStoreSettings,
} from '$lib/stores/settings';
import {
  resolveSpeciesGuideConfig,
  resetSpeciesGuideConfigCacheForTests,
  toSpeciesGuideUIConfig,
} from './speciesGuideConfig';

describe('toSpeciesGuideUIConfig', () => {
  it('applies backend *bool semantics: absent show flags default to true', () => {
    expect(toSpeciesGuideUIConfig({ enabled: true, enableWikipedia: false })).toEqual({
      enabled: true,
      showNotes: true,
      showSimilarSpecies: true,
      showTaxonomy: true,
    });
  });

  it('respects an explicit showTaxonomy opt-out', () => {
    expect(
      toSpeciesGuideUIConfig({ enabled: true, enableWikipedia: false, showTaxonomy: false })
    ).toEqual({
      enabled: true,
      showNotes: true,
      showSimilarSpecies: true,
      showTaxonomy: false,
    });
  });

  it('fails closed for null/undefined input', () => {
    expect(toSpeciesGuideUIConfig(null)).toEqual({
      enabled: false,
      showNotes: false,
      showSimilarSpecies: false,
      showTaxonomy: false,
    });
  });
});

/**
 * Regression: guests saw no species guide at all.
 *
 * createEmptySettings() seeds a fully-formed speciesGuide object, so
 * `$dashboardSettings?.speciesGuide` is truthy from the first render for everyone.
 * Gating on that made resolveSpeciesGuideConfig's public-endpoint fallback
 * unreachable, and an unauthenticated visitor — whose settings load never runs —
 * stayed pinned at the seeded enabled:false forever, even though the guide
 * endpoints are deliberately public.
 *
 * speciesGuideStoreSettings is the store-level guard: null until dataLoaded, so
 * "seeded defaults" is distinguishable from "real settings".
 */
describe('speciesGuideStoreSettings (guest gating guard)', () => {
  beforeEach(() => {
    settingsActions.resetAllSettings();
    // resetAllSettings restores formData but leaves dataLoaded alone, so clear it
    // explicitly to model a session that has never completed a settings load.
    settingsStore.update(state => ({ ...state, dataLoaded: false }));
  });

  it('is null on a fresh store even though the seeded speciesGuide object exists', () => {
    // The seeded object is present and truthy — the exact shape that made the old
    // `if (fromStore)` check short-circuit.
    expect(get(dashboardSettings)?.speciesGuide).toBeTruthy();
    // ...but the store has not loaded anything, so gating must not trust it.
    expect(get(speciesGuideStoreSettings)).toBeNull();
  });

  it('exposes the settings once an authenticated load has completed', () => {
    settingsStore.update(state => ({ ...state, dataLoaded: true }));
    expect(get(speciesGuideStoreSettings)).toBeTruthy();
  });

  it('null store settings resolve via the public endpoint, not the seeded defaults', async () => {
    resetSpeciesGuideConfigCacheForTests();
    vi.mocked(api.get).mockResolvedValue({ speciesGuide: { enabled: true } });

    await expect(resolveSpeciesGuideConfig(get(speciesGuideStoreSettings))).resolves.toMatchObject({
      enabled: true,
    });
    expect(api.get).toHaveBeenCalledWith('/api/v2/settings/dashboard');
  });
});

describe('resolveSpeciesGuideConfig', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetSpeciesGuideConfigCacheForTests();
  });

  it('prefers the settings-store value without fetching', async () => {
    const cfg = await resolveSpeciesGuideConfig({
      enabled: true,
      enableWikipedia: false,
      showSimilarSpecies: false,
    });
    expect(cfg).toEqual({
      enabled: true,
      showNotes: true,
      showSimilarSpecies: false,
      showTaxonomy: true,
    });
    expect(api.get).not.toHaveBeenCalled();
  });

  it('falls back to ONE cached fetch of the public dashboard endpoint for guests', async () => {
    vi.mocked(api.get).mockResolvedValue({
      speciesGuide: { enabled: true, showNotes: false },
    } as never);

    const [a, b] = await Promise.all([
      resolveSpeciesGuideConfig(undefined),
      resolveSpeciesGuideConfig(undefined),
    ]);
    expect(a).toEqual({
      enabled: true,
      showNotes: false,
      showSimilarSpecies: true,
      showTaxonomy: true,
    });
    expect(b).toEqual(a);
    expect(api.get).toHaveBeenCalledTimes(1);
    expect(api.get).toHaveBeenCalledWith('/api/v2/settings/dashboard');
  });

  it('fails closed (guide hidden) when the public fetch errors, and allows a retry', async () => {
    vi.mocked(api.get).mockRejectedValueOnce(new Error('network'));
    const cfg = await resolveSpeciesGuideConfig(undefined);
    expect(cfg.enabled).toBe(false);

    // The failed promise must not be cached forever: the next call retries.
    vi.mocked(api.get).mockResolvedValue({ speciesGuide: { enabled: true } } as never);
    const retry = await resolveSpeciesGuideConfig(undefined);
    expect(retry.enabled).toBe(true);
    expect(api.get).toHaveBeenCalledTimes(2);
  });
});
