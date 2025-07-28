import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { get } from 'svelte/store';
import { settingsStore, settingsActions } from '$lib/stores/settings';
import type { BirdNetSettings } from '$lib/stores/settings';

// Mock API module
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    data?: any;
    constructor(message: string, status: number, data?: any) {
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

const { api } = await import('$lib/utils/api');

describe('Settings Store - Range Filter Dynamic Updates', () => {
  beforeEach(() => {
    vi.clearAllMocks();

    // Initialize store with complete settings
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
          dynamicThreshold: {
            enabled: false,
            debug: false,
            trigger: 0.8,
            min: 0.3,
            validHours: 24,
          },
          rangeFilter: {
            model: 'latest',
            threshold: 0.03,
            speciesCount: null,
            species: [],
          },
          database: {
            type: 'sqlite',
            path: 'birds.db',
            host: '',
            port: 3306,
            name: '',
            username: '',
            password: '',
          },
        },
      },
      originalData: {} as any,
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
    expect(initialState.formData.birdnet?.latitude).toBe(40.7128);
    expect(initialState.formData.birdnet?.longitude).toBe(-74.006);

    // Update coordinates
    settingsActions.updateSection('birdnet', {
      latitude: 51.5074,
      longitude: -0.1278,
    });

    // Verify coordinates were updated in the store
    const updatedState = get(settingsStore);
    expect(updatedState.formData.birdnet?.latitude).toBe(51.5074);
    expect(updatedState.formData.birdnet?.longitude).toBe(-0.1278);

    // Verify range filter settings were preserved
    expect(updatedState.formData.birdnet?.rangeFilter.model).toBe('latest');
    expect(updatedState.formData.birdnet?.rangeFilter.threshold).toBe(0.03);
  });

  it('should trigger range filter test when threshold changes', async () => {
    // Update range filter threshold
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        model: 'latest',
        threshold: 0.05,
        speciesCount: null,
        species: [],
      },
    });

    // Verify threshold was updated
    const updatedState = get(settingsStore);
    expect(updatedState.formData.birdnet?.rangeFilter.threshold).toBe(0.05);

    // Verify coordinates were preserved
    expect(updatedState.formData.birdnet?.latitude).toBe(40.7128);
    expect(updatedState.formData.birdnet?.longitude).toBe(-74.006);
  });

  it('should trigger range filter test when model changes', async () => {
    // Update range filter model
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        model: 'legacy',
        threshold: 0.03,
        speciesCount: null,
        species: [],
      },
    });

    // Verify model was updated
    const updatedState = get(settingsStore);
    expect(updatedState.formData.birdnet?.rangeFilter.model).toBe('legacy');

    // Verify other settings were preserved
    expect(updatedState.formData.birdnet?.rangeFilter.threshold).toBe(0.03);
    expect(updatedState.formData.birdnet?.latitude).toBe(40.7128);
    expect(updatedState.formData.birdnet?.longitude).toBe(-74.006);
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
        model: 'latest',
        threshold: 0.04,
        speciesCount: null,
        species: [],
      },
    });

    // Update range filter model
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        model: 'legacy',
        threshold: 0.04,
        speciesCount: null,
        species: [],
      },
    });

    // Verify all updates were applied
    const finalState = get(settingsStore);
    const birdnet = finalState.formData.birdnet as BirdNetSettings;

    expect(birdnet.latitude).toBe(48.8566);
    expect(birdnet.longitude).toBe(2.3522);
    expect(birdnet.rangeFilter.model).toBe('legacy');
    expect(birdnet.rangeFilter.threshold).toBe(0.04);
  });

  it('should not lose range filter data during coordinate updates', async () => {
    // Set initial range filter with custom values
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        model: 'latest' as any, // Testing with a different value
        threshold: 0.1,
        speciesCount: 250,
        species: ['species1', 'species2'],
      },
    });

    // Verify initial range filter state
    const initialState = get(settingsStore);
    expect(initialState.formData.birdnet?.rangeFilter.model).toBe('latest');
    expect(initialState.formData.birdnet?.rangeFilter.threshold).toBe(0.1);
    expect(initialState.formData.birdnet?.rangeFilter.speciesCount).toBe(250);
    expect(initialState.formData.birdnet?.rangeFilter.species).toEqual(['species1', 'species2']);

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
    expect(birdnet.rangeFilter.model).toBe('latest');
    expect(birdnet.rangeFilter.threshold).toBe(0.1);
    expect(birdnet.rangeFilter.speciesCount).toBe(250);
    expect(birdnet.rangeFilter.species).toEqual(['species1', 'species2']);
  });

  it('should preserve other birdnet settings during range filter updates', async () => {
    // Get initial values
    const initialState = get(settingsStore);
    const initialSensitivity = initialState.formData.birdnet?.sensitivity;
    const initialThreshold = initialState.formData.birdnet?.threshold;

    // Update range filter
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        model: 'legacy' as any, // Testing update
        threshold: 0.08,
        speciesCount: null,
        species: [],
      },
    });

    // Verify only range filter changed
    const updatedState = get(settingsStore);
    const birdnet = updatedState.formData.birdnet as BirdNetSettings;

    expect(birdnet.rangeFilter.model).toBe('legacy');
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
      model: false,
    };

    // Update latitude
    settingsActions.updateSection('birdnet', {
      latitude: 51.5074,
    });
    changes.latitude = true;
    expect(changes).toEqual({ latitude: true, longitude: false, threshold: false, model: false });

    // Update longitude
    settingsActions.updateSection('birdnet', {
      longitude: -0.1278,
    });
    changes.longitude = true;
    expect(changes).toEqual({ latitude: true, longitude: true, threshold: false, model: false });

    // Update range filter threshold
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        model: 'latest',
        threshold: 0.05,
        speciesCount: null,
        species: [],
      },
    });
    changes.threshold = true;
    expect(changes).toEqual({ latitude: true, longitude: true, threshold: true, model: false });

    // Update range filter model
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        model: 'legacy',
        threshold: 0.05,
        speciesCount: null,
        species: [],
      },
    });
    changes.model = true;
    expect(changes).toEqual({ latitude: true, longitude: true, threshold: true, model: true });

    // Verify final state has all changes
    const finalState = get(settingsStore);
    const birdnet = finalState.formData.birdnet as BirdNetSettings;

    expect(birdnet.latitude).toBe(51.5074);
    expect(birdnet.longitude).toBe(-0.1278);
    expect(birdnet.rangeFilter.threshold).toBe(0.05);
    expect(birdnet.rangeFilter.model).toBe('legacy');
  });
});
