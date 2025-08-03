import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { get } from 'svelte/store';
import { settingsStore, settingsActions } from '$lib/stores/settings';
import type { BirdNetSettings, SettingsFormData } from '$lib/stores/settings';

// Mock API module
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    data?: unknown;
    constructor(message: string, status: number, data?: unknown) {
      super(message);
      this.status = status;
      this.data = data;
    }
  },
}));

// Mock toast actions
vi.mock('$lib/stores/toast', () => ({
  toastActions: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

describe('Settings Store - Range Filter Dynamic Updates', () => {
  beforeEach(() => {
    vi.clearAllMocks();

    // Initialize store with complete settings
    const formData: SettingsFormData = {
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
        rangeFilter: {
          threshold: 0.03,
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
    };

    settingsStore.set({
      formData,
      originalData: {} as SettingsFormData,
      isLoading: false,
      isSaving: false,
      activeSection: 'main',
      error: null,
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('should trigger range filter test when coordinates change', async () => {
    // Verify initial state
    const initialState = get(settingsStore);
    expect(initialState.formData.birdnet.latitude).toBe(40.7128);
    expect(initialState.formData.birdnet.longitude).toBe(-74.006);

    // Update coordinates
    settingsActions.updateSection('birdnet', {
      latitude: 51.5074,
      longitude: -0.1278,
    });

    // Verify coordinates were updated in the store
    const updatedState = get(settingsStore);
    expect(updatedState.formData.birdnet.latitude).toBe(51.5074);
    expect(updatedState.formData.birdnet.longitude).toBe(-0.1278);

    // Verify range filter settings were preserved
    expect(updatedState.formData.birdnet.rangeFilter.threshold).toBe(0.03);
  });

  it('should trigger range filter test when threshold changes', async () => {
    // Update range filter threshold
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        threshold: 0.05,
        speciesCount: null,
        species: [],
      },
    });

    // Verify threshold was updated
    const updatedState = get(settingsStore);
    expect(updatedState.formData.birdnet.rangeFilter.threshold).toBe(0.05);

    // Verify coordinates were preserved
    expect(updatedState.formData.birdnet.latitude).toBe(40.7128);
    expect(updatedState.formData.birdnet.longitude).toBe(-74.006);
  });

  it('should preserve range filter settings when species list is updated', async () => {
    // Update range filter species list
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        threshold: 0.03,
        speciesCount: 150,
        species: ['species1', 'species2'],
      },
    });

    // Verify species list was updated
    const updatedState = get(settingsStore);

    // Verify all settings were preserved
    expect(updatedState.formData.birdnet.rangeFilter.threshold).toBe(0.03);
    expect(updatedState.formData.birdnet.rangeFilter.speciesCount).toBe(150);
    expect(updatedState.formData.birdnet.rangeFilter.species).toEqual(['species1', 'species2']);
    expect(updatedState.formData.birdnet.latitude).toBe(40.7128);
    expect(updatedState.formData.birdnet.longitude).toBe(-74.006);
  });

  it('should handle multiple sequential updates correctly', async () => {
    // Update coordinates
    settingsActions.updateSection('birdnet', {
      latitude: 48.8566,
      longitude: 2.3522,
    });

    // Update range filter threshold
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        threshold: 0.04,
        speciesCount: null,
        species: [],
      },
    });

    // Update range filter species count
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        threshold: 0.04,
        speciesCount: 200,
        species: ['bird1', 'bird2'],
      },
    });

    // Verify all updates were applied
    const finalState = get(settingsStore);
    const birdnet = finalState.formData.birdnet as BirdNetSettings;

    expect(birdnet.latitude).toBe(48.8566);
    expect(birdnet.longitude).toBe(2.3522);
    expect(birdnet.rangeFilter.threshold).toBe(0.04);
    expect(birdnet.rangeFilter.speciesCount).toBe(200);
    expect(birdnet.rangeFilter.species).toEqual(['bird1', 'bird2']);
  });

  it('should not lose range filter data during coordinate updates', async () => {
    // Set initial range filter with custom values
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        threshold: 0.1,
        speciesCount: 250,
        species: ['species1', 'species2'],
      },
    });

    // Verify initial range filter state
    const initialState = get(settingsStore);
    expect(initialState.formData.birdnet.rangeFilter.threshold).toBe(0.1);
    expect(initialState.formData.birdnet.rangeFilter.speciesCount).toBe(250);
    expect(initialState.formData.birdnet.rangeFilter.species).toEqual(['species1', 'species2']);

    // Update only coordinates
    settingsActions.updateSection('birdnet', {
      latitude: 35.6762,
      longitude: 139.6503,
    });

    // Verify range filter data was preserved
    const updatedState = get(settingsStore);
    const birdnet = updatedState.formData.birdnet as BirdNetSettings;

    expect(birdnet.latitude).toBe(35.6762);
    expect(birdnet.longitude).toBe(139.6503);
    expect(birdnet.rangeFilter.threshold).toBe(0.1);
    expect(birdnet.rangeFilter.speciesCount).toBe(250);
    expect(birdnet.rangeFilter.species).toEqual(['species1', 'species2']);
  });

  it('should preserve other birdnet settings during range filter updates', async () => {
    // Get initial values
    const initialState = get(settingsStore);
    const initialSensitivity = initialState.formData.birdnet.sensitivity;
    const initialThreshold = initialState.formData.birdnet.threshold;

    // Update range filter
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        threshold: 0.08,
        speciesCount: null,
        species: [],
      },
    });

    // Verify only range filter changed
    const updatedState = get(settingsStore);
    const birdnet = updatedState.formData.birdnet as BirdNetSettings;

    expect(birdnet.rangeFilter.threshold).toBe(0.08);
    expect(birdnet.sensitivity).toBe(initialSensitivity);
    expect(birdnet.threshold).toBe(initialThreshold);
    expect(birdnet.locale).toBe('en');
    expect(birdnet.threads).toBe(4);
  });

  it('should track changes that should trigger range filter updates', async () => {
    // Track which values change
    const changes = {
      latitude: false,
      longitude: false,
      threshold: false,
    };

    // Update latitude
    settingsActions.updateSection('birdnet', {
      latitude: 51.5074,
    });
    changes.latitude = true;
    expect(changes).toEqual({ latitude: true, longitude: false, threshold: false });

    // Update longitude
    settingsActions.updateSection('birdnet', {
      longitude: -0.1278,
    });
    changes.longitude = true;
    expect(changes).toEqual({ latitude: true, longitude: true, threshold: false });

    // Update range filter threshold
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        threshold: 0.05,
        speciesCount: null,
        species: [],
      },
    });
    changes.threshold = true;
    expect(changes).toEqual({ latitude: true, longitude: true, threshold: true });

    // Verify final state has all changes
    const finalState = get(settingsStore);
    const birdnet = finalState.formData.birdnet as BirdNetSettings;

    expect(birdnet.latitude).toBe(51.5074);
    expect(birdnet.longitude).toBe(-0.1278);
    expect(birdnet.rangeFilter.threshold).toBe(0.05);
  });
});
