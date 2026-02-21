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
  // SelectDropdown and SpeciesInput form components
  'common.ui.search': 'Search...',
  'components.forms.select.searchOptions': 'Search options',
  'components.forms.select.noOptions': 'No options found',
  'components.forms.species.suggestionsAvailable': '{count} species suggestions available',
  'components.forms.species.noSuggestions': 'No species suggestions available',
  // Data display
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
  'common.aria.selectDate': 'Select date',
  'common.aria.datePickerCalendar': 'Date picker calendar',
  'common.aria.previousMonth': 'Previous month',
  'common.aria.nextMonth': 'Next month',
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
  'components.forms.numberField.adjustedToMinimum': 'Value was adjusted to minimum ({value})',
  'components.forms.numberField.adjustedToMaximum': 'Value was adjusted to maximum ({value})',
  'components.datePicker.today': 'Today',
  'components.datePicker.aria.dateSelected': 'Date {date} selected',
  'components.datePicker.aria.dayUnavailable': 'Day {day} is not available for selection',
  'components.datePicker.aria.todayButton': "Select today's date: {today}",
  'components.datePicker.feedback.invalidDateFormat':
    'Invalid date format. Please use YYYY-MM-DD format',
  'common.aria.calendarNavigation':
    'Use arrow keys to navigate calendar, Enter to select, Escape to close',
  'common.validation.invalid': 'Invalid value',
  'forms.placeholders.date': 'Select date',
  // Detection action menu translations
  'dashboard.recentDetections.actions.review': 'Review detection',
  'dashboard.recentDetections.actions.showSpecies': 'Show species',
  'dashboard.recentDetections.actions.ignoreSpecies': 'Ignore species',
  'dashboard.recentDetections.actions.lockDetection': 'Lock detection',
  'dashboard.recentDetections.actions.unlockDetection': 'Unlock detection',
  'dashboard.recentDetections.actions.deleteDetection': 'Delete detection',
  // Audio Settings translations
  'settings.audio.audioCapture.title': 'Audio Capture',
  'settings.audio.audioCapture.description': 'Configure audio capture settings',
  'settings.audio.audioCapture.rtspSource': 'RTSP Source',
  'settings.audio.audioCapture.rtspUrlsLabel': 'RTSP URLs',
  'settings.audio.audioCapture.rtspUrlsHelp': 'Enter RTSP stream URLs',
  'settings.audio.errors.devicesLoadFailed': 'Failed to load audio devices',
  'settings.audio.clipSettings.title': 'Clip Settings',
  'settings.audio.clipSettings.description':
    'Configure audio clip capture and processing for identified bird calls',
  // Stream timeline translations
  'settings.audio.streams.timeline.error': 'Error',
  'settings.audio.streams.timeline.noHistory': 'No state or error history available.',
  'settings.audio.streams.timeline.stateChange': 'State Change',
  'settings.audio.streams.timeline.host': 'Host',
  'settings.audio.streams.timeline.troubleshooting': 'Troubleshooting',
  'settings.audio.streams.timeline.from': 'From',
  'settings.audio.streams.timeline.to': 'To',
  'settings.audio.streams.timeline.reason': 'Reason',
  'settings.audio.streams.timeline.eventAt': 'Event at {time}',
};

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string, params?: Record<string, unknown>) => {
    // eslint-disable-next-line security/detect-object-injection -- Test mock with controlled translation data
    let result = translations[key] || key;
    // Handle interpolation for params like {time}, {count}, etc.
    if (params) {
      for (const [paramKey, paramValue] of Object.entries(params)) {
        // eslint-disable-next-line security/detect-non-literal-regexp -- paramKey from Object.entries on controlled params object
        result = result.replace(new RegExp(`\\{${paramKey}\\}`, 'g'), String(paramValue));
      }
    }
    return result;
  }),
  getLocale: vi.fn(() => 'en'),
  setLocale: vi.fn(),
  isValidLocale: vi.fn(() => true),
}));

