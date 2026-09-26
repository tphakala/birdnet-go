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
  sameSpeciesGuideUIConfig,
  toSpeciesGuideUIConfig,
} from './speciesGuideConfig';

// Mirrors PUBLIC_CONFIG_TTL_MS in speciesGuideConfig.ts (not exported: it is an
// implementation detail everywhere except this test).
const PUBLIC_CONFIG_TTL_MS = 60_000;

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

// resolveSpeciesGuideConfig allocates a new object per call, so the reactive wrapper
// compares by value before writing its $state. If this ever became identity-based,
// every unrelated settings emission would invalidate the whole guide-gating graph.
describe('sameSpeciesGuideUIConfig', () => {
  const base = toSpeciesGuideUIConfig({ enabled: true, enableWikipedia: false });

  it('treats structurally equal but distinct objects as the same', () => {
    expect(sameSpeciesGuideUIConfig(base, { ...base })).toBe(true);
  });

  it('detects a change in any single gate', () => {
    expect(sameSpeciesGuideUIConfig(base, { ...base, enabled: false })).toBe(false);
    expect(sameSpeciesGuideUIConfig(base, { ...base, showNotes: false })).toBe(false);
    expect(sameSpeciesGuideUIConfig(base, { ...base, showSimilarSpecies: false })).toBe(false);
    expect(sameSpeciesGuideUIConfig(base, { ...base, showTaxonomy: false })).toBe(false);
  });

  it('handles the unresolved (null) state on either side', () => {
    expect(sameSpeciesGuideUIConfig(null, base)).toBe(false);
    expect(sameSpeciesGuideUIConfig(base, null)).toBe(false);
    expect(sameSpeciesGuideUIConfig(null, null)).toBe(true);
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

  // The cache TTL had no test at all, only a comment on a nowMs() wrapper claiming it
  // "keeps the TTL testable". Fake timers cover it directly, so the wrapper is gone and
  // the behaviour it was meant to justify is finally pinned: a guest tab must notice a
  // guide that was switched off, without a full reload.
  it('re-fetches the public config once the TTL has elapsed', async () => {
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date('2026-01-01T00:00:00Z'));
      vi.mocked(api.get).mockResolvedValue({ speciesGuide: { enabled: true } } as never);

      await expect(resolveSpeciesGuideConfig(null)).resolves.toMatchObject({ enabled: true });
      expect(api.get).toHaveBeenCalledTimes(1);

      // Still inside the window: served from cache.
      vi.advanceTimersByTime(PUBLIC_CONFIG_TTL_MS - 1);
      await resolveSpeciesGuideConfig(null);
      expect(api.get).toHaveBeenCalledTimes(1);

      // Past the window: the endpoint is consulted again, and a since-disabled guide
      // is picked up instead of being served stale until a reload.
      vi.advanceTimersByTime(2);
      vi.mocked(api.get).mockResolvedValue({ speciesGuide: { enabled: false } } as never);
      await expect(resolveSpeciesGuideConfig(null)).resolves.toMatchObject({ enabled: false });
      expect(api.get).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
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
