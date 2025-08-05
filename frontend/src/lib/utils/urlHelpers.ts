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
  return path.startsWith('/') && !path.startsWith('//');
}

/**
 * Normalizes a path by ensuring it has proper leading/trailing slashes
 *
 * @param path - The path to normalize
 * @param addTrailingSlash - Whether to ensure a trailing slash
 * @returns The normalized path
 */
export function normalizePath(path: string, addTrailingSlash = false): string {
  // Ensure leading slash
  let normalized = path.startsWith('/') ? path : '/' + path;

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
