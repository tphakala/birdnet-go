import { describe, it, expect, beforeEach, vi } from 'vitest';
import { get } from 'svelte/store';
import { settingsStore, settingsActions } from './settings';
import type { BirdNetSettings, RealtimeSettings, SettingsFormData } from './settings';
import { settingsAPI } from '$lib/utils/settingsApi.js';

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
      restartRequired: false,
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
      restartRequired: false,
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
      restartRequired: false,
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
      restartRequired: false,
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
      restartRequired: false,
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
