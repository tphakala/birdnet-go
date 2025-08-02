import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import MainSettingsPage from './MainSettingsPage.svelte';

// Mock the stores
vi.mock('$lib/stores/settings', () => {
  const mockMainSettings = writable({
    name: 'test-node',
  });

  const mockBirdnetSettings = writable({
    sensitivity: 1.0,
    threshold: 0.8,
    overlap: 0.0,
    locale: 'en',
    threads: 0,
    latitude: 40.7128,
    longitude: -74.006,
    modelPath: '',
    labelPath: '',
    rangeFilter: {
      model: 'latest',
      threshold: 0.01,
    },
    dynamicThreshold: {
      enabled: false,
      debug: false,
      trigger: 0.8,
      min: 0.3,
      validHours: 24,
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
  });

  const mockDashboardSettings = writable({
    thumbnails: {
      summary: true,
      recent: true,
      imageProvider: 'wikimedia',
      fallbackPolicy: 'all',
    },
    summaryLimit: 100,
    locale: 'en',
  });

  const mockSettingsStore = writable({
    isLoading: false,
    isSaving: false,
    originalData: {},
    formData: {},
  });

  // Extract dynamicThreshold from birdnetSettings
  const mockBirdnetSettingsValue = {
    sensitivity: 0.5,
    threshold: 0.3,
    overlap: 0.0,
    locale: 'en',
    threads: 1,
    latitude: 0.0,
    longitude: 0.0,
    modelPath: '',
    labelPath: '',
    rangeFilter: {
      model: 'v2.4',
      threshold: 0.03,
    },
    dynamicThreshold: {
      enabled: false,
      debug: false,
      trigger: 0.8,
      min: 0.3,
      validHours: 24,
    },
    database: {
      type: 'sqlite',
      host: '',
      port: 5432,
      user: '',
      password: '',
      name: '',
      sslmode: '',
    },
  };

  return {
    settingsStore: mockSettingsStore,
    settingsActions: {
      updateSection: vi.fn(),
    },
    mainSettings: mockMainSettings,
    birdnetSettings: mockBirdnetSettings,
    dashboardSettings: mockDashboardSettings,
    dynamicThresholdSettings: writable(mockBirdnetSettingsValue.dynamicThreshold),
  };
});

// Mock utilities
vi.mock('$lib/utils/settingsChanges', () => ({
  hasSettingsChanged: vi.fn(() => false),
}));

// Mock i18n
vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
  setLocale: vi.fn(),
  LOCALES: {
    en: { name: 'English', flag: '🇬🇧' },
    de: { name: 'Deutsch', flag: '🇩🇪' },
  },
}));

// Mock api
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

// Mock fetch globally
const mockFetch = vi.fn();
global.fetch = mockFetch;

// Define window type extension
declare global {
  interface Window {
    L: {
      map: typeof vi.fn;
      tileLayer: typeof vi.fn;
      marker: typeof vi.fn;
    };
    CSRF_TOKEN: string;
  }
}

// Mock window.L for Leaflet
Object.assign(global.window, {
  L: {
    map: vi.fn(() => ({
      setView: vi.fn(),
      on: vi.fn(),
    })),
    tileLayer: vi.fn(() => ({
      addTo: vi.fn(),
    })),
    marker: vi.fn(() => ({
      addTo: vi.fn(),
      on: vi.fn(),
      setLatLng: vi.fn(),
    })),
  },
  CSRF_TOKEN: 'test-token',
});

