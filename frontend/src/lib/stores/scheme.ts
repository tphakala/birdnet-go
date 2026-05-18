/**
 * scheme.ts
 *
 * Color scheme management store. Handles scheme selection,
 * custom color persistence, and DOM attribute updates.
 * Works alongside the theme store (light/dark) independently.
 */
import { writable } from 'svelte/store';
import { getStoredValue, setStoredValue } from '$lib/utils/storage';

export type SchemeId = 'blue' | 'forest' | 'amber' | 'violet' | 'rose' | 'custom';

export interface CustomColors {
  primary: string;
  accent: string;
}

const STORAGE_KEY = 'color-scheme';
const CUSTOM_COLORS_KEY = 'custom-scheme-colors';

const DEFAULT_SCHEME: SchemeId = 'blue';
const DEFAULT_CUSTOM: CustomColors = { primary: '#2563eb', accent: '#0284c7' };

const VALID_SCHEMES: ReadonlyArray<string> = [
  'blue',
  'forest',
  'amber',
  'violet',
  'rose',
  'custom',
];

function isValidScheme(value: unknown): value is SchemeId {
  return typeof value === 'string' && VALID_SCHEMES.includes(value);
}

function isValidCustomColors(v: unknown): v is CustomColors {
  return (
    typeof v === 'object' &&
    v !== null &&
    'primary' in v &&
    'accent' in v &&
    typeof (v as CustomColors).primary === 'string' &&
    typeof (v as CustomColors).accent === 'string'
  );
}

function getInitialScheme(): SchemeId {
  return getStoredValue(STORAGE_KEY, DEFAULT_SCHEME, isValidScheme);
}

function getInitialCustomColors(): CustomColors {
  return getStoredValue(CUSTOM_COLORS_KEY, DEFAULT_CUSTOM, isValidCustomColors);
}

/**
 * Calculate WCAG relative luminance to determine if text should be black or white.
 * Uses proper sRGB linearization per WCAG 2.1 contrast ratio spec.
 */
function sRGBtoLinear(c: number): number {
  const s = c / 255;
  return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
}

function getContrastColor(hex: string): string {
  const num = parseInt(hex.replace('#', ''), 16);
  const r = (num >> 16) & 0xff;
  const g = (num >> 8) & 0xff;
  const b = num & 0xff;
  const luminance = 0.2126 * sRGBtoLinear(r) + 0.7152 * sRGBtoLinear(g) + 0.0722 * sRGBtoLinear(b);
  return luminance > 0.179 ? '#020617' : '#ffffff';
}

function applyScheme(scheme: SchemeId): void {
  if (typeof window === 'undefined') return;
  document.documentElement.setAttribute('data-scheme', scheme);
  setStoredValue(STORAGE_KEY, scheme);
}

function applyCustomColors(colors: CustomColors): void {
  if (typeof window === 'undefined') return;
  const root = document.documentElement.style;
  root.setProperty('--custom-primary', colors.primary);
  root.setProperty('--custom-primary-content', getContrastColor(colors.primary));
  root.setProperty('--custom-accent', colors.accent);
  root.setProperty('--custom-accent-content', getContrastColor(colors.accent));
  setStoredValue(CUSTOM_COLORS_KEY, colors);
}

function createSchemeStore() {
  const { subscribe, set } = writable<SchemeId>(getInitialScheme());
  const customColors = writable<CustomColors>(getInitialCustomColors());

  return {
    subscribe,
    customColors,

    setScheme(scheme: SchemeId) {
      set(scheme);
      applyScheme(scheme);
      if (scheme === 'custom') {
        const colors = getInitialCustomColors();
        customColors.set(colors);
        applyCustomColors(colors);
      }
    },

    setCustomColors(colors: CustomColors) {
      customColors.set(colors);
      applyCustomColors(colors);
    },

    initialize() {
      const scheme = getInitialScheme();
      applyScheme(scheme);
      if (scheme === 'custom') {
        const colors = getInitialCustomColors();
        customColors.set(colors);
        applyCustomColors(colors);
      }
    },

    /** Apply server-configured scheme (overrides localStorage for visitors) */
    applyServerScheme(serverScheme: string, serverCustomColors?: CustomColors) {
      if (!serverScheme || !isValidScheme(serverScheme)) return;
      set(serverScheme);
      applyScheme(serverScheme);
      if (serverScheme === 'custom' && serverCustomColors) {
        customColors.set(serverCustomColors);
        applyCustomColors(serverCustomColors);
      }
    },
  };
}

export const scheme = createSchemeStore();
