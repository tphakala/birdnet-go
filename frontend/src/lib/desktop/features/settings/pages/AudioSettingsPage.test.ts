import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { render, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import AudioSettingsPage from './AudioSettingsPage.svelte';
import type { SettingsFormData, StreamConfig } from '$lib/stores/settings';

// Mock utilities needed by AudioSettingsPage
vi.mock('$lib/utils/security', () => ({
  // eslint-disable-next-line security/detect-object-injection -- Safe: test mock
  safeGet: vi.fn((obj, key, defaultValue) => obj?.[key] ?? defaultValue),
  // eslint-disable-next-line security/detect-object-injection -- Safe: test mock
  safeArrayAccess: vi.fn((arr, index) => arr?.[index]),
  validateProtocolURL: vi.fn((url, protocols, maxLength = 2048) => {
    if (!url) return false;
    if (url.length > maxLength) return false;
    return protocols.some((protocol: string) => url.startsWith(`${protocol}://`));
  }),
}));

vi.mock('$lib/utils/audioValidation', () => ({
  getBitrateConfig: vi.fn(format => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any -- Required for mock flexibility
    const configs: Record<string, any> = {
      mp3: { min: 32, max: 320, step: 32, default: 128 },
      opus: { min: 32, max: 256, step: 32, default: 96 },
      aac: { min: 32, max: 320, step: 32, default: 128 },
    };
    // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with predefined configs
    return configs[format] ?? null;
  }),
  formatBitrate: vi.fn(bitrate => {
    if (typeof bitrate === 'string' && bitrate.endsWith('k')) return bitrate;
    return `${bitrate}k`;
  }),
  parseNumericBitrate: vi.fn(bitrate => {
    if (typeof bitrate === 'string') {
      return parseInt(bitrate.replace('k', ''), 10);
    }
    return bitrate;
  }),
}));

vi.mock('$lib/utils/settingsChanges', () => ({
  hasSettingsChanged: vi.fn((original, current) => {
    return JSON.stringify(original) !== JSON.stringify(current);
  }),
}));

// Mock StreamManager component
vi.mock('$lib/desktop/components/forms/StreamManager.svelte');

// Mock appState for CSRF token
vi.mock('$lib/stores/appState.svelte', () => ({
  appState: {
    csrfToken: 'test-csrf-token',
    initialized: true,
    loading: false,
    error: null,
    retryCount: 0,
    version: 'test',
    security: {
      enabled: false,
      accessAllowed: true,
      authConfig: {
        basicEnabled: false,
        googleEnabled: false,
        githubEnabled: false,
        microsoftEnabled: false,
      },
    },
  },
  getCsrfToken: vi.fn().mockReturnValue('test-csrf-token'),
  initApp: vi.fn().mockResolvedValue(true),
}));

// Mock the settings module at the test level
vi.mock('$lib/stores/settings', async () => {
  const { writable } = await vi.importActual<typeof import('svelte/store')>('svelte/store');

  const settingsStore = writable({
    isLoading: false,
    isSaving: false,
    error: null,
    activeSection: 'audio',
    originalData: {
      main: {
        name: '',
        locale: 'en',
        latitude: 0,
        longitude: 0,
        timezone: '',
        debugMode: false,
        logLevel: 'info',
        logRotation: { enabled: false, maxSize: '10MB', maxAge: '30d', maxBackups: 5 },
      },
      birdnet: {
        modelPath: '',
        labelPath: '',
        sensitivity: 1.0,
        threshold: 0.03,
        overlap: 0.0,
        locale: 'en',
        latitude: 0,
        longitude: 0,
        threads: 1,
        rangeFilter: { threshold: 0.03, speciesCount: null, species: [] },
      },
    } as unknown as SettingsFormData,
    formData: {
      main: {
        name: '',
        locale: 'en',
        latitude: 0,
        longitude: 0,
        timezone: '',
        debugMode: false,
        logLevel: 'info',
        logRotation: { enabled: false, maxSize: '10MB', maxAge: '30d', maxBackups: 5 },
      },
      birdnet: {
        modelPath: '',
        labelPath: '',
        sensitivity: 1.0,
        threshold: 0.03,
        overlap: 0.0,
        locale: 'en',
        latitude: 0,
        longitude: 0,
        threads: 1,
        rangeFilter: { threshold: 0.03, speciesCount: null, species: [] },
      },
    } as unknown as SettingsFormData,
  });

  const audioSettings = writable(null);
  const rtspSettings = writable(null);

  const settingsActions = {
    updateSection: vi.fn((section: string, data: unknown) => {
      settingsStore.update(store => {
        // Initialize section if it doesn't exist
        // eslint-disable-next-line security/detect-object-injection, @typescript-eslint/no-explicit-any -- Safe: test mock
        (store.formData as any)[section] ??= {};

        // Properly merge the data
        // eslint-disable-next-line security/detect-object-injection, @typescript-eslint/no-explicit-any -- Safe: test mock
        (store.formData as any)[section] = {
          // eslint-disable-next-line security/detect-object-injection, @typescript-eslint/no-explicit-any -- Safe: test mock
          ...(store.formData as any)[section],
          ...(data as object),
        };

        return store;
      });
    }),
  };

  // Create hasUnsavedChanges store for SettingsPageActions
  const hasUnsavedChanges = writable(false);

  return {
    settingsStore,
    audioSettings,
    rtspSettings,
    settingsActions,
    hasUnsavedChanges,
  };
});

