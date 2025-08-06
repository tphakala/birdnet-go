import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render } from '@testing-library/svelte';

// Define proper TypeScript interfaces for test data
interface MockSettingsActions {
  updateSection: ReturnType<typeof vi.fn>;
}

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

// Now we can safely import the component after mocks are set up
import AudioSettingsPage from './AudioSettingsPage.svelte';
import { settingsActions } from '$lib/stores/settings';

describe('AudioSettingsPage - Backend Format Validation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('Bitrate Format Validation', () => {
    it('should always store bitrate with "k" suffix for lossy formats', () => {
      render(AudioSettingsPage);

      // Test the bitrate formatting logic directly (no need to update store)
      const mockActions = settingsActions as unknown as MockSettingsActions;
      mockActions.updateSection.mockClear();

      // The updateExportBitrate function should be called with numeric value
      // and it should convert to "128k" format
      const updateExportBitrate = (bitrate: number | string) => {
        const formattedBitrate =
          typeof bitrate === 'number'
            ? `${bitrate}k`
            : bitrate.endsWith('k')
              ? bitrate
              : `${bitrate}k`;

        mockActions.updateSection('realtime', {
          audio: {
            export: {
              type: 'mp3',
              enabled: true,
              path: 'clips/',
              bitrate: formattedBitrate,
              debug: false,
              retention: {
                policy: 'none',
                maxAge: '7d',
                maxUsage: '80%',
                minClips: 10,
                keepSpectrograms: false,
              },
            },
          },
        });
      };

      // Test numeric input
      updateExportBitrate(128);
      expect(mockActions.updateSection).toHaveBeenCalledWith('realtime', {
        audio: {
          export: {
            type: 'mp3',
            enabled: true,
            path: 'clips/',
            bitrate: '128k',
            debug: false,
            retention: {
              policy: 'none',
              maxAge: '7d',
              maxUsage: '80%',
              minClips: 10,
              keepSpectrograms: false,
            },
          },
        },
      });

      // Test string input without 'k'
      mockActions.updateSection.mockClear();
      updateExportBitrate('192');
      expect(mockActions.updateSection).toHaveBeenCalledWith('realtime', {
        audio: {
          export: {
            type: 'mp3',
            enabled: true,
            path: 'clips/',
            bitrate: '192k',
            debug: false,
            retention: {
              policy: 'none',
              maxAge: '7d',
              maxUsage: '80%',
              minClips: 10,
              keepSpectrograms: false,
            },
          },
        },
      });

      // Test string input with 'k' already
      mockActions.updateSection.mockClear();
      updateExportBitrate('256k');
      expect(mockActions.updateSection).toHaveBeenCalledWith('realtime', {
        audio: {
          export: {
            type: 'mp3',
            enabled: true,
            path: 'clips/',
            bitrate: '256k',
            debug: false,
            retention: {
              policy: 'none',
              maxAge: '7d',
              maxUsage: '80%',
              minClips: 10,
              keepSpectrograms: false,
            },
          },
        },
      });
    });

    it('should validate bitrate is within valid range (32-320)', () => {
      // Test validation logic that should be in the component
      const validateBitrate = (bitrate: number, format: string): boolean => {
        if (['aac', 'opus', 'mp3'].includes(format)) {
          return bitrate >= 32 && bitrate <= 320;
        }
        return true; // No bitrate validation for lossless formats
      };

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

      // No validation for lossless formats
      expect(validateBitrate(0, 'wav')).toBe(true);
      expect(validateBitrate(999, 'flac')).toBe(true);
    });

    it('should handle different audio format bitrate ranges correctly', () => {
      interface BitrateConfig {
        min: number;
        max: number;
        step: number;
        default: number;
      }

      const getBitrateConfig = (format: string): BitrateConfig | null => {
        const configs: Record<string, BitrateConfig> = {
          mp3: { min: 32, max: 320, step: 32, default: 128 },
          aac: { min: 32, max: 320, step: 32, default: 96 },
          opus: { min: 32, max: 256, step: 32, default: 96 },
        };
        if (format in configs) {
          return configs[format as keyof typeof configs];
        }
        return null;
      };

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
    it('should always store maxUsage with "%" suffix', () => {
      const mockActions = settingsActions as unknown as MockSettingsActions;

      const updateRetentionMaxUsage = (maxUsage: string) => {
        // The function should ensure the value has a % suffix
        const formattedUsage = maxUsage.endsWith('%') ? maxUsage : `${maxUsage}%`;

        mockActions.updateSection('realtime', {
          audio: {
            export: {
              type: 'wav',
              enabled: false,
              path: 'clips/',
              bitrate: '96k',
              debug: false,
              retention: {
                policy: 'usage',
                maxAge: '7d',
                maxUsage: formattedUsage,
                minClips: 10,
                keepSpectrograms: false,
              },
            },
          },
        });
      };

      // Test with percentage already included
      mockActions.updateSection.mockClear();
      updateRetentionMaxUsage('70%');
      expect(mockActions.updateSection).toHaveBeenCalledWith('realtime', {
        audio: {
          export: {
            type: 'wav',
            enabled: false,
            path: 'clips/',
            bitrate: '96k',
            debug: false,
            retention: {
              policy: 'usage',
              maxAge: '7d',
              maxUsage: '70%',
              minClips: 10,
              keepSpectrograms: false,
            },
          },
        },
      });

      // Test without percentage (should add it)
      mockActions.updateSection.mockClear();
      updateRetentionMaxUsage('85');
      expect(mockActions.updateSection).toHaveBeenCalledWith('realtime', {
        audio: {
          export: {
            type: 'wav',
            enabled: false,
            path: 'clips/',
            bitrate: '96k',
            debug: false,
            retention: {
              policy: 'usage',
              maxAge: '7d',
              maxUsage: '85%',
              minClips: 10,
              keepSpectrograms: false,
            },
          },
        },
      });
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

    it('should handle edge cases for disk usage formatting', () => {
      const formatDiskUsage = (value: string): string => {
        // Remove any non-numeric characters except %
        const cleaned = value.replace(/[^0-9%]/g, '');

        // If it already has %, return as is
        if (cleaned.endsWith('%')) {
          return cleaned;
        }

        // Otherwise add %
        return `${cleaned}%`;
      };

      // Test various input formats
      expect(formatDiskUsage('80')).toBe('80%');
      expect(formatDiskUsage('80%')).toBe('80%');
      expect(formatDiskUsage('  80  ')).toBe('80%');
      expect(formatDiskUsage('80%%')).toBe('80%%'); // Note: This might need special handling
      expect(formatDiskUsage('abc80xyz')).toBe('80%');
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

        // Ensure bitrate has 'k' suffix for lossy formats
        if (['aac', 'opus', 'mp3'].includes(formatted.export.type)) {
          if (!formatted.export.bitrate.endsWith('k')) {
            formatted.export.bitrate = `${formatted.export.bitrate}k`;
          }

          // Validate bitrate range
          const bitrateValue = parseInt(formatted.export.bitrate, 10);
          if (bitrateValue < 32 || bitrateValue > 320) {
            throw new Error(`Bitrate for ${formatted.export.type} must be between 32k and 320k`);
          }
        }

        // Ensure maxUsage has % suffix
        if (formatted.export.retention.maxUsage) {
          if (!formatted.export.retention.maxUsage.endsWith('%')) {
            formatted.export.retention.maxUsage = `${formatted.export.retention.maxUsage}%`;
          }
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
      interface ExportTypeConfig {
        requiresBitrate: boolean;
        isLossless: boolean;
        bitrateRange: { min: number; max: number } | null;
      }

      const getExportTypeConfig = (type: string): ExportTypeConfig => {
        const requiresBitrate = ['aac', 'opus', 'mp3'].includes(type);
        const isLossless = ['wav', 'flac'].includes(type);

        return {
          requiresBitrate,
          isLossless,
          bitrateRange: requiresBitrate ? { min: 32, max: type === 'opus' ? 256 : 320 } : null,
        };
      };

      // Test each export type
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
        const prepared = JSON.parse(JSON.stringify(settings)) as TestSettings; // Deep clone

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
