/**
 * Tests for the speciesDictionary store.
 *
 * Note: Common mocks (logger, i18n, toast) are defined in src/test/setup.ts.
 */

import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';

// --- Module mocks (must be hoisted above imports) ---

vi.mock('$lib/stores/appState.svelte', () => ({
  appState: { speciesDictVersion: '' },
  getSpeciesDictVersion: vi.fn(() => ''),
}));

vi.mock('$lib/i18n/store.svelte', () => ({
  getLocale: vi.fn(() => 'fi' as const),
}));

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

// Import after mocks are declared
import {
  loadDictionary,
  localizeScientific,
  resolveCommonToScientific,
  searchScientificByCommon,
  resetDictionaryForTest,
} from './speciesDictionary.svelte';
import { api } from '$lib/utils/api';
import { getLocale } from '$lib/i18n/store.svelte';
import { getSpeciesDictVersion } from '$lib/stores/appState.svelte';

/** A small Finnish-locale mock dictionary for testing. */
const MOCK_FI_DICT: Record<string, string> = {
  'Barbastella barbastellus': 'Länsilepakko',
  'Turdus merula': 'Mustarastas',
  'Parus major': 'Talitiainen',
};

/** Dict where two scientific names share the same normalized common name. */
const MOCK_AMBIGUOUS_DICT: Record<string, string> = {
  'Corvus corax': 'Raven',
  'Corvus corax subsp': 'Raven', // same normalized common name
  'Picus viridis': 'Green Woodpecker',
};

/** Dict for French locale. */
const MOCK_FR_DICT: Record<string, string> = {
  'Barbastella barbastellus': 'Barbastelle commune',
  'Turdus merula': 'Merle noir',
};

/**
 * Finnish bat dictionary: several common names share the substring "lepakko"
 * (Finnish for "bat"), so a substring query should match multiple species.
 */
const MOCK_FI_BAT_DICT: Record<string, string> = {
  'Barbastella barbastellus': 'mopsilepakko',
  'Myotis daubentonii': 'vesilepakko',
  'Turdus merula': 'mustarastas',
};

function mockApiGet(dict: Record<string, string>): void {
  vi.mocked(api.get).mockResolvedValue(dict as unknown);
}

