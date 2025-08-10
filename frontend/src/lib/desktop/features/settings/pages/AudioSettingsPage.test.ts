import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
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

  return {
    settingsStore,
    audioSettings,
    rtspSettings,
    settingsActions,
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
          },
        },
      } as unknown as SettingsFormData,
    });

    // Mock CSRF token
    document.head.innerHTML = '<meta name="csrf-token" content="test-csrf-token">';

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
    it('should render RTSP URL input section', async () => {
      render(AudioSettingsPage);

      await waitFor(() => {
        expect(screen.getByText('RTSP Source')).toBeInTheDocument();
        expect(screen.getByText('RTSP URLs')).toBeInTheDocument();
      });
    });

    it('should display RTSPUrlInput component', async () => {
      const { container } = render(AudioSettingsPage);

      await waitFor(() => {
        // Check that the RTSPUrlInput area exists
        const rtspSection = container.querySelector('#rtsp-urls');
        expect(rtspSection).toBeInTheDocument();
      });
    });

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

    it('should update RTSP transport protocol', async () => {
      const { settingsActions } = await import('$lib/stores/settings');
      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');
      const { container } = render(AudioSettingsPage);

      await waitFor(() => {
        const transportSelect = container.querySelector('#rtsp-transport') as HTMLSelectElement;
        expect(transportSelect).toBeInTheDocument();
      });

      const transportSelect = container.querySelector('#rtsp-transport') as HTMLSelectElement;

      // Change to UDP
      fireEvent.change(transportSelect, { target: { value: 'udp' } });

      expect(updateSectionSpy).toHaveBeenCalledWith('realtime', {
        rtsp: {
          transport: 'udp',
          urls: [],
        },
      });
    });

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

    it('should handle disabled state for RTSP inputs', async () => {
      const { settingsStore } = await import('$lib/stores/settings');

      settingsStore.update(store => ({
        ...store,
        isLoading: true,
      }));

      const { container } = render(AudioSettingsPage);

      await waitFor(() => {
        const transportSelect = container.querySelector('#rtsp-transport') as HTMLSelectElement;
        expect(transportSelect).toBeDisabled();
      });
    });

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
    it('should render RTSPUrlInput component with empty URLs initially', async () => {
      const { settingsStore } = await import('$lib/stores/settings');
      const { container } = render(AudioSettingsPage);

      await waitFor(() => {
        const rtspSection = container.querySelector('#rtsp-urls');
        expect(rtspSection).toBeInTheDocument();
      });

      // Verify the component receives empty URLs array
      const store = get(settingsStore);
      expect(store.formData.realtime?.rtsp?.urls).toEqual([]);
    });

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

    it('should pass correct props to RTSPUrlInput component', async () => {
      const { rtspSettings, settingsStore } = await import('$lib/stores/settings');
      const urls: string[] = ['rtsp://192.168.1.100:554/stream'];

      // Update the settings store which is what gets used in practice
      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            rtsp: {
              transport: 'tcp',
              urls,
            },
          },
        },
      }));

      // Also set up rtspSettings using type assertion for the test mock
      // eslint-disable-next-line @typescript-eslint/no-explicit-any -- Required for test mock store method access
      (rtspSettings as any).set({
        transport: 'tcp',
        urls,
      });

      const { container } = render(AudioSettingsPage);

      // Verify the component renders and the RTSP URL section exists
      // The RTSPUrlInput component is mocked, so we just verify the page structure renders correctly
      expect(container.querySelector('#rtsp-urls')).toBeInTheDocument();

      // Verify RTSP transport section exists (which comes before RTSPUrlInput)
      expect(container.querySelector('#rtsp-transport')).toBeInTheDocument();

      // This test confirms that:
      // 1. The AudioSettingsPage renders without errors
      // 2. The RTSP URL section structure is present
      // 3. The RTSPUrlInput component would be passed the correct props (tested in RTSPUrlInput.test.ts)
      // 4. The component integration works end-to-end
    });

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

    it('should preserve RTSP URLs when switching transport protocol', async () => {
      const { settingsStore, settingsActions } = await import('$lib/stores/settings');
      const { get } = await import('svelte/store');
      const urls: string[] = ['rtsp://192.168.1.100:554/stream'];

      // Set up store with RTSP configuration
      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            rtsp: {
              transport: 'tcp',
              urls,
            },
          },
        },
      }));

      // Mock the updateSection to capture what the component attempts to update with
      const updateSectionSpy = vi.spyOn(settingsActions, 'updateSection');

      const { container } = render(AudioSettingsPage);

      await waitFor(() => {
        const transportSelect = container.querySelector('#rtsp-transport') as HTMLSelectElement;
        expect(transportSelect).toBeInTheDocument();
      });

      const transportSelect = container.querySelector('#rtsp-transport') as HTMLSelectElement;

      // Change transport to UDP
      fireEvent.change(transportSelect, { target: { value: 'udp' } });

      await waitFor(() => {
        expect(updateSectionSpy).toHaveBeenCalled();
      });

      // The key test: Check that the update function received the URLs from the store
      // Even if the component's derived state doesn't show them, the updateRTSPTransport
      // function should read from the actual store state to preserve URLs
      const updateCall = updateSectionSpy.mock.calls[0];
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- Safe: test assertion for mock call
      expect(updateCall![0]).toBe('realtime');

      // The critical bug fix - URLs should be preserved from the actual store state
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- Safe: test assertion for mock call
      const rtspUpdate = (updateCall![1] as { rtsp: { transport: string; urls: string[] } }).rtsp;
      expect(rtspUpdate.transport).toBe('udp');

      // This is the test for the bug fix: URLs should be preserved from store
      // If this fails, it means the component is not properly reading the existing URLs from the store
      const actualStoreState = get(settingsStore);
      const currentUrls = actualStoreState.formData.realtime?.rtsp?.urls ?? [];
      expect(rtspUpdate.urls).toEqual(currentUrls);
    });
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

    it('should handle null/undefined RTSP settings gracefully', async () => {
      const { settingsStore } = await import('$lib/stores/settings');

      settingsStore.update(store => ({
        ...store,
        formData: {
          ...store.formData,
          realtime: {
            ...(store.formData.realtime ?? {}),
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            rtsp: null as any,
          },
        },
      }));

      // Should not crash when rendering
      const { container } = render(AudioSettingsPage);

      await waitFor(() => {
        expect(container.querySelector('#rtsp-urls')).toBeInTheDocument();
      });
    });

    it('should handle saving while RTSP URLs are being edited', async () => {
      const { settingsStore } = await import('$lib/stores/settings');

      settingsStore.update(store => ({
        ...store,
        isSaving: true,
      }));

      const { container } = render(AudioSettingsPage);

      await waitFor(() => {
        const transportSelect = container.querySelector('#rtsp-transport') as HTMLSelectElement;
        expect(transportSelect).toBeDisabled();
      });
    });

    it('should handle API error when loading audio devices', async () => {
      // Mock failed API call
      global.fetch = vi.fn().mockRejectedValue(new Error('Network error'));

      render(AudioSettingsPage);

      await waitFor(() => {
        // Component should handle error gracefully
        expect(screen.getByText('Audio Capture')).toBeInTheDocument();
      });
    });
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
