/**
 * Source field helpers shared by the sound card and stream cards: a disabled
 * equalizer without filters is stored as nothing.
 */
import { describe, it, expect } from 'vitest';
import type { EqualizerFilter } from '$lib/stores/settings';
import { savedEqualizer } from './sourceFields';

const FILTER: EqualizerFilter = { type: 'HighPass', frequency: 100, q: 0.7, passes: 1 };

describe('savedEqualizer', () => {
  it('stores a disabled equalizer without filters as nothing, and any other as it is', () => {
    expect(savedEqualizer({ enabled: false, filters: [] })).toBeUndefined();
    expect(savedEqualizer({ enabled: true, filters: [] })).toEqual({ enabled: true, filters: [] });
    expect(savedEqualizer({ enabled: false, filters: [FILTER] })).toEqual({
      enabled: false,
      filters: [FILTER],
    });
  });
});
