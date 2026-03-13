import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { getCurrentTheme, ThemeStore } from '../theme';
import type { ChartTheme } from '../theme';

describe('getCurrentTheme', () => {
  let originalWindow: typeof globalThis.window;
  let originalDocument: typeof globalThis.document;

  beforeEach(() => {
    originalWindow = globalThis.window;
    originalDocument = globalThis.document;
    vi.clearAllMocks();
  });

  afterEach(() => {
    // Restore original values
    if (originalWindow) {
      globalThis.window = originalWindow;
    }
    if (originalDocument) {
      globalThis.document = originalDocument;
    }
  });

  describe('SSR behavior', () => {
    it('should return fallback theme when window is undefined', () => {
      // @ts-expect-error - Testing SSR scenario
      globalThis.window = undefined;

      const theme = getCurrentTheme();

      expect(theme).toMatchObject({
        background: '#ffffff',
        foreground: 'rgba(55, 65, 81, 1)',
        muted: 'rgba(0, 0, 0, 0.6)',
        accent: '#0284c7',
        primary: '#2563eb',
        secondary: '#4b5563',
        success: '#22c55e',
        warning: '#f59e0b',
        error: '#ef4444',
        text: 'rgba(55, 65, 81, 1)',
        grid: 'rgba(0, 0, 0, 0.1)',
      });

      expect(theme.axis).toMatchObject({
        color: 'rgba(55, 65, 81, 1)',
        fontSize: '12px',
        fontFamily: "'Inter', system-ui, -apple-system, sans-serif",
        strokeWidth: 1,
        gridColor: 'rgba(0, 0, 0, 0.1)',
      });

      expect(theme.tooltip).toMatchObject({
        background: 'rgba(255, 255, 255, 0.95)',
        text: 'rgba(55, 65, 81, 1)',
        border: 'rgba(0, 0, 0, 0.2)',
      });
    });

    it('should return fallback theme when document is undefined', () => {
      // @ts-expect-error - Testing SSR scenario
      globalThis.document = undefined;

      const theme = getCurrentTheme();

      expect(theme).toMatchObject({
        background: '#ffffff',
        foreground: 'rgba(55, 65, 81, 1)',
        primary: '#2563eb',
      });
    });
  });

  describe('light theme', () => {
    beforeEach(() => {
      // Mock getComputedStyle to return empty values (use fallbacks)
      const mockGetComputedStyle = vi.fn(() => ({
        getPropertyValue: vi.fn(() => ''),
      }));
      vi.stubGlobal('getComputedStyle', mockGetComputedStyle);

      // Mock document.documentElement with light theme
      const mockElement = {
        getAttribute: vi.fn((attr: string) => {
          if (attr === 'data-theme') return 'light';
          return null;
        }),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });
    });

    it('should use light theme fallback colors when CSS variables are not defined', () => {
      const theme = getCurrentTheme();

      // Light theme fallbacks
      expect(theme.background).toBe('#ffffff');
      expect(theme.foreground).toBe('#1f2937');
      expect(theme.text).toBe('#1f2937');
      expect(theme.grid).toBe('rgba(0, 0, 0, 0.1)');
      expect(theme.muted).toBe('#475569');
    });

    it('should have light theme tooltip colors', () => {
      const theme = getCurrentTheme();

      expect(theme.tooltip.background).toBe('rgba(255, 255, 255, 0.95)');
      expect(theme.tooltip.text).toBe('#1f2937');
      expect(theme.tooltip.border).toBe('rgba(0, 0, 0, 0.2)');
    });

    it('should use fallback values for semantic colors', () => {
      const theme = getCurrentTheme();

      expect(theme.primary).toBe('#2563eb');
      expect(theme.accent).toBe('#0284c7');
      expect(theme.secondary).toBe('#4b5563');
      expect(theme.success).toBe('#22c55e');
      expect(theme.warning).toBe('#f59e0b');
      expect(theme.error).toBe('#ef4444');
    });
  });

  describe('dark theme', () => {
    beforeEach(() => {
      // Mock getComputedStyle to return empty values (use fallbacks)
      const mockGetComputedStyle = vi.fn(() => ({
        getPropertyValue: vi.fn(() => ''),
      }));
      vi.stubGlobal('getComputedStyle', mockGetComputedStyle);

      // Mock document.documentElement with dark theme
      const mockElement = {
        getAttribute: vi.fn((attr: string) => {
          if (attr === 'data-theme') return 'dark';
          return null;
        }),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });
    });

    it('should use dark theme fallback colors when CSS variables are not defined', () => {
      const theme = getCurrentTheme();

      // Dark theme fallbacks
      expect(theme.background).toBe('#0f172a');
      expect(theme.foreground).toBe('#f1f5f9');
      expect(theme.text).toBe('#f1f5f9');
      expect(theme.grid).toBe('rgba(255, 255, 255, 0.1)');
      expect(theme.muted).toBe('#e2e8f0');
    });

    it('should have dark theme tooltip colors', () => {
      const theme = getCurrentTheme();

      expect(theme.tooltip.background).toBe('rgba(15, 23, 42, 0.95)');
      expect(theme.tooltip.text).toBe('#f1f5f9');
      expect(theme.tooltip.border).toBe('rgba(255, 255, 255, 0.2)');
    });
  });

  describe('CSS variable reading', () => {
    it('should read CSS variables when available', () => {
      const cssVariables: Record<string, string> = {
        '--color-base-100': '#f0f0f0',
        '--color-base-content': '#111111',
        '--text-muted': '#666666',
        '--color-accent': '#00aaff',
        '--color-primary': '#ff0000',
        '--color-secondary': '#00ff00',
        '--color-success': '#00ff00',
        '--color-warning': '#ffaa00',
        '--color-error': '#ff0000',
      };

      const mockGetComputedStyle = vi.fn(() => ({
        getPropertyValue: vi.fn((prop: string) => cssVariables[prop] || ''),
      }));
      vi.stubGlobal('getComputedStyle', mockGetComputedStyle);

      const mockElement = {
        getAttribute: vi.fn(() => 'light'),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });

      const theme = getCurrentTheme();

      expect(theme.background).toBe('#f0f0f0');
      expect(theme.foreground).toBe('#111111');
      expect(theme.muted).toBe('#666666');
      expect(theme.accent).toBe('#00aaff');
      expect(theme.primary).toBe('#ff0000');
      expect(theme.secondary).toBe('#00ff00');
      expect(theme.success).toBe('#00ff00');
      expect(theme.warning).toBe('#ffaa00');
      expect(theme.error).toBe('#ff0000');
    });

    it('should trim whitespace from CSS variable values', () => {
      const mockGetComputedStyle = vi.fn(() => ({
        getPropertyValue: vi.fn((prop: string) => {
          if (prop === '--color-primary') return '  #2563eb  ';
          return '';
        }),
      }));
      vi.stubGlobal('getComputedStyle', mockGetComputedStyle);

      const mockElement = {
        getAttribute: vi.fn(() => 'light'),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });

      const theme = getCurrentTheme();

      expect(theme.primary).toBe('#2563eb');
    });

    it('should use fallback when CSS variable is empty string', () => {
      const mockGetComputedStyle = vi.fn(() => ({
        getPropertyValue: vi.fn(() => ''),
      }));
      vi.stubGlobal('getComputedStyle', mockGetComputedStyle);

      const mockElement = {
        getAttribute: vi.fn(() => 'light'),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });

      const theme = getCurrentTheme();

      // Should use fallback values
      expect(theme.primary).toBe('#2563eb');
      expect(theme.accent).toBe('#0284c7');
    });
  });

  describe('axis theme', () => {
    it('should include correct axis configuration', () => {
      const mockGetComputedStyle = vi.fn(() => ({
        getPropertyValue: vi.fn(() => ''),
      }));
      vi.stubGlobal('getComputedStyle', mockGetComputedStyle);

      const mockElement = {
        getAttribute: vi.fn(() => 'light'),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });

      const theme = getCurrentTheme();

      expect(theme.axis.fontSize).toBe('12px');
      expect(theme.axis.fontFamily).toBe("'Inter', system-ui, -apple-system, sans-serif");
      expect(theme.axis.strokeWidth).toBe(1);
      expect(theme.axis.color).toBe(theme.text);
      expect(theme.axis.gridColor).toBe(theme.grid);
    });
  });

  describe('grid color consistency', () => {
    it('should use light grid color when theme is light', () => {
      const mockGetComputedStyle = vi.fn(() => ({
        getPropertyValue: vi.fn(() => ''),
      }));
      vi.stubGlobal('getComputedStyle', mockGetComputedStyle);

      const mockElement = {
        getAttribute: vi.fn((attr: string) => (attr === 'data-theme' ? 'light' : null)),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });

      const theme = getCurrentTheme();

      // Light theme should have dark grid lines on light background
      expect(theme.grid).toBe('rgba(0, 0, 0, 0.1)');
      expect(theme.axis.gridColor).toBe('rgba(0, 0, 0, 0.1)');
    });

    it('should use dark grid color when theme is dark', () => {
      const mockGetComputedStyle = vi.fn(() => ({
        getPropertyValue: vi.fn(() => ''),
      }));
      vi.stubGlobal('getComputedStyle', mockGetComputedStyle);

      const mockElement = {
        getAttribute: vi.fn((attr: string) => (attr === 'data-theme' ? 'dark' : null)),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });

      const theme = getCurrentTheme();

      // Dark theme should have light grid lines on dark background
      expect(theme.grid).toBe('rgba(255, 255, 255, 0.1)');
      expect(theme.axis.gridColor).toBe('rgba(255, 255, 255, 0.1)');
    });

    it('should maintain consistency between theme.grid and theme.axis.gridColor', () => {
      const mockGetComputedStyle = vi.fn(() => ({
        getPropertyValue: vi.fn(() => ''),
      }));
      vi.stubGlobal('getComputedStyle', mockGetComputedStyle);

      const mockElement = {
        getAttribute: vi.fn(() => 'light'),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });

      const theme = getCurrentTheme();

      // Grid color should be consistent across theme
      expect(theme.grid).toBe(theme.axis.gridColor);
    });
  });

  describe('mixed CSS variables and fallbacks', () => {
    it('should correctly mix defined CSS variables with fallback values', () => {
      // Some variables defined, others not
      const cssVariables: Record<string, string> = {
        '--color-primary': '#custom-primary',
        '--color-base-100': '#custom-bg',
        // Other variables undefined - should use fallbacks
      };

      const mockGetComputedStyle = vi.fn(() => ({
        getPropertyValue: vi.fn((prop: string) => cssVariables[prop] || ''),
      }));
      vi.stubGlobal('getComputedStyle', mockGetComputedStyle);

      const mockElement = {
        getAttribute: vi.fn(() => 'light'),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });

      const theme = getCurrentTheme();

      // Should use custom variables where defined
      expect(theme.primary).toBe('#custom-primary');
      expect(theme.background).toBe('#custom-bg');

      // Should use fallbacks where not defined
      expect(theme.accent).toBe('#0284c7');
      expect(theme.secondary).toBe('#4b5563');
      expect(theme.success).toBe('#22c55e');
    });
  });
});

