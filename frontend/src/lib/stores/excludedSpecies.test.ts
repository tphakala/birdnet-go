/**
 * Tests for the excludedSpecies store.
 *
 * Note: common mocks (logger, i18n, toast) are defined in src/test/setup.ts.
 */

import { describe, it, expect, beforeEach, vi } from 'vitest';

vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(message: string, status: number) {
      super(message);
      this.name = 'ApiError';
      this.status = status;
    }
  },
}));

import {
  isExcluded,
  setExcluded,
  hydrateExcludedSpecies,
  resetExcludedSpeciesForTest,
} from './excludedSpecies.svelte';
import { api } from '$lib/utils/api';

const mockGet = vi.mocked(api.get);

describe('excludedSpecies store', () => {
  beforeEach(() => {
    resetExcludedSpeciesForTest();
    mockGet.mockReset();
  });

  it('hydrates the set from GET /api/v2/detections/ignored', async () => {
    mockGet.mockResolvedValue({ species: ['Eurasian Wren', 'House Sparrow'], count: 2 });

    expect(isExcluded('House Sparrow')).toBe(false);

    await hydrateExcludedSpecies();

    expect(mockGet).toHaveBeenCalledWith('/api/v2/detections/ignored');
    expect(isExcluded('House Sparrow')).toBe(true);
    expect(isExcluded('Eurasian Wren')).toBe(true);
    expect(isExcluded('Common Blackbird')).toBe(false);
  });

  it('setExcluded toggles membership', () => {
    expect(isExcluded('Great Tit')).toBe(false);
    setExcluded('Great Tit', true);
    expect(isExcluded('Great Tit')).toBe(true);
    setExcluded('Great Tit', false);
    expect(isExcluded('Great Tit')).toBe(false);
  });

  it('does not throw and leaves the set empty when hydration fails', async () => {
    mockGet.mockRejectedValue(new Error('network down'));

    await expect(hydrateExcludedSpecies()).resolves.toBeUndefined();
    expect(isExcluded('anything')).toBe(false);
  });

  it('hydrates at most once unless forced', async () => {
    mockGet.mockResolvedValue({ species: ['House Sparrow'], count: 1 });

    await Promise.all([hydrateExcludedSpecies(), hydrateExcludedSpecies()]);
    await hydrateExcludedSpecies();
    expect(mockGet).toHaveBeenCalledTimes(1);

    await hydrateExcludedSpecies(true);
    expect(mockGet).toHaveBeenCalledTimes(2);
  });

  it('a forced hydrate during an in-flight fetch triggers a fresh fetch (not coalesced)', async () => {
    let resolveFirst!: (v: { species: string[]; count: number }) => void;
    mockGet.mockReturnValueOnce(
      new Promise(resolve => {
        resolveFirst = resolve;
      })
    );
    mockGet.mockResolvedValueOnce({ species: ['Great Tit'], count: 1 });

    const p1 = hydrateExcludedSpecies(); // fetch #1, left pending
    const p2 = hydrateExcludedSpecies(true); // forced while #1 in flight
    resolveFirst({ species: ['House Sparrow'], count: 1 });
    await Promise.all([p1, p2]);

    // The forced call must not have coalesced onto the in-flight non-forced fetch.
    expect(mockGet).toHaveBeenCalledTimes(2);
    // Final state reflects the forced (second) fetch, which replaced the set.
    expect(isExcluded('Great Tit')).toBe(true);
    expect(isExcluded('House Sparrow')).toBe(false);
  });

  it('replaces (not merges) the set contents on re-hydrate', async () => {
    mockGet.mockResolvedValueOnce({ species: ['House Sparrow'], count: 1 });
    await hydrateExcludedSpecies();
    expect(isExcluded('House Sparrow')).toBe(true);

    mockGet.mockResolvedValueOnce({ species: ['Great Tit'], count: 1 });
    await hydrateExcludedSpecies(true);
    expect(isExcluded('House Sparrow')).toBe(false);
    expect(isExcluded('Great Tit')).toBe(true);
  });

  it('tolerates a malformed response without throwing', async () => {
    // species missing entirely
    mockGet.mockResolvedValue({ count: 0 } as unknown as { species: string[]; count: number });
    await expect(hydrateExcludedSpecies()).resolves.toBeUndefined();
    expect(isExcluded('anything')).toBe(false);
  });
});
