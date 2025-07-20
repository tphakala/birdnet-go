import { api } from './api.js';
import type {
  SettingsFormData,
  TestResult,
  BirdWeatherSettings,
  MQTTSettings,
} from '$lib/stores/settings.js';

/**
 * Settings API client extending the base API client
 */
export const settingsAPI = {
  /**
   * Load all settings from the server
   */
  load: (): Promise<SettingsFormData> => {
    return api.get<SettingsFormData>('/api/v2/settings');
  },

  /**
   * Save all settings to the server
   */
  save: (data: SettingsFormData): Promise<any> => {
    return api.put<any>('/api/v2/settings', data);
  },

  /**
   * Test endpoints for validating settings
   */
  test: {
    /**
     * Test BirdWeather integration
     */
    birdweather: (config: BirdWeatherSettings): Promise<TestResult> => {
      return api.post<TestResult>('/api/v2/test/birdweather', config);
    },

    /**
     * Test MQTT connection
     */
    mqtt: (config: MQTTSettings): Promise<TestResult> => {
      return api.post<TestResult>('/api/v2/test/mqtt', config);
    },

    /**
     * Test database connection
     */
    database: (config: Record<string, unknown>): Promise<TestResult> => {
      return api.post<TestResult>('/api/v2/test/database', config);
    },

    /**
     * Test audio source
     */
    audio: (config: Record<string, unknown>): Promise<TestResult> => {
      return api.post<TestResult>('/api/v2/test/audio', config);
    },
  },

  /**
   * Species-related API calls
   */
  species: {
    /**
     * Search for species by query string
     */
    search: (query: string): Promise<string[]> => {
      const encodedQuery = encodeURIComponent(query);
      return api.get<string[]>(`/api/v2/species/search?q=${encodedQuery}`);
    },

    /**
     * Get species filtered by range/location
     */
    rangeFilter: (lat: number, lon: number, model: string): Promise<string[]> => {
      return api.get<string[]>(`/api/v2/species/range?lat=${lat}&lon=${lon}&model=${model}`);
    },

    /**
     * Get all available species
     */
    all: (): Promise<string[]> => {
      return api.get<string[]>('/api/v2/species');
    },
  },

  /**
   * System information endpoints
   */
  system: {
    /**
     * Get available audio devices
     */
    audioDevices: (): Promise<Array<{ id: string; name: string }>> => {
      return api.get('/api/v2/system/audio-devices');
    },

    /**
     * Check FFmpeg availability
     */
    ffmpegStatus: (): Promise<{ available: boolean; version?: string }> => {
      return api.get('/api/v2/system/ffmpeg');
    },

    /**
     * Generate system support dump
     */
    supportDump: (options: { includePrivateInfo: boolean }): Promise<{ downloadUrl: string }> => {
      return api.post('/api/v2/system/support-dump', options);
    },
  },

  /**
   * Configuration validation
   */
  validate: {
    /**
     * Validate entire settings configuration
     */
    all: (
      data: SettingsFormData
    ): Promise<{ valid: boolean; errors?: Record<string, string[]> }> => {
      return api.post('/api/v2/settings/validate', data);
    },

    /**
     * Validate specific section
     */
    section: (
      section: string,
      data: Record<string, unknown>
    ): Promise<{ valid: boolean; errors?: Record<string, string[]> }> => {
      return api.post(`/api/v2/settings/validate/${section}`, data);
    },
  },
};
