/**
 * theme.ts
 *
 * Theme store for dark/light mode. The first value comes from the shared resolver
 * (`$lib/utils/theme-resolver.js`, also inlined into the pre-paint script in index.html):
 * the stored choice, else the OS preference, else light.
 *
 * Persistence: only an explicit choice (`set` and `toggle`) is saved to localStorage, as a raw
 * `light` or `dark` string. Until then the theme follows the OS, at load and when it changes.
 * Storage may be blocked, so every access is guarded and the theme still switches in memory.
 * A choice that storage cannot keep (blocked or full) is held in memory instead, so it still
 * outlasts OS changes and a second `initialize` for the rest of the session.
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

  // The user's choice when storage did not keep it (blocked or full); null once storage holds it
  let chosen: Theme | null = null;

  // Save the theme as the user's choice
  const persistTheme = (theme: Theme) => {
    try {
      localStorage.setItem(THEME_STORAGE_KEY, theme);
      chosen = null;
    } catch {
      // Storage may be blocked or full; keep the choice in memory for this session
      chosen = theme;
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
    const theme = chosen ?? resolveTheme();
    applyTheme(theme);
    set(theme);

    // Listen for system theme changes
    // matchMedia is missing in some environments, which the types do not model
    // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
    if (window.matchMedia) {
      const mediaQuery = window.matchMedia(PREFERS_DARK_QUERY);
      const handleChange = (e: MediaQueryListEvent) => {
        if (chosen === null && readStoredTheme() === null) {
          // No explicit choice yet: follow the OS without saving it as a choice
          const newTheme = e.matches ? 'dark' : 'light';
          applyTheme(newTheme);
          set(newTheme);
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