describe('speciesDictionary store', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetDictionaryForTest();
    // Default: Finnish locale, no version
    vi.mocked(getLocale).mockReturnValue('fi');
    vi.mocked(getSpeciesDictVersion).mockReturnValue('');
  });

  afterEach(() => {
    vi.clearAllMocks();
    resetDictionaryForTest();
  });

  // --- Basic fetch and forward map ---

  it('fetches the active locale dictionary and builds the forward map', async () => {
    mockApiGet(MOCK_FI_DICT);
    await loadDictionary('fi');

    expect(api.get).toHaveBeenCalledOnce();
    expect(vi.mocked(api.get).mock.calls[0][0]).toContain('/api/v2/species/dictionary/fi');

    expect(localizeScientific('Barbastella barbastellus')).toBe('Länsilepakko');
    expect(localizeScientific('Turdus merula')).toBe('Mustarastas');
    expect(localizeScientific('Unknown Species')).toBeUndefined();
  });

  // --- Caching ---

  it('does not refetch for the same (locale, version) pair', async () => {
    mockApiGet(MOCK_FI_DICT);
    await loadDictionary('fi');
    await loadDictionary('fi');

    expect(api.get).toHaveBeenCalledOnce();
  });

  it('refetches when the version changes', async () => {
    mockApiGet(MOCK_FI_DICT);
    vi.mocked(getSpeciesDictVersion).mockReturnValue('v1');
    await loadDictionary('fi');
    expect(api.get).toHaveBeenCalledOnce();

    vi.mocked(getSpeciesDictVersion).mockReturnValue('v2');
    mockApiGet(MOCK_FI_DICT);
    await loadDictionary('fi');
    expect(api.get).toHaveBeenCalledTimes(2);
  });

  // --- Language switch ---

  it('switches current maps when locale changes', async () => {
    mockApiGet(MOCK_FI_DICT);
    await loadDictionary('fi');
    expect(localizeScientific('Turdus merula')).toBe('Mustarastas');

    vi.mocked(api.get).mockResolvedValue(MOCK_FR_DICT as unknown);
    await loadDictionary('fr');
    expect(localizeScientific('Turdus merula')).toBe('Merle noir');
  });

  // --- Abort / race condition ---

  it('does not overwrite current maps when a stale locale fetch resolves later', async () => {
    const ES_DICT: Record<string, string> = { 'Turdus merula': 'Mirlo comun' };
    const FR_DICT_RACE: Record<string, string> = { 'Turdus merula': 'Merle noir race' };

    // 'es' fetch starts but is held in flight
    let resolveEs!: (value: unknown) => void;
    const esPromise = new Promise(resolve => {
      resolveEs = resolve;
    });
    vi.mocked(api.get).mockImplementationOnce(() => esPromise as Promise<unknown>);
    const esLoad = loadDictionary('es');

    // 'fr' fetch starts and resolves quickly - this becomes the "current" locale
    vi.mocked(api.get).mockResolvedValueOnce(FR_DICT_RACE as unknown);
    await loadDictionary('fr');
    expect(localizeScientific('Turdus merula')).toBe('Merle noir race');

    // Now resolve the stale 'es' fetch
    resolveEs(ES_DICT);
    await esLoad;

    // Maps must still reflect 'fr', not the late-resolving 'es'
    expect(localizeScientific('Turdus merula')).toBe('Merle noir race');
  });

  it('discards an in-flight fetch when a cached locale wins the race', async () => {
    // Pre-populate the cache for locale 'fr' (the locale that should win).
    const FR_DICT_CACHED: Record<string, string> = { 'Turdus merula': 'Merle noir cache' };
    mockApiGet(FR_DICT_CACHED);
    await loadDictionary('fr');
    expect(api.get).toHaveBeenCalledOnce();

    // Start an uncached 'es' fetch and hold it in flight.
    const ES_DICT: Record<string, string> = { 'Turdus merula': 'Mirlo comun' };
    let resolveEs!: (value: unknown) => void;
    const esPromise = new Promise(resolve => {
      resolveEs = resolve;
    });
    vi.mocked(api.get).mockImplementationOnce(() => esPromise as Promise<unknown>);
    const esLoad = loadDictionary('es');

    // Load the already-cached 'fr'. The cache-hit path must bump the sequence
    // counter so the pending 'es' fetch is superseded; current becomes 'fr'.
    await loadDictionary('fr');
    expect(localizeScientific('Turdus merula')).toBe('Merle noir cache');
    // No additional fetch for the cached locale.
    expect(api.get).toHaveBeenCalledTimes(2);

    // Resolve the stale 'es' fetch. Without the race guard on the cache-hit path,
    // this would clobber current with the 'es' result.
    resolveEs(ES_DICT);
    await esLoad;

    // current must still reflect the cached 'fr' locale, not the late 'es'.
    expect(localizeScientific('Turdus merula')).toBe('Merle noir cache');
  });

  // --- Reverse map / ambiguity ---

  it('builds the reverse map so resolveCommonToScientific returns matches', async () => {
    mockApiGet(MOCK_FI_DICT);
    await loadDictionary('fi');

    const result = resolveCommonToScientific('Mustarastas');
    expect(result).toContain('Turdus merula');
  });

  it('returns empty array for unknown common names', async () => {
    mockApiGet(MOCK_FI_DICT);
    await loadDictionary('fi');
    expect(resolveCommonToScientific('Tuntematon Lintu')).toEqual([]);
  });

  it('keeps ALL scientific names when multiple map to the same normalized common name', async () => {
    mockApiGet(MOCK_AMBIGUOUS_DICT);
    await loadDictionary('fi');

    const result = resolveCommonToScientific('Raven');
    expect(result).toHaveLength(2);
    expect(result).toContain('Corvus corax');
    expect(result).toContain('Corvus corax subsp');
  });

  it('matches NFC-normalized lookup against NFC-stored keys', async () => {
    // U+00E4 is precomposed NFC 'ä'; decomposed NFD is 'a' + U+0308
    const NFD_A_UMLAUT = 'ä';
    const nfdDict: Record<string, string> = {
      'Parus major': `T${NFD_A_UMLAUT}lltiainen`, // key uses NFD umlaut in common name
    };
    mockApiGet(nfdDict);
    await loadDictionary('fi');

    // Lookup with NFC 'ä' (U+00E4) should still find the entry
    const result = resolveCommonToScientific('Tälltiainen');
    expect(result).toContain('Parus major');
  });

  // --- Version in URL ---

  it('appends ?v=<version> when speciesDictVersion is set', async () => {
    vi.mocked(getSpeciesDictVersion).mockReturnValue('2024-abc');
    mockApiGet(MOCK_FI_DICT);
    await loadDictionary('fi');

    const url = vi.mocked(api.get).mock.calls[0][0] as string;
    expect(url).toContain('?v=2024-abc');
  });

  it('omits ?v= when speciesDictVersion is empty', async () => {
    vi.mocked(getSpeciesDictVersion).mockReturnValue('');
    mockApiGet(MOCK_FI_DICT);
    await loadDictionary('fi');

    const url = vi.mocked(api.get).mock.calls[0][0] as string;
    expect(url).not.toContain('?v=');
  });

  // --- Substring search (searchScientificByCommon) ---

  describe('searchScientificByCommon', () => {
    it('returns the scientific name for an exact localized common name', async () => {
      mockApiGet(MOCK_FI_BAT_DICT);
      await loadDictionary('fi');

      expect(searchScientificByCommon('mopsilepakko')).toEqual(['Barbastella barbastellus']);
    });

    it('returns multiple scientific names for a shared substring', async () => {
      mockApiGet(MOCK_FI_BAT_DICT);
      await loadDictionary('fi');

      const result = searchScientificByCommon('lepakko');
      expect(result).toHaveLength(2);
      expect(result).toContain('Barbastella barbastellus');
      expect(result).toContain('Myotis daubentonii');
      // The non-bat species must not be matched.
      expect(result).not.toContain('Turdus merula');
    });

    it('matches an NFD-encoded query against NFC-stored keys', async () => {
      // Precomposed NFC umlaut (U+00E4) stored in the dictionary common name.
      const NFC_A_UMLAUT = 'ä';
      const dict: Record<string, string> = {
        'Parus major': `Talliti${NFC_A_UMLAUT}inen`,
      };
      mockApiGet(dict);
      await loadDictionary('fi');

      // Query uses decomposed (NFD) umlaut = 'a' + combining diaeresis (U+0308).
      // Both sides normalize to NFC, so the substring still matches.
      const NFD_A_UMLAUT = 'ä';
      const nfdQuery = `talliti${NFD_A_UMLAUT}inen`;
      expect(searchScientificByCommon(nfdQuery)).toContain('Parus major');
    });

    it('returns an empty array for a single-character query', async () => {
      mockApiGet(MOCK_FI_BAT_DICT);
      await loadDictionary('fi');

      expect(searchScientificByCommon('m')).toEqual([]);
    });

    it('returns an empty array when nothing matches', async () => {
      mockApiGet(MOCK_FI_BAT_DICT);
      await loadDictionary('fi');

      expect(searchScientificByCommon('zzzz')).toEqual([]);
    });

    it('de-duplicates scientific names that share a normalized common name', async () => {
      mockApiGet(MOCK_AMBIGUOUS_DICT);
      await loadDictionary('fi');

      const result = searchScientificByCommon('raven');
      expect(result).toHaveLength(2);
      expect(result).toContain('Corvus corax');
      expect(result).toContain('Corvus corax subsp');
    });
  });
});
