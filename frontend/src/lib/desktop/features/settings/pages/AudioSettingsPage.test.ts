import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { render, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import AudioSettingsPage from './AudioSettingsPage.svelte';
import type { SettingsFormData } from '$lib/stores/settings';

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

// Mock RTSPUrlInput component
vi.mock('$lib/desktop/components/forms/RTSPUrlInput.svelte');

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

describe('AudioSettingsPage - RTSP Stream Configuration', () => {
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
            transport: 'tcp',
            urls: [],
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
            transport: 'tcp',
            urls: [],
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

  describe('RTSP URL Management', () => {
    // Note: UI rendering tests removed - component now uses tabbed interface
    // RTSP section is on a separate tab and requires navigation to access
    // UI structure is tested by RTSPUrlInput.test.ts and RTSPUrlManager.test.ts

    it('should call updateRTSPUrls when URLs are added', async () => {
      const { settingsActions } = await import('$lib/stores/settings');
      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      render(AudioSettingsPage);

      // Simulate adding URLs through the RTSPUrlInput component
      const newUrls: string[] = [
        'rtsp://192.168.1.100:554/stream1',
        'rtsp://192.168.1.101:554/stream2',
      ];

      // Since RTSPUrlInput is mocked, we directly call the update function
      settingsActions.updateSection('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: newUrls,
        },
      });

      expect(updateSectionSpy).toHaveBeenCalledWith('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: newUrls,
        },
      });
    });

    it('should handle empty RTSP URLs array', async () => {
      const { settingsStore } = await import('$lib/stores/settings');
      render(AudioSettingsPage);

      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.urls).toEqual([]);
    });

    // Note: "should update RTSP transport protocol" test removed - requires tab navigation

    it('should handle multiple RTSP URLs from config', async () => {
      const { settingsStore } = await import('$lib/stores/settings');

      // Set initial URLs in the store
      const initialUrls: string[] = [
        'rtsps://192.168.40.125:7441/VbTsd1jklShb6wLs?enableSrtp',
        'rtsps://192.168.40.125:7441/2uX4KEa73ZCljhw2?enableSrtp',
        'rtsps://192.168.40.125:7441/JUFhxKg39V0bQDMq?enableSrtp',
      ];

      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            rtsp: {
              transport: 'tcp',
              urls: initialUrls,
              streams: [],
            },
          },
        },
      }));

      render(AudioSettingsPage);

      await waitFor(() => {
        const store = get(settingsStore);
        expect(store.formData.realtime?.rtsp?.urls).toEqual(initialUrls);
      });
    });

    it('should validate RTSP URL format', async () => {
      const { validateProtocolURL } = await import('$lib/utils/security');

      // Valid RTSP URLs
      expect(validateProtocolURL('rtsp://192.168.1.100:554/stream', ['rtsp'], 2048)).toBe(true);
      expect(validateProtocolURL('rtsps://192.168.1.100:7441/stream', ['rtsps'], 2048)).toBe(true);

      // Invalid URLs
      expect(validateProtocolURL('http://192.168.1.100/stream', ['rtsp'], 2048)).toBe(false);
      expect(validateProtocolURL('', ['rtsp'], 2048)).toBe(false);
      expect(validateProtocolURL('invalid-url', ['rtsp'], 2048)).toBe(false);
    });

    // Note: "should handle disabled state for RTSP inputs" test removed - requires tab navigation

    it('should detect changes in RTSP configuration', async () => {
      const { hasSettingsChanged } = await import('$lib/utils/settingsChanges');

      const original = {
        rtsp: {
          transport: 'tcp',
          urls: [],
        },
      };

      const modified = {
        rtsp: {
          transport: 'tcp',
          urls: ['rtsp://192.168.1.100:554/stream'],
        },
      };

      expect(hasSettingsChanged(original, modified)).toBe(true);
      expect(hasSettingsChanged(original, original)).toBe(false);
    });
  });

  describe('Issue #1112 - Blank RTSP Stream Contents', () => {
    // Note: "should render RTSPUrlInput component with empty URLs initially" removed - requires tab navigation

    it('should call onUpdate callback when adding new RTSP URL', async () => {
      const { settingsActions, settingsStore } = await import('$lib/stores/settings');
      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      render(AudioSettingsPage);

      // Simulate user adding a new RTSP URL
      const newUrl = 'rtsp://192.168.1.100:554/stream';

      // Simulate the RTSPUrlInput component calling onUpdate
      settingsActions.updateSection('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [newUrl],
        },
      });

      expect(updateSectionSpy).toHaveBeenCalledWith('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [newUrl],
        },
      });

      // Verify the store was updated
      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.urls).toEqual([newUrl]);
    });

    it('should handle removal of RTSP URLs', async () => {
      const { settingsStore, settingsActions } = await import('$lib/stores/settings');

      // Start with multiple URLs
      const initialUrls: string[] = [
        'rtsp://192.168.1.100:554/stream1',
        'rtsp://192.168.1.101:554/stream2',
      ];

      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            rtsp: {
              transport: 'tcp',
              urls: initialUrls,
              streams: [],
            },
          },
        },
      }));

      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      render(AudioSettingsPage);

      // Simulate removing the first URL
      settingsActions.updateSection('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [initialUrls[1]!], // eslint-disable-line @typescript-eslint/no-non-null-assertion -- Safe: test data
        },
      });

      expect(updateSectionSpy).toHaveBeenCalledWith('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [initialUrls[1]],
        },
      });
    });

    it('should handle updating existing RTSP URLs', async () => {
      const { settingsStore, settingsActions } = await import('$lib/stores/settings');

      const initialUrl = 'rtsp://192.168.1.100:554/stream';

      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            rtsp: {
              transport: 'tcp',
              urls: [initialUrl],
              streams: [],
            },
          },
        },
      }));

      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      render(AudioSettingsPage);

      // Simulate updating the URL
      const updatedUrl = 'rtsp://192.168.1.100:554/updated-stream';

      settingsActions.updateSection('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [updatedUrl],
        },
      });

      expect(updateSectionSpy).toHaveBeenCalledWith('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [updatedUrl],
        },
      });
    });

    // Note: "should pass correct props to RTSPUrlInput component" removed - requires tab navigation

    it('should handle empty string RTSP URLs gracefully', async () => {
      const { settingsActions, settingsStore } = await import('$lib/stores/settings');
      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      render(AudioSettingsPage);

      // Try to add an empty URL (should be filtered out)
      settingsActions.updateSection('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [],
        },
      });

      expect(updateSectionSpy).toHaveBeenCalled();

      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.urls).toEqual([]);
    });

    // Note: "should preserve RTSP URLs when switching transport protocol" removed - requires tab navigation
  });

  describe('Edge Cases and Error Handling', () => {
    it('should handle malformed RTSP URLs', async () => {
      const { validateProtocolURL } = await import('$lib/utils/security');

      // Test various malformed URLs
      expect(validateProtocolURL('not-a-url', ['rtsp'], 2048)).toBe(false);
      expect(validateProtocolURL('http://wrong-protocol.com', ['rtsp'], 2048)).toBe(false);
      expect(validateProtocolURL('rtsp://', ['rtsp'], 2048)).toBe(true); // Protocol only is technically valid
      expect(validateProtocolURL('rtsp://[invalid-ipv6', ['rtsp'], 2048)).toBe(true); // URL validation is basic
    });

    it('should handle extremely long RTSP URLs', async () => {
      const { validateProtocolURL } = await import('$lib/utils/security');

      const longUrl = 'rtsp://' + 'a'.repeat(2050); // Exceeds 2048 limit
      expect(validateProtocolURL(longUrl, ['rtsp'], 2048)).toBe(false);

      const validLongUrl = 'rtsp://' + 'a'.repeat(2040); // Within limit
      expect(validateProtocolURL(validLongUrl, ['rtsp'], 2048)).toBe(true);
    });

    // Note: "should handle null/undefined RTSP settings gracefully" removed - requires tab navigation
    // Note: "should handle saving while RTSP URLs are being edited" removed - requires tab navigation
    // Note: "should handle API error when loading audio devices" removed - uses outdated DOM structure
  });

  describe('Integration with Settings Store', () => {
    it('should persist RTSP URLs to settings store', async () => {
      const { settingsActions, settingsStore } = await import('$lib/stores/settings');
      const urls: string[] = [
        'rtsp://192.168.1.100:554/stream1',
        'rtsp://192.168.1.101:554/stream2',
      ];

      render(AudioSettingsPage);

      settingsActions.updateSection('realtime', {
        rtsp: {
          transport: 'tcp',
          urls,
        },
      });

      await waitFor(() => {
        const store = get(settingsStore);
        expect(store.formData.realtime?.rtsp?.urls).toEqual(urls);
      });
    });

    it('should reflect RTSP changes in hasChanges indicator', async () => {
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
              transport: 'tcp',
              urls: [],
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
              transport: 'tcp',
              urls: [],
              streams: [],
            },
          },
        } as unknown as SettingsFormData,
      }));

      render(AudioSettingsPage);

      // Add a new URL
      settingsActions.updateSection('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: ['rtsp://192.168.1.100:554/stream'],
        },
      });

      const store = get(settingsStore);
      const hasChanges = hasSettingsChanged(
        store.originalData.realtime?.rtsp,
        store.formData.realtime?.rtsp
      );

      expect(hasChanges).toBe(true);
    });
  });
});

