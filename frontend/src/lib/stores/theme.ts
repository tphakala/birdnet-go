/**
 * theme.ts
 * 
 * Theme management store for application-wide dark/light mode control.
 * Handles theme persistence, system preference detection, and DOM updates.
 * 
 * Usage:
 * - Theme toggle components in headers/settings
 * - Application initialization for theme setup
 * - System preference synchronization
 * - Theme persistence across sessions
 * 
 * Features:
 * - Light/dark theme switching
 * - System preference detection and following
 * - localStorage persistence
 * - Automatic DOM attribute updates
 * - Real-time theme change listeners
 * - SSR-safe initialization
 * 
 * Theme Integration:
 * - Works with DaisyUI theme system
 * - Updates CSS custom properties
 * - Manages data-theme attributes
 * - Coordinates with Tailwind dark mode
 * 
 * System Integration:
 * - Detects prefers-color-scheme media query
 * - Follows system theme changes automatically
 * - Respects user preference override
 * - Graceful fallback to light theme
 * 
 * State Management:
 * - Reactive theme store
 * - Toggle functionality
 * - Initialization helpers
 * - Change event handling
 * 
 * Persistence:
 * - localStorage for user preferences
 * - DOM attribute synchronization
 * - Session restoration
 */
/* eslint-disable @typescript-eslint/no-unnecessary-condition */
import { writable } from 'svelte/store';

export type Theme = 'light' | 'dark';

// Create a custom theme store
function createThemeStore() {
  // Get initial theme from localStorage or default to light
  const getInitialTheme = (): Theme => {
    if (typeof window === 'undefined') return 'light';

    const stored = localStorage.getItem('theme');
    if (stored === 'dark' || stored === 'light') {
      return stored;
    }

    // Check system preference
    if (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) {
      return 'dark';
    }

    return 'light';
  };

  const { subscribe, set, update } = writable<Theme>(getInitialTheme());

  // Apply theme to document
  const applyTheme = (theme: Theme) => {
    if (typeof window === 'undefined') return;

    document.documentElement.setAttribute('data-theme', theme);
    document.documentElement.setAttribute('data-theme-controller', theme);
    localStorage.setItem('theme', theme);
  };

  // Set theme with side effects
  const setTheme = (theme: Theme) => {
    set(theme);
    applyTheme(theme);
  };

  // Toggle theme
  const toggle = () => {
    update(currentTheme => {
      const newTheme = currentTheme === 'dark' ? 'light' : 'dark';
      applyTheme(newTheme);
      return newTheme;
    });
  };

  // Initialize theme on mount
  const initialize = () => {
    const theme = getInitialTheme();
    applyTheme(theme);
    set(theme);

    // Listen for system theme changes
    if (window.matchMedia) {
      const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
      const handleChange = (e: MediaQueryListEvent) => {
        if (!localStorage.getItem('theme')) {
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
