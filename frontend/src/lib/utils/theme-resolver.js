/**
 * Theme resolution shared by the theme store and the pre-paint script in index.html.
 *
 * The text of this file is inlined verbatim into a classic inline script (see
 * theme-prepaint.js), so it must stay a plain script: no imports, no top-level side effects,
 * only top-level `export ` declarations starting at column 0, and none of the text that ends a
 * test block or a script (an IIFE close, a script end tag). Syntax that every current browser parses without transpiling is fine.
 *
 * The stored value is a raw string, not JSON, so it is read here with `localStorage` directly
 * and not through `$lib/utils/storage`.
 */

/** localStorage key holding an explicit theme choice, as the raw string `light` or `dark`. */
export const THEME_STORAGE_KEY = 'theme';

/** Media query that matches when the operating system prefers a dark colour scheme. */
export const PREFERS_DARK_QUERY = '(prefers-color-scheme: dark)';

/**
 * Reads the explicit theme choice. Never throws.
 * @returns {'light' | 'dark' | null} The stored theme, or null when none is stored, the value
 *   is not a valid theme, or storage is blocked.
 */
export function readStoredTheme() {
  let stored = null;
  try {
    stored = localStorage.getItem(THEME_STORAGE_KEY);
  } catch {
    // Storage can be blocked (for example Safari private browsing), even to touch
  }
  return stored === 'dark' || stored === 'light' ? stored : null;
}

/**
 * Resolves the theme to show: the stored choice, else the OS preference, else light.
 * Never throws.
 * @returns {'light' | 'dark'} The resolved theme.
 */
export function resolveTheme() {
  const stored = readStoredTheme();
  if (stored) return stored;
  try {
    if (window.matchMedia(PREFERS_DARK_QUERY).matches) return 'dark';
  } catch {
    // matchMedia is unavailable
  }
  return 'light';
}

/**
 * Writes the theme to the attributes that CSS and charts read from the document element.
 * @param {'light' | 'dark'} theme The theme to apply.
 * @returns {void}
 */
export function applyThemeAttributes(theme) {
  document.documentElement.setAttribute('data-theme', theme);
  document.documentElement.setAttribute('data-theme-controller', theme);
}