// Mock settingsAPI to prevent actual API calls during settings page imports
// This prevents tests from timing out when importing settings pages
vi.mock('$lib/utils/settingsApi.js', () => {
  // Default empty settings structure that matches SettingsFormData interface
  const defaultSettings = {
    node: {
      name: 'Test Node',
      timeAs24h: true,
      log: {
        enabled: false,
        path: '/tmp/logs',
        rotation: 'daily',
        maxSize: 10,
        rotationDay: 'monday',
      },
    },
    realtime: {
      interval: 15,
      processingTime: true,
      dynamicThreshold: {
        enabled: false,
        debug: false,
        trigger: 10,
        min: 0.1,
        validHours: 24,
      },
      species: {
        include: [],
        exclude: [],
        config: {},
      },
      database: {
        enabled: true,
        path: '/tmp/test.db',
      },
      rangeFilter: {
        threshold: 0.01,
        speciesCount: null,
        species: [],
      },
    },
    audio: {
      source: 'sysdefault',
      export: {
        enabled: false,
        type: 'wav' as const,
        path: '/tmp/clips',
        bitrate: '128k',
        retention: {
          policy: 'usage',
          maxAge: '7d',
          maxUsage: '80%',
          minClips: 100,
          keepSpectrograms: false,
        },
        length: 15,
        preCapture: 3,
        gain: 0,
        normalization: {
          enabled: false,
          targetLUFS: -23,
          loudnessRange: 7,
          truePeak: -2,
        },
      },
      soundLevel: {
        enabled: false,
        interval: 60,
      },
      equalizer: {
        enabled: false,
        filters: [],
      },
    },
    filters: {
      privacy: {
        enabled: false,
        confidence: 0.9,
        debug: false,
      },
      dogBark: {
        enabled: false,
        confidence: 0.7,
        remember: 300,
        debug: false,
        species: [],
      },
    },
    integration: {
      birdweather: {
        enabled: false,
        id: '',
        latitude: 0,
        longitude: 0,
        locationAccuracy: 500,
        threshold: 0.8,
        debug: false,
      },
      mqtt: {
        enabled: false,
        broker: 'localhost',
        port: 1883,
        topic: 'birdnet',
        tls: {
          enabled: false,
          skipVerify: false,
        },
      },
      observability: {
        pprof: {
          enabled: false,
          port: 6060,
        },
        metrics: {
          enabled: false,
          port: 9090,
        },
      },
      weather: {
        enabled: false,
        provider: 'openweathermap' as const,
        apiKey: '',
        units: 'metric' as const,
        language: 'en',
        updateInterval: 3600,
        location: {
          latitude: 0,
          longitude: 0,
        },
      },
    },
    security: {
      autoTLS: {
        enabled: false,
        host: '',
      },
      basicAuth: {
        enabled: false,
        username: '',
        password: '',
      },
      googleAuth: {
        enabled: false,
        clientId: '',
        clientSecret: '',
        redirectURI: '',
        userId: '',
      },
      githubAuth: {
        enabled: false,
        clientId: '',
        clientSecret: '',
        redirectURI: '',
        userId: '',
      },
      allowSubnetBypass: {
        enabled: false,
        subnets: [],
      },
    },
    sentry: {
      enabled: false,
      dsn: '',
      environment: 'test',
      debug: false,
    },
  };

  return {
    settingsAPI: {
      load: vi.fn().mockResolvedValue(defaultSettings),
      save: vi.fn().mockResolvedValue({ success: true }),
      test: {
        birdweather: vi.fn().mockResolvedValue({ success: true, message: 'Test successful' }),
        mqtt: vi.fn().mockResolvedValue({ success: true, message: 'Test successful' }),
        database: vi.fn().mockResolvedValue({ success: true, message: 'Test successful' }),
        audio: vi.fn().mockResolvedValue({ success: true, message: 'Test successful' }),
      },
      species: {
        search: vi.fn().mockResolvedValue([]),
        rangeFilter: vi.fn().mockResolvedValue([]),
        all: vi.fn().mockResolvedValue([]),
      },
      system: {
        audioDevices: vi.fn().mockResolvedValue([]),
        ffmpegVersion: vi.fn().mockResolvedValue({ version: '5.0.0', available: true }),
        soxVersion: vi.fn().mockResolvedValue({ version: '14.4.2', available: true }),
      },
    },
  };
});

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
  value: vi.fn().mockImplementation(function (callback: FrameRequestCallback): number {
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
  value: vi.fn().mockImplementation(function (id: number): void {
    animationFrameCallbacks.delete(id);
  }),
});

