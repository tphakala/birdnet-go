/**
 * The bare settings path has no page of its own: it shows the default section
 * (General) under the URL the visitor used, without a redirect. The page title
 * and the sidebar share this alias.
 *
 * Paths are built with join so they stay immune to proxy sub_filter rewriting
 * of quoted '/ui/...' strings.
 */
export const SETTINGS_BASE_PATH = ['', 'ui', 'settings'].join('/');
const DEFAULT_SECTION = 'main';

/** Canonical path of the section shown for the bare settings path. */
export const SETTINGS_DEFAULT_PATH = [SETTINGS_BASE_PATH, DEFAULT_SECTION].join('/');

/**
 * Maps the bare settings path to the default section path and returns every
 * other path unchanged. Expects a canonical path (no trailing slash, query or
 * hash), such as navigation.currentPath.
 */
export function resolveSettingsAlias(path: string): string {
  return path === SETTINGS_BASE_PATH ? SETTINGS_DEFAULT_PATH : path;
}