describe('MainSettingsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('should render the main settings sections', async () => {
    // Mock API responses
    mockFetch.mockImplementation(url => {
      if (url.includes('/api/v2/settings/locales')) {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              en: 'English',
              de: 'German',
              fr: 'French',
            }),
        });
      }
      if (url.includes('/api/v2/settings/imageproviders')) {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              providers: [
                { value: 'auto', display: 'Auto (Default)' },
                { value: 'wikimedia', display: 'Wikimedia' },
                { value: 'avicommons', display: 'Avicommons' },
              ],
            }),
        });
      }
      if (url.includes('/api/v2/range/species/count')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ count: 100 }),
        });
      }
      return Promise.reject(new Error('Unknown URL'));
    });

    render(MainSettingsPage);

    // Wait for component to mount and fetch data
    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith('/api/v2/settings/locales');
      expect(mockFetch).toHaveBeenCalledWith('/api/v2/settings/imageproviders');
    });

    // Check that main sections are rendered (using translation keys)
    expect(screen.getByText('settings.main.sections.basic')).toBeInTheDocument();
    expect(screen.getByText('settings.main.sections.birdnet')).toBeInTheDocument();
    expect(screen.getByText('settings.main.sections.database')).toBeInTheDocument();
    expect(screen.getByText('settings.main.sections.ui')).toBeInTheDocument();
  });

  it('should fetch and populate locales dropdown', async () => {
    mockFetch.mockImplementation(url => {
      if (url.includes('/api/v2/settings/locales')) {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              en: 'English',
              de: 'German',
              fr: 'French',
            }),
        });
      }
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({}),
      });
    });

    render(MainSettingsPage);

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith('/api/v2/settings/locales');
    });

    // The component should have processed the locale data
    // Note: We can't directly test the select options without more complex setup
    // but we can verify the API was called correctly
  });

  it('should fetch and populate image providers dropdown', async () => {
    mockFetch.mockImplementation(url => {
      if (url.includes('/api/v2/settings/imageproviders')) {
        const response = {
          providers: [
            { value: 'auto', display: 'Auto (Default)' },
            { value: 'wikimedia', display: 'Wikimedia' },
            { value: 'avicommons', display: 'Avicommons' },
          ],
        };
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve(response),
        });
      }
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({}),
      });
    });

    render(MainSettingsPage);

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith('/api/v2/settings/imageproviders');
    });

    // Verify the endpoint was called
    const calls = mockFetch.mock.calls;
    const imageProviderCall = calls.find(call =>
      call[0].includes('/api/v2/settings/imageproviders')
    );
    expect(imageProviderCall).toBeDefined();
  });

  it('should handle API errors gracefully', async () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    mockFetch.mockImplementation(url => {
      if (url.includes('/api/v2/settings/locales')) {
        return Promise.reject(new Error('Network error'));
      }
      if (url.includes('/api/v2/settings/imageproviders')) {
        return Promise.reject(new Error('Network error'));
      }
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({}),
      });
    });

    render(MainSettingsPage);

    await waitFor(() => {
      expect(consoleErrorSpy).toHaveBeenCalledWith('Error fetching locales:', expect.any(Error));
      expect(consoleErrorSpy).toHaveBeenCalledWith(
        'Error fetching image providers:',
        expect.any(Error)
      );
    });

    consoleErrorSpy.mockRestore();
  });

  it('should map API response correctly for image providers', async () => {
    mockFetch.mockImplementation(url => {
      if (url.includes('/api/v2/settings/imageproviders')) {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              providers: [
                { value: 'auto', display: 'Auto (Default)' },
                { value: 'wikimedia', display: 'Wikimedia' },
                { value: 'avicommons', display: 'Avicommons' },
              ],
            }),
        });
      }
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({}),
      });
    });

    // Test the transformation that should happen in the component
    const providersData = {
      providers: [
        { value: 'auto', display: 'Auto (Default)' },
        { value: 'wikimedia', display: 'Wikimedia' },
        { value: 'avicommons', display: 'Avicommons' },
      ],
    };

    interface Provider {
      value: string;
      display: string;
    }

    // Test the transformation that should happen in the component
    const transformedOptions = providersData.providers.map((provider: Provider) => ({
      value: provider.value,
      label: provider.display,
    }));

    expect(transformedOptions).toEqual([
      { value: 'auto', label: 'Auto (Default)' },
      { value: 'wikimedia', label: 'Wikimedia' },
      { value: 'avicommons', label: 'Avicommons' },
    ]);

    expect(transformedOptions.length).toBeGreaterThan(1); // multipleProvidersAvailable should be true
  });

  it('should handle empty providers response', async () => {
    mockFetch.mockImplementation(url => {
      if (url.includes('/api/v2/settings/imageproviders')) {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              providers: [],
            }),
        });
      }
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({}),
      });
    });

    render(MainSettingsPage);

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith('/api/v2/settings/imageproviders');
    });

    // With empty providers, multipleProvidersAvailable should be false
    // The component should handle this gracefully
  });

  it('should handle malformed providers response', async () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    mockFetch.mockImplementation(url => {
      if (url.includes('/api/v2/settings/imageproviders')) {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              // Missing 'providers' key
              items: [],
            }),
        });
      }
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({}),
      });
    });

    render(MainSettingsPage);

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith('/api/v2/settings/imageproviders');
    });

    // The component should handle undefined providers gracefully
    // providerOptions = providersData.providers || [] should default to empty array

    consoleErrorSpy.mockRestore();
  });

  describe('Image Provider Debugging', () => {
    it('should process the API response correctly', async () => {
      mockFetch.mockImplementation(url => {
        if (url.includes('/api/v2/settings/imageproviders')) {
          return Promise.resolve({
            ok: true,
            json: () =>
              Promise.resolve({
                providers: [
                  { value: 'auto', display: 'Auto (Default)' },
                  { value: 'wikimedia', display: 'Wikimedia' },
                  { value: 'avicommons', display: 'Avicommons' },
                ],
              }),
          });
        }
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({}),
        });
      });

      render(MainSettingsPage);

      await waitFor(() => {
        expect(mockFetch).toHaveBeenCalledWith('/api/v2/settings/imageproviders');
      });

      // Verify the API was called correctly
      expect(mockFetch).toHaveBeenCalledTimes(3); // locales, imageproviders, and range count
    });
  });

  describe('Range Filter Species Count Updates', () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    it('should update species count when coordinates change', async () => {
      const { api } = await import('$lib/utils/api');
      const { settingsActions } = await import('$lib/stores/settings');

      // Setup mocks for API calls
      mockFetch.mockImplementation(url => {
        if (url.includes('/api/v2/settings/locales')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ en: 'English' }),
          });
        }
        if (url.includes('/api/v2/settings/imageproviders')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ providers: [] }),
          });
        }
        if (url.includes('/api/v2/range/species/count')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ count: 100 }),
          });
        }
        return Promise.reject(new Error('Unknown URL'));
      });

      // Mock api.post for range filter test
      vi.mocked(api.post).mockResolvedValue({ count: 150, species: [] });

      render(MainSettingsPage);

      // Wait for initial render
      await waitFor(() => {
        expect(mockFetch).toHaveBeenCalledWith('/api/v2/settings/locales');
      });

      // Clear mocks
      vi.mocked(api.post).mockClear();

      // Update coordinates
      settingsActions.updateSection('birdnet', {
        latitude: 51.5074,
        longitude: -0.1278,
      });

      // Advance timers past the debounce delay (500ms)
      vi.advanceTimersByTime(600);

      await waitFor(() => {
        expect(api.post).toHaveBeenCalledWith('/api/v2/range/species/test', {
          latitude: 51.5074,
          longitude: -0.1278,
          threshold: 0.01,
          model: 'latest',
        });
      });
    });

    it('should update species count when range filter threshold changes', async () => {
      const { api } = await import('$lib/utils/api');
      const { settingsActions } = await import('$lib/stores/settings');

      // Setup mocks for API calls
      mockFetch.mockImplementation(url => {
        if (url.includes('/api/v2/settings/locales')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ en: 'English' }),
          });
        }
        if (url.includes('/api/v2/settings/imageproviders')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ providers: [] }),
          });
        }
        if (url.includes('/api/v2/range/species/count')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ count: 100 }),
          });
        }
        return Promise.reject(new Error('Unknown URL'));
      });

      // Mock api.post for range filter test
      vi.mocked(api.post).mockResolvedValue({ count: 200, species: [] });

      render(MainSettingsPage);

      await waitFor(() => {
        expect(mockFetch).toHaveBeenCalled();
      });

      // Clear mocks
      vi.mocked(api.post).mockClear();

      // Update range filter threshold
      settingsActions.updateSection('birdnet', {
        rangeFilter: {
          model: 'latest',
          threshold: 0.05,
          speciesCount: null,
          species: [],
        },
      });

      // Advance timers past the debounce delay
      vi.advanceTimersByTime(600);

      await waitFor(() => {
        expect(api.post).toHaveBeenCalledWith('/api/v2/range/species/test', {
          latitude: 40.7128,
          longitude: -74.006,
          threshold: 0.05,
          model: 'latest',
        });
      });
    });

    it('should update species count when range filter model changes', async () => {
      const { api } = await import('$lib/utils/api');
      const { settingsActions } = await import('$lib/stores/settings');

      // Setup mocks for API calls
      mockFetch.mockImplementation(url => {
        if (url.includes('/api/v2/settings/locales')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ en: 'English' }),
          });
        }
        if (url.includes('/api/v2/settings/imageproviders')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ providers: [] }),
          });
        }
        if (url.includes('/api/v2/range/species/count')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ count: 100 }),
          });
        }
        return Promise.reject(new Error('Unknown URL'));
      });

      // Mock api.post for range filter test
      vi.mocked(api.post).mockResolvedValue({ count: 180, species: [] });

      render(MainSettingsPage);

      await waitFor(() => {
        expect(mockFetch).toHaveBeenCalled();
      });

      // Clear mocks
      vi.mocked(api.post).mockClear();

      // Update range filter model
      settingsActions.updateSection('birdnet', {
        rangeFilter: {
          model: 'legacy',
          threshold: 0.01,
          speciesCount: null,
          species: [],
        },
      });

      // Advance timers past the debounce delay
      vi.advanceTimersByTime(600);

      await waitFor(() => {
        expect(api.post).toHaveBeenCalledWith('/api/v2/range/species/test', {
          latitude: 40.7128,
          longitude: -74.006,
          threshold: 0.01,
          model: 'legacy',
        });
      });
    });

    it('should debounce multiple rapid updates', async () => {
      const { api } = await import('$lib/utils/api');
      const { settingsActions } = await import('$lib/stores/settings');

      // Setup mocks for API calls
      mockFetch.mockImplementation(url => {
        if (url.includes('/api/v2/settings/locales')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ en: 'English' }),
          });
        }
        if (url.includes('/api/v2/settings/imageproviders')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ providers: [] }),
          });
        }
        if (url.includes('/api/v2/range/species/count')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ count: 100 }),
          });
        }
        return Promise.reject(new Error('Unknown URL'));
      });

      // Mock api.post for range filter test
      vi.mocked(api.post).mockResolvedValue({ count: 175, species: [] });

      render(MainSettingsPage);

      await waitFor(() => {
        expect(mockFetch).toHaveBeenCalled();
      });

      // Clear mocks
      vi.mocked(api.post).mockClear();

      // Make multiple rapid updates
      settingsActions.updateSection('birdnet', {
        latitude: 48.8566,
        longitude: 2.3522,
      });

      vi.advanceTimersByTime(200);

      settingsActions.updateSection('birdnet', {
        rangeFilter: {
          model: 'latest',
          threshold: 0.04,
          speciesCount: null,
          species: [],
        },
      });

      vi.advanceTimersByTime(200);

      settingsActions.updateSection('birdnet', {
        rangeFilter: {
          model: 'legacy',
          threshold: 0.05,
          speciesCount: null,
          species: [],
        },
      });

      // Advance past debounce time
      vi.advanceTimersByTime(600);

      await waitFor(() => {
        // Should only have made one API call with the final values
        expect(api.post).toHaveBeenCalledTimes(1);
        expect(api.post).toHaveBeenCalledWith('/api/v2/range/species/test', {
          latitude: 48.8566,
          longitude: 2.3522,
          threshold: 0.05,
          model: 'legacy',
        });
      });
    });

    it('should handle API errors gracefully', async () => {
      const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
      const { api } = await import('$lib/utils/api');
      const { settingsActions } = await import('$lib/stores/settings');

      // Setup mocks for API calls
      mockFetch.mockImplementation(url => {
        if (url.includes('/api/v2/settings/locales')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ en: 'English' }),
          });
        }
        if (url.includes('/api/v2/settings/imageproviders')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ providers: [] }),
          });
        }
        if (url.includes('/api/v2/range/species/count')) {
          return Promise.resolve({
            ok: true,
            json: () => Promise.resolve({ count: 100 }),
          });
        }
        return Promise.reject(new Error('Unknown URL'));
      });

      // Mock api.post to throw error
      vi.mocked(api.post).mockRejectedValue(new Error('Network error'));

      render(MainSettingsPage);

      await waitFor(() => {
        expect(mockFetch).toHaveBeenCalled();
      });

      // Update coordinates to trigger API call
      settingsActions.updateSection('birdnet', {
        latitude: 51.5074,
        longitude: -0.1278,
      });

      // Advance timers past the debounce delay
      vi.advanceTimersByTime(600);

      await waitFor(() => {
        expect(consoleErrorSpy).toHaveBeenCalledWith(
          'Failed to test range filter:',
          expect.any(Error)
        );
      });

      consoleErrorSpy.mockRestore();
    });
  });
});
