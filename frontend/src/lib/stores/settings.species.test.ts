import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { get } from 'svelte/store';
import {
  settingsStore,
  settingsActions,
  type SettingsFormData,
  type SpeciesConfig,
} from './settings';
import { settingsAPI } from '$lib/utils/settingsApi';

// Mock the settingsAPI
vi.mock('$lib/utils/settingsApi', () => ({
  settingsAPI: {
    load: vi.fn(),
    save: vi.fn(),
  },
}));

// Mock the toast actions
vi.mock('./toast', () => ({
  toastActions: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

describe('Species Settings Store', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    // Reset store to initial state
    settingsStore.set({
      formData: createEmptySettings(),
      originalData: createEmptySettings(),
      isLoading: false,
      isSaving: false,
      activeSection: 'main',
      error: null,
    });
  });

  describe('Zero Value Handling', () => {
    it('should preserve zero threshold and interval values in species config', () => {
      const testConfig: Record<string, SpeciesConfig> = {
        'Test Bird': {
          threshold: 0.0,
          interval: 0,
          actions: [],
        },
      };

      settingsActions.updateSection('realtime', {
        species: {
          include: [],
          exclude: [],
          config: testConfig,
        },
      });

      const state = get(settingsStore);
      const speciesConfig = state.formData.realtime?.species?.config['Test Bird'];

      expect(speciesConfig).toBeDefined();
      expect(speciesConfig?.threshold).toBe(0.0);
      expect(speciesConfig?.interval).toBe(0);
      expect(speciesConfig?.actions).toEqual([]);
    });

    it('should handle mixed zero and non-zero values', () => {
      const testConfig: Record<string, SpeciesConfig> = {
        'Zero Bird': {
          threshold: 0.0,
          interval: 0,
          actions: [],
        },
        'Normal Bird': {
          threshold: 0.75,
          interval: 30,
          actions: [
            {
              type: 'ExecuteCommand',
              command: '/bin/notify',
              parameters: ['CommonName'],
              executeDefaults: true,
            },
          ],
        },
      };

      settingsActions.updateSection('realtime', {
        species: {
          include: ['Robin'],
          exclude: ['Crow'],
          config: testConfig,
        },
      });

      const state = get(settingsStore);
      const species = state.formData.realtime?.species;

      // Check Zero Bird
      const zeroBird = species?.config['Zero Bird'];
      expect(zeroBird?.threshold).toBe(0.0);
      expect(zeroBird?.interval).toBe(0);

      // Check Normal Bird
      const normalBird = species?.config['Normal Bird'];
      expect(normalBird?.threshold).toBe(0.75);
      expect(normalBird?.interval).toBe(30);
      expect(normalBird?.actions).toHaveLength(1);
    });
  });

  describe('Save Operation', () => {
    it('should send zero values to API when saving', async () => {
      const mockSave = vi.mocked(settingsAPI.save);
      mockSave.mockResolvedValueOnce({ success: true });

      // Set up species config with zero values
      settingsActions.updateSection('realtime', {
        species: {
          include: [],
          exclude: [],
          config: {
            'Zero Test': {
              threshold: 0.0,
              interval: 0,
              actions: [],
            },
          },
        },
      });

      // Save settings
      await settingsActions.saveSettings();

      // Verify save was called
      expect(mockSave).toHaveBeenCalledTimes(1);

      // Get the data that was sent to save
      const savedData = mockSave.mock.calls[0][0] as SettingsFormData;
      const speciesConfig = savedData.realtime?.species?.config['Zero Test'];

      // Verify zero values were included in save
      expect(speciesConfig).toBeDefined();
      expect(speciesConfig?.threshold).toBe(0.0);
      expect(speciesConfig?.interval).toBe(0);
    });

    it('should preserve all species configs when saving partial updates', async () => {
      const mockSave = vi.mocked(settingsAPI.save);
      mockSave.mockResolvedValueOnce({ success: true });

      // Set initial configs
      settingsActions.updateSection('realtime', {
        species: {
          include: [],
          exclude: [],
          config: {
            'Bird A': {
              threshold: 0.5,
              interval: 20,
              actions: [],
            },
            'Bird B': {
              threshold: 0.8,
              interval: 40,
              actions: [],
            },
          },
        },
      });

      // Update only Bird A to zero values
      const currentState = get(settingsStore);
      const currentConfig = currentState.formData.realtime?.species?.config ?? {};

      settingsActions.updateSection('realtime', {
        species: {
          include: [],
          exclude: [],
          config: {
            ...currentConfig,
            'Bird A': {
              threshold: 0.0,
              interval: 0,
              actions: [],
            },
          },
        },
      });

      // Save settings
      await settingsActions.saveSettings();

      // Verify both birds are in saved data
      const savedData = mockSave.mock.calls[0][0] as SettingsFormData;
      const species = savedData.realtime?.species;

      expect(species?.config['Bird A']).toEqual({
        threshold: 0.0,
        interval: 0,
        actions: [],
      });

      expect(species?.config['Bird B']).toEqual({
        threshold: 0.8,
        interval: 40,
        actions: [],
      });
    });
  });

  describe('Load Operation', () => {
    it('should correctly load species configs with zero values from API', async () => {
      const mockLoad = vi.mocked(settingsAPI.load);

      // Mock API response with zero values
      const completeSettings = createEmptySettings();
      mockLoad.mockResolvedValueOnce({
        main: { name: 'TestNode' },
        birdnet: {
          ...completeSettings.birdnet,
          threshold: 0.3,
          locale: 'en',
        },
        realtime: {
          species: {
            include: ['Robin'],
            exclude: [],
            config: {
              'Zero Config': {
                threshold: 0.0,
                interval: 0,
                actions: [],
              },
              'Normal Config': {
                threshold: 0.9,
                interval: 60,
                actions: [],
              },
            },
          },
        },
      });

      await settingsActions.loadSettings();

      const state = get(settingsStore);
      const species = state.formData.realtime?.species;

      // Verify zero values were loaded correctly
      expect(species?.config['Zero Config']).toEqual({
        threshold: 0.0,
        interval: 0,
        actions: [],
      });

      // Verify normal values
      expect(species?.config['Normal Config']).toEqual({
        threshold: 0.9,
        interval: 60,
        actions: [],
      });

      // Verify originalData matches formData (for change detection)
      expect(state.originalData.realtime?.species?.config).toEqual(
        state.formData.realtime?.species?.config
      );
    });
  });

  describe('Coercion Edge Cases', () => {
    it('should coerce negative values to valid minimums', () => {
      // Test that coercion properly handles invalid negative values
      settingsActions.updateSection('realtime', {
        species: {
          include: [],
          exclude: [],
          config: {
            'Edge Case': {
              threshold: -0.1, // Invalid, should be coerced to 0
              interval: -5, // Invalid, should be coerced to 0
              actions: [],
            },
          },
        },
      });

      const state = get(settingsStore);
      const edgeCase = state.formData.realtime?.species?.config['Edge Case'];

      // Negative values should be coerced to 0 (the minimum valid value)
      expect(edgeCase?.threshold).toBe(0);
      expect(edgeCase?.interval).toBe(0);
    });

    it('should preserve valid zero values without changing them', () => {
      // Test that valid zero values are not incorrectly changed to defaults
      settingsActions.updateSection('realtime', {
        species: {
          include: [],
          exclude: [],
          config: {
            'Zero Test': {
              threshold: 0.0, // Valid zero, should remain 0
              interval: 0, // Valid zero, should remain 0
              actions: [],
            },
          },
        },
      });

      const state = get(settingsStore);
      const zeroTest = state.formData.realtime?.species?.config['Zero Test'];

      // Zero values should be preserved as-is
      expect(zeroTest?.threshold).toBe(0);
      expect(zeroTest?.interval).toBe(0);
    });
  });
});

// Helper function to create empty settings
function createEmptySettings(): SettingsFormData {
  return {
    main: {
      name: '',
    },
    birdnet: {
      modelPath: '',
      labelPath: '',
      sensitivity: 1.0,
      threshold: 0.3,
      overlap: 0.0,
      locale: 'en',
      threads: 4,
      latitude: 0,
      longitude: 0,
      rangeFilter: {
        threshold: 0.03,
        speciesCount: null,
        species: [],
      },
    },
    realtime: {
      species: {
        include: [],
        exclude: [],
        config: {},
      },
    },
  };
}