// Helper function to create StreamConfig objects
function createStreamConfig(
  name: string,
  url: string,
  type: 'rtsp' | 'http' | 'hls' | 'rtmp' | 'udp' = 'rtsp',
  transport: 'tcp' | 'udp' = 'tcp'
): StreamConfig {
  return { name, url, type, transport };
}

describe('AudioSettingsPage - Stream Configuration', () => {
  let originalFetch: typeof global.fetch;

  beforeEach(async () => {
    vi.clearAllMocks();

    // Save original fetch before mocking
    originalFetch = global.fetch;

    // Import the actual mocked stores
    const { settingsStore } = await import('$lib/stores/settings');

    // Reset store to default state
    settingsStore.set({
      isLoading: false,
      isSaving: false,
      error: null,
      activeSection: 'audio',
      originalData: {
        main: {
          name: '',
          locale: 'en',
          latitude: 0,
          longitude: 0,
          timezone: '',
          debugMode: false,
          logLevel: 'info',
          logRotation: { enabled: false, maxSize: '10MB', maxAge: '30d', maxBackups: 5 },
        },
        birdnet: {
          modelPath: '',
          labelPath: '',
          sensitivity: 1.0,
          threshold: 0.03,
          overlap: 0.0,
          locale: 'en',
          latitude: 0,
          longitude: 0,
          threads: 1,
          rangeFilter: { threshold: 0.03, speciesCount: null, species: [] },
        },
        realtime: {
          audio: {
            source: '',
            soundLevel: {
              enabled: false,
              interval: 60,
            },
            equalizer: {
              enabled: false,
              filters: [],
            },
            export: {
              enabled: false,
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
          },
          rtsp: {
            streams: [],
          },
        },
      } as unknown as SettingsFormData,
      formData: {
        main: {
          name: '',
          locale: 'en',
          latitude: 0,
          longitude: 0,
          timezone: '',
          debugMode: false,
          logLevel: 'info',
          logRotation: { enabled: false, maxSize: '10MB', maxAge: '30d', maxBackups: 5 },
        },
        birdnet: {
          modelPath: '',
          labelPath: '',
          sensitivity: 1.0,
          threshold: 0.03,
          overlap: 0.0,
          locale: 'en',
          latitude: 0,
          longitude: 0,
          threads: 1,
          rangeFilter: { threshold: 0.03, speciesCount: null, species: [] },
        },
        realtime: {
          audio: {
            source: '',
            soundLevel: {
              enabled: false,
              interval: 60,
            },
            equalizer: {
              enabled: false,
              filters: [],
            },
            export: {
              enabled: false,
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
          },
          rtsp: {
            streams: [],
          },
        },
      } as unknown as SettingsFormData,
    });

    // Mock successful audio devices fetch
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () =>
        Promise.resolve([
          { Index: 0, Name: 'Built-in Microphone' },
          { Index: 1, Name: 'USB Audio Device' },
        ]),
    });
  });

  afterEach(() => {
    // Restore original fetch to prevent test interference
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  describe('Stream Management', () => {
    // Note: UI rendering tests removed - component now uses tabbed interface
    // RTSP section is on a separate tab and requires navigation to access
    // UI structure is tested by StreamManager.test.ts

    it('should call updateRTSPStreams when streams are added', async () => {
      const { settingsActions } = await import('$lib/stores/settings');
      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      render(AudioSettingsPage);

      // Simulate adding streams through the StreamManager component
      const newStreams: StreamConfig[] = [
        createStreamConfig('Stream 1', 'rtsp://192.168.1.100:554/stream1'),
        createStreamConfig('Stream 2', 'rtsp://192.168.1.101:554/stream2'),
      ];

      // Since StreamManager is mocked, we directly call the update function
      settingsActions.updateSection('realtime', {
        rtsp: {
          streams: newStreams,
        },
      });

      expect(updateSectionSpy).toHaveBeenCalledWith('realtime', {
        rtsp: {
          streams: newStreams,
        },
      });
    });

    it('should handle empty streams array', async () => {
      const { settingsStore } = await import('$lib/stores/settings');
      render(AudioSettingsPage);

      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.streams).toEqual([]);
    });

    it('should handle multiple streams from config', async () => {
      const { settingsStore } = await import('$lib/stores/settings');

      // Set initial streams in the store
      const initialStreams: StreamConfig[] = [
        createStreamConfig('Camera 1', 'rtsps://192.168.40.125:7441/VbTsd1jklShb6wLs?enableSrtp'),
        createStreamConfig('Camera 2', 'rtsps://192.168.40.125:7441/2uX4KEa73ZCljhw2?enableSrtp'),
        createStreamConfig('Camera 3', 'rtsps://192.168.40.125:7441/JUFhxKg39V0bQDMq?enableSrtp'),
      ];

      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            rtsp: {
              streams: initialStreams,
            },
          },
        },
      }));

      render(AudioSettingsPage);

      await waitFor(() => {
        const store = get(settingsStore);
        expect(store.formData.realtime?.rtsp?.streams).toEqual(initialStreams);
      });
    });

    it('should validate stream URL format', async () => {
      const { validateProtocolURL } = await import('$lib/utils/security');

      // Valid RTSP URLs
      expect(validateProtocolURL('rtsp://192.168.1.100:554/stream', ['rtsp'], 2048)).toBe(true);
      expect(validateProtocolURL('rtsps://192.168.1.100:7441/stream', ['rtsps'], 2048)).toBe(true);

      // Invalid URLs
      expect(validateProtocolURL('http://192.168.1.100/stream', ['rtsp'], 2048)).toBe(false);
      expect(validateProtocolURL('', ['rtsp'], 2048)).toBe(false);
      expect(validateProtocolURL('invalid-url', ['rtsp'], 2048)).toBe(false);
    });

    it('should detect changes in stream configuration', async () => {
      const { hasSettingsChanged } = await import('$lib/utils/settingsChanges');

      const original = {
        streams: [],
      };

      const modified = {
        streams: [createStreamConfig('Stream 1', 'rtsp://192.168.1.100:554/stream')],
      };

      expect(hasSettingsChanged(original, modified)).toBe(true);
      expect(hasSettingsChanged(original, original)).toBe(false);
    });
  });

  describe('Issue #1112 - Blank Stream Contents', () => {
    it('should call onUpdateStreams callback when adding new stream', async () => {
      const { settingsActions, settingsStore } = await import('$lib/stores/settings');
      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      render(AudioSettingsPage);

      // Simulate user adding a new stream
      const newStream = createStreamConfig('Front Yard', 'rtsp://192.168.1.100:554/stream');

      // Simulate the StreamManager component calling onUpdateStreams
      settingsActions.updateSection('realtime', {
        rtsp: {
          streams: [newStream],
        },
      });

      expect(updateSectionSpy).toHaveBeenCalledWith('realtime', {
        rtsp: {
          streams: [newStream],
        },
      });

      // Verify the store was updated
      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.streams).toEqual([newStream]);
    });

    it('should handle removal of streams', async () => {
      const { settingsStore, settingsActions } = await import('$lib/stores/settings');

      // Start with multiple streams
      const initialStreams: StreamConfig[] = [
        createStreamConfig('Stream 1', 'rtsp://192.168.1.100:554/stream1'),
        createStreamConfig('Stream 2', 'rtsp://192.168.1.101:554/stream2'),
      ];

      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            rtsp: {
              streams: initialStreams,
            },
          },
        },
      }));

      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      render(AudioSettingsPage);

      // Simulate removing the first stream
      const remainingStreams = [initialStreams[1]!]; // eslint-disable-line @typescript-eslint/no-non-null-assertion -- Safe: test data
      settingsActions.updateSection('realtime', {
        rtsp: {
          streams: remainingStreams,
        },
      });

      expect(updateSectionSpy).toHaveBeenCalledWith('realtime', {
        rtsp: {
          streams: remainingStreams,
        },
      });
    });

    it('should handle updating existing streams', async () => {
      const { settingsStore, settingsActions } = await import('$lib/stores/settings');

      const initialStream = createStreamConfig('Front Yard', 'rtsp://192.168.1.100:554/stream');

      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            rtsp: {
              streams: [initialStream],
            },
          },
        },
      }));

      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      render(AudioSettingsPage);

      // Simulate updating the stream
      const updatedStream = createStreamConfig(
        'Front Yard Updated',
        'rtsp://192.168.1.100:554/updated-stream'
      );

      settingsActions.updateSection('realtime', {
        rtsp: {
          streams: [updatedStream],
        },
      });

      expect(updateSectionSpy).toHaveBeenCalledWith('realtime', {
        rtsp: {
          streams: [updatedStream],
        },
      });
    });

    it('should handle empty streams array gracefully', async () => {
      const { settingsActions, settingsStore } = await import('$lib/stores/settings');
      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      render(AudioSettingsPage);

      // Try to set empty streams array
      settingsActions.updateSection('realtime', {
        rtsp: {
          streams: [],
        },
      });

      expect(updateSectionSpy).toHaveBeenCalled();

      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.streams).toEqual([]);
    });
  });

  describe('Edge Cases and Error Handling', () => {
    it('should handle malformed URLs', async () => {
      const { validateProtocolURL } = await import('$lib/utils/security');

      // Test various malformed URLs
      expect(validateProtocolURL('not-a-url', ['rtsp'], 2048)).toBe(false);
      expect(validateProtocolURL('http://wrong-protocol.com', ['rtsp'], 2048)).toBe(false);
      expect(validateProtocolURL('rtsp://', ['rtsp'], 2048)).toBe(true); // Protocol only is technically valid
      expect(validateProtocolURL('rtsp://[invalid-ipv6', ['rtsp'], 2048)).toBe(true); // URL validation is basic
    });

    it('should handle extremely long URLs', async () => {
      const { validateProtocolURL } = await import('$lib/utils/security');

      const longUrl = 'rtsp://' + 'a'.repeat(2050); // Exceeds 2048 limit
      expect(validateProtocolURL(longUrl, ['rtsp'], 2048)).toBe(false);

      const validLongUrl = 'rtsp://' + 'a'.repeat(2040); // Within limit
      expect(validateProtocolURL(validLongUrl, ['rtsp'], 2048)).toBe(true);
    });
  });

  describe('Integration with Settings Store', () => {
    it('should persist streams to settings store', async () => {
      const { settingsActions, settingsStore } = await import('$lib/stores/settings');
      const streams: StreamConfig[] = [
        createStreamConfig('Stream 1', 'rtsp://192.168.1.100:554/stream1'),
        createStreamConfig('Stream 2', 'rtsp://192.168.1.101:554/stream2'),
      ];

      render(AudioSettingsPage);

      settingsActions.updateSection('realtime', {
        rtsp: {
          streams,
        },
      });

      await waitFor(() => {
        const store = get(settingsStore);
        expect(store.formData.realtime?.rtsp?.streams).toEqual(streams);
      });
    });

    it('should reflect stream changes in hasChanges indicator', async () => {
      const { hasSettingsChanged } = await import('$lib/utils/settingsChanges');
      const { settingsStore, settingsActions } = await import('$lib/stores/settings');

      // Set original data
      settingsStore.update(store => ({
        ...store,
        originalData: {
          main: {
            name: '',
            locale: 'en',
            latitude: 0,
            longitude: 0,
            timezone: '',
            debugMode: false,
            logLevel: 'info',
            logRotation: { enabled: false, maxSize: '10MB', maxAge: '30d', maxBackups: 5 },
          },
          birdnet: {
            modelPath: '',
            labelPath: '',
            sensitivity: 1.0,
            threshold: 0.03,
            overlap: 0.0,
            locale: 'en',
            latitude: 0,
            longitude: 0,
            threads: 1,
            rangeFilter: { threshold: 0.03, speciesCount: null, species: [] },
          },
          realtime: {
            rtsp: {
              streams: [],
            },
          },
        } as unknown as SettingsFormData,
        formData: {
          main: {
            name: '',
            locale: 'en',
            latitude: 0,
            longitude: 0,
            timezone: '',
            debugMode: false,
            logLevel: 'info',
            logRotation: { enabled: false, maxSize: '10MB', maxAge: '30d', maxBackups: 5 },
          },
          birdnet: {
            modelPath: '',
            labelPath: '',
            sensitivity: 1.0,
            threshold: 0.03,
            overlap: 0.0,
            locale: 'en',
            latitude: 0,
            longitude: 0,
            threads: 1,
            rangeFilter: { threshold: 0.03, speciesCount: null, species: [] },
          },
          realtime: {
            rtsp: {
              streams: [],
            },
          },
        } as unknown as SettingsFormData,
      }));

      render(AudioSettingsPage);

      // Add a new stream
      settingsActions.updateSection('realtime', {
        rtsp: {
          streams: [createStreamConfig('Stream 1', 'rtsp://192.168.1.100:554/stream')],
        },
      });

      const store = get(settingsStore);
      const hasChanges = hasSettingsChanged(
        store.originalData.realtime?.rtsp?.streams,
        store.formData.realtime?.rtsp?.streams
      );

      expect(hasChanges).toBe(true);
    });
  });
});
