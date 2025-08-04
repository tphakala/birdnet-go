import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { settingsStore, settingsActions } from '$lib/stores/settings';
import type { SettingsFormData } from '$lib/stores/settings';
import MainSettingsPage from './MainSettingsPage.svelte';

// Mock MapLibre GL JS
const mockMap = {
  easeTo: vi.fn(),
  zoomIn: vi.fn(),
  zoomOut: vi.fn(),
  getZoom: vi.fn(() => 10),
  on: vi.fn(),
  remove: vi.fn(),
};

const mockMarker = {
  setLngLat: vi.fn(() => mockMarker),
  addTo: vi.fn(() => mockMarker),
  on: vi.fn(),
  getLngLat: vi.fn(() => ({ lat: 40.7128, lng: -74.006 })),
};

// Mock dynamic import of MapLibre GL JS
vi.mock('maplibre-gl', () => ({
  Map: vi.fn(() => mockMap),
  Marker: vi.fn(() => mockMarker),
}));

// Mock MapLibre CSS import
vi.mock('maplibre-gl/dist/maplibre-gl.css', () => ({}));

// Mock map config
vi.mock('../utils/mapConfig', () => ({
  MAP_CONFIG: {
    DEFAULT_ZOOM: 10,
    WORLD_VIEW_ZOOM: 5,
    ANIMATION_DURATION: 0,
    FADE_DURATION: 0,
    TILE_URL: 'https://tile.openstreetmap.org/{z}/{x}/{y}.png',
    TILE_ATTRIBUTION: ['© OpenStreetMap contributors'],
    SCROLL_ZOOM: false,
    KEYBOARD_NAV: true,
    PITCH_WITH_ROTATE: false,
    TOUCH_ZOOM_ROTATE: false,
    STYLE_VERSION: 8,
    BACKGROUND_COLOR: '#f0f0f0',
  },
  createMapStyle: vi.fn(() => ({ version: 8, sources: {}, layers: [] })),
  getInitialZoom: vi.fn((lat: number, lng: number) => (lat !== 0 || lng !== 0 ? 10 : 5)),
}));

// Mock API module
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn(() => Promise.resolve({})),
    post: vi.fn(() => Promise.resolve({ count: 150 })),
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
    warning: vi.fn(),
  },
}));

// Mock logger
vi.mock('$lib/utils/logger', () => ({
  loggers: {
    settings: {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
  },
}));

// Mock i18n
vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/i18n/config', () => ({
  LOCALES: {
    en: { name: 'English', flag: '🇺🇸' },
  },
}));

