/**
 * Navigation store for SPA client-side routing.
 * Manages route state and provides navigate() function using History API.
 *
 * Supports reverse proxy scenarios (Home Assistant Ingress, custom proxies)
 * by using proxy-aware URL building for the browser address bar while
 * maintaining internal paths without proxy prefix for routing.
 *
 * Every writer of the route (navigate, redirect, handlePopState) first asks the
 * unsaved-changes registry. A held move waits for the user's Leave or Stay; an
 * unblocked move still applies synchronously. A held Back/Forward cannot be
 * cancelled, so the store pushes the page's own URL back on top of the popped
 * entry and, on Leave, goes back once more to land on the popped entry.
 */

import { buildAppUrl, getAppBasePath, extractRelativePath } from '$lib/utils/urlHelpers';
import { DEFAULT_LOGIN_TARGET as DEFAULT_PATH } from '$lib/utils/loginRedirect';
import { unsavedChanges, type NavigationTarget } from '$lib/stores/unsavedChanges.svelte';

// Path constants built via array join at runtime.
// IMPORTANT: Do NOT use string literals like '/ui/dashboard' or '/ui/' here.
// nginx sub_filter rewrites quoted strings matching patterns like '/u' in JS bundles,
// corrupting constants and breaking path logic. Array.join() is immune to this.
const UI_PREFIX_STR = ['', 'ui'].join('/');

// Regex patterns for UI path matching.
// Regex syntax (/^\/ui\//) is immune to sub_filter rules that target quoted strings.
const UI_PATH_RE = /^\/ui\//;
const UI_ROOT_RE = /^\/ui\/?$/;
// One or more trailing slashes.
const TRAILING_SLASHES_RE = /\/+$/;

/**
 * Navigation store interface
 */
export interface NavigationStore {
  currentPath: string;
  /** Reactive query string, including '?', independent of page routing. */
  currentSearch: string;
  /** Pushes a history entry. Resolves true once applied, false when the user chose Stay or the move was dropped. */
  navigate: (url: string) => Promise<boolean>;
  /** Like navigate, but replaces the current history entry. */
  redirect: (url: string) => Promise<boolean>;
  /** Applies a browser Back/Forward. Returns false when the pop was held for the unsaved-changes prompt. */
  handlePopState: () => boolean;
  /**
   * For pages that rewrite their own query with history.replaceState or pushState: records the
   * address bar's query and URL as the current entry, so a held Back restores it.
   */
  syncFromLocation: () => void;
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
  const stripped = stripProxyPrefix(url);

  // A path made only of slashes becomes empty and falls into the root case below.
  const cleaned = stripped.replace(TRAILING_SLASHES_RE, '');

  // Handle root or empty (including /ui and /ui/)
  // Use regex instead of string literals to avoid sub_filter corruption.
  if (cleaned === '' || UI_ROOT_RE.test(cleaned)) {
    return DEFAULT_PATH;
  }
  // Already has /ui/ prefix
  if (UI_PATH_RE.test(cleaned)) {
    return cleaned;
  }
  // Add /ui/ prefix using string concat with runtime-built prefix
  return UI_PREFIX_STR + (cleaned.startsWith('/') ? cleaned : '/' + cleaned);
}

/** A URL split into the internal route state and the suffix (query and hash) the browser keeps. */
interface ParsedUrl {
  path: string;
  search: string;
  suffix: string;
}

/**
 * Create a navigation store instance.
 * Used for testing and the singleton export.
 *
 * The store maintains `currentPath` as the internal path (without proxy prefix)
 * for routing, while using proxy-aware URLs for the browser address bar.
 */
