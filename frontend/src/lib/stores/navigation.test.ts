// frontend/src/lib/stores/navigation.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createNavigation } from './navigation.svelte';

// Mock the urlHelpers module
vi.mock('$lib/utils/urlHelpers', () => ({
  buildAppUrl: vi.fn((path: string) => path),
  getAppBasePath: vi.fn(() => ''),
  extractRelativePath: vi.fn((fullPath: string, basePath: string) => {
    if (!basePath) return fullPath;
    if (fullPath.startsWith(basePath)) {
      const relativePath = fullPath.substring(basePath.length);
      return relativePath.startsWith('/') ? relativePath : '/' + relativePath;
    }
    return fullPath;
  }),
}));

describe('navigation store', () => {
  const originalLocation = window.location;

  beforeEach(() => {
    vi.clearAllMocks();
    // Mock history methods
    vi.spyOn(window.history, 'pushState').mockImplementation(() => {});
    vi.spyOn(window.history, 'replaceState').mockImplementation(() => {});
    // Reset location mock
    Object.defineProperty(window, 'location', {
      value: { pathname: '/ui/dashboard' },
      writable: true,
      configurable: true,
    });
  });

  afterEach(() => {
    // Restore original location
    Object.defineProperty(window, 'location', {
      value: originalLocation,
      writable: true,
      configurable: true,
    });
  });

  describe('navigate', () => {
    it('should update currentPath', () => {
      const nav = createNavigation();
      nav.navigate('/ui/settings');
      expect(nav.currentPath).toBe('/ui/settings');
    });

    it('should call history.pushState', () => {
      const nav = createNavigation();
      nav.navigate('/ui/analytics');
      expect(window.history.pushState).toHaveBeenCalledWith({}, '', '/ui/analytics');
    });

    it('should normalize paths without /ui/ prefix', () => {
      const nav = createNavigation();
      nav.navigate('/settings');
      expect(nav.currentPath).toBe('/ui/settings');
    });

    it('should handle root path', () => {
      const nav = createNavigation();
      nav.navigate('/');
      expect(nav.currentPath).toBe('/ui/dashboard');
    });

    it('should handle empty path', () => {
      const nav = createNavigation();
      nav.navigate('');
      expect(nav.currentPath).toBe('/ui/dashboard');
    });

    it('should handle /ui path without trailing slash', () => {
      const nav = createNavigation();
      nav.navigate('/ui');
      expect(nav.currentPath).toBe('/ui/dashboard');
    });

    it('should handle /ui/ path with trailing slash', () => {
      const nav = createNavigation();
      nav.navigate('/ui/');
      expect(nav.currentPath).toBe('/ui/dashboard');
    });

    it('should handle paths without leading slash', () => {
      const nav = createNavigation();
      nav.navigate('settings');
      expect(nav.currentPath).toBe('/ui/settings');
    });

    it('should preserve existing /ui/ prefix', () => {
      const nav = createNavigation();
      nav.navigate('/ui/detections/123');
      expect(nav.currentPath).toBe('/ui/detections/123');
    });

    it('should handle nested paths correctly', () => {
      const nav = createNavigation();
      nav.navigate('/ui/settings/audio');
      expect(nav.currentPath).toBe('/ui/settings/audio');
    });

    it('should handle paths with query parameters in internal state', () => {
      const nav = createNavigation();
      nav.navigate('/ui/detections?page=2');
      expect(nav.currentPath).toBe('/ui/detections?page=2');
    });
  });

  describe('handlePopState', () => {
    it('should update currentPath from window.location', () => {
      const nav = createNavigation();
      Object.defineProperty(window, 'location', {
        value: { pathname: '/ui/about' },
        writable: true,
        configurable: true,
      });
      nav.handlePopState();
      expect(nav.currentPath).toBe('/ui/about');
    });

    it('should normalize root path on popstate', () => {
      const nav = createNavigation();
      Object.defineProperty(window, 'location', {
        value: { pathname: '/' },
        writable: true,
        configurable: true,
      });
      nav.handlePopState();
      expect(nav.currentPath).toBe('/ui/dashboard');
    });

    it('should handle /ui path on popstate', () => {
      const nav = createNavigation();
      Object.defineProperty(window, 'location', {
        value: { pathname: '/ui' },
        writable: true,
        configurable: true,
      });
      nav.handlePopState();
      expect(nav.currentPath).toBe('/ui/dashboard');
    });
  });

  describe('initial state', () => {
    it('should initialize with normalized path', () => {
      Object.defineProperty(window, 'location', {
        value: { pathname: '/ui/dashboard' },
        writable: true,
        configurable: true,
      });
      const nav = createNavigation();
      expect(nav.currentPath).toBe('/ui/dashboard');
    });

    it('should normalize root path on initialization', () => {
      Object.defineProperty(window, 'location', {
        value: { pathname: '/' },
        writable: true,
        configurable: true,
      });
      const nav = createNavigation();
      expect(nav.currentPath).toBe('/ui/dashboard');
      expect(window.history.replaceState).toHaveBeenCalled();
    });
  });
});

describe('navigation store with proxy prefix', () => {
  const originalLocation = window.location;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(window.history, 'pushState').mockImplementation(() => {});
    vi.spyOn(window.history, 'replaceState').mockImplementation(() => {});
  });

  afterEach(() => {
    Object.defineProperty(window, 'location', {
      value: originalLocation,
      writable: true,
      configurable: true,
    });
    vi.resetModules();
  });

  it('should use buildAppUrl for history.pushState', async () => {
    const { buildAppUrl } = await import('$lib/utils/urlHelpers');

    // Configure mock to add proxy prefix
    vi.mocked(buildAppUrl).mockImplementation((path: string) => `/proxy${path}`);

    Object.defineProperty(window, 'location', {
      value: { pathname: '/proxy/ui/dashboard' },
      writable: true,
      configurable: true,
    });

    const nav = createNavigation();
    nav.navigate('/ui/settings');

    expect(buildAppUrl).toHaveBeenCalledWith('/ui/settings');
    expect(window.history.pushState).toHaveBeenCalledWith({}, '', '/proxy/ui/settings');
  });
});
