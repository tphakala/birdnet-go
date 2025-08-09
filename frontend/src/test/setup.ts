/**
 * Shared Test Setup Configuration
 *
 * This file contains common mock definitions that are used across multiple test suites
 * to avoid duplication and ensure consistency in testing environment setup.
 *
 * Automatically loaded by Vitest via setupFiles configuration.
 */

import '@testing-library/jest-dom';
import { vi } from 'vitest';

// Note: API utilities are not mocked globally to allow their own tests to run
// Component tests that need API mocks should mock them individually

// Mock logger utilities (used by API and other modules) - consolidated mock
vi.mock('$lib/utils/logger', () => ({
  loggers: {
    api: {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
    ui: {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
    system: {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
    settings: {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
    audio: {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
  },
  getLogger: vi.fn((_category: string) => ({
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  })),
}));

// Mock toast notifications
vi.mock('$lib/stores/toast', () => ({
  toastActions: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
    warning: vi.fn(), // Added missing warning method
  },
}));

// Mock internationalization - map common keys to actual text for tests
const translations: Record<string, string> = {
  'dataDisplay.table.noData': 'No data available',
  'dataDisplay.table.sortBy': 'Sort by',
  'settings.species.customConfiguration.title': 'Custom Configuration',
  'settings.species.customConfiguration.description': 'Configure custom settings for species',
  'common.ui.loading': 'Loading...',
  'common.close': 'Close',
  'common.confirm': 'Confirm',
  'common.cancel': 'Cancel',
  'common.aria.closeNotification': 'Close notification',
  'common.aria.closeModal': 'Close modal',
  'forms.labels.showPassword': 'Show password',
  'forms.labels.hidePassword': 'Hide password',
  'forms.password.strength.label': 'Password Strength:',
  'forms.password.strength.levels.weak': 'Weak',
  'forms.password.strength.levels.fair': 'Fair',
  'forms.password.strength.levels.good': 'Good',
  'forms.password.strength.levels.strong': 'Strong',
  'forms.password.strength.suggestions.title': 'Suggestions:',
  'forms.password.strength.suggestions.minLength': 'At least 8 characters',
  'forms.password.strength.suggestions.mixedCase': 'Use both uppercase and lowercase letters',
  'forms.password.strength.suggestions.number': 'Include at least one number',
  'forms.password.strength.suggestions.special': 'Include at least one special character',
  'common.buttons.cancel': 'Cancel',
  'common.buttons.confirm': 'Confirm',
};

vi.mock('$lib/i18n', () => ({
  // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with predefined translations
  t: vi.fn((key: string) => translations[key] || key),
  getLocale: vi.fn(() => 'en'),
  setLocale: vi.fn(),
  isValidLocale: vi.fn(() => true),
}));

// Mock settingsAPI for settings-related tests
vi.mock('$lib/utils/settingsApi', () => ({
  settingsAPI: {
    load: vi.fn().mockResolvedValue({
      main: { name: 'Test Node' },
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
        interval: 15,
        processingTime: false,
        species: {
          include: [],
          exclude: [],
          config: {},
        },
      },
    }),
    save: vi.fn().mockResolvedValue({}),
    test: vi.fn().mockResolvedValue({ success: true }),
  },
}));

// Mock SvelteKit navigation
vi.mock('$app/navigation', () => ({
  goto: vi.fn(),
  invalidate: vi.fn(),
  invalidateAll: vi.fn(),
  preloadData: vi.fn(),
  preloadCode: vi.fn(),
  afterNavigate: vi.fn(),
  beforeNavigate: vi.fn(),
  onNavigate: vi.fn(),
  pushState: vi.fn(),
  replaceState: vi.fn(),
}));

// Mock SvelteKit stores
vi.mock('$app/stores', () => ({
  page: {
    subscribe: vi.fn(callback => {
      callback({
        url: new URL('http://localhost:3000/'),
        params: {},
        route: { id: '/' },
        status: 200,
        error: null,
        data: {},
        form: undefined,
        state: {},
      });
      return () => {};
    }),
  },
  navigating: {
    subscribe: vi.fn(callback => {
      callback(null);
      return () => {};
    }),
  },
  updated: {
    subscribe: vi.fn(callback => {
      callback(false);
      return () => {};
    }),
    check: vi.fn().mockResolvedValue(false),
  },
}));

// Mock MapLibre GL - provide both default and named exports
vi.mock('maplibre-gl', () => {
  const MockMap = vi.fn(() => ({
    // Add methods that are used in the components
    getZoom: vi.fn(() => 10),
    setZoom: vi.fn(),
    getCenter: vi.fn(() => ({ lng: 0, lat: 0 })),
    setCenter: vi.fn(),
    easeTo: vi.fn(),
    flyTo: vi.fn(),
    remove: vi.fn(),
    on: vi.fn(),
    off: vi.fn(),
    once: vi.fn(),
    addControl: vi.fn(),
    removeControl: vi.fn(),
    resize: vi.fn(),
    getBounds: vi.fn(),
    fitBounds: vi.fn(),
    setPadding: vi.fn(),
    project: vi.fn(),
    unproject: vi.fn(),
  }));

  const MockMarker = vi.fn(() => ({
    setLngLat: vi.fn().mockReturnThis(),
    addTo: vi.fn().mockReturnThis(),
    remove: vi.fn().mockReturnThis(),
    getLngLat: vi.fn(() => ({ lng: 0, lat: 0 })),
    setPopup: vi.fn().mockReturnThis(),
    togglePopup: vi.fn().mockReturnThis(),
    getPopup: vi.fn(),
    setDraggable: vi.fn().mockReturnThis(),
    isDraggable: vi.fn(() => false),
    getElement: vi.fn(() => document.createElement('div')),
  }));

  return {
    default: {
      Map: MockMap,
      Marker: MockMarker,
    },
    // Named exports for compatibility with all import styles
    Map: MockMap,
    Marker: MockMarker,
  };
});

// Mock requestAnimationFrame and cancelAnimationFrame
const animationFrameCallbacks = new Map<number, FrameRequestCallback>();
let animationFrameId = 0;

Object.defineProperty(globalThis, 'requestAnimationFrame', {
  writable: true,
  value: vi.fn().mockImplementation((callback: FrameRequestCallback): number => {
    const id = ++animationFrameId;
    animationFrameCallbacks.set(id, callback);
    // Synchronously invoke the callback with a timestamp for deterministic testing
    // Use Date.now() for consistent behavior in test environment
    const timestamp = Date.now();
    setTimeout(() => callback(timestamp), 0);
    return id;
  }),
});

Object.defineProperty(globalThis, 'cancelAnimationFrame', {
  writable: true,
  value: vi.fn().mockImplementation((id: number): void => {
    animationFrameCallbacks.delete(id);
  }),
});

// Mock window.matchMedia
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation(query => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(), // deprecated
    removeListener: vi.fn(), // deprecated
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
});

// Mock IntersectionObserver
globalThis.IntersectionObserver = vi.fn().mockImplementation(() => ({
  observe: vi.fn(),
  unobserve: vi.fn(),
  disconnect: vi.fn(),
}));

// Mock ResizeObserver
globalThis.ResizeObserver = vi.fn().mockImplementation(() => ({
  observe: vi.fn(),
  unobserve: vi.fn(),
  disconnect: vi.fn(),
}));

// Mock HTMLCanvasElement.getContext for axe-core accessibility tests
HTMLCanvasElement.prototype.getContext = vi.fn().mockImplementation(contextType => {
  if (contextType === '2d') {
    return {
      fillRect: vi.fn(),
      clearRect: vi.fn(),
      getImageData: vi.fn().mockReturnValue({ data: [] }),
      putImageData: vi.fn(),
      createImageData: vi.fn().mockReturnValue({ data: [] }),
      setTransform: vi.fn(),
      drawImage: vi.fn(),
      save: vi.fn(),
      fillText: vi.fn(),
      restore: vi.fn(),
      beginPath: vi.fn(),
      moveTo: vi.fn(),
      lineTo: vi.fn(),
      closePath: vi.fn(),
      stroke: vi.fn(),
      translate: vi.fn(),
      scale: vi.fn(),
      rotate: vi.fn(),
      arc: vi.fn(),
      fill: vi.fn(),
      measureText: vi.fn().mockReturnValue({ width: 0 }),
      transform: vi.fn(),
      rect: vi.fn(),
      clip: vi.fn(),
    };
  }
  return null;
});

// Mock window.getComputedStyle for axe-core accessibility tests
const DEFAULT_COMPUTED_STYLES = {
  color: 'rgb(0, 0, 0)',
  backgroundColor: 'rgb(255, 255, 255)',
  fontSize: '16px',
  fontFamily: 'Arial',
  display: 'block',
  visibility: 'visible',
  opacity: '1',
  position: 'static',
  width: '100px',
  height: '100px',
  margin: '0px',
  padding: '0px',
  border: 'none',
};

window.getComputedStyle = vi.fn().mockImplementation(() => {
  const style = {
    ...DEFAULT_COMPUTED_STYLES,
    getPropertyValue: vi.fn().mockImplementation((property: string) => {
      const computedStyle = { ...DEFAULT_COMPUTED_STYLES } as Record<string, string>;
      return (
        // eslint-disable-next-line security/detect-object-injection -- intentional property access in test mock
        computedStyle[property] ||
        computedStyle[
          property.replace(/-([a-z])/g, (_: string, letter: string) => letter.toUpperCase())
        ] ||
        ''
      );
    }),
  };

  return style;
});

// Note: CSRF token mocking is handled per-test as needed to avoid interfering with API tests

// Mock fetch for i18n translation loading and API calls
globalThis.fetch = vi.fn().mockImplementation(url => {
  // Mock translation files for i18n system
  if (url.includes('/ui/assets/messages/') && url.endsWith('.json')) {
    const mockTranslations = {
      common: {
        loading: 'Loading...',
        error: 'Error',
        save: 'Save',
        cancel: 'Cancel',
        buttons: {
          save: 'Save Changes',
          reset: 'Reset',
          delete: 'Delete',
          cancel: 'Cancel',
        },
      },
      forms: {
        validation: {
          required: 'This field is required',
          invalid: 'Invalid value',
        },
      },
    };

    return Promise.resolve({
      ok: true,
      status: 200,
      statusText: 'OK',
      headers: new Headers({ 'content-type': 'application/json' }),
      json: () => Promise.resolve(mockTranslations),
      text: () => Promise.resolve(JSON.stringify(mockTranslations)),
    });
  }

  // Mock API endpoints to prevent unmocked fetch warnings
  if (url.includes('/api/')) {
    return Promise.resolve({
      ok: true,
      status: 200,
      statusText: 'OK',
      headers: new Headers({
        'content-type': 'application/json',
        'x-csrf-token': 'mock-csrf-token-123',
      }),
      json: () => Promise.resolve({ data: [] }),
      text: () => Promise.resolve('{"data":[]}'),
    });
  }

  // Default mock for other fetch requests
  return Promise.reject(new Error(`Unmocked fetch call to: ${url}`));
});

// Mock window.location for navigation tests
Object.defineProperty(window, 'location', {
  writable: true,
  value: {
    href: 'http://localhost:3000/',
    origin: 'http://localhost:3000',
    protocol: 'http:',
    host: 'localhost:3000',
    hostname: 'localhost',
    port: '3000',
    pathname: '/',
    search: '',
    hash: '',
    assign: vi.fn(),
    replace: vi.fn(),
    reload: vi.fn(),
    toString: vi.fn(() => 'http://localhost:3000/'),
  },
});

// Global test utilities
export const testUtils = {
  // Helper to reset all mocked functions
  resetAllMocks: () => {
    vi.clearAllMocks();
  },

  // Helper to create mock settings data
  createMockSettings: (overrides = {}) => ({
    include: [],
    exclude: [],
    config: {},
    ...overrides,
  }),

  // Helper to suppress console errors during testing
  suppressConsoleErrors: () => {
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    return {
      restore: () => consoleSpy.mockRestore(),
      spy: consoleSpy,
    };
  },

  // Helper to suppress console warnings during testing
  suppressConsoleWarnings: () => {
    const consoleSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    return {
      restore: () => consoleSpy.mockRestore(),
      spy: consoleSpy,
    };
  },
};
