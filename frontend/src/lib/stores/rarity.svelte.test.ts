/**
 * Tests for the rarity store.
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
  loadRarityScores,
  getOccurrence,
  isRareSpecies,
  rarityScoresLoaded,
  resetRarityScores,
} from './rarity.svelte';
import { api } from '$lib/utils/api';

const mockGet = vi.mocked(api.get);

// A representative slice of GET /api/v2/range/species/scores.
const scoresResponse = {
  species: [
    { scientificName: 'Agelaius phoeniceus', score: 0.82 }, // common
    { scientificName: 'Spinus tristis', score: 0.2 }, // rare at 0.25
    { scientificName: 'Setophaga cerulea', score: 0.03 }, // very rare
    // No score -> ignored
    { scientificName: 'Corvus corax' },
  ],
};

describe('rarity store', () => {
  beforeEach(() => {
    resetRarityScores();
    mockGet.mockReset();
  });

  it('loads occurrence scores from GET /api/v2/range/species/scores', async () => {
    mockGet.mockResolvedValue(scoresResponse);

    expect(rarityScoresLoaded()).toBe(false);
    await loadRarityScores();

    expect(mockGet).toHaveBeenCalledWith('/api/v2/range/species/scores?names=false');
    expect(rarityScoresLoaded()).toBe(true);
    expect(getOccurrence('Agelaius phoeniceus')).toBe(0.82);
    expect(getOccurrence('Setophaga cerulea')).toBe(0.03);
  });

  it('looks up scientific names case- and whitespace-insensitively', async () => {
    mockGet.mockResolvedValue(scoresResponse);
    await loadRarityScores();

    expect(getOccurrence('  agelaius PHOENICEUS ')).toBe(0.82);
  });

  it('returns undefined for species without a score or geomodel coverage', async () => {
    mockGet.mockResolvedValue(scoresResponse);
    await loadRarityScores();

    // Present in the list but without a numeric score.
    expect(getOccurrence('Corvus corax')).toBeUndefined();
    // Not in the geomodel at all (e.g. a bat).
    expect(getOccurrence('Myotis lucifugus')).toBeUndefined();
  });

  it('flags a species rare only when occurrence is at or below the threshold', async () => {
    mockGet.mockResolvedValue(scoresResponse);
    await loadRarityScores();

    expect(isRareSpecies('Spinus tristis', 0.25)).toBe(true); // 0.2 <= 0.25
    expect(isRareSpecies('Spinus tristis', 0.1)).toBe(false); // 0.2 > 0.1
    expect(isRareSpecies('Agelaius phoeniceus', 0.25)).toBe(false); // 0.82 > 0.25
  });

  it('never flags species without geomodel coverage as rare', async () => {
    mockGet.mockResolvedValue(scoresResponse);
    await loadRarityScores();

    expect(isRareSpecies('Myotis lucifugus', 0.9)).toBe(false);
    expect(isRareSpecies(undefined, 0.9)).toBe(false);
  });

  it('fetches only once for concurrent and repeat calls', async () => {
    mockGet.mockResolvedValue(scoresResponse);

    await Promise.all([loadRarityScores(), loadRarityScores()]);
    await loadRarityScores();

    expect(mockGet).toHaveBeenCalledTimes(1);
  });
});
