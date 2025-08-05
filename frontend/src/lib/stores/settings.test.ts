import { describe, it, expect, beforeEach, vi } from 'vitest';
import { get } from 'svelte/store';
import { settingsStore, settingsActions } from './settings';
import type { BirdNetSettings, RealtimeSettings, SettingsFormData, Dashboard } from './settings';

// Mock the settings API
vi.mock('$lib/utils/settingsApi.js', () => ({
  settingsAPI: {
    load: vi.fn(),
    save: vi.fn(),
  },
}));

// Mock the toast actions
vi.mock('./toast.js', () => ({
  toastActions: {
    success: vi.fn(),
    error: vi.fn(),
  },
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
      },
      originalData: {} as SettingsFormData,
      isLoading: false,
      isSaving: false,
      activeSection: 'main',
      error: null,
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

describe('Dashboard Settings - New UI Field', () => {
  beforeEach(() => {
    // Reset store to initial state with dashboard settings
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
          rangeFilter: {
            threshold: 0.03,
            speciesCount: null,
            species: [],
          },
        },
        realtime: {
          dashboard: {
            thumbnails: {
              summary: true,
              recent: true,
              imageProvider: 'wikimedia',
              fallbackPolicy: 'all',
            },
            summaryLimit: 100,
            newUI: false, // New UI field
          },
        },
      },
      originalData: {} as SettingsFormData,
      isLoading: false,
      isSaving: false,
      activeSection: 'main',
      error: null,
    });
  });

  it('should have newUI field in dashboard settings', () => {
    const state = get(settingsStore);
    const dashboard = state.formData.realtime?.dashboard as Dashboard | undefined;

    expect(dashboard).toBeDefined();
    expect(dashboard?.newUI).toBeDefined();
    expect(dashboard?.newUI).toBe(false);
  });

  it('should update newUI setting correctly', () => {
    // Enable new UI
    settingsActions.updateSection('realtime', {
      dashboard: {
        thumbnails: {
          summary: true,
          recent: true,
          imageProvider: 'wikimedia',
          fallbackPolicy: 'all',
        },
        summaryLimit: 100,
        newUI: true, // Enable new UI
      },
    });

    const state = get(settingsStore);
    const dashboard = state.formData.realtime?.dashboard as Dashboard | undefined;

    expect(dashboard?.newUI).toBe(true);
  });

  it('should preserve other dashboard settings when updating newUI', () => {
    const initialState = get(settingsStore);
    const initialDashboard = initialState.formData.realtime?.dashboard as Dashboard | undefined;

    // Update only newUI
    settingsActions.updateSection('realtime', {
      dashboard: {
        ...(initialDashboard ?? {
          thumbnails: {
            summary: true,
            recent: true,
            imageProvider: 'wikimedia',
            fallbackPolicy: 'all',
          },
          summaryLimit: 100,
          newUI: false,
        }),
        newUI: true,
      },
    });

    const updatedState = get(settingsStore);
    const updatedDashboard = updatedState.formData.realtime?.dashboard as Dashboard | undefined;

    // Verify newUI was updated
    expect(updatedDashboard?.newUI).toBe(true);

    // Verify other settings were preserved
    expect(updatedDashboard?.summaryLimit).toBe(100);
    expect(updatedDashboard?.thumbnails.summary).toBe(true);
    expect(updatedDashboard?.thumbnails.recent).toBe(true);
    expect(updatedDashboard?.thumbnails.imageProvider).toBe('wikimedia');
    expect(updatedDashboard?.thumbnails.fallbackPolicy).toBe('all');
  });
});
