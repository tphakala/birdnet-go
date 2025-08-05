/**
 * URL manipulation utility functions
 */

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
