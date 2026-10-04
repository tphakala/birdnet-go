import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { get } from 'svelte/store';
import { settingsStore, settingsActions, hasUnsavedChanges, SECTION_STORE_PATHS } from './settings';
import type { BirdNetSettings, RealtimeSettings, SettingsFormData } from './settings';
import { settingsAPI } from '$lib/utils/settingsApi.js';
import { hasSettingsChanged } from '$lib/utils/settingsChanges';
import { deferred, lookup, serverSettings } from '../../test/settings-helpers';

// Mock the settings API
vi.mock('$lib/utils/settingsApi.js', () => ({
  settingsAPI: {
    load: vi.fn(),
    save: vi.fn().mockResolvedValue(undefined),
    patchSection: vi.fn().mockResolvedValue({}),
  },
}));

// Mock the toast actions
vi.mock('./toast.js', () => ({
  toastActions: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

// Mock the i18n module
vi.mock('$lib/i18n/index.js', () => ({
  getLocale: vi.fn().mockReturnValue('en'),
  setLocale: vi.fn(),
  isValidLocale: vi.fn().mockReturnValue(true),
  t: vi.fn((key: string) => key),
}));

describe('Settings Store - Dynamic Threshold and Range Filter', () => {
  beforeEach(() => {
    // Reset store to initial state
    settingsStore.set({
      formData: {
        main: { name: 'TestNode' },
        birdnet: {
          modelPath: '',
          labelPath: '',
          sensitivity: 1.0,
          threshold: 0.8,
          overlap: 0.0,
          locale: 'en',
          threads: 4,
          latitude: 40.7128,
          longitude: -74.006,
          locationConfigured: true,
          rangeFilter: {
            threshold: 0.03,
            passUnmappedSpecies: false,
            speciesCount: null,
            species: [],
          },
        },
        realtime: {
          dynamicThreshold: {
            enabled: false,
            debug: false,
            trigger: 0.8,
            min: 0.3,
            validHours: 24,
          },
        },
      },
      originalData: {} as SettingsFormData,
      isLoading: false,
      isSaving: false,
      activeSection: 'main',
      error: null,
      dataLoaded: false,
    });
  });

  it('should preserve rangeFilter when updating coordinates', () => {
    // Get initial state
    const initialState = get(settingsStore);
    expect(initialState.formData.birdnet).toBeDefined();
    const birdnetSettings = initialState.formData.birdnet as BirdNetSettings;

    const initialRangeFilter = birdnetSettings.rangeFilter;
    expect(initialRangeFilter).toBeDefined();

    // Verify initial range filter values
    expect(initialRangeFilter.threshold).toBe(0.03);

    // Update coordinates (simulating what happens when clicking on the map)
    settingsActions.updateSection('birdnet', {
      latitude: 51.5074,
      longitude: -0.1278,
    });

    // Get updated state
    const updatedState = get(settingsStore);
    const updatedBirdnet = updatedState.formData.birdnet as BirdNetSettings;

    // Verify coordinates were updated
    expect(updatedBirdnet.latitude).toBe(51.5074);
    expect(updatedBirdnet.longitude).toBe(-0.1278);

    // Verify rangeFilter was preserved
    expect(updatedBirdnet.rangeFilter.threshold).toBe(0.03);
    expect(updatedBirdnet.rangeFilter).toEqual(initialRangeFilter);
  });

  it('should preserve coordinates when updating rangeFilter threshold', () => {
    // Get initial coordinates
    const initialState = get(settingsStore);
    expect(initialState.formData.birdnet).toBeDefined();
    const birdnetSettings = initialState.formData.birdnet as BirdNetSettings;

    const initialLat = birdnetSettings.latitude;
    const initialLng = birdnetSettings.longitude;

    // Update range filter threshold
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        threshold: 0.05,
        passUnmappedSpecies: false,
        speciesCount: null,
        species: [],
      },
    });

    // Get updated state
    const updatedState = get(settingsStore);
    const updatedBirdnet = updatedState.formData.birdnet as BirdNetSettings;

    // Verify range filter was updated
    expect(updatedBirdnet.rangeFilter.threshold).toBe(0.05);

    // Verify coordinates were preserved
    expect(updatedBirdnet.latitude).toBe(initialLat);
    expect(updatedBirdnet.longitude).toBe(initialLng);
  });

  it('should handle nested updates correctly', () => {
    // Update multiple nested properties in sequence
    settingsActions.updateSection('birdnet', {
      latitude: 48.8566,
      longitude: 2.3522,
    });

    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        threshold: 0.01,
        passUnmappedSpecies: false,
        speciesCount: null,
        species: [],
      },
    });

    settingsActions.updateSection('birdnet', {
      sensitivity: 1.2,
      threshold: 0.85,
    });

    // Get final state
    const finalState = get(settingsStore);
    const finalBirdnet = finalState.formData.birdnet as BirdNetSettings;

    // Verify all updates were applied correctly
    expect(finalBirdnet.latitude).toBe(48.8566);
    expect(finalBirdnet.longitude).toBe(2.3522);
    expect(finalBirdnet.rangeFilter.threshold).toBe(0.01);
    expect(finalBirdnet.sensitivity).toBe(1.2);
    expect(finalBirdnet.threshold).toBe(0.85);
  });

  it('should merge partial rangeFilter updates correctly', () => {
    // Update only the range filter threshold (partial update)
    const storeState = get(settingsStore);
    expect(storeState.formData.birdnet).toBeDefined();
    const birdnetSettings = storeState.formData.birdnet as BirdNetSettings;

    const currentRangeFilter = birdnetSettings.rangeFilter;
    expect(currentRangeFilter).toBeDefined();

    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        ...currentRangeFilter,
        threshold: 0.07,
      },
    });

    // Get updated state
    const updatedState = get(settingsStore);
    const updatedBirdnet = updatedState.formData.birdnet as BirdNetSettings;

    // Verify only threshold was updated, other fields preserved
    expect(updatedBirdnet.rangeFilter.threshold).toBe(0.07);
    expect(updatedBirdnet.rangeFilter.speciesCount).toBe(null);
    expect(updatedBirdnet.rangeFilter.species).toEqual([]);
  });

  it('should update dynamicThreshold settings in realtime section', () => {
    // Verify initial dynamic threshold state
    const initialState = get(settingsStore);
    const initialDynamicThreshold = initialState.formData.realtime?.dynamicThreshold;

    expect(initialDynamicThreshold?.enabled).toBe(false);
    expect(initialDynamicThreshold?.trigger).toBe(0.8);
    expect(initialDynamicThreshold?.min).toBe(0.3);

    // Update dynamic threshold enabled state
    settingsActions.updateSection('realtime', {
      dynamicThreshold: {
        ...(initialDynamicThreshold ?? {
          enabled: false,
          debug: false,
          trigger: 0.8,
          min: 0.3,
          validHours: 24,
        }),
        enabled: true,
        min: 0.4,
      },
    });

    // Get updated state
    const updatedState = get(settingsStore);
    const updatedRealtime = updatedState.formData.realtime as RealtimeSettings;

    // Verify dynamic threshold was updated in realtime section
    expect(updatedRealtime.dynamicThreshold?.enabled).toBe(true);
    expect(updatedRealtime.dynamicThreshold?.min).toBe(0.4);
    expect(updatedRealtime.dynamicThreshold?.trigger).toBe(0.8); // Preserved
    expect(updatedRealtime.dynamicThreshold?.validHours).toBe(24); // Preserved
  });

  it('should not have dynamicThreshold in birdnet section', () => {
    // Verify that birdnet section doesn't contain dynamicThreshold
    const state = get(settingsStore);
    const birdnetData = state.formData.birdnet as BirdNetSettings | undefined;

    expect(birdnetData).not.toHaveProperty('dynamicThreshold');
    expect(state.formData.realtime?.dynamicThreshold).toBeDefined();
  });
});

