/**
 * URL manipulation utility functions
 */

import { loggers } from './logger';

const logger = loggers.ui;

// Module-level cache for the backend-provided base path.
// null = not yet set (use URL heuristic), string = authoritative value from backend.
let _cachedBasePath: string | null = null;

/**
 * Called by appState after a successful config fetch to set the authoritative base path.
 * Once set, getAppBasePath() returns this value instead of the URL heuristic.
 */
export function setBasePath(basePath: string): void {
  _cachedBasePath = basePath;
}

/**
 * Resets the cached base path to null (unset state).
 * Exported only for testing; production code should not call this.
 */
export function resetBasePath(): void {
  _cachedBasePath = null;
}

/**
 * Extracts a relative path from a full path by removing the base path prefix.
 * Ensures the resulting path always starts with '/'.
 *
 * @param fullPath - The complete URL path (e.g., '/ui/dashboard')
 * @param basePath - The base path to remove (e.g., '/ui/')
 * @returns The relative path (e.g., '/dashboard')
 *
 * @example
 * extractRelativePath('/ui/dashboard', '/ui/') // returns '/dashboard'
 * extractRelativePath('/ui/analytics/species', '/ui/') // returns '/analytics/species'
 * extractRelativePath('/custom/path', '/ui/') // returns '/custom/path' (unchanged)
 * extractRelativePath('/ui/', '/ui/') // returns '/ui/' (unchanged when equal)
 */
export function extractRelativePath(fullPath: string, basePath: string): string {
  // Input validation: check for empty, undefined, or non-string inputs
  if (!fullPath || typeof fullPath !== 'string' || fullPath.trim() === '') {
    return typeof fullPath === 'string' ? fullPath : '';
  }

  if (!basePath || typeof basePath !== 'string' || basePath.trim() === '') {
    return fullPath;
  }

  // Return unchanged if fullPath doesn't start with basePath or if they're equal
  if (!fullPath.startsWith(basePath) || fullPath === basePath) {
    return fullPath;
  }

  // Extract the relative portion
  const relativePath = fullPath.substring(basePath.length);

  // Ensure it starts with '/'
  return relativePath.startsWith('/') ? relativePath : '/' + relativePath;
}

/**
 * Validates if a path is a relative URL (starts with '/' but not '//')
 *
 * @param path - The path to validate
 * @returns true if the path is a valid relative URL
 */
export function isRelativePath(path: string): boolean {
  // Input validation
  if (!path || typeof path !== 'string') {
    return false;
  }

  return path.startsWith('/') && !path.startsWith('//');
}

/**
 * Normalizes a path by ensuring it has proper leading/trailing slashes
 *
 * @param path - The path to normalize
 * @param addTrailingSlash - Whether to ensure a trailing slash
 * @returns The normalized path
 */
export function normalizePath(path: unknown, addTrailingSlash = false): string {
  // Input validation: check for undefined, null, or empty string
  if (path === undefined || path === null || path === '') {
    return '/';
  }

  // Convert to string
  const pathStr = String(path);

  // Ensure leading slash
  let normalized = pathStr.startsWith('/') ? pathStr : '/' + pathStr;

  // Handle trailing slash
  if (addTrailingSlash && !normalized.endsWith('/')) {
    normalized += '/';
  } else if (!addTrailingSlash && normalized.length > 1 && normalized.endsWith('/')) {
    // Only remove trailing slash if it's not a special case like '///'
    // For paths like '///', preserve the structure as it might be intentional
    if (!normalized.match(/^\/+$/)) {
      normalized = normalized.slice(0, -1);
    }
  }

  return normalized;
}

/**
 * Gets the base path prefix for the app (everything before /ui/).
 * Handles reverse proxy scenarios like Home Assistant Ingress.
 *
 * Uses a priority chain:
 * 1. Backend-provided value (set after config fetch via setBasePath())
 * 2. URL heuristic from pathname (used during bootstrapping)
 *
 * @returns The base path prefix (empty string if no prefix, or the prefix path)
 *
 * @example
 * // Direct access
 * // pathname: '/ui/dashboard'
 * getAppBasePath() // returns ''
 *
 * @example
 * // Home Assistant Ingress
 * // pathname: '/api/hassio_ingress/TOKEN/ui/dashboard'
 * getAppBasePath() // returns '/api/hassio_ingress/TOKEN'
 *
 * @example
 * // Custom proxy
 * // pathname: '/proxy/birdnet/ui/settings'
 * getAppBasePath() // returns '/proxy/birdnet'
 */
export function getAppBasePath(): string {
  if (typeof window === 'undefined') return '';

  // Prefer backend-provided value (set after config fetch)
  if (_cachedBasePath !== null) return _cachedBasePath;

  // Fallback: runtime detection from pathname (handles bootstrapping)
  const pathname = window.location.pathname;

  // Split pathname into segments and find the first exact 'ui' segment
  // This avoids false matches on paths like '/ui-proxy/...' or '/my-ui-service/...'
  // Using indexOf (first match) ensures we find the app boundary, not nested ui paths
  const segments = pathname.split('/').filter(Boolean);
  const uiIndex = segments.indexOf('ui');

  // If 'ui' segment not found or is the first segment, there's no prefix
  if (uiIndex <= 0) return '';

  // Return everything before the 'ui' segment
  return '/' + segments.slice(0, uiIndex).join('/');
}

/**
 * Builds a full URL path that works with any proxy configuration.
 * Automatically detects and prepends the app's base path (if behind a proxy).
 * Idempotent: if the path already starts with the base path, it won't be added again.
 *
 * Use this function instead of hardcoded paths like `/ui/detections/123`
 * to ensure URLs work correctly when accessed through reverse proxies.
 *
 * @param path - Relative path (e.g., '/api/v2/detections/123' or '/ui/dashboard')
 * @returns Full path including any proxy prefix
 *
 * @example
 * // Direct access (pathname: '/ui/dashboard')
 * buildAppUrl('/ui/detections/123?tab=review')
 * // returns '/ui/detections/123?tab=review'
 *
 * @example
 * // Home Assistant Ingress (pathname: '/api/hassio_ingress/TOKEN/ui/dashboard')
 * buildAppUrl('/ui/detections/123?tab=review')
 * // returns '/api/hassio_ingress/TOKEN/ui/detections/123?tab=review'
 *
 * @example
 * // Idempotent: already-prefixed path is not double-prefixed
 * // basePath = '/api/hassio_ingress/TOKEN'
 * buildAppUrl('/api/hassio_ingress/TOKEN/ui/dashboard')
 * // returns '/api/hassio_ingress/TOKEN/ui/dashboard' (unchanged)
 */
export function buildAppUrl(path: string): string {
  const basePath = getAppBasePath();

  // Validate input to prevent open redirect vulnerabilities
  // Only allow relative paths (starting with / but not //)
  if (!isRelativePath(path)) {
    logger.error('buildAppUrl was called with a non-relative path:', path);
    // Return safe fallback to prevent open redirect
    return basePath + '/ui/dashboard';
  }

  // Idempotent: if the path already starts with the base path on a segment
  // boundary, don't add it again. This prevents double-prefixing when
  // sub_filter has already rewritten the URL.
  if (basePath && path.startsWith(basePath)) {
    // Ensure it's a segment boundary (next char is '/' or end of path)
    if (path.length === basePath.length || path[basePath.length] === '/') {
      return path;
    }
  }

  return basePath + path;
}
