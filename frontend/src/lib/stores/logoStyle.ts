/**
 * logoStyle.ts
 *
 * Stores user preference for logo display style (gradient vs solid).
 * Persisted in localStorage under 'logo-style'.
 */
import { writable } from 'svelte/store';

export type LogoStyle = 'gradient' | 'solid';

const STORAGE_KEY = 'logo-style';
const DEFAULT_STYLE: LogoStyle = 'gradient';

function getInitialStyle(): LogoStyle {
  if (typeof window === 'undefined') return DEFAULT_STYLE;
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === 'gradient' || stored === 'solid') return stored;
  } catch {
    // localStorage unavailable (private browsing, storage full)
  }
  return DEFAULT_STYLE;
}

function createLogoStyleStore() {
  const { subscribe, set } = writable<LogoStyle>(getInitialStyle());

  return {
    subscribe,
    setStyle(style: LogoStyle) {
      set(style);
      if (typeof window !== 'undefined') {
        try {
          localStorage.setItem(STORAGE_KEY, style);
        } catch {
          // localStorage unavailable (private browsing, storage full)
        }
      }
    },
  };
}

export const logoStyle = createLogoStyleStore();
