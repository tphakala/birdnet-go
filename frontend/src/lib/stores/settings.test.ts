import { describe, it, expect, beforeEach, vi } from 'vitest';
import { get } from 'svelte/store';
import { settingsStore, settingsActions } from './settings';
import type { BirdNetSettings, RealtimeSettings, SettingsFormData } from './settings';

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
    const initialRangeFilter = initialState.formData.birdnet!.rangeFilter;

    // Verify initial range filter values
    expect(initialRangeFilter?.model).toBe('latest');
    expect(initialRangeFilter ? initialRangeFilter.threshold : undefined).toBe(0.03);

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
    expect(updatedBirdnet.rangeFilter.model).toBe('latest');
    expect(updatedBirdnet.rangeFilter.threshold).toBe(0.03);
    expect(updatedBirdnet.rangeFilter).toEqual(initialRangeFilter);
  });

  it('should preserve coordinates when updating rangeFilter threshold', () => {
    // Get initial coordinates
    const initialState = get(settingsStore);
    const initialLat = initialState.formData.birdnet!.latitude;
    const initialLng = initialState.formData.birdnet!.longitude;

    // Update range filter threshold
    settingsActions.updateSection('birdnet', {
      rangeFilter: {
        model: 'latest',
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
        model: 'legacy',
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
    expect(finalBirdnet.rangeFilter.model).toBe('legacy');
    expect(finalBirdnet.rangeFilter.threshold).toBe(0.01);
    expect(finalBirdnet.sensitivity).toBe(1.2);
    expect(finalBirdnet.threshold).toBe(0.85);
  });

  it('should merge partial rangeFilter updates correctly', () => {
    // Update only the range filter threshold (partial update)
    const storeState = get(settingsStore);
    const currentRangeFilter = storeState.formData.birdnet!.rangeFilter;

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
    expect(updatedBirdnet.rangeFilter.model).toBe('latest');
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
