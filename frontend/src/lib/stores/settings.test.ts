import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { get } from 'svelte/store';
import { settingsStore, settingsActions } from './settings';
import type { BirdNetSettings, RealtimeSettings, SettingsFormData } from './settings';
import { settingsAPI } from '$lib/utils/settingsApi.js';
import { hasSettingsChanged } from '$lib/utils/settingsChanges';

// Mock the settings API
vi.mock('$lib/utils/settingsApi.js', () => ({
  settingsAPI: {
    load: vi.fn(),
    save: vi.fn().mockResolvedValue(undefined),
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
      dataLoaded: false,
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
      dataLoaded: false,
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