export function createNavigation(): NavigationStore {
  let currentSearch = $state(typeof window === 'undefined' ? '' : window.location.search);
  let currentPath = $state(
    (() => {
      if (typeof window === 'undefined') return DEFAULT_PATH;

      // Strip proxy prefix from current pathname for internal routing
      const pathname = stripProxyPrefix(window.location.pathname);
      const path = normalizeUiPath(pathname);

      // Update URL if normalization changed the path
      // Use buildAppUrl to maintain proxy prefix in browser
      if (path !== pathname) {
        window.history.replaceState(
          {},
          '',
          buildAppUrl(path) + currentSearch + window.location.hash
        );
      }
      return path;
    })()
  );

  // Address-bar URL of the current entry (proxy-prefixed), put back after a held Back/Forward.
  let committedUrl =
    typeof window === 'undefined'
      ? ''
      : buildAppUrl(currentPath) + currentSearch + window.location.hash;
  // Set when the user chose Leave on a held pop, so the pop that follows is accepted unasked.
  let leavingByHistory = false;

  /**
   * Split a URL into the internal route state and the suffix the browser keeps.
   *
   * Only the pathname goes to currentPath (matches window.location.pathname on a
   * reload); the query string is exposed separately as currentSearch and the hash
   * stays only in the address bar.
   */
  function parseUrl(url: string): ParsedUrl {
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

    const pathname = splitIndex === -1 ? url : url.substring(0, splitIndex);
    const suffix = splitIndex === -1 ? '' : url.substring(splitIndex);
    return {
      path: normalizeUiPath(pathname),
      search: suffix.startsWith('?') ? suffix.split('#')[0] : '',
      suffix,
    };
  }

  /**
   * Apply a new URL to internal routing state and the browser address bar.
   *
   * Query parameters are exposed separately as currentSearch; query and hash
   * remain outside currentPath. This matches the behavior on page reload where
   * window.location.pathname (which doesn't include query string) is used for
   * routing.
   *
   * @param target - The parsed URL (see parseUrl)
   * @param mode - 'push' adds a history entry; 'replace' overwrites the current one
   */
  function applyUrl(target: ParsedUrl, mode: 'push' | 'replace'): void {
    const { path, search, suffix } = target;
    leavingByHistory = false;
    currentPath = path;
    currentSearch = search;

    if (typeof window !== 'undefined') {
      // Build proxy-aware URL for the browser address bar (includes query/hash)
      const fullUrl = buildAppUrl(path) + suffix;
      if (mode === 'replace') {
        window.history.replaceState({}, '', fullUrl);
      } else {
        window.history.pushState({}, '', fullUrl);
      }
      committedUrl = fullUrl;
    }
  }

  /**
   * Apply the URL now when nothing blocks the move, otherwise after the user chooses Leave.
   * A move made while a prompt is open is dropped.
   */
  function move(url: string, mode: 'push' | 'replace'): Promise<boolean> {
    const target = parseUrl(url);
    const decision = unsavedChanges.request(
      { path: target.path, search: target.search },
      { path: currentPath, search: currentSearch }
    );
    if (decision === null) {
      applyUrl(target, mode);
      return Promise.resolve(true);
    }
    return decision.then(leave => {
      if (leave) applyUrl(target, mode);
      return leave;
    });
  }

  /**
   * Navigate to a new path using SPA navigation (no page reload).
   * Pushes a new history entry so Back returns to the previous view.
   *
   * @param url - Path to navigate to, optionally with query string (e.g., '/ui/detections/123?tab=review')
   */
  function navigate(url: string): Promise<boolean> {
    return move(url, 'push');
  }

  /**
   * Redirect to a new path, replacing the current history entry instead of
   * pushing a new one. Used for retired routes so the dead URL does not linger
   * in history (Back skips straight past it).
   *
   * @param url - Path to redirect to, optionally with query string
   */
  function redirect(url: string): Promise<boolean> {
    return move(url, 'replace');
  }

  /**
   * Handle browser back/forward button navigation.
   * Strips proxy prefix from pathname for internal routing.
   *
   * The browser has already moved when this runs. When a registered guard holds the move, the
   * page's own URL is pushed back on top and false is returned (the caller should stop the
   * event); on Leave the store goes back once more, which lands on the popped entry.
   */
  function handlePopState(): boolean {
    if (typeof window === 'undefined') return true;

    const target: NavigationTarget = {
      path: normalizeUiPath(stripProxyPrefix(window.location.pathname)),
      search: window.location.search,
    };
    // The pop the user's Leave caused is accepted unasked. Back or Forward while the prompt is
    // open answers it with Stay; that pop is a fresh attempt.
    let decision: Promise<boolean> | null = null;
    if (leavingByHistory) {
      leavingByHistory = false;
    } else {
      if (unsavedChanges.prompting) unsavedChanges.stay();
      decision = unsavedChanges.request(target, { path: currentPath, search: currentSearch });
    }
    if (decision === null) {
      currentPath = target.path;
      currentSearch = target.search;
      committedUrl = window.location.pathname + window.location.search + window.location.hash;
      return true;
    }

    window.history.pushState({}, '', committedUrl);
    void decision.then(leave => {
      if (leave) {
        leavingByHistory = true;
        window.history.back();
      }
    });
    return false;
  }

  function syncFromLocation(): void {
    if (typeof window === 'undefined') return;
    currentSearch = window.location.search;
    committedUrl = window.location.pathname + window.location.search + window.location.hash;
  }

  return {
    get currentPath() {
      return currentPath;
    },
    get currentSearch() {
      return currentSearch;
    },
    navigate,
    redirect,
    handlePopState,
    syncFromLocation,
  };
}

// Singleton instance
export const navigation = createNavigation();

/**
 * Navigate an application anchor without reloading in-memory forms.
 * Modified clicks, downloads and explicit browsing targets retain native behavior.
 */
export function handleAppLinkClick(event: MouseEvent): void {
  const anchor = event.currentTarget;
  if (
    event.defaultPrevented ||
    event.button !== 0 ||
    event.metaKey ||
    event.ctrlKey ||
    event.shiftKey ||
    event.altKey ||
    !(anchor instanceof window.HTMLAnchorElement) ||
    (anchor.target && anchor.target !== '_self') ||
    anchor.hasAttribute('download')
  ) {
    return;
  }

  const url = new URL(anchor.href);
  if (url.origin !== window.location.origin || !UI_PATH_RE.test(stripProxyPrefix(url.pathname))) {
    return;
  }

  event.preventDefault();
  navigation.navigate(url.pathname + url.search + url.hash);
}