describe('Settings Store - Model/Label Path Null Conversion', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Reset store to initial state
    settingsStore.set({
      formData: {
        main: { name: 'TestNode' },
        birdnet: {
          modelPath: '',
          labelPath: '',
          sensitivity: 1.0,
          threshold: 0.8,
          overlap: 0.0,
          locale: 'en',
          threads: 4,
          latitude: 40.7128,
          longitude: -74.006,
          locationConfigured: true,
          rangeFilter: {
            threshold: 0.03,
            passUnmappedSpecies: false,
            speciesCount: null,
            species: [],
          },
        },
      },
      originalData: {
        main: { name: 'TestNode' },
        birdnet: {
          modelPath: '',
          labelPath: '',
          sensitivity: 1.0,
          threshold: 0.8,
          overlap: 0.0,
          locale: 'en',
          threads: 4,
          latitude: 40.7128,
          longitude: -74.006,
          locationConfigured: true,
          rangeFilter: {
            threshold: 0.03,
            passUnmappedSpecies: false,
            speciesCount: null,
            species: [],
          },
        },
      } as SettingsFormData,
      isLoading: false,
      isSaving: false,
      activeSection: 'main',
      error: null,
      dataLoaded: true,
    });
  });

  it('should convert empty modelPath to null when saving', async () => {
    // Set empty string for modelPath
    settingsActions.updateSection('birdnet', {
      modelPath: '',
    });

    // Save settings
    await settingsActions.saveSettings();

    // Verify settingsAPI.save was called with null instead of empty string
    expect(settingsAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({
        birdnet: expect.objectContaining({
          modelPath: null,
        }),
      })
    );
  });

  it('should convert empty labelPath to null when saving', async () => {
    // Set empty string for labelPath
    settingsActions.updateSection('birdnet', {
      labelPath: '',
    });

    // Save settings
    await settingsActions.saveSettings();

    // Verify settingsAPI.save was called with null instead of empty string
    expect(settingsAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({
        birdnet: expect.objectContaining({
          labelPath: null,
        }),
      })
    );
  });

  it('should convert whitespace-only modelPath to null when saving', async () => {
    // Set whitespace-only string for modelPath
    settingsActions.updateSection('birdnet', {
      modelPath: '   ',
    });

    // Save settings
    await settingsActions.saveSettings();

    // Verify settingsAPI.save was called with null
    expect(settingsAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({
        birdnet: expect.objectContaining({
          modelPath: null,
        }),
      })
    );
  });

  it('should convert whitespace-only labelPath to null when saving', async () => {
    // Set whitespace-only string for labelPath
    settingsActions.updateSection('birdnet', {
      labelPath: '  \t  ',
    });

    // Save settings
    await settingsActions.saveSettings();

    // Verify settingsAPI.save was called with null
    expect(settingsAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({
        birdnet: expect.objectContaining({
          labelPath: null,
        }),
      })
    );
  });

  it('should preserve non-empty modelPath when saving', async () => {
    // Set valid path for modelPath
    const validPath = '/path/to/model.tflite';
    settingsActions.updateSection('birdnet', {
      modelPath: validPath,
    });

    // Save settings
    await settingsActions.saveSettings();

    // Verify settingsAPI.save was called with the actual path
    expect(settingsAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({
        birdnet: expect.objectContaining({
          modelPath: validPath,
        }),
      })
    );
  });

  it('should preserve non-empty labelPath when saving', async () => {
    // Set valid path for labelPath
    const validPath = '/path/to/labels.txt';
    settingsActions.updateSection('birdnet', {
      labelPath: validPath,
    });

    // Save settings
    await settingsActions.saveSettings();

    // Verify settingsAPI.save was called with the actual path
    expect(settingsAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({
        birdnet: expect.objectContaining({
          labelPath: validPath,
        }),
      })
    );
  });

  it('should handle both paths being cleared simultaneously', async () => {
    // First set valid paths
    settingsActions.updateSection('birdnet', {
      modelPath: '/path/to/model.tflite',
      labelPath: '/path/to/labels.txt',
    });

    // Then clear both
    settingsActions.updateSection('birdnet', {
      modelPath: '',
      labelPath: '',
    });

    // Save settings
    await settingsActions.saveSettings();

    // Verify both are converted to null
    expect(settingsAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({
        birdnet: expect.objectContaining({
          modelPath: null,
          labelPath: null,
        }),
      })
    );
  });

  it('should handle mixed empty and non-empty paths', async () => {
    // Set one path empty, one valid
    settingsActions.updateSection('birdnet', {
      modelPath: '/path/to/model.tflite',
      labelPath: '',
    });

    // Save settings
    await settingsActions.saveSettings();

    // Verify correct conversion
    expect(settingsAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({
        birdnet: expect.objectContaining({
          modelPath: '/path/to/model.tflite',
          labelPath: null,
        }),
      })
    );
  });
});

