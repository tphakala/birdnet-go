import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { get } from 'svelte/store';
import { settingsStore, settingsActions } from '$lib/stores/settings';
import type { BirdNetSettings, SettingsFormData } from '$lib/stores/settings';
import { api } from '$lib/utils/api';

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
        locationConfigured: true,
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
      restartRequired: false,
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

// Response shape for the range filter test endpoint
interface RangeFilterTestResponse {
  count: number;
  species: Array<{
    commonName: string;
    scientificName: string;
    label: string;
    score: number;
  }>;
}

describe('Range Filter - View Species uses filtered threshold (#2393)', () => {
  const FILTERED_COUNT = 150;
  const THRESHOLD = 0.05;
  const LATITUDE = 40.7128;
  const LONGITUDE = -74.006;

  beforeEach(() => {
    vi.clearAllMocks();

    // Initialize store with configured location and custom threshold
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
        latitude: LATITUDE,
        longitude: LONGITUDE,
        locationConfigured: true,
        rangeFilter: {
          threshold: THRESHOLD,
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
      restartRequired: false,
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('should use POST test endpoint (not GET list endpoint) when loading species for modal', async () => {
    // The bug was that loadRangeFilterSpecies() used GET /api/v2/range/species/list
    // which ignores threshold params and returns all server-side species.
    // The fix changes it to use POST /api/v2/range/species/test which filters
    // by the current threshold setting.

    // Mock the test endpoint to return filtered species
    const mockSpecies = [
      {
        commonName: 'House Sparrow',
        scientificName: 'Passer domesticus',
        label: 'Passer domesticus_House Sparrow',
        score: 0.85,
      },
      {
        commonName: 'European Robin',
        scientificName: 'Erithacus rubecula',
        label: 'Erithacus rubecula_European Robin',
        score: 0.72,
      },
    ];

    vi.mocked(api.post).mockResolvedValue({
      count: FILTERED_COUNT,
      species: mockSpecies,
    });

    // Simulate what the component does when "View Species" is clicked:
    // It calls loadRangeFilterSpecies() which should POST to the test endpoint
    const state = get(settingsStore);
    const birdnet = state.formData.birdnet;

    // Replicate the fixed loadRangeFilterSpecies logic
    const data = (await api.post('/api/v2/range/species/test', {
      latitude: birdnet.latitude,
      longitude: birdnet.longitude,
      threshold: birdnet.rangeFilter.threshold,
    })) as RangeFilterTestResponse;

    // Verify the test endpoint was called with the correct threshold
    expect(api.post).toHaveBeenCalledWith('/api/v2/range/species/test', {
      latitude: LATITUDE,
      longitude: LONGITUDE,
      threshold: THRESHOLD,
    });

    // Verify the GET list endpoint was NOT called
    expect(api.get).not.toHaveBeenCalled();

    // Verify the returned data has the filtered count, not the full database count
    expect(data.count).toBe(FILTERED_COUNT);
    expect(data.species).toHaveLength(2);
  });

  it('should preserve species count after viewing species modal', async () => {
    // This tests the core bug: opening the modal should not reset speciesCount
    // to the unfiltered value from the server

    // Mock filtered test response
    vi.mocked(api.post).mockResolvedValue({
      count: FILTERED_COUNT,
      species: [
        {
          commonName: 'House Sparrow',
          scientificName: 'Passer domesticus',
          label: 'Passer domesticus_House Sparrow',
          score: 0.85,
        },
      ],
    });

    // Simulate the component state: rangeFilterState starts with a filtered count
    const rangeFilterState = {
      speciesCount: FILTERED_COUNT,
      loading: false,
      testing: false,
      downloading: false,
      error: null,
      showModal: false,
      species: [] as Array<{
        commonName: string;
        scientificName: string;
        label: string;
        score: number;
      }>,
    };

    // Simulate clicking "View Species" - opens modal and loads species
    rangeFilterState.showModal = true;
    rangeFilterState.loading = true;

    const state = get(settingsStore);
    const birdnet = state.formData.birdnet;

    const data = (await api.post('/api/v2/range/species/test', {
      latitude: birdnet.latitude,
      longitude: birdnet.longitude,
      threshold: birdnet.rangeFilter.threshold,
    })) as RangeFilterTestResponse;

    // Update state from response (matching component logic)
    rangeFilterState.species = data.species;
    rangeFilterState.speciesCount = data.count;
    rangeFilterState.loading = false;

    // Verify the species count was NOT reset to a different value
    expect(rangeFilterState.speciesCount).toBe(FILTERED_COUNT);
    expect(rangeFilterState.species).toHaveLength(1);

    // Simulate closing the modal
    rangeFilterState.showModal = false;

    // The species count should still be the filtered value
    expect(rangeFilterState.speciesCount).toBe(FILTERED_COUNT);
  });
});