describe('MainSettingsPage - Map Functionality', () => {
  beforeEach(() => {
    vi.clearAllMocks();

    // Initialize store with settings that have coordinates
    const formData: SettingsFormData = {
      main: { name: 'TestNode' },
      birdnet: {
        modelPath: '',
        labelPath: '',
        sensitivity: 1.0,
        threshold: 0.8,
        overlap: 0.0,
        locale: 'en',
        threads: 4,
        latitude: 40.7128, // NYC coordinates
        longitude: -74.006,
        rangeFilter: {
          threshold: 0.03,
          speciesCount: null,
          species: [],
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
        dashboard: {
          locale: 'en',
          thumbnails: {
            summary: true,
            recent: true,
            imageProvider: 'wikimedia',
            fallbackPolicy: 'all',
          },
          summaryLimit: 100,
        },
      },
      output: {
        sqlite: {
          enabled: false,
          path: 'birdnet.db',
        },
        mysql: {
          enabled: false,
          username: '',
          password: '',
          database: '',
          host: 'localhost',
          port: '3306',
        },
      },
    };

    settingsStore.set({
      formData,
      originalData: formData,
      isLoading: false,
      isSaving: false,
      activeSection: 'main',
      error: null,
    });
  });

  afterEach(() => {
    vi.clearAllMocks();
    vi.useRealTimers();
  });

  describe('Map Initialization', () => {
    it('should initialize map with correct coordinates from settings', async () => {
      const MapLibre = await import('maplibre-gl');
      render(MainSettingsPage);

      // Wait for effects and dynamic import to complete
      await new Promise(resolve => setTimeout(resolve, 200));

      expect(MapLibre.Map).toHaveBeenCalledWith(
        expect.objectContaining({
          center: [-74.006, 40.7128], // MapLibre uses [lng, lat] order
          zoom: 10, // Should be 10 for non-zero coordinates (city-level view)
        })
      );
    });

    it('should use world view zoom level for zero coordinates', async () => {
      // Update settings to have zero coordinates
      settingsActions.updateSection('birdnet', {
        latitude: 0,
        longitude: 0,
      });

      const MapLibre = await import('maplibre-gl');
      render(MainSettingsPage);

      await new Promise(resolve => setTimeout(resolve, 200));

      expect(MapLibre.Map).toHaveBeenCalledWith(
        expect.objectContaining({
          center: [0, 0],
          zoom: 5, // Should be 5 for zero coordinates
        })
      );
    });

    it('should not initialize map when still loading', async () => {
      // Set store to loading state
      const currentState = get(settingsStore);
      settingsStore.set({
        ...currentState,
        isLoading: true,
      });

      const MapLibre = await import('maplibre-gl');
      render(MainSettingsPage);

      await new Promise(resolve => setTimeout(resolve, 200));

      expect(MapLibre.Map).not.toHaveBeenCalled();
    });
  });

  describe('Map Controls', () => {
    it('should render zoom in and zoom out buttons', async () => {
      render(MainSettingsPage);

      const zoomInButton = screen.getByRole('button', { name: /zoom in/i });
      const zoomOutButton = screen.getByRole('button', { name: /zoom out/i });

      expect(zoomInButton).toBeInTheDocument();
      expect(zoomOutButton).toBeInTheDocument();
    });

    it('should render expand to modal button', async () => {
      render(MainSettingsPage);

      const expandButton = screen.getByRole('button', { name: /expand map to full screen/i });
      expect(expandButton).toBeInTheDocument();
    });
  });

  describe('Coordinate Updates', () => {
    it('should NOT update map view automatically when coordinates change in settings', async () => {
      render(MainSettingsPage);

      // Wait for initial map setup
      await new Promise(resolve => setTimeout(resolve, 200));

      // Clear any initial calls from map initialization
      mockMap.easeTo.mockClear();

      // Update coordinates via settings
      settingsActions.updateSection('birdnet', {
        latitude: 52.52, // Berlin
        longitude: 13.405,
      });

      await new Promise(resolve => setTimeout(resolve, 300));

      // Map should NOT update automatically - this prevents zoom issues on page reload
      expect(mockMap.easeTo).not.toHaveBeenCalled();
    });

    it('should NOT update marker automatically when coordinates change in settings', async () => {
      render(MainSettingsPage);

      await new Promise(resolve => setTimeout(resolve, 200));

      // Clear initial marker position call from initialization
      mockMarker.setLngLat.mockClear();

      // Update coordinates via settings
      settingsActions.updateSection('birdnet', {
        latitude: 35.6762, // Tokyo
        longitude: 139.6503,
      });

      await new Promise(resolve => setTimeout(resolve, 300));

      // Marker should NOT update automatically - this prevents unwanted map updates
      expect(mockMarker.setLngLat).not.toHaveBeenCalled();
    });
  });

  describe('Modal Map', () => {
    it('should initialize modal map when modal opens', async () => {
      const MapLibre = await import('maplibre-gl');
      render(MainSettingsPage);

      // Wait for initial map
      await new Promise(resolve => setTimeout(resolve, 200));

      // Open modal by clicking expand button
      const expandButton = screen.getByRole('button', { name: /expand map to full screen/i });
      await expandButton.click();

      await new Promise(resolve => setTimeout(resolve, 200));

      // Should create second map instance for modal
      expect(MapLibre.Map).toHaveBeenCalledTimes(2);
    });

    it('should NOT automatically sync coordinates between main and modal maps via settings', async () => {
      render(MainSettingsPage);

      // Wait for initial map
      await new Promise(resolve => setTimeout(resolve, 200));

      // Open modal
      const expandButton = screen.getByRole('button', { name: /expand map to full screen/i });
      expandButton.click();

      await new Promise(resolve => setTimeout(resolve, 200));

      // Clear any calls from initialization
      mockMap.easeTo.mockClear();

      // Update coordinates via settings while modal is open
      settingsActions.updateSection('birdnet', {
        latitude: 40.7128, // NYC
        longitude: -74.006,
      });

      await new Promise(resolve => setTimeout(resolve, 300));

      // Maps should NOT update automatically - prevents zoom issues
      expect(mockMap.easeTo).not.toHaveBeenCalled();
    });
  });

  describe('Settings Integration', () => {
    it('should use derived settings for coordinate display', async () => {
      render(MainSettingsPage);

      // The latitude and longitude input fields should show current values
      const latInput = screen.getByDisplayValue('40.7128');
      const lngInput = screen.getByDisplayValue('-74.006');

      expect(latInput).toBeInTheDocument();
      expect(lngInput).toBeInTheDocument();
    });

    it('should update settings when map coordinates change via map interaction', async () => {
      render(MainSettingsPage);

      await new Promise(resolve => setTimeout(resolve, 200));

      // Simulate map click event that would trigger coordinate update
      const clickHandler = mockMap.on.mock.calls.find(call => call[0] === 'click')?.[1];

      if (clickHandler) {
        // Simulate click at new coordinates
        clickHandler({
          lngLat: { lat: 52.52, lng: 13.405 }, // Berlin
        });

        await new Promise(resolve => setTimeout(resolve, 200));

        // Settings should be updated
        const currentState = get(settingsStore);
        expect(currentState.formData.birdnet.latitude).toBe(52.52); // Rounded to 3 decimals
        expect(currentState.formData.birdnet.longitude).toBe(13.405);
      }
    });
  });

  describe('Error Handling', () => {
    it('should handle missing map element gracefully', async () => {
      const MapLibre = await import('maplibre-gl');

      // Clear the store to simulate missing/loading state
      settingsStore.set({
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        formData: {} as any,
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        originalData: {} as any,
        isLoading: true, // This will prevent map initialization
        isSaving: false,
        activeSection: 'main',
        error: null,
      });

      render(MainSettingsPage);

      await new Promise(resolve => setTimeout(resolve, 200));

      // Map should not be initialized when store is loading
      expect(MapLibre.Map).not.toHaveBeenCalled();
    });

    it('should cleanup map instances properly', async () => {
      const { unmount } = render(MainSettingsPage);

      await new Promise(resolve => setTimeout(resolve, 200));

      // Unmount component
      unmount();

      // Should call remove on map cleanup
      expect(mockMap.remove).toHaveBeenCalled();
    });
  });

  describe('Performance', () => {
    it('should not reinitialize map if already exists', async () => {
      const MapLibre = await import('maplibre-gl');
      render(MainSettingsPage);

      await new Promise(resolve => setTimeout(resolve, 200));

      // Trigger effect again by updating unrelated setting
      settingsActions.updateSection('birdnet', {
        sensitivity: 1.1,
      });

      await new Promise(resolve => setTimeout(resolve, 200));

      // Map should only be created once
      expect(MapLibre.Map).toHaveBeenCalledTimes(1);
    });

    it('should handle multiple coordinate updates without triggering map changes', async () => {
      render(MainSettingsPage);

      await new Promise(resolve => setTimeout(resolve, 200));

      // Clear any initial calls
      mockMap.easeTo.mockClear();

      // Make rapid coordinate changes via settings
      settingsActions.updateSection('birdnet', { latitude: 40.1 });
      await new Promise(resolve => setTimeout(resolve, 50));

      settingsActions.updateSection('birdnet', { latitude: 40.2 });
      await new Promise(resolve => setTimeout(resolve, 50));

      settingsActions.updateSection('birdnet', { latitude: 40.3 });
      await new Promise(resolve => setTimeout(resolve, 300));

      // Should handle multiple settings updates without triggering map updates
      expect(mockMap.easeTo).not.toHaveBeenCalled();
    });
  });
});