describe('Settings Store - UI Locale Preservation (#2756/#2760)', () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    // Default mock behaviour: runtime locale = "en", all locales valid.
    const { getLocale, setLocale, isValidLocale } = await import('$lib/i18n/index.js');
    vi.mocked(getLocale).mockReturnValue('en');
    vi.mocked(isValidLocale).mockReturnValue(true);
    vi.mocked(setLocale).mockReset();
  });

  /**
   * Helper: seed the store so formData and originalData share the same
   * backend-loaded locale. Tests then mutate formData.realtime.dashboard.locale
   * to simulate either (a) no locale change in this save session or (b) a
   * genuine locale change via the Settings > UI Language page.
   */
  const seedStore = (backendLocale: string) => {
    const snapshot: SettingsFormData = {
      main: { name: 'TestNode' },
      birdnet: {
        modelPath: '/path/to/model.tflite',
        labelPath: '/path/to/labels.txt',
        sensitivity: 1.0,
        threshold: 0.8,
        overlap: 0.0,
        locale: 'en',
        threads: 4,
        latitude: 0,
        longitude: 0,
        locationConfigured: true,
        rangeFilter: {
          threshold: 0.03,
          passUnmappedSpecies: false,
          speciesCount: null,
          species: [],
        },
      },
      realtime: {
        dashboard: {
          thumbnails: {
            summary: false,
            recent: false,
            imageProvider: 'auto',
            fallbackPolicy: 'all',
          },
          summaryLimit: 100,
          locale: backendLocale,
        },
      },
    } as unknown as SettingsFormData;

    settingsStore.set({
      formData: JSON.parse(JSON.stringify(snapshot)) as SettingsFormData,
      originalData: JSON.parse(JSON.stringify(snapshot)) as SettingsFormData,
      isLoading: false,
      isSaving: false,
      activeSection: 'main',
      error: null,
      dataLoaded: true,
    });
  };

  it('does NOT call setLocale when formData locale matches originalData, even if runtime locale differs (sidebar-set)', async () => {
    // Backend loaded "en". Sidebar changed runtime locale to "hu" (localStorage
    // only, not synced to backend). formData and originalData both still "en".
    seedStore('en');
    const { getLocale, setLocale } = await import('$lib/i18n/index.js');
    vi.mocked(getLocale).mockReturnValue('hu');

    await settingsActions.saveSettings();

    // Critical: must NOT overwrite the sidebar-set runtime locale.
    expect(setLocale).not.toHaveBeenCalled();
  });

  it('calls setLocale(newLocale) when user actually changed locale via the Settings UI', async () => {
    seedStore('en');
    // User selects German on the UI Language page.
    settingsActions.updateSection('realtime', {
      dashboard: {
        thumbnails: { summary: false, recent: false, imageProvider: 'auto', fallbackPolicy: 'all' },
        summaryLimit: 100,
        locale: 'de',
      },
    });

    const { setLocale } = await import('$lib/i18n/index.js');

    await settingsActions.saveSettings();

    expect(setLocale).toHaveBeenCalledTimes(1);
    expect(setLocale).toHaveBeenCalledWith('de');
  });

  it('does NOT call setLocale when formData locale is invalid', async () => {
    seedStore('en');
    settingsActions.updateSection('realtime', {
      dashboard: {
        thumbnails: { summary: false, recent: false, imageProvider: 'auto', fallbackPolicy: 'all' },
        summaryLimit: 100,
        locale: 'xx-invalid',
      },
    });

    const { isValidLocale, setLocale } = await import('$lib/i18n/index.js');
    vi.mocked(isValidLocale).mockReturnValue(false);

    await settingsActions.saveSettings();

    expect(setLocale).not.toHaveBeenCalled();
  });
});