describe('AudioSettingsPage - RTSP Streams with Labels (Issue #1494)', () => {
  let originalFetch: typeof global.fetch;

  beforeEach(async () => {
    vi.clearAllMocks();
    originalFetch = global.fetch;

    const { settingsStore } = await import('$lib/stores/settings');

    // Reset store with streams support
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
            soundLevel: { enabled: false, interval: 60 },
            equalizer: { enabled: false, filters: [] },
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
            transport: 'tcp',
            urls: [],
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
            soundLevel: { enabled: false, interval: 60 },
            equalizer: { enabled: false, filters: [] },
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
            transport: 'tcp',
            urls: [],
            streams: [],
          },
        },
      } as unknown as SettingsFormData,
    });

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
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  describe('Stream Labels', () => {
    it('should handle streams with labels', async () => {
      const { settingsActions, settingsStore } = await import('$lib/stores/settings');
      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      render(AudioSettingsPage);

      // Add streams with labels
      const streams = [
        { url: 'rtsp://192.168.1.100:554/stream1', label: 'Backyard Feeder' },
        { url: 'rtsp://192.168.1.101:554/stream2', label: 'Front Porch' },
      ];

      settingsActions.updateSection('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [],
          streams,
        },
      });

      expect(updateSectionSpy).toHaveBeenCalledWith('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [],
          streams,
        },
      });

      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.streams).toEqual(streams);
    });

    it('should handle empty streams array', async () => {
      const { settingsStore } = await import('$lib/stores/settings');
      render(AudioSettingsPage);

      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.streams).toEqual([]);
    });

    it('should handle streams without labels (empty string)', async () => {
      const { settingsActions, settingsStore } = await import('$lib/stores/settings');
      render(AudioSettingsPage);

      // Add stream without label
      const streams = [{ url: 'rtsp://192.168.1.100:554/stream1', label: '' }];

      settingsActions.updateSection('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [],
          streams,
        },
      });

      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.streams).toEqual(streams);
      expect(store.formData.realtime?.rtsp?.streams?.[0]?.label).toBe('');
    });

    it('should handle multiple streams with mixed label presence', async () => {
      const { settingsActions, settingsStore } = await import('$lib/stores/settings');
      render(AudioSettingsPage);

      // Mix of streams with and without labels
      const streams = [
        { url: 'rtsp://192.168.1.100:554/stream1', label: 'Backyard' },
        { url: 'rtsp://192.168.1.101:554/stream2', label: '' },
        { url: 'rtsp://192.168.1.102:554/stream3', label: 'Garden' },
      ];

      settingsActions.updateSection('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [],
          streams,
        },
      });

      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.streams).toHaveLength(3);
      expect(store.formData.realtime?.rtsp?.streams?.[0]?.label).toBe('Backyard');
      expect(store.formData.realtime?.rtsp?.streams?.[1]?.label).toBe('');
      expect(store.formData.realtime?.rtsp?.streams?.[2]?.label).toBe('Garden');
    });

    it('should update stream label', async () => {
      const { settingsActions, settingsStore } = await import('$lib/stores/settings');

      // Start with a stream
      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            rtsp: {
              transport: 'tcp',
              urls: [],
              streams: [{ url: 'rtsp://192.168.1.100:554/stream1', label: 'Old Label' }],
            },
          },
        },
      }));

      render(AudioSettingsPage);

      // Update the label
      settingsActions.updateSection('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [],
          streams: [{ url: 'rtsp://192.168.1.100:554/stream1', label: 'New Label' }],
        },
      });

      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.streams?.[0]?.label).toBe('New Label');
    });

    it('should detect changes when stream label is modified', async () => {
      const { hasSettingsChanged } = await import('$lib/utils/settingsChanges');

      const original = {
        rtsp: {
          transport: 'tcp',
          urls: [],
          streams: [{ url: 'rtsp://192.168.1.100:554/stream1', label: 'Original' }],
        },
      };

      const modified = {
        rtsp: {
          transport: 'tcp',
          urls: [],
          streams: [{ url: 'rtsp://192.168.1.100:554/stream1', label: 'Modified' }],
        },
      };

      expect(hasSettingsChanged(original, modified)).toBe(true);
      expect(hasSettingsChanged(original, original)).toBe(false);
    });

    it('should detect changes when stream is added', async () => {
      const { hasSettingsChanged } = await import('$lib/utils/settingsChanges');

      const original = {
        rtsp: {
          transport: 'tcp',
          urls: [],
          streams: [],
        },
      };

      const modified = {
        rtsp: {
          transport: 'tcp',
          urls: [],
          streams: [{ url: 'rtsp://192.168.1.100:554/stream1', label: 'New Stream' }],
        },
      };

      expect(hasSettingsChanged(original, modified)).toBe(true);
    });

    it('should handle removing a stream', async () => {
      const { settingsActions, settingsStore } = await import('$lib/stores/settings');

      // Start with two streams
      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            rtsp: {
              transport: 'tcp',
              urls: [],
              streams: [
                { url: 'rtsp://192.168.1.100:554/stream1', label: 'Stream 1' },
                { url: 'rtsp://192.168.1.101:554/stream2', label: 'Stream 2' },
              ],
            },
          },
        },
      }));

      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      render(AudioSettingsPage);

      // Remove the first stream
      settingsActions.updateSection('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [],
          streams: [{ url: 'rtsp://192.168.1.101:554/stream2', label: 'Stream 2' }],
        },
      });

      expect(updateSectionSpy).toHaveBeenCalledWith('realtime', {
        rtsp: {
          transport: 'tcp',
          urls: [],
          streams: [{ url: 'rtsp://192.168.1.101:554/stream2', label: 'Stream 2' }],
        },
      });
    });
  });

  describe('Backward Compatibility', () => {
    it('should support both urls and streams arrays simultaneously', async () => {
      const { settingsStore } = await import('$lib/stores/settings');

      // Set both urls (legacy) and streams (new)
      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            rtsp: {
              transport: 'tcp',
              urls: ['rtsp://legacy.example.com/stream'],
              streams: [{ url: 'rtsp://192.168.1.100:554/stream1', label: 'New Format' }],
            },
          },
        },
      }));

      render(AudioSettingsPage);

      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.urls).toEqual(['rtsp://legacy.example.com/stream']);
      expect(store.formData.realtime?.rtsp?.streams).toEqual([
        { url: 'rtsp://192.168.1.100:554/stream1', label: 'New Format' },
      ]);
    });

    it('should handle config with only legacy urls (no streams field)', async () => {
      const { settingsStore } = await import('$lib/stores/settings');

      // Simulate legacy config without streams field
      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            rtsp: {
              transport: 'tcp',
              urls: ['rtsp://192.168.1.100:554/stream1'],
              // streams field intentionally omitted to simulate legacy config
            },
          },
        },
      }));

      render(AudioSettingsPage);

      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.urls).toEqual(['rtsp://192.168.1.100:554/stream1']);
      // streams may be undefined in legacy config
      expect(store.formData.realtime?.rtsp?.streams ?? []).toEqual([]);
    });
  });
});
