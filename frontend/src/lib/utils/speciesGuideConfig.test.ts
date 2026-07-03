import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('$lib/utils/api', () => ({
  api: { get: vi.fn() },
}));

import { api } from '$lib/utils/api';
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
    });
  });

  it('fails closed for null/undefined input', () => {
    expect(toSpeciesGuideUIConfig(null)).toEqual({
      enabled: false,
      showNotes: false,
      showSimilarSpecies: false,
    });
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
    expect(cfg).toEqual({ enabled: true, showNotes: true, showSimilarSpecies: false });
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
    expect(a).toEqual({ enabled: true, showNotes: false, showSimilarSpecies: true });
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
