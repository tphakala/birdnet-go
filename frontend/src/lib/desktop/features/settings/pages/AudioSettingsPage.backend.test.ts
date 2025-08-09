import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  getBitrateConfig,
  validateBitrate,
  getExportTypeConfig,
  formatBitrate,
  formatDiskUsage,
} from '$lib/utils/audioValidation';

// Mock the imports with factory functions
vi.mock('$lib/i18n', () => ({
  t: (key: string, params?: Record<string, unknown>) => {
    // Simple mock translation that returns the key or a default value
    const translations: Record<string, string> = {
      'settings.audio.audioExport.bitrateHelp':
        'Audio compression bitrate for lossy formats. Range: {min}-{max} kbps.',
      'settings.audio.audioClipRetention.maxUsageHelp':
        'Delete oldest clips when the audio directory uses more than this percentage of available disk space.',
    };

    const translation = key in translations ? translations[key as keyof typeof translations] : key;
    if (params) {
      // Process parameter replacements safely
      return Object.entries(params).reduce((result, [paramKey, value]) => {
        return result.replace(`{${paramKey}}`, String(value));
      }, translation);
    }
    return translation;
  },
  getLocale: () => 'en',
}));

// Mock the stores module with factory function
vi.mock('$lib/stores/settings', async () => {
  const { writable } = await import('svelte/store');

  const mockAudioSettings = writable({
    export: {
      enabled: false,
      debug: false,
      path: 'clips/',
      type: 'wav',
      bitrate: '96k',
      retention: {
        policy: 'none',
        maxAge: '7d',
        maxUsage: '80%',
        minClips: 10,
        keepSpectrograms: false,
      },
    },
    equalizer: {
      enabled: false,
      filters: [],
    },
    soundLevel: {
      enabled: false,
      interval: 10,
    },
    source: '',
  });

  const mockRtspSettings = writable({
    urls: [],
  });

  const mockSettingsStore = writable({
    isLoading: false,
    isSaving: false,
    error: null,
    originalData: {},
    formData: {},
  });

  const mockSettingsActions = {
    updateSection: vi.fn(),
  };

  return {
    audioSettings: mockAudioSettings,
    rtspSettings: mockRtspSettings,
    settingsActions: mockSettingsActions,
    settingsStore: mockSettingsStore,
  };
});

