/**
 * Navigation store for SPA client-side routing.
 * Manages route state and provides navigate() function using History API.
 *
 * Supports reverse proxy scenarios (Home Assistant Ingress, custom proxies)
 * by using proxy-aware URL building for the browser address bar while
 * maintaining internal paths without proxy prefix for routing.
 */

import { buildAppUrl, getAppBasePath, extractRelativePath } from '$lib/utils/urlHelpers';

// Path constants built via array join at runtime.
// IMPORTANT: Do NOT use string literals like '/ui/dashboard' or '/ui/' here.
// nginx sub_filter rewrites quoted strings matching patterns like '/u' in JS bundles,
// corrupting constants and breaking path logic. Array.join() is immune to this.
const DEFAULT_PATH = ['', 'ui', 'dashboard'].join('/');
const UI_PREFIX_STR = ['', 'ui'].join('/');

// Regex patterns for UI path matching.
// Regex syntax (/^\/ui\//) is immune to sub_filter rules that target quoted strings.
const UI_PATH_RE = /^\/ui\//;
const UI_ROOT_RE = /^\/ui\/?$/;

/**
 * Navigation store interface
 */
export interface NavigationStore {
  currentPath: string;
  navigate: (url: string) => void;
  redirect: (url: string) => void;
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
  // Strip any proxy prefix that sub_filter may have injected into the value.
  // This handles cases where sub_filter rewrites constants or URL values.
  const cleaned = stripProxyPrefix(url);

  // Handle root or empty (including /ui and /ui/)
  // Use regex instead of string literals to avoid sub_filter corruption.
  if (cleaned === '/' || cleaned === '' || UI_ROOT_RE.test(cleaned)) {
    return DEFAULT_PATH;
  }
  // Already has /ui/ prefix
  if (UI_PATH_RE.test(cleaned)) {
    return cleaned;
  }
  // Add /ui/ prefix using string concat with runtime-built prefix
  return UI_PREFIX_STR + (cleaned.startsWith('/') ? cleaned : '/' + cleaned);
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
   * Apply a new URL to internal routing state and the browser address bar.
   *
   * Query parameters and hash fragments are preserved in the browser URL but NOT
   * stored in currentPath. This matches the behavior on page reload where
   * window.location.pathname (which doesn't include query string) is used for
   * routing.
   *
   * @param url - Path to apply, optionally with query string / hash
   * @param mode - 'push' adds a history entry; 'replace' overwrites the current one
   */
  function applyUrl(url: string, mode: 'push' | 'replace'): void {
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
      if (mode === 'replace') {
        window.history.replaceState({}, '', fullUrl);
      } else {
        window.history.pushState({}, '', fullUrl);
      }
    }
  }

  /**
   * Navigate to a new path using SPA navigation (no page reload).
   * Pushes a new history entry so Back returns to the previous view.
   *
   * @param url - Path to navigate to, optionally with query string (e.g., '/ui/detections/123?tab=review')
   */
  function navigate(url: string): void {
    applyUrl(url, 'push');
  }

  /**
   * Redirect to a new path, replacing the current history entry instead of
   * pushing a new one. Used for retired routes so the dead URL does not linger
   * in history (Back skips straight past it).
   *
   * @param url - Path to redirect to, optionally with query string
   */
  function redirect(url: string): void {
    applyUrl(url, 'replace');
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
    redirect,
    handlePopState,
  };
}

// Singleton instance
export const navigation = createNavigation();
