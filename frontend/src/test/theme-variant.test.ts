/**
 * The Tailwind `dark:` variant and the pre-paint theme script must follow the app theme
 * (`html[data-theme]`), the same source the theme store and the token blocks use.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { PREFERS_DARK_QUERY } from '$lib/utils/theme-resolver.js';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { indexHtmlScriptBlock } from './index-html';

const here = dirname(fileURLToPath(import.meta.url));
const stylesheet = readFileSync(join(here, '..', 'styles', 'tailwind.css'), 'utf8');

/** The theme block of the inline pre-paint script, with the shared resolver inlined. */
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
      matches: os === 'dark' && query === PREFERS_DARK_QUERY,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    })) as unknown as typeof window.matchMedia;
  }

  /**
   * Replaces matchMedia with a stub whose `matches` and change listeners move together through
   * the returned `setOs`. A partial stub of MediaQueryList, hence the cast.
   */
  function controllableOs(initial: 'dark' | 'light') {
    let dark = initial === 'dark';
    const listeners = new Set<(e: { matches: boolean }) => void>();
    window.matchMedia = (() => ({
      get matches() {
        return dark;
      },
      addEventListener: (_: string, fn: (e: { matches: boolean }) => void) => listeners.add(fn),
      removeEventListener: (_: string, fn: (e: { matches: boolean }) => void) =>
        listeners.delete(fn),
    })) as unknown as typeof window.matchMedia;
    return (value: 'dark' | 'light') => {
      dark = value === 'dark';
      listeners.forEach(fn => fn({ matches: dark }));
    };
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

      clearAttributes();
      new Function(prePaintScript)();
      expect(attributes()).toEqual([want, want]);

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

  it.each([
    ['throws', 'dark'],
    ['light', 'dark'],
    ['dark', 'light'],
  ] as const)(
    'with %s stored, an OS change to %s is followed only when no valid theme is stored',
    async (stored, change) => {
      setStorage(stored);
      const setOs = controllableOs(change === 'dark' ? 'light' : 'dark');
      const { theme } = await freshStore();
      theme.initialize();
      const before = get(theme);

      setOs(change);

      const followed = stored === 'throws';
      expect(get(theme)).toBe(followed ? change : before);
      expect(attributes()).toEqual([get(theme), get(theme)]);
    }
  );

  it('subscribers see the new theme only after the attributes changed', async () => {
    setStorage(null);
    const setOs = controllableOs('light');
    const { theme } = await freshStore();
    const seen: Array<[string, string | null]> = [];
    theme.subscribe(value => seen.push([value, root.getAttribute('data-theme')]));
    seen.length = 0;

    theme.toggle();
    theme.set('light');
    setOs('dark');
    localStorage.removeItem('theme');
    theme.initialize();
    setOs('light');

    expect(seen).toEqual([
      ['dark', 'dark'],
      ['light', 'light'],
      ['dark', 'dark'],
      ['light', 'light'],
    ]);
  });

  it('stub: OS changes are followed without being stored until a theme is chosen', async () => {
    setStorage(null);
    const setOs = controllableOs('light');
    const { theme } = await freshStore();

    // First visit: the OS decides and nothing is stored
    theme.initialize();
    expect(get(theme)).toBe('light');
    expect(attributes()).toEqual(['light', 'light']);
    expect(localStorage.getItem('theme')).toBeNull();

    // The OS flips while the page is open
    setOs('dark');
    expect(get(theme)).toBe('dark');
    expect(attributes()).toEqual(['dark', 'dark']);
    expect(localStorage.getItem('theme')).toBeNull();

    // A second mount changes nothing
    theme.initialize();
    expect(get(theme)).toBe('dark');
    expect(localStorage.getItem('theme')).toBeNull();

    // The user picks a theme; the OS then changes to the opposite of the choice
    theme.toggle();
    expect(localStorage.getItem('theme')).toBe('light');
    setOs('dark');
    expect(get(theme)).toBe('light');
    expect(attributes()).toEqual(['light', 'light']);

    // A reload with the OS still dark: the stored choice wins and keeps winning
    const reloaded = (await freshStore()).theme;
    reloaded.initialize();
    expect(get(reloaded)).toBe('light');
    expect(localStorage.getItem('theme')).toBe('light');
  });

  it('ignores a stored value that is not a theme and does not rewrite it', async () => {
    setStorage('garbage');
    const setOs = controllableOs('dark');
    const { theme } = await freshStore();

    theme.initialize();
    expect(get(theme)).toBe('dark');
    setOs('light');
    expect(get(theme)).toBe('light');
    expect(localStorage.getItem('theme')).toBe('garbage');
  });
});
