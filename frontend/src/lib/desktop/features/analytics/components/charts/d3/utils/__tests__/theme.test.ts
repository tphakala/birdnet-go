import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { getCurrentTheme, ThemeStore } from '../theme';

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
    globalThis.window = originalWindow;
    globalThis.document = originalDocument;
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

    it('should use light theme colors', () => {
      const theme = getCurrentTheme();

      expect(theme.background).toBe('#ffffff');
      expect(theme.foreground).toBe('rgba(55, 65, 81, 1)');
      expect(theme.text).toBe('rgba(55, 65, 81, 1)');
      expect(theme.grid).toBe('rgba(0, 0, 0, 0.1)');
      expect(theme.muted).toBe('rgba(0, 0, 0, 0.6)');
    });

    it('should have light theme tooltip colors', () => {
      const theme = getCurrentTheme();

      expect(theme.tooltip.background).toBe('rgba(255, 255, 255, 0.95)');
      expect(theme.tooltip.text).toBe('rgba(55, 65, 81, 1)');
      expect(theme.tooltip.border).toBe('rgba(0, 0, 0, 0.2)');
    });

    it('should use correct semantic colors', () => {
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

    it('should use dark theme colors', () => {
      const theme = getCurrentTheme();

      expect(theme.background).toBe('#1f2937');
      expect(theme.foreground).toBe('rgba(200, 200, 200, 1)');
      expect(theme.text).toBe('rgba(200, 200, 200, 1)');
      expect(theme.grid).toBe('rgba(255, 255, 255, 0.1)');
      expect(theme.muted).toBe('rgba(255, 255, 255, 0.6)');
    });

    it('should have dark theme tooltip colors', () => {
      const theme = getCurrentTheme();

      expect(theme.tooltip.background).toBe('rgba(55, 65, 81, 0.95)');
      expect(theme.tooltip.text).toBe('rgba(200, 200, 200, 1)');
      expect(theme.tooltip.border).toBe('rgba(255, 255, 255, 0.2)');
    });

    it('should use correct dark semantic colors', () => {
      const theme = getCurrentTheme();

      expect(theme.primary).toBe('#3b82f6');
      expect(theme.accent).toBe('#0369a1');
      expect(theme.secondary).toBe('#6b7280');
      expect(theme.success).toBe('#16a34a');
      expect(theme.warning).toBe('#d97706');
      expect(theme.error).toBe('#dc2626');
    });
  });

  describe('axis theme', () => {
    it('should include correct axis configuration', () => {
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
      expect(theme.axis.fontFamily).toBe('system-ui, -apple-system, sans-serif');
      expect(theme.axis.strokeWidth).toBe(1);
      expect(theme.axis.color).toBe(theme.text);
      expect(theme.axis.gridColor).toBe(theme.grid);
    });
  });

  describe('grid color consistency', () => {
    it('should use light grid color when theme is light', () => {
      const mockElement = {
        getAttribute: vi.fn((attr: string) => (attr === 'data-theme' ? 'light' : null)),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });

      const theme = getCurrentTheme();

      expect(theme.grid).toBe('rgba(0, 0, 0, 0.1)');
      expect(theme.axis.gridColor).toBe('rgba(0, 0, 0, 0.1)');
    });

    it('should use dark grid color when theme is dark', () => {
      const mockElement = {
        getAttribute: vi.fn((attr: string) => (attr === 'data-theme' ? 'dark' : null)),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });

      const theme = getCurrentTheme();

      expect(theme.grid).toBe('rgba(255, 255, 255, 0.1)');
      expect(theme.axis.gridColor).toBe('rgba(255, 255, 255, 0.1)');
    });

    it('should maintain consistency between theme.grid and theme.axis.gridColor', () => {
      const mockElement = {
        getAttribute: vi.fn(() => 'light'),
      };
      Object.defineProperty(document, 'documentElement', {
        value: mockElement,
        writable: true,
        configurable: true,
      });

      const theme = getCurrentTheme();

      expect(theme.grid).toBe(theme.axis.gridColor);
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
  });

  afterEach(() => {
    store.destroy();
  });

  describe('initialization', () => {
    it('should initialize with current theme', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const MockMutationObserver = vi.fn(function (this: any) {
        this.observe = vi.fn();
        this.disconnect = vi.fn();
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

      store = new ThemeStore();

      const theme = store.theme;

      expect(theme).toBeDefined();
      expect(theme.primary).toBeDefined();
      expect(theme.background).toBeDefined();
    });

    it('should set up MutationObserver for theme changes', () => {
      const observeSpy = vi.fn();
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const MockMutationObserver = vi.fn(function (this: any, _callback: MutationCallback) {
        this.observe = observeSpy;
        this.disconnect = vi.fn();
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

      store = new ThemeStore();

      expect(MockMutationObserver).toHaveBeenCalled();
      expect(observeSpy).toHaveBeenCalledWith(document.documentElement, {
        attributes: true,
        attributeFilter: ['data-theme'],
      });
    });

    it('should set up media query listener', () => {
      const addEventListenerSpy = vi.fn();
      const mockMediaQuery = {
        addEventListener: addEventListenerSpy,
        removeEventListener: vi.fn(),
        matches: false,
        media: '(prefers-color-scheme: dark)',
      };
      vi.stubGlobal(
        'matchMedia',
        vi.fn(() => mockMediaQuery)
      );

      store = new ThemeStore();

      expect(addEventListenerSpy).toHaveBeenCalledWith('change', expect.any(Function));
    });
  });

  describe('theme subscription', () => {
    it('should allow subscribing to theme changes', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const MockMutationObserver = vi.fn(function (this: any) {
        this.observe = vi.fn();
        this.disconnect = vi.fn();
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

      store = new ThemeStore();
      const callback = vi.fn();

      const unsubscribe = store.subscribe(callback);

      expect(typeof unsubscribe).toBe('function');
    });

    it('should notify subscribers on theme change via requestAnimationFrame', () => {
      const rafCallbacks: Array<() => void> = [];
      vi.stubGlobal(
        'requestAnimationFrame',
        vi.fn((cb: () => void) => {
          rafCallbacks.push(cb);
          return 1;
        })
      );

      let mutationCallback: MutationCallback = () => {};
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
          } as unknown as MutationRecord,
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
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
          } as unknown as MutationRecord,
        ],
        {} as MutationObserver
      );

      // Execute RAF callbacks
      rafCallbacks.forEach(cb => cb());

      // Callback should not be called
      expect(callback).not.toHaveBeenCalled();
    });

    it('should use setTimeout fallback when requestAnimationFrame is not available', () => {
      // Use vi.stubGlobal for automatic cleanup on test failure
      vi.stubGlobal('requestAnimationFrame', undefined);

      const timeoutCallbacks: Array<() => void> = [];
      vi.stubGlobal(
        'setTimeout',
        vi.fn((cb: () => void) => {
          timeoutCallbacks.push(cb);
          return 1;
        })
      );

      let mutationCallback: MutationCallback = () => {};
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
          } as unknown as MutationRecord,
        ],
        {} as MutationObserver
      );

      // Execute timeout callbacks
      timeoutCallbacks.forEach(cb => cb());

      expect(callback).toHaveBeenCalled();
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
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
          } as unknown as MutationRecord,
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

      // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
      vi.stubGlobal(
        'matchMedia',
        vi.fn(() => mockMediaQuery)
      );

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
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
          } as unknown as MutationRecord,
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
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const MockMutationObserver = vi.fn(function (this: any) {
        this.observe = vi.fn();
        this.disconnect = vi.fn();
      });
      vi.stubGlobal('MutationObserver', MockMutationObserver);

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
