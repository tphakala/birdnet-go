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
  },
}));

// Mock internationalization
vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

// Mock MapLibre GL
vi.mock('maplibre-gl', () => ({
  default: {
    Map: vi.fn(),
    Marker: vi.fn(),
  },
}));

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

// Mock fetch for i18n translation loading
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

  // Default mock for other fetch requests
  return Promise.reject(new Error(`Unmocked fetch call to: ${url}`));
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
