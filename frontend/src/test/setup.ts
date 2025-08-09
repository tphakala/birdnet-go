/**
 * Shared Test Setup Configuration
 *
 * This file contains common mock definitions that are used across multiple test suites
 * to avoid duplication and ensure consistency in testing environment setup.
 *
 * Automatically loaded by Vitest via setupFiles configuration.
 */

import { vi } from 'vitest';

// Mock API utilities
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn().mockResolvedValue({ data: { species: [] } }),
    post: vi.fn().mockResolvedValue({ data: {} }),
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

// Mock logging utilities
vi.mock('$lib/utils/logger', () => ({
  loggers: {
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
    ui: {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
  },
}));

// Mock MapLibre GL
vi.mock('maplibre-gl', () => ({
  default: {
    Map: vi.fn(),
    Marker: vi.fn(),
  },
}));

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