describe('Settings Store - syncTLSMode preserves unsaved Security edits', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  const baseSecurity = () => ({
    baseUrl: '',
    host: '',
    autoTls: false,
    tlsMode: '',
    tlsPort: '8443',
    selfSignedValidity: '1825d',
    redirectToHttps: false,
    basicAuth: { enabled: false, username: '', password: '' },
    oauthProviders: [],
    allowSubnetBypass: { enabled: false, subnet: '' },
  });

  const seed = (
    formSecurity: ReturnType<typeof baseSecurity>,
    originalSecurity: ReturnType<typeof baseSecurity>
  ) => {
    settingsStore.set({
      formData: {
        main: { name: 'TestNode' },
        birdnet: {} as BirdNetSettings,
        security: formSecurity,
      } as SettingsFormData,
      originalData: {
        main: { name: 'TestNode' },
        birdnet: {} as BirdNetSettings,
        security: originalSecurity,
      } as SettingsFormData,
      isLoading: false,
      isSaving: false,
      activeSection: 'security',
      error: null,
      dataLoaded: true,
    });
  };

  it('syncs tlsMode/autoTls into both formData and originalData (no spurious diff)', () => {
    seed(baseSecurity(), baseSecurity());

    settingsActions.syncTLSMode('selfsigned');

    const s = get(settingsStore);
    // Synced into both copies, so change detection sees no pending edit.
    expect(s.formData.security?.tlsMode).toBe('selfsigned');
    expect(s.originalData.security?.tlsMode).toBe('selfsigned');
    expect(s.formData.security?.autoTls).toBe(false);
    expect(s.originalData.security?.autoTls).toBe(false);
  });

  it('preserves unsaved edits in other Security fields', () => {
    // The user typed a new Basic Auth password but has NOT saved it yet.
    const form = {
      ...baseSecurity(),
      tlsMode: 'manual',
      basicAuth: { enabled: true, username: '', password: 'unsaved-secret' },
    };
    const original = {
      ...baseSecurity(),
      tlsMode: '',
      basicAuth: { enabled: false, username: '', password: '' },
    };
    seed(form, original);

    settingsActions.syncTLSMode('manual');

    const s = get(settingsStore);
    // TLS mode is synced in both copies.
    expect(s.formData.security?.tlsMode).toBe('manual');
    expect(s.originalData.security?.tlsMode).toBe('manual');

    // The unsaved password edit survives in formData...
    expect(s.formData.security?.basicAuth.password).toBe('unsaved-secret');
    expect(s.formData.security?.basicAuth.enabled).toBe(true);
    // ...and is NOT promoted into the originalData baseline (still unsaved).
    expect(s.originalData.security?.basicAuth.password).toBe('');
    expect(s.originalData.security?.basicAuth.enabled).toBe(false);

    // Other top-level fields must not vanish from either copy.
    expect(s.formData.security?.tlsPort).toBe('8443');
    expect(s.formData.security?.selfSignedValidity).toBe('1825d');
    expect(s.originalData.security?.tlsPort).toBe('8443');
  });

  it('sets autoTls true for the autotls mode (both copies)', () => {
    seed(baseSecurity(), baseSecurity());

    settingsActions.syncTLSMode('autotls');

    const s = get(settingsStore);
    expect(s.formData.security?.tlsMode).toBe('autotls');
    expect(s.originalData.security?.tlsMode).toBe('autotls');
    expect(s.formData.security?.autoTls).toBe(true);
    expect(s.originalData.security?.autoTls).toBe(true);
  });

  it('resets to none mode (empty string) on delete (both copies)', () => {
    seed(
      { ...baseSecurity(), tlsMode: 'selfsigned', autoTls: false },
      { ...baseSecurity(), tlsMode: 'selfsigned', autoTls: false }
    );

    settingsActions.syncTLSMode('');

    const s = get(settingsStore);
    expect(s.formData.security?.tlsMode).toBe('');
    expect(s.originalData.security?.tlsMode).toBe('');
    expect(s.formData.security?.autoTls).toBe(false);
    expect(s.originalData.security?.autoTls).toBe(false);
  });

  it('falls back to default security fields when the section is absent', () => {
    // Defensive branch: a store seeded before the security section loaded.
    // The sync must still yield a complete security object, not a bare
    // { tlsMode, autoTls } that strips required fields.
    settingsStore.set({
      formData: { main: { name: 'TestNode' }, birdnet: {} as BirdNetSettings } as SettingsFormData,
      originalData: {} as SettingsFormData,
      isLoading: false,
      isSaving: false,
      activeSection: 'security',
      error: null,
      dataLoaded: true,
    });

    settingsActions.syncTLSMode('manual');

    const s = get(settingsStore);
    expect(s.formData.security?.tlsMode).toBe('manual');
    expect(s.formData.security?.autoTls).toBe(false);
    // Required fields are present (sourced from createEmptySettings defaults),
    // not missing as they would be with a bare {} fallback.
    expect(s.formData.security?.tlsPort).toBe('8443');
    expect(s.formData.security?.basicAuth).toBeDefined();
    expect(s.originalData.security?.tlsMode).toBe('manual');
    expect(s.originalData.security?.basicAuth).toBeDefined();
  });
});

// The settings store has no TypeScript model for the diagnostics section: there
// is deliberately no UI for the profiling switches, so nothing here declares
// them. That makes the section's survival an accident of implementation rather
// than something anyone states, and the accident is load-bearing.
//
// GET /api/v2/settings returns the whole Settings struct, and PUT replaces what
// it is given: the backend merges the request body field by field over the
// current config WITHOUT skipping zero values, so a body that omits diagnostics
// writes 0 over diagnostics.profiling.blockrate and mutexfraction and false
// over enabled. Sampling that an operator turned on in config.yaml would then be
// silently switched off the next time anyone saved an unrelated setting from the
// UI, and the block and mutex profiles would go quietly empty.
//
// Two things keep that from happening: object spread in loadSettings copies
// properties the interfaces do not declare, and coerceSettings returns unknown
// sections untouched. Both are easy to remove while "cleaning up types".
describe('Settings Store - Unmodelled Section Round-Trip', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    // clearAllMocks resets call history but LEAVES mockResolvedValue in place,
    // so without this the stub below keeps answering settingsAPI.load for
    // whatever describe block is appended after this one.
    vi.mocked(settingsAPI.load).mockReset();
  });

  it('preserves the diagnostics section through load and save', async () => {
    const diagnostics = {
      profiling: {
        enabled: true,
        // The sentinel the backend actually substitutes, so the fixture looks
        // like a real GET response rather than an invented one.
        token: '**********',
        blockRate: 10000,
        mutexFraction: 100,
      },
    };
    // Compare against a deep snapshot, not against the object the mock resolves.
    // The store spreads the response, so formData.diagnostics is the SAME
    // reference; asserting it equals itself would pass even against a coercer
    // that stripped fields in place on the object it was handed.
    const expected = JSON.parse(JSON.stringify(diagnostics));

    vi.mocked(settingsAPI.load).mockResolvedValue({
      main: { name: 'TestNode' },
      diagnostics,
    } as unknown as SettingsFormData);

    await settingsActions.loadSettings();

    expect((get(settingsStore).formData as unknown as Record<string, unknown>).diagnostics).toEqual(
      expected
    );

    await settingsActions.saveSettings();

    expect(settingsAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({ diagnostics: expected })
    );
  });
});

