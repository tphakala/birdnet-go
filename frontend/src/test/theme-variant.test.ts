/**
 * The Tailwind `dark:` variant and the pre-paint theme script must follow the app theme
 * (`html[data-theme]`), the same source the theme store and the token blocks use.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { indexHtmlScriptBlock } from './index-html';

const here = dirname(fileURLToPath(import.meta.url));
const stylesheet = readFileSync(join(here, '..', 'styles', 'tailwind.css'), 'utf8');

/** The theme block of the inline pre-paint script (the tag also holds other init code). */
const prePaintScript = indexHtmlScriptBlock('// Immediately set the theme');

describe('dark variant', () => {
  const declaration = stylesheet.match(/^@custom-variant dark \(&(.*)\);$/m);

  afterEach(() => {
    document.documentElement.setAttribute('data-theme', 'light');
  });

  it('tailwind.css declares the dark variant', () => {
    expect(declaration).not.toBeNull();
  });

  it.each([
    ['dark', true],
    ['light', false],
  ] as const)(
    'with data-theme %s the selector matches html and a descendant: %s',
    (theme, want) => {
      const selector = declaration?.[1] ?? '';
      const child = document.body.appendChild(document.createElement('div'));
      document.documentElement.setAttribute('data-theme', theme);
      try {
        expect(document.documentElement.matches(selector)).toBe(want);
        expect(child.matches(selector)).toBe(want);
      } finally {
        child.remove();
      }
    }
  );
});

describe('theme resolution: pre-paint script and store', () => {
  const originalMatchMedia = window.matchMedia;
  const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  const root = document.documentElement;

  type Stored = string | null | 'throws';
  type Os = 'dark' | 'light' | 'unavailable';

  afterEach(() => {
    vi.restoreAllMocks();
    // Restore storage first: removeItem below needs the real one
    if (originalStorage) Object.defineProperty(globalThis, 'localStorage', originalStorage);
    window.matchMedia = originalMatchMedia;
    localStorage.removeItem('theme');
    root.setAttribute('data-theme', 'light');
    root.removeAttribute('data-theme-controller');
  });

  /** Replaces matchMedia with a stub that reads only `matches` and `media`, hence the cast. */
  function stubMatchMedia(os: Os) {
    if (os === 'unavailable') {
      window.matchMedia = undefined as unknown as typeof window.matchMedia;
      return;
    }
    window.matchMedia = ((query: string) => ({
      matches: os === 'dark' && query === '(prefers-color-scheme: dark)',
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    })) as unknown as typeof window.matchMedia;
  }

  /** Puts `stored` in storage, or makes every access to localStorage throw. */
  function setStorage(stored: Stored) {
    if (stored === 'throws') {
      Object.defineProperty(globalThis, 'localStorage', {
        configurable: true,
        get() {
          throw new Error('storage blocked');
        },
      });
    } else if (stored === null) {
      localStorage.removeItem('theme');
    } else {
      localStorage.setItem('theme', stored);
    }
  }

  function clearAttributes() {
    root.removeAttribute('data-theme');
    root.removeAttribute('data-theme-controller');
  }

  function attributes() {
    return [root.getAttribute('data-theme'), root.getAttribute('data-theme-controller')];
  }

  /** Runs the script from a clean root and returns the attributes it set. */
  function attributesAfterRunning() {
    clearAttributes();
    new Function(prePaintScript)();
    return attributes();
  }

  /** Imports a fresh store, so its import-time resolution runs against the current inputs. */
  async function freshStore() {
    vi.resetModules();
    return import('$lib/stores/theme');
  }

  it('is found in index.html', () => {
    expect(prePaintScript).not.toBe('');
  });

  it.each([
    ['dark', 'light', 'dark'],
    ['light', 'dark', 'light'],
    [null, 'dark', 'dark'],
    [null, 'light', 'light'],
    ['garbage', 'dark', 'dark'],
    ['garbage', 'light', 'light'],
    [null, 'unavailable', 'light'],
    ['dark', 'unavailable', 'dark'],
    ['throws', 'dark', 'dark'],
    ['throws', 'light', 'light'],
    ['throws', 'unavailable', 'light'],
  ] as const)(
    'stored %s with OS %s gives %s in the script and the store',
    async (stored, os, want) => {
      setStorage(stored);
      stubMatchMedia(os);

      expect(attributesAfterRunning()).toEqual([want, want]);

      const { theme } = await freshStore();
      expect(get(theme)).toBe(want);
      clearAttributes();
      theme.initialize()?.();
      expect(attributes()).toEqual([want, want]);
    }
  );

  it('the store still switches theme when storage is blocked', async () => {
    setStorage('throws');
    stubMatchMedia('light');
    const { theme } = await freshStore();

    theme.toggle();
    expect(get(theme)).toBe('dark');
    expect(attributes()).toEqual(['dark', 'dark']);

    theme.set('light');
    expect(get(theme)).toBe('light');
    expect(attributes()).toEqual(['light', 'light']);
  });

  it('a chosen theme is stored as a raw string and wins on the next load', async () => {
    setStorage(null);
    stubMatchMedia('light');
    const { theme } = await freshStore();

    theme.toggle();
    expect(localStorage.getItem('theme')).toBe('dark');

    const next = await freshStore();
    expect(get(next.theme)).toBe('dark');

    next.theme.set('light');
    expect(localStorage.getItem('theme')).toBe('light');
  });

  it('initialize resolves again, so a change between module load and mount is applied', async () => {
    setStorage(null);
    stubMatchMedia('light');
    const { theme } = await freshStore();
    expect(get(theme)).toBe('light');

    stubMatchMedia('dark');
    clearAttributes();
    theme.initialize()?.();
    expect(get(theme)).toBe('dark');
    expect(attributes()).toEqual(['dark', 'dark']);
  });
});
