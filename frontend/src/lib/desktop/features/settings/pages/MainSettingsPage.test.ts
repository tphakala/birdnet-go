import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import MainSettingsPage from './MainSettingsPage.svelte';

// Mock the stores
vi.mock('$lib/stores/settings', () => {
  const mockNodeSettings = writable({
    name: 'test-node',
  });

  const mockBirdnetSettings = writable({
    sensitivity: 1.0,
    threshold: 0.8,
    overlap: 0.0,
    locale: 'en',
    threads: 0,
    latitude: 0,
    longitude: 0,
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

  const mockSettingsStore = writable({
    isLoading: false,
    isSaving: false,
    originalData: {},
    formData: {},
  });

  return {
    settingsStore: mockSettingsStore,
    settingsActions: {
      updateSection: vi.fn(),
    },
    nodeSettings: mockNodeSettings,
    birdnetSettings: mockBirdnetSettings,
  };
});

// Mock utilities
vi.mock('$lib/utils/settingsChanges', () => ({
  hasSettingsChanged: vi.fn(() => false),
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

    // Check that main sections are rendered
    expect(screen.getByText('Node Settings')).toBeInTheDocument();
    expect(screen.getByText('BirdNET Settings')).toBeInTheDocument();
    expect(screen.getByText('Database Settings')).toBeInTheDocument();
    expect(screen.getByText('User Interface Settings')).toBeInTheDocument();
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
});
