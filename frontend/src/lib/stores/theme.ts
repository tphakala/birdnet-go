/**
 * theme.ts
 *
 * Theme store for dark/light mode. The first value comes from the shared resolver
 * (`$lib/utils/theme-resolver.js`, also inlined into the pre-paint script in index.html):
 * the stored choice, else the OS preference, else light.
 *
 * Persistence: the choice is saved to localStorage as a raw `light` or `dark` string.
 * Storage may be blocked, so every access is guarded and the theme still switches in memory.
 */
import { writable } from 'svelte/store';
import {
  PREFERS_DARK_QUERY,
  THEME_STORAGE_KEY,
  applyThemeAttributes,
  readStoredTheme,
  resolveTheme,
} from '$lib/utils/theme-resolver.js';

export type Theme = 'light' | 'dark';

// Create a custom theme store
function createThemeStore() {
  const { subscribe, set, update } = writable<Theme>(
    typeof window === 'undefined' ? 'light' : resolveTheme()
  );

  // Apply theme to the document
  const applyTheme = (theme: Theme) => {
    if (typeof window === 'undefined') return;
    applyThemeAttributes(theme);
  };

  // Save the theme as the user's choice
  const persistTheme = (theme: Theme) => {
    try {
      localStorage.setItem(THEME_STORAGE_KEY, theme);
    } catch {
      // Storage may be blocked or full; the theme still applies for this session
    }
  };

  // Set theme with side effects
  const setTheme = (theme: Theme) => {
    applyTheme(theme);
    persistTheme(theme);
    set(theme);
  };

  // Toggle theme
  const toggle = () => {
    update(currentTheme => {
      const newTheme = currentTheme === 'dark' ? 'light' : 'dark';
      applyTheme(newTheme);
      persistTheme(newTheme);
      return newTheme;
    });
  };

  // Initialize theme on mount
  const initialize = () => {
    const theme = resolveTheme();
    applyTheme(theme);
    persistTheme(theme);
    set(theme);

    // Listen for system theme changes
    // matchMedia is missing in some environments, which the types do not model
    // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
    if (window.matchMedia) {
      const mediaQuery = window.matchMedia(PREFERS_DARK_QUERY);
      const handleChange = (e: MediaQueryListEvent) => {
        if (readStoredTheme() === null) {
          // Only update if user hasn't set a preference
          const newTheme = e.matches ? 'dark' : 'light';
          setTheme(newTheme);
        }
      };

      mediaQuery.addEventListener('change', handleChange);
      return () => mediaQuery.removeEventListener('change', handleChange);
    }
  };

  return {
    subscribe,
    set: setTheme,
    toggle,
    initialize,
  };
}

export const theme = createThemeStore();
