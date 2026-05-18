/**
 * logoStyle.ts
 *
 * Stores user preference for logo display style (gradient vs solid).
 * Persisted in localStorage under 'logo-style'.
 */
import { writable } from 'svelte/store';
import { getStoredValue, setStoredValue } from '$lib/utils/storage';

export type LogoStyle = 'gradient' | 'solid';

const STORAGE_KEY = 'logo-style';
const DEFAULT_STYLE: LogoStyle = 'gradient';

const isValidStyle = (v: unknown): v is LogoStyle =>
  typeof v === 'string' && ['gradient', 'solid'].includes(v);

function createLogoStyleStore() {
  const { subscribe, set } = writable<LogoStyle>(
    getStoredValue(STORAGE_KEY, DEFAULT_STYLE, isValidStyle)
  );

  return {
    subscribe,
    setStyle(style: LogoStyle) {
      set(style);
      setStoredValue(STORAGE_KEY, style);
    },
  };
}

export const logoStyle = createLogoStyleStore();