describe('Settings Store - HuggingFace endpoint', () => {
  // Mirrors the shape the API returns for a fresh install: the backend tags the
  // field `omitempty`, so an unset endpoint arrives absent rather than as ''.
  function baseBirdnet(overrides: Partial<BirdNetSettings> = {}): BirdNetSettings {
    return {
      modelPath: '',
      labelPath: '',
      sensitivity: 1.0,
      threshold: 0.8,
      overlap: 0.0,
      locale: 'en',
      threads: 4,
      latitude: 40.7128,
      longitude: -74.006,
      locationConfigured: true,
      rangeFilter: {
        threshold: 0.03,
        passUnmappedSpecies: false,
        speciesCount: null,
        species: [],
      },
      ...overrides,
    };
  }

  function setStore(current: BirdNetSettings, original: BirdNetSettings) {
    settingsStore.set({
      formData: { main: { name: 'TestNode' }, birdnet: current },
      originalData: {
        main: { name: 'TestNode' },
        birdnet: original,
      } as SettingsFormData,
      isLoading: false,
      isSaving: false,
      activeSection: 'birdnet',
      error: null,
      dataLoaded: true,
    });
  }

  beforeEach(() => {
    vi.clearAllMocks();
    setStore(baseBirdnet(), baseBirdnet());
  });

  it('persists the endpoint the form submits, including a trimmed value', () => {
    // The page's onchange trims before calling updateBirdnetSetting, so this
    // asserts the store keeps exactly what the form hands it.
    settingsActions.updateSection('birdnet', {
      huggingFaceEndpoint: '  https://hf-mirror.com  '.trim(),
    });

    expect(get(settingsStore).formData.birdnet.huggingFaceEndpoint).toBe('https://hf-mirror.com');
  });

  it('keeps an empty endpoint as an empty string rather than dropping it', () => {
    settingsActions.updateSection('birdnet', {
      huggingFaceEndpoint: 'https://hf-mirror.com',
    });
    settingsActions.updateSection('birdnet', { huggingFaceEndpoint: '' });

    // Empty means "fall back to HF_ENDPOINT, then the default host". It must
    // survive as '' so clearing the field is actually persisted.
    expect(get(settingsStore).formData.birdnet.huggingFaceEndpoint).toBe('');
  });

  // The remaining cases go through hasSettingsChanged, the same function the
  // section wrapper uses, rather than comparing locally coalesced values: a
  // local comparison would still pass if change detection itself regressed.
  it('reports no change when an absent endpoint is cleared to an empty string', () => {
    // The API omits the key when unset (omitempty), so originalData has no
    // endpoint at all while formData has ''. The wrapper coalesces both sides
    // with ?? '' precisely so this does not read as a pending change.
    setStore(baseBirdnet({ huggingFaceEndpoint: '' }), baseBirdnet());
    const store = get(settingsStore);

    expect(
      hasSettingsChanged(
        { huggingFaceEndpoint: store.originalData.birdnet.huggingFaceEndpoint ?? '' },
        { huggingFaceEndpoint: store.formData.birdnet.huggingFaceEndpoint ?? '' }
      )
    ).toBe(false);
  });

  it('reports a change when the endpoint actually differs', () => {
    setStore(baseBirdnet({ huggingFaceEndpoint: 'https://hf-mirror.com' }), baseBirdnet());
    const store = get(settingsStore);

    expect(
      hasSettingsChanged(
        { huggingFaceEndpoint: store.originalData.birdnet.huggingFaceEndpoint ?? '' },
        { huggingFaceEndpoint: store.formData.birdnet.huggingFaceEndpoint ?? '' }
      )
    ).toBe(true);
  });

  it('reports a change when an absent endpoint is set to a mirror', () => {
    setStore(baseBirdnet(), baseBirdnet());
    settingsActions.updateSection('birdnet', {
      huggingFaceEndpoint: 'https://hf-mirror.com',
    });
    const store = get(settingsStore);

    expect(
      hasSettingsChanged(
        { huggingFaceEndpoint: store.originalData.birdnet.huggingFaceEndpoint ?? '' },
        { huggingFaceEndpoint: store.formData.birdnet.huggingFaceEndpoint ?? '' }
      )
    ).toBe(true);
  });
});

describe('Settings Store - saveSettings refuses before settings load', () => {
  const seed = (dataLoaded: boolean) => {
    const snapshot = { main: { name: 'TestNode' }, birdnet: {} } as unknown as SettingsFormData;
    settingsStore.set({
      formData: JSON.parse(JSON.stringify(snapshot)) as SettingsFormData,
      originalData: JSON.parse(JSON.stringify(snapshot)) as SettingsFormData,
      isLoading: false,
      isSaving: false,
      activeSection: 'main',
      error: null,
      dataLoaded,
    });
  };

  beforeEach(async () => {
    vi.mocked(settingsAPI.save).mockClear();
    const { toastActions } = await import('./toast.js');
    vi.mocked(toastActions.success).mockClear();
    vi.mocked(toastActions.error).mockClear();
  });

  it('rejects without calling the API while dataLoaded is false', async () => {
    seed(false);

    await expect(settingsActions.saveSettings()).rejects.toThrow('settings.errors.loadFailed');

    expect(settingsAPI.save).not.toHaveBeenCalled();
    const state = get(settingsStore);
    expect(state.error).toBe('settings.errors.loadFailed');
    expect(state.isSaving).toBe(false);
  });

  it('shows the failure toast on refusal by default and none with notify false', async () => {
    const { toastActions } = await import('./toast.js');
    seed(false);

    await expect(settingsActions.saveSettings()).rejects.toThrow();
    expect(toastActions.error).toHaveBeenCalledTimes(1);

    vi.mocked(toastActions.error).mockClear();
    await expect(settingsActions.saveSettings({ notify: false })).rejects.toThrow();
    expect(toastActions.error).not.toHaveBeenCalled();
  });

  it('saves normally once dataLoaded is true', async () => {
    seed(true);

    await settingsActions.saveSettings();

    expect(settingsAPI.save).toHaveBeenCalledTimes(1);
    expect(get(settingsStore).error).toBeNull();
  });

  it('notify false suppresses the success toast and default keeps it', async () => {
    const { toastActions } = await import('./toast.js');
    seed(true);

    await settingsActions.saveSettings({ notify: false });
    expect(toastActions.success).not.toHaveBeenCalled();

    await settingsActions.saveSettings();
    expect(toastActions.success).toHaveBeenCalledTimes(1);
  });

  it('notify false suppresses the failure toast when the API rejects', async () => {
    const { toastActions } = await import('./toast.js');
    seed(true);
    vi.mocked(settingsAPI.save).mockRejectedValueOnce(new Error('boom'));

    await expect(settingsActions.saveSettings({ notify: false })).rejects.toThrow('boom');

    expect(toastActions.error).not.toHaveBeenCalled();
  });

  it('refreshes the restart status after a successful save and not after a failed one', async () => {
    const restart = await import('$lib/stores/restart.svelte');
    const refresh = vi.spyOn(restart, 'fetchRestartStatus').mockResolvedValue(undefined);
    try {
      seed(true);
      vi.mocked(settingsAPI.save).mockRejectedValueOnce(new Error('boom'));
      await expect(settingsActions.saveSettings({ notify: false })).rejects.toThrow('boom');

      await settingsActions.saveSettings({ notify: false });

      await vi.waitFor(() => expect(refresh).toHaveBeenCalled());
      expect(refresh).toHaveBeenCalledTimes(1);
    } finally {
      refresh.mockRestore();
    }
  });
});

