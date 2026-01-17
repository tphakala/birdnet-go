/* eslint-disable no-undef */
/**
 * Navigation store for SPA client-side routing.
 * Manages route state and provides navigate() function using History API.
 *
 * Supports reverse proxy scenarios (Home Assistant Ingress, custom proxies)
 * by using proxy-aware URL building for the browser address bar while
 * maintaining internal paths without proxy prefix for routing.
 */

import { buildAppUrl, getAppBasePath, extractRelativePath } from '$lib/utils/urlHelpers';

/** Default path when no valid path is available */
const DEFAULT_PATH = '/ui/dashboard';

/** UI path prefix for the application */
const UI_PREFIX = '/ui/';

/**
 * Navigation store interface
 */
export interface NavigationStore {
  currentPath: string;
  navigate: (url: string) => void;
  handlePopState: () => void;
}

/**
 * Strip proxy prefix from a pathname for internal routing.
 * Uses extractRelativePath from urlHelpers for consistency.
 *
 * @param pathname - Full pathname possibly including proxy prefix
 * @returns Path without proxy prefix (e.g., '/ui/dashboard')
 */
function stripProxyPrefix(pathname: string): string {
  const basePath = getAppBasePath();
  if (!basePath) return pathname;
  return extractRelativePath(pathname, basePath);
}

/**
 * Normalize URL path to ensure /ui/ prefix format.
 * This is app-specific normalization for routing, distinct from
 * urlHelpers.normalizePath which handles generic slash normalization.
 *
 * @param url - The URL to normalize
 * @returns Path with /ui/ prefix (e.g., '/ui/dashboard')
 */
function normalizeUiPath(url: string): string {
  // Handle root or empty (including /ui and /ui/)
  if (url === '/' || url === '' || url === '/ui' || url === '/ui/') {
    return DEFAULT_PATH;
  }
  // Already has /ui/ prefix
  if (url.startsWith(UI_PREFIX)) {
    return url;
  }
  // Add /ui/ prefix
  return `/ui${url.startsWith('/') ? url : '/' + url}`;
}

/**
 * Create a navigation store instance.
 * Used for testing and the singleton export.
 *
 * The store maintains `currentPath` as the internal path (without proxy prefix)
 * for routing, while using proxy-aware URLs for the browser address bar.
 */
export function createNavigation(): NavigationStore {
  let currentPath = $state(
    (() => {
      if (typeof window === 'undefined') return DEFAULT_PATH;

      // Strip proxy prefix from current pathname for internal routing
      const pathname = stripProxyPrefix(window.location.pathname);
      const path = normalizeUiPath(pathname);

      // Update URL if normalization changed the path
      // Use buildAppUrl to maintain proxy prefix in browser
      if (path !== pathname) {
        window.history.replaceState({}, '', buildAppUrl(path));
      }
      return path;
    })()
  );

  /**
   * Navigate to a new path using SPA navigation (no page reload).
   * Updates both internal state and browser URL bar with proxy-aware URL.
   *
   * @param url - Path to navigate to (e.g., '/ui/detections/123')
   */
  function navigate(url: string): void {
    // Normalize the path for internal state
    const normalizedPath = normalizeUiPath(url);
    currentPath = normalizedPath;

    if (typeof window !== 'undefined') {
      // Build proxy-aware URL for the browser address bar
      const fullUrl = buildAppUrl(normalizedPath);
      window.history.pushState({}, '', fullUrl);
    }
  }

  /**
   * Handle browser back/forward button navigation.
   * Strips proxy prefix from pathname for internal routing.
   */
  function handlePopState(): void {
    if (typeof window === 'undefined') return;

    // Get pathname and strip proxy prefix for internal routing
    const pathname = stripProxyPrefix(window.location.pathname);
    currentPath = normalizeUiPath(pathname);
  }

  return {
    get currentPath() {
      return currentPath;
    },
    navigate,
    handlePopState,
  };
}

// Singleton instance
export const navigation = createNavigation();