describe('AudioSettingsPage - Backend Format Validation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('Bitrate Format Validation', () => {
    it('should format bitrate values correctly using utility function', () => {
      // Test the utility function that the component uses
      expect(formatBitrate(128)).toBe('128k');
      expect(formatBitrate('192')).toBe('192k');
      expect(formatBitrate('256k')).toBe('256k');

      // Test edge cases
      expect(formatBitrate(0)).toBe('0k');
      expect(formatBitrate('')).toBe('k');
    });

    it('should validate bitrate is within valid range (32-320)', () => {
      // Valid ranges for lossy formats
      expect(validateBitrate(32, 'mp3')).toBe(true);
      expect(validateBitrate(128, 'aac')).toBe(true);
      expect(validateBitrate(256, 'opus')).toBe(true);
      expect(validateBitrate(320, 'mp3')).toBe(true);

      // Invalid ranges for lossy formats
      expect(validateBitrate(16, 'mp3')).toBe(false);
      expect(validateBitrate(31, 'aac')).toBe(false);
      expect(validateBitrate(321, 'opus')).toBe(false);
      expect(validateBitrate(512, 'mp3')).toBe(false);

      // Opus has different max (256 vs 320)
      expect(validateBitrate(300, 'opus')).toBe(false);
      expect(validateBitrate(256, 'opus')).toBe(true);

      // No validation for lossless formats
      expect(validateBitrate(0, 'wav')).toBe(true);
      expect(validateBitrate(999, 'flac')).toBe(true);
    });

    it('should handle different audio format bitrate ranges correctly', () => {
      // Test MP3 configuration
      const mp3Config = getBitrateConfig('mp3');
      expect(mp3Config).toEqual({
        min: 32,
        max: 320,
        step: 32,
        default: 128,
      });

      // Test Opus configuration (different max)
      const opusConfig = getBitrateConfig('opus');
      expect(opusConfig).toEqual({
        min: 32,
        max: 256,
        step: 32,
        default: 96,
      });

      // Test lossless format returns null
      expect(getBitrateConfig('wav')).toBeNull();
      expect(getBitrateConfig('flac')).toBeNull();
    });
  });

  describe('Disk Usage Threshold Format Validation', () => {
    it('should format disk usage percentage correctly using utility function', () => {
      // Test the utility function that handles disk usage formatting
      expect(formatDiskUsage('70')).toBe('70%');
      expect(formatDiskUsage('70%')).toBe('70%');
      expect(formatDiskUsage('  80  ')).toBe('80%');
      expect(formatDiskUsage('80%%')).toBe('80%'); // Multiple % signs normalized to single %
      expect(formatDiskUsage('abc80xyz')).toBe('80%');

      // Edge cases
      expect(formatDiskUsage('')).toBe('%');
      expect(formatDiskUsage('%')).toBe('%');
    });

    it('should validate maxUsage options are properly formatted', () => {
      const maxUsageOptions = [
        { value: '70%', label: '70%' },
        { value: '75%', label: '75%' },
        { value: '80%', label: '80%' },
        { value: '85%', label: '85%' },
        { value: '90%', label: '90%' },
        { value: '95%', label: '95%' },
      ];

      // All options should have % suffix in value
      maxUsageOptions.forEach(option => {
        expect(option.value).toMatch(/^\d{2}%$/);
        expect(option.value.endsWith('%')).toBe(true);
      });

      // All values should be valid percentages between 70 and 95
      maxUsageOptions.forEach(option => {
        const numericValue = parseInt(option.value, 10);
        expect(numericValue).toBeGreaterThanOrEqual(70);
        expect(numericValue).toBeLessThanOrEqual(95);
      });
    });
  });

  describe('Integration with Backend Validation', () => {
    it('should format values to match Go validateAudioSettings expectations', () => {
      interface BackendSettings {
        export: {
          type: string;
          bitrate: string;
          retention: {
            maxUsage: string;
          };
        };
      }

      // Based on validate.go lines 431-455
      const formatForBackend = (settings: BackendSettings): BackendSettings => {
        const formatted = { ...settings };

        // Ensure bitrate has 'k' suffix for lossy formats using utility function
        if (['aac', 'opus', 'mp3'].includes(formatted.export.type)) {
          formatted.export.bitrate = formatBitrate(formatted.export.bitrate);

          // Validate bitrate range using utility function
          const bitrateValue = parseInt(formatted.export.bitrate, 10);
          if (!validateBitrate(bitrateValue, formatted.export.type)) {
            const maxBitrate = formatted.export.type === 'opus' ? 256 : 320;
            throw new Error(
              `Bitrate for ${formatted.export.type} must be between 32k and ${maxBitrate}k`
            );
          }
        }

        // Ensure maxUsage has % suffix using utility function
        if (formatted.export.retention.maxUsage) {
          formatted.export.retention.maxUsage = formatDiskUsage(
            formatted.export.retention.maxUsage
          );
        }

        return formatted;
      };

      // Test valid MP3 settings
      const mp3Settings: BackendSettings = {
        export: {
          type: 'mp3',
          bitrate: '128',
          retention: {
            maxUsage: '80',
          },
        },
      };

      const formatted = formatForBackend(mp3Settings);
      expect(formatted.export.bitrate).toBe('128k');
      expect(formatted.export.retention.maxUsage).toBe('80%');

      // Test invalid bitrate throws error
      const invalidSettings: BackendSettings = {
        export: {
          type: 'mp3',
          bitrate: '16',
          retention: {
            maxUsage: '80',
          },
        },
      };

      expect(() => formatForBackend(invalidSettings)).toThrow(
        'Bitrate for mp3 must be between 32k and 320k'
      );
    });

    it('should handle all export types correctly', () => {
      // Test each export type using the utility function
      expect(getExportTypeConfig('mp3')).toEqual({
        requiresBitrate: true,
        isLossless: false,
        bitrateRange: { min: 32, max: 320 },
      });

      expect(getExportTypeConfig('opus')).toEqual({
        requiresBitrate: true,
        isLossless: false,
        bitrateRange: { min: 32, max: 256 },
      });

      expect(getExportTypeConfig('wav')).toEqual({
        requiresBitrate: false,
        isLossless: true,
        bitrateRange: null,
      });

      expect(getExportTypeConfig('flac')).toEqual({
        requiresBitrate: false,
        isLossless: true,
        bitrateRange: null,
      });
    });
  });

  describe('Settings Persistence Format', () => {
    it('should maintain correct format when saving to backend', () => {
      interface TestSettings {
        audio: {
          export: {
            enabled: boolean;
            type: string;
            bitrate: string;
            retention: {
              policy: string;
              maxUsage: string;
            };
          };
        };
      }

      const prepareSettingsForSave = (settings: TestSettings): TestSettings => {
        // This simulates what should happen before sending to backend

        const prepared = JSON.parse(JSON.stringify(settings)) as TestSettings; // Deep clone for test

        // Ensure all required formats
        // Bitrate formatting
        const bitrate = prepared.audio.export.bitrate;
        if (!bitrate.endsWith('k')) {
          prepared.audio.export.bitrate = `${bitrate}k`;
        }

        // Retention settings formatting
        const maxUsage = prepared.audio.export.retention.maxUsage;
        if (!maxUsage.endsWith('%')) {
          prepared.audio.export.retention.maxUsage = `${maxUsage}%`;
        }

        return prepared;
      };

      const testSettings: TestSettings = {
        audio: {
          export: {
            enabled: true,
            type: 'mp3',
            bitrate: '192',
            retention: {
              policy: 'usage',
              maxUsage: '85',
            },
          },
        },
      };

      const prepared = prepareSettingsForSave(testSettings);

      // Verify formatting
      expect(prepared.audio.export.bitrate).toBe('192k');
      expect(prepared.audio.export.retention.maxUsage).toBe('85%');

      // Original should be unchanged
      expect(testSettings.audio.export.bitrate).toBe('192');
      expect(testSettings.audio.export.retention.maxUsage).toBe('85');
    });

    it('should validate complete settings structure matches Go types', () => {
      // Based on config.go ExportSettings and RetentionSettings
      interface ExportSettings {
        debug: boolean;
        enabled: boolean;
        path: string;
        type: string;
        bitrate: string; // Must end with 'k' for lossy formats
        retention: RetentionSettings;
      }

      interface RetentionSettings {
        debug: boolean;
        policy: string;
        maxAge: string;
        maxUsage: string; // Must end with '%'
        minClips: number;
        keepSpectrograms: boolean;
      }

      const validateExportSettings = (settings: ExportSettings): boolean => {
        // Validate bitrate format for lossy types
        if (['aac', 'opus', 'mp3'].includes(settings.type)) {
          if (!settings.bitrate.endsWith('k')) {
            return false;
          }
          const bitrateNum = parseInt(settings.bitrate, 10);
          if (bitrateNum < 32 || bitrateNum > 320) {
            return false;
          }
        }

        // Validate retention settings
        if (settings.retention.policy === 'usage') {
          if (!settings.retention.maxUsage.endsWith('%')) {
            return false;
          }
        }

        return true;
      };

      // Valid settings
      const validSettings: ExportSettings = {
        debug: false,
        enabled: true,
        path: 'clips/',
        type: 'mp3',
        bitrate: '128k',
        retention: {
          debug: false,
          policy: 'usage',
          maxAge: '7d',
          maxUsage: '80%',
          minClips: 10,
          keepSpectrograms: false,
        },
      };

      expect(validateExportSettings(validSettings)).toBe(true);

      // Invalid bitrate format
      const invalidBitrate = { ...validSettings, bitrate: '128' };
      expect(validateExportSettings(invalidBitrate)).toBe(false);

      // Invalid maxUsage format
      const invalidMaxUsage = {
        ...validSettings,
        retention: { ...validSettings.retention, maxUsage: '80' },
      };
      expect(validateExportSettings(invalidMaxUsage)).toBe(false);
    });
  });
});
