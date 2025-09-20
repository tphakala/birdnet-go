/**
 * Tests for date persistence utilities
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import {
  getDateFromURL,
  updateURLWithDate,
  getStoredDate,
  setStoredDate,
  clearStoredDate,
  getInitialDate,
  persistDate,
  createDatePersistence,
  DATE_STORAGE_KEY,
  DATE_URL_PARAM,
  DATE_RETENTION_MS,
} from './datePersistence';
import { getLocalDateString } from './date';

describe('Date Persistence Utilities', () => {
  const mockDate = '2024-01-15';
  const futureDate = '2099-12-31';
  const currentDate = getLocalDateString();

  // Mock window.location and localStorage
  let originalLocation: Location;
  let originalLocalStorage: Storage;

  beforeEach(() => {
    vi.clearAllMocks();
    // Save originals
    originalLocation = window.location;
    originalLocalStorage = window.localStorage;

    // Reset URL to base state
    delete (window as { location?: Location }).location;
    Object.defineProperty(window, 'location', {
      value: {
        href: 'http://localhost:3000/ui/dashboard',
        search: '',
      } as Location,
      writable: true,
      configurable: true,
    });

    // Clear localStorage
    window.localStorage.clear();

    // Mock console to suppress logs in tests
    vi.spyOn(console, 'debug').mockImplementation(() => {});
    vi.spyOn(console, 'error').mockImplementation(() => {});
  });

  afterEach(() => {
    // Restore originals
    Object.defineProperty(window, 'location', {
      value: originalLocation,
      writable: true,
      configurable: true,
    });
    window.localStorage = originalLocalStorage;
    vi.restoreAllMocks();
  });

  describe('getDateFromURL', () => {
    it('returns null when no date parameter exists', () => {
      window.location.search = '';
      expect(getDateFromURL()).toBeNull();
    });

    it('returns valid date from URL parameter', () => {
      window.location.search = `?${DATE_URL_PARAM}=${mockDate}`;
      expect(getDateFromURL()).toBe(mockDate);
    });

    it('returns null for future dates', () => {
      window.location.search = `?${DATE_URL_PARAM}=${futureDate}`;
      expect(getDateFromURL()).toBeNull();
    });

    it('returns null for invalid date format', () => {
      window.location.search = `?${DATE_URL_PARAM}=invalid-date`;
      expect(getDateFromURL()).toBeNull();
    });

    it('uses custom URL parameter name', () => {
      window.location.search = '?customDate=2024-01-15';
      expect(getDateFromURL('customDate')).toBe('2024-01-15');
    });

    it('handles multiple URL parameters correctly', () => {
      window.location.search = `?view=grid&${DATE_URL_PARAM}=${mockDate}&limit=10`;
      expect(getDateFromURL()).toBe(mockDate);
    });

    it('returns null in non-browser environment', () => {
      const tempWindow = global.window;
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      delete (global as any).window;
      expect(getDateFromURL()).toBeNull();
      global.window = tempWindow;
    });
  });

  describe('updateURLWithDate', () => {
    it('adds date parameter to URL', () => {
      const mockReplaceState = vi.fn();
      window.history.replaceState = mockReplaceState;

      updateURLWithDate(mockDate);

      expect(mockReplaceState).toHaveBeenCalledWith(
        null,
        '',
        expect.stringContaining(`${DATE_URL_PARAM}=${mockDate}`)
      );
    });

    it('removes date parameter when date is today', () => {
      const mockReplaceState = vi.fn();
      window.history.replaceState = mockReplaceState;
      window.location.search = `?${DATE_URL_PARAM}=2024-01-01`;

      updateURLWithDate(currentDate);

      expect(mockReplaceState).toHaveBeenCalledWith(
        null,
        '',
        expect.not.stringContaining(DATE_URL_PARAM)
      );
    });

    it('preserves other URL parameters', () => {
      const mockReplaceState = vi.fn();
      window.history.replaceState = mockReplaceState;
      // Update both href and search to include the parameters
      Object.defineProperty(window, 'location', {
        value: {
          href: 'http://localhost:3000/ui/dashboard?view=grid&limit=10',
          search: '?view=grid&limit=10',
        } as Location,
        writable: true,
        configurable: true,
      });

      updateURLWithDate(mockDate);

      const callArgs = mockReplaceState.mock.calls[0][2] as string;
      expect(callArgs).toContain('view=grid');
      expect(callArgs).toContain('limit=10');
      expect(callArgs).toContain(`${DATE_URL_PARAM}=${mockDate}`);
    });

    it('uses custom URL parameter name', () => {
      const mockReplaceState = vi.fn();
      window.history.replaceState = mockReplaceState;

      updateURLWithDate(mockDate, 'customDate');

      expect(mockReplaceState).toHaveBeenCalledWith(
        null,
        '',
        expect.stringContaining(`customDate=${mockDate}`)
      );
    });

    it('handles errors gracefully', () => {
      window.history.replaceState = () => {
        throw new Error('replaceState failed');
      };

      // Should not throw
      expect(() => updateURLWithDate(mockDate)).not.toThrow();
    });
  });

  describe('getStoredDate', () => {
    it('returns null when no stored date exists', () => {
      expect(getStoredDate()).toBeNull();
    });

    it('returns valid stored date within retention period', () => {
      const stored = {
        date: mockDate,
        timestamp: Date.now() - 5 * 60 * 1000, // 5 minutes ago
      };
      window.localStorage.setItem(DATE_STORAGE_KEY, JSON.stringify(stored));

      expect(getStoredDate()).toBe(mockDate);
    });

    it('returns null for expired stored date', () => {
      const stored = {
        date: mockDate,
        timestamp: Date.now() - (DATE_RETENTION_MS + 1000), // Expired
      };
      window.localStorage.setItem(DATE_STORAGE_KEY, JSON.stringify(stored));

      expect(getStoredDate()).toBeNull();
      // Should clean up expired entry
      expect(window.localStorage.getItem(DATE_STORAGE_KEY)).toBeNull();
    });

    it('returns null for future stored dates', () => {
      const stored = {
        date: futureDate,
        timestamp: Date.now(),
      };
      window.localStorage.setItem(DATE_STORAGE_KEY, JSON.stringify(stored));

      expect(getStoredDate()).toBeNull();
      // Should clean up invalid entry
      expect(window.localStorage.getItem(DATE_STORAGE_KEY)).toBeNull();
    });

    it('returns null for corrupted stored data', () => {
      window.localStorage.setItem(DATE_STORAGE_KEY, 'not-json');
      expect(getStoredDate()).toBeNull();
      // Should clean up corrupted entry
      expect(window.localStorage.getItem(DATE_STORAGE_KEY)).toBeNull();
    });

    it('returns null for invalid stored structure', () => {
      const invalidStructures = [
        { date: 123, timestamp: Date.now() }, // Invalid date type
        { date: mockDate }, // Missing timestamp
        { timestamp: Date.now() }, // Missing date
        { date: mockDate, timestamp: 'not-a-number' }, // Invalid timestamp type
      ];

      invalidStructures.forEach(invalid => {
        window.localStorage.setItem(DATE_STORAGE_KEY, JSON.stringify(invalid));
        expect(getStoredDate()).toBeNull();
        window.localStorage.clear();
      });
    });

    it('uses custom storage key and retention', () => {
      const customKey = 'custom-date-key';
      const customRetention = 5 * 60 * 1000; // 5 minutes
      const stored = {
        date: mockDate,
        timestamp: Date.now() - 3 * 60 * 1000, // 3 minutes ago
      };
      window.localStorage.setItem(customKey, JSON.stringify(stored));

      expect(getStoredDate(customKey, customRetention)).toBe(mockDate);
    });

    it('returns null in non-browser environment', () => {
      const tempWindow = global.window;
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      delete (global as any).window;
      expect(getStoredDate()).toBeNull();
      global.window = tempWindow;
    });
  });

  describe('setStoredDate', () => {
    it('stores date with current timestamp', () => {
      const now = Date.now();
      vi.spyOn(Date, 'now').mockReturnValue(now);

      setStoredDate(mockDate);

      const stored = JSON.parse(window.localStorage.getItem(DATE_STORAGE_KEY) ?? '{}');
      expect(stored.date).toBe(mockDate);
      expect(stored.timestamp).toBe(now);
    });

    it('uses custom storage key', () => {
      const customKey = 'custom-date-key';
      setStoredDate(mockDate, customKey);

      const stored = JSON.parse(window.localStorage.getItem(customKey) ?? '{}');
      expect(stored.date).toBe(mockDate);
    });

    it('handles storage quota exceeded gracefully', () => {
      const mockSetItem = vi.fn().mockImplementation(() => {
        throw new Error('QuotaExceededError');
      });
      window.localStorage.setItem = mockSetItem;

      // Should not throw
      expect(() => setStoredDate(mockDate)).not.toThrow();
    });
  });

  describe('clearStoredDate', () => {
    it('removes stored date from localStorage', () => {
      window.localStorage.setItem(DATE_STORAGE_KEY, 'some-value');
      clearStoredDate();
      expect(window.localStorage.getItem(DATE_STORAGE_KEY)).toBeNull();
    });

    it('uses custom storage key', () => {
      const customKey = 'custom-date-key';
      window.localStorage.setItem(customKey, 'some-value');
      clearStoredDate(customKey);
      expect(window.localStorage.getItem(customKey)).toBeNull();
    });

    it('handles errors gracefully', () => {
      const mockRemoveItem = vi.fn().mockImplementation(() => {
        throw new Error('RemoveItem failed');
      });
      window.localStorage.removeItem = mockRemoveItem;

      // Should not throw
      expect(() => clearStoredDate()).not.toThrow();
    });
  });

  describe('getInitialDate', () => {
    it('prioritizes URL date over stored date', () => {
      window.location.search = `?${DATE_URL_PARAM}=2024-01-10`;
      const stored = {
        date: '2024-01-05',
        timestamp: Date.now(),
      };
      window.localStorage.setItem(DATE_STORAGE_KEY, JSON.stringify(stored));

      expect(getInitialDate()).toBe('2024-01-10');
    });

    it('uses stored date when URL has no date', () => {
      window.location.search = '';
      const stored = {
        date: mockDate,
        timestamp: Date.now(),
      };
      window.localStorage.setItem(DATE_STORAGE_KEY, JSON.stringify(stored));

      expect(getInitialDate()).toBe(mockDate);
    });

    it('falls back to current date when no URL or stored date', () => {
      window.location.search = '';
      window.localStorage.clear();

      expect(getInitialDate()).toBe(currentDate);
    });

    it('falls back to current date for expired stored date', () => {
      window.location.search = '';
      const stored = {
        date: mockDate,
        timestamp: Date.now() - (DATE_RETENTION_MS + 1000),
      };
      window.localStorage.setItem(DATE_STORAGE_KEY, JSON.stringify(stored));

      expect(getInitialDate()).toBe(currentDate);
    });

    it('uses custom configuration', () => {
      const customConfig = {
        storageKey: 'custom-key',
        urlParam: 'customDate',
        retentionMs: 5 * 60 * 1000,
      };

      window.location.search = '?customDate=2024-01-20';
      expect(getInitialDate(customConfig)).toBe('2024-01-20');
    });
  });

  describe('persistDate', () => {
    it('updates both URL and localStorage', () => {
      const mockReplaceState = vi.fn();
      window.history.replaceState = mockReplaceState;

      persistDate(mockDate);

      // Check URL was updated
      expect(mockReplaceState).toHaveBeenCalled();

      // Check localStorage was updated
      const stored = JSON.parse(window.localStorage.getItem(DATE_STORAGE_KEY) ?? '{}');
      expect(stored.date).toBe(mockDate);
    });

    it('uses custom configuration', () => {
      const mockReplaceState = vi.fn();
      window.history.replaceState = mockReplaceState;
      const customConfig = {
        storageKey: 'custom-key',
        urlParam: 'customDate',
      };

      persistDate(mockDate, customConfig);

      // Check custom URL param
      const callArgs = mockReplaceState.mock.calls[0][2] as string;
      expect(callArgs).toContain('customDate=');

      // Check custom storage key
      const stored = JSON.parse(window.localStorage.getItem('custom-key') ?? '{}');
      expect(stored.date).toBe(mockDate);
    });
  });

  describe('createDatePersistence', () => {
    it('creates a persistence manager with all methods', () => {
      const manager = createDatePersistence();

      expect(manager).toHaveProperty('getInitial');
      expect(manager).toHaveProperty('persist');
      expect(manager).toHaveProperty('clear');
      expect(manager).toHaveProperty('getFromURL');
      expect(manager).toHaveProperty('getFromStorage');

      // All methods should be functions
      expect(typeof manager.getInitial).toBe('function');
      expect(typeof manager.persist).toBe('function');
      expect(typeof manager.clear).toBe('function');
      expect(typeof manager.getFromURL).toBe('function');
      expect(typeof manager.getFromStorage).toBe('function');
    });

    it('manager methods work correctly', () => {
      const mockReplaceState = vi.fn();
      window.history.replaceState = mockReplaceState;

      const manager = createDatePersistence();

      // Test getInitial
      expect(manager.getInitial()).toBe(currentDate);

      // Test persist
      manager.persist(mockDate);
      const stored = JSON.parse(window.localStorage.getItem(DATE_STORAGE_KEY) ?? '{}');
      expect(stored.date).toBe(mockDate);

      // Test getFromStorage
      expect(manager.getFromStorage()).toBe(mockDate);

      // Test getFromURL
      window.location.search = `?${DATE_URL_PARAM}=${mockDate}`;
      expect(manager.getFromURL()).toBe(mockDate);

      // Test clear
      manager.clear();
      expect(window.localStorage.getItem(DATE_STORAGE_KEY)).toBeNull();
    });

    it('manager uses custom configuration', () => {
      const customConfig = {
        storageKey: 'custom-key',
        urlParam: 'customDate',
        retentionMs: 5 * 60 * 1000,
      };

      const manager = createDatePersistence(customConfig);

      // Test with custom URL param
      window.location.search = '?customDate=2024-01-20';
      expect(manager.getFromURL()).toBe('2024-01-20');

      // Test with custom storage key
      manager.persist(mockDate);
      const stored = JSON.parse(window.localStorage.getItem('custom-key') ?? '{}');
      expect(stored.date).toBe(mockDate);
    });
  });

  describe('Edge Cases and Error Handling', () => {
    it('handles malformed URL gracefully', () => {
      window.location.href = 'not-a-valid-url';
      window.location.search = '?malformed';

      // Should not throw and return null
      expect(getDateFromURL()).toBeNull();
    });

    it('handles localStorage not available', () => {
      // Simulate localStorage not available
      const mockLocalStorage = {
        getItem: vi.fn().mockImplementation(() => {
          throw new Error('SecurityError');
        }),
        setItem: vi.fn().mockImplementation(() => {
          throw new Error('SecurityError');
        }),
        removeItem: vi.fn().mockImplementation(() => {
          throw new Error('SecurityError');
        }),
        clear: vi.fn(),
        key: vi.fn(),
        length: 0,
      };
      Object.defineProperty(window, 'localStorage', {
        value: mockLocalStorage,
        writable: true,
      });

      // All operations should handle gracefully
      expect(getStoredDate()).toBeNull();
      expect(() => setStoredDate(mockDate)).not.toThrow();
      expect(() => clearStoredDate()).not.toThrow();
    });

    it('handles very long retention periods correctly', () => {
      const yearInMs = 365 * 24 * 60 * 60 * 1000;
      const stored = {
        date: mockDate,
        timestamp: Date.now() - 100, // Very recent
      };
      window.localStorage.setItem(DATE_STORAGE_KEY, JSON.stringify(stored));

      // Should still return the date since it's within the year
      expect(getStoredDate(DATE_STORAGE_KEY, yearInMs)).toBe(mockDate);
    });

    it('handles zero retention period', () => {
      const stored = {
        date: mockDate,
        timestamp: Date.now(),
      };
      window.localStorage.setItem(DATE_STORAGE_KEY, JSON.stringify(stored));

      // With zero retention, any stored date should be considered expired
      expect(getStoredDate(DATE_STORAGE_KEY, 0)).toBeNull();
    });
  });
});
