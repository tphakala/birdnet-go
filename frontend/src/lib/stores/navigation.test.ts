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
      value: { pathname: '/ui/dashboard', search: '', hash: '' },
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
    it('exposes same-page query changes independently of the route', () => {
      const nav = createNavigation();
      nav.navigate('/ui/settings/main?tab=general');
      nav.navigate('/ui/settings/main?tab=location&source=species#map');
      expect(nav.currentPath).toBe('/ui/settings/main');
      expect(nav.currentSearch).toBe('?tab=location&source=species');
      nav.navigate('/ui/settings/main#map');
      expect(nav.currentSearch).toBe('');
    });

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

    it('should strip query parameters from currentPath but preserve in browser URL', () => {
      const nav = createNavigation();
      nav.navigate('/ui/detections?page=2');
      // currentPath should only contain pathname (matches window.location.pathname behavior)
      expect(nav.currentPath).toBe('/ui/detections');
      // Browser URL should include query string
      expect(window.history.pushState).toHaveBeenCalledWith({}, '', '/ui/detections?page=2');
    });

    it('should handle detection detail with tab query parameter', () => {
      const nav = createNavigation();
      nav.navigate('/ui/detections/123?tab=review');
      // currentPath should only contain pathname for routing
      expect(nav.currentPath).toBe('/ui/detections/123');
      // Browser URL should include query string
      expect(window.history.pushState).toHaveBeenCalledWith(
        {},
        '',
        '/ui/detections/123?tab=review'
      );
    });

    it('should strip hash fragments from currentPath but preserve in browser URL', () => {
      const nav = createNavigation();
      nav.navigate('/ui/dashboard#section');
      // currentPath should only contain pathname
      expect(nav.currentPath).toBe('/ui/dashboard');
      // Browser URL should include hash
      expect(window.history.pushState).toHaveBeenCalledWith({}, '', '/ui/dashboard#section');
    });

    it('should handle both query string and hash fragment', () => {
      const nav = createNavigation();
      nav.navigate('/ui/detections/123?tab=review#notes');
      // currentPath should only contain pathname
      expect(nav.currentPath).toBe('/ui/detections/123');
      // Browser URL should include both query and hash
      expect(window.history.pushState).toHaveBeenCalledWith(
        {},
        '',
        '/ui/detections/123?tab=review#notes'
      );
    });
  });

  describe('redirect', () => {
    it('updates reactive search on query-only redirects', () => {
      const nav = createNavigation();
      nav.navigate('/ui/settings/main?tab=general');
      nav.redirect('/ui/settings/main?tab=location#map');
      expect(nav.currentSearch).toBe('?tab=location');
      expect(nav.currentPath).toBe('/ui/settings/main');
    });

    it('should update currentPath and replace (not push) the history entry', () => {
      const nav = createNavigation();
      nav.redirect('/ui/analytics?tab=patterns');
      // currentPath strips the query, matching window.location.pathname behavior.
      expect(nav.currentPath).toBe('/ui/analytics');
      // A redirect replaces the current entry so the dead URL does not linger.
      expect(window.history.replaceState).toHaveBeenCalledWith(
        {},
        '',
        '/ui/analytics?tab=patterns'
      );
      expect(window.history.pushState).not.toHaveBeenCalled();
    });

    it('should normalize paths without the /ui/ prefix', () => {
      const nav = createNavigation();
      nav.redirect('/analytics');
      expect(nav.currentPath).toBe('/ui/analytics');
    });
  });

  describe('handlePopState', () => {
    it('restores query-only history entries and bare URLs', () => {
      const nav = createNavigation();
      nav.navigate('/ui/settings/main?tab=location');
      Object.defineProperty(window, 'location', {
        value: { pathname: '/ui/settings/main', search: '?tab=database', hash: '#details' },
        writable: true,
        configurable: true,
      });
      nav.handlePopState();
      expect(nav.currentPath).toBe('/ui/settings/main');
      expect(nav.currentSearch).toBe('?tab=database');
      window.location.search = '';
      nav.handlePopState();
      expect(nav.currentSearch).toBe('');
    });

    it('should update currentPath from window.location', () => {
      const nav = createNavigation();
      Object.defineProperty(window, 'location', {
        value: { pathname: '/ui/about', search: '', hash: '' },
        writable: true,
        configurable: true,
      });
      nav.handlePopState();
      expect(nav.currentPath).toBe('/ui/about');
    });

    it('should normalize root path on popstate', () => {
      const nav = createNavigation();
      Object.defineProperty(window, 'location', {
        value: { pathname: '/', search: '', hash: '' },
        writable: true,
        configurable: true,
      });
      nav.handlePopState();
      expect(nav.currentPath).toBe('/ui/dashboard');
    });

    it('should handle /ui path on popstate', () => {
      const nav = createNavigation();
      Object.defineProperty(window, 'location', {
        value: { pathname: '/ui', search: '', hash: '' },
        writable: true,
        configurable: true,
      });
      nav.handlePopState();
      expect(nav.currentPath).toBe('/ui/dashboard');
    });
  });

  describe('initial state', () => {
    it('restores the query on load and preserves suffixes when normalizing', () => {
      Object.defineProperty(window, 'location', {
        value: { pathname: '/settings/main', search: '?tab=location', hash: '#map' },
        writable: true,
        configurable: true,
      });
      const nav = createNavigation();
      expect(nav.currentPath).toBe('/ui/settings/main');
      expect(nav.currentSearch).toBe('?tab=location');
      expect(window.history.replaceState).toHaveBeenCalledWith(
        {},
        '',
        '/ui/settings/main?tab=location#map'
      );
    });

    it('should initialize with normalized path', () => {
      Object.defineProperty(window, 'location', {
        value: { pathname: '/ui/dashboard', search: '', hash: '' },
        writable: true,
        configurable: true,
      });
      const nav = createNavigation();
      expect(nav.currentPath).toBe('/ui/dashboard');
    });

    it('should normalize root path on initialization', () => {
      Object.defineProperty(window, 'location', {
        value: { pathname: '/', search: '', hash: '' },
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
      value: { pathname: '/proxy/ui/dashboard', search: '', hash: '' },
      writable: true,
      configurable: true,
    });

    const nav = createNavigation();
    nav.navigate('/ui/settings');

    expect(buildAppUrl).toHaveBeenCalledWith('/ui/settings');
    expect(window.history.pushState).toHaveBeenCalledWith({}, '', '/proxy/ui/settings');
  });

  it('should preserve query string with proxy prefix in browser URL', async () => {
    const { buildAppUrl } = await import('$lib/utils/urlHelpers');

    // Configure mock to add proxy prefix
    vi.mocked(buildAppUrl).mockImplementation((path: string) => `/proxy${path}`);

    Object.defineProperty(window, 'location', {
      value: { pathname: '/proxy/ui/dashboard', search: '', hash: '' },
      writable: true,
      configurable: true,
    });

    const nav = createNavigation();
    nav.navigate('/ui/detections/123?tab=review');

    // currentPath should not include query string
    expect(nav.currentPath).toBe('/ui/detections/123');
    // buildAppUrl should be called with pathname only
    expect(buildAppUrl).toHaveBeenCalledWith('/ui/detections/123');
    // Browser URL should include proxy prefix AND query string
    expect(window.history.pushState).toHaveBeenCalledWith(
      {},
      '',
      '/proxy/ui/detections/123?tab=review'
    );
  });
});