describe('Settings Store - saveSection', () => {
  type Snapshot = Record<string, unknown>;
  const loadFresh = async () => {
    settingsStore.set({
      formData: { main: { name: '' } } as unknown as SettingsFormData,
      originalData: { main: { name: '' } } as unknown as SettingsFormData,
      isLoading: false,
      isSaving: false,
      activeSection: 'main',
      error: null,
      dataLoaded: false,
    });
    vi.mocked(settingsAPI.load).mockResolvedValue(serverSettings());
    await settingsActions.loadSettings();
  };

  beforeEach(async () => {
    vi.mocked(settingsAPI.save).mockReset().mockResolvedValue(undefined);
    vi.mocked(settingsAPI.patchSection).mockReset().mockResolvedValue({});
    const { toastActions } = await import('./toast.js');
    vi.mocked(toastActions.success).mockClear();
    vi.mocked(toastActions.error).mockClear();
    const { getLocale, setLocale, isValidLocale } = await import('$lib/i18n/index.js');
    vi.mocked(getLocale).mockReturnValue('en');
    vi.mocked(isValidLocale).mockReturnValue(true);
    vi.mocked(setLocale).mockReset();
    await loadFresh();
  });

  it('refuses before settings load and sends nothing', async () => {
    settingsStore.update(state => ({ ...state, dataLoaded: false }));

    await expect(settingsActions.saveSection('birdnet', { threshold: 0.9 })).rejects.toThrow(
      'settings.errors.loadFailed'
    );

    expect(settingsAPI.patchSection).not.toHaveBeenCalled();
  });

  it('patches the backend section with exactly the given partial', async () => {
    await settingsActions.saveSection('birdnet', { threshold: 0.9 });

    expect(settingsAPI.patchSection).toHaveBeenCalledTimes(1);
    expect(settingsAPI.patchSection).toHaveBeenCalledWith('birdnet', { threshold: 0.9 });
    expect(settingsAPI.save).not.toHaveBeenCalled();
  });

  it('never records a server-owned key handed to it inside a full section object', async () => {
    const current = get(settingsStore).formData.birdnet;
    const birdnet: BirdNetSettings = {
      ...current,
      threshold: 0.9,
      rangeFilter: { ...current.rangeFilter, species: ['Turdus merula'] },
    };

    await settingsActions.saveSection('birdnet', birdnet);

    const saved = get(settingsStore).originalData.birdnet;
    expect(saved.threshold).toBe(0.9);
    expect(saved.rangeFilter.species).toEqual([]);
    expect(get(settingsStore).formData.birdnet.rangeFilter.species).toEqual([]);
  });

  it('takes the patched value where the key had no pending edit', async () => {
    await settingsActions.saveSection('birdnet', { threshold: 0.9 });

    const state = get(settingsStore);
    expect(state.originalData.birdnet.threshold).toBe(0.9);
    expect(state.formData.birdnet.threshold).toBe(0.9);
  });

  it('keeps a pending edit to the same key and shows it as unsaved', async () => {
    settingsActions.updateSection('birdnet', { threshold: 0.5 });

    await settingsActions.saveSection('birdnet', { threshold: 0.9 });

    const state = get(settingsStore);
    expect(state.originalData.birdnet.threshold).toBe(0.9);
    expect(state.formData.birdnet.threshold).toBe(0.5);
    expect(get(hasUnsavedChanges)).toBe(true);
  });

  it('applies a nested section partial to both formData and originalData', async () => {
    await settingsActions.saveSection('privacyfilter', { enabled: true });
    await settingsActions.saveSection('audio', { source: 'new-device' });

    for (const copy of [get(settingsStore).formData, get(settingsStore).originalData]) {
      const privacy = lookup(copy, ['realtime', 'privacyFilter']) as Record<string, unknown>;
      expect(privacy.enabled).toBe(true);
      expect(privacy.confidence).toBe(0.7);
      expect(privacy.debug).toBe(true);
      expect(privacy.vad).toMatchObject({ enabled: true });
      const audio = lookup(copy, ['realtime', 'audio']) as Record<string, unknown>;
      expect(audio.source).toBe('new-device');
      expect(audio.sources).toEqual([{ name: 'Card', device: 'hw:0' }]);
      expect(lookup(audio, ['equalizer', 'enabled'])).toBe(true);
      expect(lookup(audio, ['export', 'type'])).toBe('wav');
    }
  });

  it('replaces arrays like the server', async () => {
    const streams = [
      {
        name: 'Stream 1',
        url: 'rtsp://new',
        enabled: true,
        type: 'rtsp' as const,
        transport: 'tcp' as const,
      },
    ];

    await settingsActions.saveSection('rtsp', { streams });

    for (const copy of [get(settingsStore).formData, get(settingsStore).originalData]) {
      expect(lookup(copy, ['realtime', 'rtsp', 'streams'])).toEqual(streams);
      expect(lookup(copy, ['realtime', 'rtsp', 'health'])).toEqual({ healthyDataThreshold: 60 });
      expect(lookup(copy, ['realtime', 'rtsp', 'ffmpegParameters'])).toEqual(['-x']);
    }
  });

  it('leaves hasUnsavedChanges false after a save with no pending edits', async () => {
    expect(get(hasUnsavedChanges)).toBe(false);

    await settingsActions.saveSection('birdweather', { enabled: true, id: 'abc' });
    await settingsActions.saveSection('dashboard', { locale: 'de' });

    expect(get(hasUnsavedChanges)).toBe(false);
  });

  it('keeps pending unrelated edits unsaved', async () => {
    settingsActions.updateSection('main', { name: 'Edited' });

    await settingsActions.saveSection('birdnet', { threshold: 0.9 });

    expect(get(settingsStore).formData.main.name).toBe('Edited');
    expect(get(hasUnsavedChanges)).toBe(true);
  });

  it('leaves both store copies untouched and rethrows when the PATCH fails', async () => {
    const before = JSON.stringify(get(settingsStore));
    vi.mocked(settingsAPI.patchSection).mockRejectedValueOnce(new Error('boom'));

    await expect(settingsActions.saveSection('birdnet', { threshold: 0.9 })).rejects.toThrow(
      'boom'
    );

    expect(JSON.stringify(get(settingsStore))).toBe(before);
  });

  it('a later saveSettings sends the patched values', async () => {
    await settingsActions.saveSection('birdnet', { threshold: 0.9 });
    await settingsActions.saveSection('privacyfilter', { enabled: true });

    await settingsActions.saveSettings({ notify: false });

    const body = vi.mocked(settingsAPI.save).mock.calls[0]?.[0] as unknown as Snapshot;
    expect(lookup(body, ['birdnet', 'threshold'])).toBe(0.9);
    expect(lookup(body, ['realtime', 'privacyFilter', 'enabled'])).toBe(true);
    expect(lookup(body, ['realtime', 'privacyFilter', 'confidence'])).toBe(0.7);
  });

  it('saveSettings waits for an in-flight saveSection before snapshotting', async () => {
    const patch = deferred<Record<string, never>>();
    vi.mocked(settingsAPI.patchSection).mockReturnValueOnce(patch.promise);

    const sectionSave = settingsActions.saveSection('birdnet', { threshold: 0.9 });
    const fullSave = settingsActions.saveSettings({ notify: false });
    await Promise.resolve();
    await Promise.resolve();
    expect(settingsAPI.save).not.toHaveBeenCalled();

    patch.resolve({});
    await Promise.all([sectionSave, fullSave]);

    expect(settingsAPI.save).toHaveBeenCalledTimes(1);
    const body = vi.mocked(settingsAPI.save).mock.calls[0]?.[0] as unknown as Snapshot;
    expect(lookup(body, ['birdnet', 'threshold'])).toBe(0.9);
  });

  it('saveSettings also waits for a section save that starts while it is waiting', async () => {
    const first = deferred<Record<string, never>>();
    const second = deferred<Record<string, never>>();
    vi.mocked(settingsAPI.patchSection)
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);

    const firstSave = settingsActions.saveSection('birdnet', { threshold: 0.9 });
    const fullSave = settingsActions.saveSettings({ notify: false });
    await Promise.resolve();
    const secondSave = settingsActions.saveSection('sentry', { enabled: true });
    first.resolve({});
    await firstSave;
    await Promise.resolve();
    await Promise.resolve();
    expect(settingsAPI.save).not.toHaveBeenCalled();

    second.resolve({});
    await Promise.all([secondSave, fullSave]);

    expect(settingsAPI.save).toHaveBeenCalledTimes(1);
    const body = vi.mocked(settingsAPI.save).mock.calls[0]?.[0] as unknown as Snapshot;
    expect(lookup(body, ['birdnet', 'threshold'])).toBe(0.9);
    expect(lookup(body, ['sentry', 'enabled'])).toBe(true);
  });

  it('saveSettings waits for the store merge, not only the request', async () => {
    const patch = deferred<Record<string, never>>();
    vi.mocked(settingsAPI.patchSection).mockReturnValueOnce(patch.promise);

    const sectionSave = settingsActions.saveSection('birdnet', { threshold: 0.9 });
    patch.resolve({});
    const fullSave = settingsActions.saveSettings({ notify: false });
    await Promise.all([sectionSave, fullSave]);

    const body = vi.mocked(settingsAPI.save).mock.calls[0]?.[0] as unknown as Snapshot;
    expect(lookup(body, ['birdnet', 'threshold'])).toBe(0.9);
  });

  it('saveSettings still runs after an in-flight saveSection fails', async () => {
    const patch = deferred<Record<string, never>>();
    vi.mocked(settingsAPI.patchSection).mockReturnValueOnce(patch.promise);

    const sectionSave = settingsActions.saveSection('birdnet', { threshold: 0.9 });
    const sectionResult = sectionSave.catch((err: unknown) => err);
    const fullSave = settingsActions.saveSettings({ notify: false });
    patch.reject(new Error('boom'));

    await expect(sectionResult).resolves.toBeInstanceOf(Error);
    await fullSave;

    expect(settingsAPI.save).toHaveBeenCalledTimes(1);
    const body = vi.mocked(settingsAPI.save).mock.calls[0]?.[0] as unknown as Snapshot;
    expect(lookup(body, ['birdnet', 'threshold'])).toBe(0.8);
  });

  it('applies a changed dashboard locale to the UI', async () => {
    const { setLocale } = await import('$lib/i18n/index.js');

    await settingsActions.saveSection('dashboard', { locale: 'de' });

    expect(setLocale).toHaveBeenCalledTimes(1);
    expect(setLocale).toHaveBeenCalledWith('de');
  });

  it('does not touch the UI locale when the dashboard locale is unchanged', async () => {
    const { setLocale } = await import('$lib/i18n/index.js');

    await settingsActions.saveSection('dashboard', { locale: 'en' });

    expect(setLocale).not.toHaveBeenCalled();
  });

  it('refreshes the restart status after a successful save and not after a failed one', async () => {
    const restart = await import('$lib/stores/restart.svelte');
    const refresh = vi.spyOn(restart, 'fetchRestartStatus').mockResolvedValue(undefined);
    try {
      vi.mocked(settingsAPI.patchSection).mockRejectedValueOnce(new Error('boom'));
      await expect(settingsActions.saveSection('birdnet', { threshold: 0.7 })).rejects.toThrow(
        'boom'
      );

      await settingsActions.saveSection('birdnet', { threshold: 0.9 });

      await vi.waitFor(() => expect(refresh).toHaveBeenCalled());
      expect(refresh).toHaveBeenCalledTimes(1);
    } finally {
      refresh.mockRestore();
    }
  });

  it('does not show a toast', async () => {
    const { toastActions } = await import('./toast.js');

    await settingsActions.saveSection('birdnet', { threshold: 0.9 });
    vi.mocked(settingsAPI.patchSection).mockRejectedValueOnce(new Error('boom'));
    await expect(settingsActions.saveSection('birdnet', { threshold: 0.7 })).rejects.toThrow();

    expect(toastActions.success).not.toHaveBeenCalled();
    expect(toastActions.error).not.toHaveBeenCalled();
    expect(get(settingsStore).isSaving).toBe(false);
  });

  describe('transition table', () => {
    interface Step {
      section: 'birdnet' | 'dashboard' | 'privacyfilter' | 'birdweather' | 'sentry' | 'audio';
      partial: Record<string, unknown>;
      outcome: 'ok' | 'fail';
    }

    const sequences: Array<{ name: string; steps: Step[] }> = [
      {
        name: 'all sections succeed once',
        steps: [
          { section: 'birdnet', partial: { latitude: 10, longitude: 20 }, outcome: 'ok' },
          { section: 'dashboard', partial: { locale: 'fi' }, outcome: 'ok' },
          { section: 'privacyfilter', partial: { enabled: true }, outcome: 'ok' },
          { section: 'sentry', partial: { enabled: true }, outcome: 'ok' },
        ],
      },
      {
        name: 'a failure between successes',
        steps: [
          { section: 'birdweather', partial: { enabled: true, id: 'x' }, outcome: 'fail' },
          { section: 'privacyfilter', partial: { enabled: true }, outcome: 'ok' },
          { section: 'sentry', partial: { enabled: true }, outcome: 'fail' },
          { section: 'audio', partial: { source: 'dev-2' }, outcome: 'ok' },
        ],
      },
      {
        name: 'the same section twice with different values',
        steps: [
          { section: 'birdnet', partial: { threshold: 0.6 }, outcome: 'ok' },
          { section: 'birdnet', partial: { threshold: 0.9 }, outcome: 'ok' },
        ],
      },
      {
        name: 'a repeat of a successful save and a failed repeat with a new value',
        steps: [
          { section: 'privacyfilter', partial: { enabled: true }, outcome: 'ok' },
          { section: 'privacyfilter', partial: { enabled: true }, outcome: 'ok' },
          { section: 'privacyfilter', partial: { enabled: false }, outcome: 'fail' },
        ],
      },
      {
        name: 'only failures',
        steps: [
          { section: 'birdnet', partial: { threshold: 0.6 }, outcome: 'fail' },
          { section: 'birdnet', partial: { threshold: 0.6 }, outcome: 'fail' },
        ],
      },
    ];

    it.each(sequences)('$name', async ({ steps }) => {
      const loaded = JSON.parse(JSON.stringify(get(settingsStore).originalData)) as Snapshot;
      const expected = new Map<string, unknown>();
      const touched = new Set<string>();
      const results: string[] = [];

      for (const step of steps) {
        if (step.outcome === 'fail') {
          vi.mocked(settingsAPI.patchSection).mockRejectedValueOnce(new Error('rejected'));
        } else {
          vi.mocked(settingsAPI.patchSection).mockResolvedValueOnce({});
        }
        const error = await settingsActions
          .saveSection(step.section, step.partial)
          .then(() => null)
          .catch((err: unknown) => err);
        results.push(error instanceof Error ? error.message : 'ok');
        for (const [key, value] of Object.entries(step.partial)) {
          const path = [...SECTION_STORE_PATHS[step.section], key].join('.');
          touched.add(path);
          if (step.outcome === 'ok') expected.set(path, value);
        }
      }

      expect(results).toEqual(steps.map(step => (step.outcome === 'ok' ? 'ok' : 'rejected')));
      const state = get(settingsStore);
      expect(JSON.stringify(state.formData)).toBe(JSON.stringify(state.originalData));
      for (const copy of [state.formData, state.originalData]) {
        for (const path of touched) {
          const segments = path.split('.');
          const want = expected.has(path) ? expected.get(path) : lookup(loaded, segments);
          expect({ path, value: lookup(copy, segments) }).toEqual({ path, value: want });
        }
        // Keys no partial named still hold the loaded value.
        expect(lookup(copy, ['main', 'name'])).toBe('TestNode');
        expect(lookup(copy, ['realtime', 'privacyFilter', 'confidence'])).toBe(0.7);
        expect(lookup(copy, ['realtime', 'birdweather', 'threshold'])).toBe(0.9);
      }
      expect(get(hasUnsavedChanges)).toBe(false);
    });
  });
});