// Mock window.matchMedia
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation(function (query: string) {
    return {
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(), // deprecated
      removeListener: vi.fn(), // deprecated
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    };
  }),
});

// Mock IntersectionObserver - use class syntax for Vitest 4.x compatibility
class MockIntersectionObserver {
  readonly root: Element | null = null;
  readonly rootMargin: string = '';
  readonly thresholds: ReadonlyArray<number> = [];
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
  takeRecords = vi.fn().mockReturnValue([]);
}
globalThis.IntersectionObserver =
  MockIntersectionObserver as unknown as typeof IntersectionObserver;

// Mock ResizeObserver - use class syntax for Vitest 4.x compatibility
class MockResizeObserver {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
}
globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver;

// Mock HTMLCanvasElement.getContext for axe-core accessibility tests
HTMLCanvasElement.prototype.getContext = vi.fn().mockImplementation(function (contextType: string) {
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

window.getComputedStyle = vi.fn().mockImplementation(function () {
  const style = {
    ...DEFAULT_COMPUTED_STYLES,
    getPropertyValue: vi.fn().mockImplementation(function (property: string) {
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
globalThis.fetch = vi.fn().mockImplementation(function (url: string) {
  // Mock translation files for i18n system
  if (url.includes('/ui/assets/messages/') && url.split('?')[0].endsWith('.json')) {
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

// Mock security utilities - consolidated mock for consistent test behavior
vi.mock('$lib/utils/security', () => ({
  safeGet: vi.fn(
    (
      obj: Record<string, unknown> | null | undefined,
      key: string,
      defaultValue?: unknown
    ): unknown => {
      if (obj === null || obj === undefined || typeof obj !== 'object') {
        return defaultValue;
      }
      // Use hasOwnProperty check for proper property access validation
      if (!Object.prototype.hasOwnProperty.call(obj, key)) {
        return defaultValue;
      }
      // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with controlled data, property validated above
      const value = obj[key];
      return value ?? defaultValue;
    }
  ),
  safeArrayAccess: vi.fn((arr: unknown, index: number, defaultValue?: unknown): unknown => {
    if (!Array.isArray(arr)) {
      return defaultValue;
    }
    // Enforce index bounds checking
    if (index < 0 || index >= arr.length) {
      return defaultValue;
    }
    // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with controlled data, bounds validated above
    const value = arr[index];
    return value ?? defaultValue;
  }),
  // Mock safeSpread to just spread objects without security validation for tests
  safeSpread: vi.fn(
    (...objects: Array<Record<string, unknown> | null | undefined>): Record<string, unknown> => {
      return objects.reduce(
        (result: Record<string, unknown>, obj) => {
          if (obj != null && typeof obj === 'object') {
            return { ...result, ...obj };
          }
          return result;
        },
        {} as Record<string, unknown>
      );
    }
  ),
  // Mock URL validation for RTSP and other protocols
  validateProtocolURL: vi.fn(
    (
      url: unknown,
      allowedProtocols: string[] = ['rtsp', 'http', 'https'],
      maxLength: number = 2048
    ): boolean => {
      if (!url || typeof url !== 'string' || url.length > maxLength) {
        return false;
      }
      try {
        const parsed = new URL(url);
        return allowedProtocols.includes(parsed.protocol.slice(0, -1));
      } catch {
        return false;
      }
    }
  ),
  // Mock CIDR validation
  validateCIDR: vi.fn(cidr => {
    if (!cidr || typeof cidr !== 'string' || cidr.length > 18) return false;
    const parts = cidr.split('/');
    if (parts.length !== 2) return false;
    const [ip, mask] = parts;
    const octets = ip.split('.');
    if (octets.length !== 4) return false;
    for (const octet of octets) {
      if (!/^\d{1,3}$/.test(octet)) return false;
      const num = parseInt(octet, 10);
      if (num > 255) return false;
    }
    if (!/^\d{1,2}$/.test(mask)) return false;
    const maskNum = parseInt(mask, 10);
    return maskNum >= 0 && maskNum <= 32;
  }),
  // Mock other security utilities with basic implementations for tests
  safeLookup: vi.fn((lookupTable, key, allowedKeys, defaultValue) => {
    if (Array.isArray(allowedKeys) && !allowedKeys.includes(key)) {
      return defaultValue;
    }
    // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with controlled data
    return lookupTable[key] ?? defaultValue;
  }),
  createSafeMap: vi.fn(obj => {
    if (obj) {
      return new Map(Object.entries(obj));
    }
    return new Map();
  }),
  validateInput: vi.fn((input, maxLength, pattern) => {
    if (!input || input.length > maxLength) {
      return false;
    }
    return pattern ? pattern.test(input) : true;
  }),
  safeRegexTest: vi.fn((pattern, input, maxLength = 1000) => {
    const str = String(input ?? '');
    if (str.length > maxLength) {
      return false;
    }
    try {
      return pattern.test(str);
    } catch {
      return false;
    }
  }),
  sanitizeFilename: vi.fn(filename => {
    const basename = filename.split(/[\\/]/).pop() ?? '';
    if (basename === '') {
      return 'screenshot.png';
    }
    return basename.replace(/[^a-zA-Z0-9._-]/g, '_');
  }),
  createEnumLookup: vi.fn(enumObj => {
    const validKeys = Object.keys(enumObj);
    return (key: string) => {
      if (validKeys.includes(key)) {
        // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with validated key
        return enumObj[key];
      }
      return undefined;
    };
  }),
  safeSwitch: vi.fn((key, cases, defaultValue) => {
    // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with controlled data
    return cases[key] ?? defaultValue;
  }),
  safeArraySpread: vi.fn((...arrays) => {
    const result = [];
    for (const arr of arrays) {
      if (Array.isArray(arr)) {
        result.push(...arr);
      }
    }
    return result;
  }),
  SafeAccessMap: vi.fn().mockImplementation(function () {
    const map = new Map();
    return Object.assign(map, {
      safeGet: vi.fn((key, defaultValue) => map.get(key) ?? defaultValue),
      hasError: vi.fn(key => map.has(key)),
      getError: vi.fn(key => map.get(key)),
    });
  }),
  nodeListToArray: vi.fn(nodeList => Array.from(nodeList)),
  safeElementAccess: vi.fn((elements, index, elementType) => {
    const arr = Array.isArray(elements) ? elements : Array.from(elements);
    // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with controlled array access
    const element = arr[index];
    if (!element) return undefined;
    if (elementType && !(element instanceof elementType)) return undefined;
    return element;
  }),
  numberToStringKey: vi.fn(key => key.toString()),
  createIndexMap: vi.fn(() => new Map()),
  IndexMap: vi.fn().mockImplementation(function () {
    const map = new Map();
    return Object.assign(map, {
      setByIndex: vi.fn((index, value) => map.set(index.toString(), value)),
      getByIndex: vi.fn(index => map.get(index.toString())),
      deleteByIndex: vi.fn(index => map.delete(index.toString())),
      hasByIndex: vi.fn(index => map.has(index.toString())),
    });
  }),
  safePropertyAccess: vi.fn((obj, key) => {
    if (obj == null) return undefined;
    // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with controlled data
    return obj[key];
  }),
  safePropertyAccessWithFallback: vi.fn((obj, key, fallback) => {
    if (obj == null) return fallback;
    // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with controlled data
    return obj[key] ?? fallback;
  }),
  createTypedDefault: vi.fn(defaults => ({ ...defaults })),
  ensureRequiredProperties: vi.fn((obj, defaults) => {
    if (obj == null) return { ...defaults };
    return { ...defaults, ...obj };
  }),
}));

// Note: Other utility modules are not mocked globally to allow their own tests to run properly
// Component tests that need other utility mocks should mock them individually

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
