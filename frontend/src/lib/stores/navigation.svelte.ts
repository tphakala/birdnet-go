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
   * Query parameters are preserved in the browser URL but NOT stored in currentPath.
   * This matches the behavior on page reload where window.location.pathname
   * (which doesn't include query string) is used for routing.
   *
   * @param url - Path to navigate to, optionally with query string (e.g., '/ui/detections/123?tab=review')
   */
  function navigate(url: string): void {
    // Separate pathname from query string and hash fragment
    // Only pathname goes to currentPath (matches window.location.pathname behavior)
    // Query and hash are preserved in browser URL only
    let pathname: string;
    let suffix: string; // query string + hash fragment

    // Find the first ? or # to split pathname from suffix
    const queryIndex = url.indexOf('?');
    const hashIndex = url.indexOf('#');

    let splitIndex: number;
    if (queryIndex === -1 && hashIndex === -1) {
      splitIndex = -1;
    } else if (queryIndex === -1) {
      splitIndex = hashIndex;
    } else if (hashIndex === -1) {
      splitIndex = queryIndex;
    } else {
      splitIndex = Math.min(queryIndex, hashIndex);
    }

    if (splitIndex !== -1) {
      pathname = url.substring(0, splitIndex);
      suffix = url.substring(splitIndex);
    } else {
      pathname = url;
      suffix = '';
    }

    // Normalize only the pathname for internal routing state
    const normalizedPath = normalizeUiPath(pathname);
    currentPath = normalizedPath;

    if (typeof window !== 'undefined') {
      // Build proxy-aware URL for the browser address bar (includes query/hash)
      const fullUrl = buildAppUrl(normalizedPath) + suffix;
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