describe('ThemeStore', () => {
  let store: ThemeStore;

  beforeEach(() => {
    vi.clearAllMocks();

    // Setup minimal DOM environment
    const mockElement = {
      getAttribute: vi.fn(() => 'light'),
    };
    Object.defineProperty(document, 'documentElement', {
      value: mockElement,
      writable: true,
      configurable: true,
    });

    const mockGetComputedStyle = vi.fn(() => ({
      getPropertyValue: vi.fn(() => ''),
    }));
    vi.stubGlobal('getComputedStyle', mockGetComputedStyle);
  });

  afterEach(() => {
    if (store) {
      store.destroy();
    }
  });

  describe('initialization', () => {
    it('should initialize with current theme', () => {
      store = new ThemeStore();

      const theme = store.theme;

      expect(theme).toBeDefined();
      expect(theme.primary).toBeDefined();
      expect(theme.background).toBeDefined();
    });

    it('should set up MutationObserver for theme changes', () => {
      const observeSpy = vi.fn();
      const MockMutationObserver = vi.fn(function (this: any, callback: MutationCallback) {
        this.observe = observeSpy;
        this.disconnect = vi.fn();
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

      store = new ThemeStore();

      expect(MockMutationObserver).toHaveBeenCalled();
      expect(observeSpy).toHaveBeenCalledWith(document.documentElement, {
        attributes: true,
        attributeFilter: ['data-theme', 'data-scheme'],
      });
    });

    it('should watch both data-theme and data-scheme attributes', () => {
      const observeSpy = vi.fn();
      const MockMutationObserver = vi.fn(function (this: any, callback: MutationCallback) {
        this.observe = observeSpy;
        this.disconnect = vi.fn();
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

      store = new ThemeStore();

      const observeCall = observeSpy.mock.calls[0];
      expect(observeCall[1].attributeFilter).toEqual(['data-theme', 'data-scheme']);
    });

    it('should set up media query listener', () => {
      const addEventListenerSpy = vi.fn();
      const mockMediaQuery = {
        addEventListener: addEventListenerSpy,
        removeEventListener: vi.fn(),
        matches: false,
        media: '(prefers-color-scheme: dark)',
      };
      vi.stubGlobal('matchMedia', vi.fn(() => mockMediaQuery));

      store = new ThemeStore();

      expect(addEventListenerSpy).toHaveBeenCalledWith('change', expect.any(Function));
    });
  });

  describe('theme subscription', () => {
    it('should allow subscribing to theme changes', () => {
      store = new ThemeStore();
      const callback = vi.fn();

      const unsubscribe = store.subscribe(callback);

      expect(typeof unsubscribe).toBe('function');
    });

    it('should notify subscribers on theme change via requestAnimationFrame', () => {
      // Mock requestAnimationFrame
      const rafCallbacks: Array<() => void> = [];
      vi.stubGlobal(
        'requestAnimationFrame',
        vi.fn((cb: () => void) => {
          rafCallbacks.push(cb);
          return 1;
        })
      );

      let mutationCallback: MutationCallback = () => {};
      const MockMutationObserver = vi.fn(function (this: any, callback: MutationCallback) {
        mutationCallback = callback;
        this.observe = vi.fn();
        this.disconnect = vi.fn();
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

      store = new ThemeStore();
      const callback = vi.fn();
      store.subscribe(callback);

      // Trigger a mutation
      mutationCallback(
        [
          {
            type: 'attributes',
            attributeName: 'data-theme',
            target: document.documentElement,
          } as MutationRecord,
        ],
        {} as MutationObserver
      );

      // Callback shouldn't be called yet
      expect(callback).not.toHaveBeenCalled();

      // Execute RAF callbacks
      rafCallbacks.forEach(cb => cb());

      // Now callback should be called with updated theme
      expect(callback).toHaveBeenCalledWith(expect.any(Object));
    });

    it('should notify subscribers on data-scheme attribute change', () => {
      const rafCallbacks: Array<() => void> = [];
      vi.stubGlobal(
        'requestAnimationFrame',
        vi.fn((cb: () => void) => {
          rafCallbacks.push(cb);
          return 1;
        })
      );

      let mutationCallback: MutationCallback = () => {};
      const MockMutationObserver = vi.fn(function (this: any, callback: MutationCallback) {
        mutationCallback = callback;
        this.observe = vi.fn();
        this.disconnect = vi.fn();
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

      store = new ThemeStore();
      const callback = vi.fn();
      store.subscribe(callback);

      // Trigger a data-scheme mutation
      mutationCallback(
        [
          {
            type: 'attributes',
            attributeName: 'data-scheme',
            target: document.documentElement,
          } as MutationRecord,
        ],
        {} as MutationObserver
      );

      // Execute RAF callbacks
      rafCallbacks.forEach(cb => cb());

      expect(callback).toHaveBeenCalled();
    });

    it('should not notify on other attribute changes', () => {
      const rafCallbacks: Array<() => void> = [];
      vi.stubGlobal(
        'requestAnimationFrame',
        vi.fn((cb: () => void) => {
          rafCallbacks.push(cb);
          return 1;
        })
      );

      let mutationCallback: MutationCallback = () => {};
      const MockMutationObserver = vi.fn(function (this: any, callback: MutationCallback) {
        mutationCallback = callback;
        this.observe = vi.fn();
        this.disconnect = vi.fn();
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

      store = new ThemeStore();
      const callback = vi.fn();
      store.subscribe(callback);

      // Trigger a different attribute mutation
      mutationCallback(
        [
          {
            type: 'attributes',
            attributeName: 'class',
            target: document.documentElement,
          } as MutationRecord,
        ],
        {} as MutationObserver
      );

      // Execute RAF callbacks
      rafCallbacks.forEach(cb => cb());

      // Callback should not be called
      expect(callback).not.toHaveBeenCalled();
    });

    it('should use setTimeout fallback when requestAnimationFrame is not available', () => {
      // Mock environment without requestAnimationFrame
      const originalRAF = globalThis.requestAnimationFrame;
      // @ts-expect-error - Testing fallback scenario
      globalThis.requestAnimationFrame = undefined;

      const timeoutCallbacks: Array<() => void> = [];
      vi.stubGlobal(
        'setTimeout',
        vi.fn((cb: () => void) => {
          timeoutCallbacks.push(cb);
          return 1;
        })
      );

      let mutationCallback: MutationCallback = () => {};
      const MockMutationObserver = vi.fn(function (this: any, callback: MutationCallback) {
        mutationCallback = callback;
        this.observe = vi.fn();
        this.disconnect = vi.fn();
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

      store = new ThemeStore();
      const callback = vi.fn();
      store.subscribe(callback);

      // Trigger a mutation
      mutationCallback(
        [
          {
            type: 'attributes',
            attributeName: 'data-theme',
            target: document.documentElement,
          } as MutationRecord,
        ],
        {} as MutationObserver
      );

      // Execute timeout callbacks
      timeoutCallbacks.forEach(cb => cb());

      expect(callback).toHaveBeenCalled();

      // Restore
      globalThis.requestAnimationFrame = originalRAF;
    });

    it('should allow unsubscribing', () => {
      const rafCallbacks: Array<() => void> = [];
      vi.stubGlobal(
        'requestAnimationFrame',
        vi.fn((cb: () => void) => {
          rafCallbacks.push(cb);
          return 1;
        })
      );

      let mutationCallback: MutationCallback = () => {};
      const MockMutationObserver = vi.fn(function (this: any, callback: MutationCallback) {
        mutationCallback = callback;
        this.observe = vi.fn();
        this.disconnect = vi.fn();
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

      store = new ThemeStore();
      const callback = vi.fn();
      const unsubscribe = store.subscribe(callback);

      // Unsubscribe
      unsubscribe();

      // Trigger a mutation
      mutationCallback(
        [
          {
            type: 'attributes',
            attributeName: 'data-theme',
            target: document.documentElement,
          } as MutationRecord,
        ],
        {} as MutationObserver
      );

      // Execute RAF callbacks
      rafCallbacks.forEach(cb => cb());

      // Callback should not be called
      expect(callback).not.toHaveBeenCalled();
    });
  });

  describe('cleanup', () => {
    it('should disconnect observer and remove listeners on destroy', () => {
      const disconnectSpy = vi.fn();
      const removeEventListenerSpy = vi.fn();

      const MockMutationObserver = vi.fn(function (this: any) {
        this.observe = vi.fn();
        this.disconnect = disconnectSpy;
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

      const mockMediaQuery = {
        addEventListener: vi.fn(),
        removeEventListener: removeEventListenerSpy,
        matches: false,
        media: '(prefers-color-scheme: dark)',
      };
      vi.stubGlobal('matchMedia', vi.fn(() => mockMediaQuery));

      store = new ThemeStore();
      store.destroy();

      expect(disconnectSpy).toHaveBeenCalled();
      expect(removeEventListenerSpy).toHaveBeenCalledWith('change', expect.any(Function));
    });

    it('should clear all callbacks on destroy', () => {
      const rafCallbacks: Array<() => void> = [];
      vi.stubGlobal(
        'requestAnimationFrame',
        vi.fn((cb: () => void) => {
          rafCallbacks.push(cb);
          return 1;
        })
      );

      let mutationCallback: MutationCallback = () => {};
      const MockMutationObserver = vi.fn(function (this: any, callback: MutationCallback) {
        mutationCallback = callback;
        this.observe = vi.fn();
        this.disconnect = vi.fn();
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

      store = new ThemeStore();
      const callback = vi.fn();
      store.subscribe(callback);

      store.destroy();

      // Trigger a mutation after destroy
      mutationCallback(
        [
          {
            type: 'attributes',
            attributeName: 'data-theme',
            target: document.documentElement,
          } as MutationRecord,
        ],
        {} as MutationObserver
      );

      // Execute RAF callbacks
      rafCallbacks.forEach(cb => cb());

      // Callback should not be called
      expect(callback).not.toHaveBeenCalled();
    });
  });

  describe('theme getter', () => {
    it('should return current theme', () => {
      store = new ThemeStore();

      const theme = store.theme;

      expect(theme).toBeDefined();
      expect(theme).toHaveProperty('primary');
      expect(theme).toHaveProperty('background');
      expect(theme).toHaveProperty('axis');
      expect(theme).toHaveProperty('tooltip');
    });
  });
});