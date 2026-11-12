/**
 * The Tailwind `dark:` variant and the pre-paint theme script must follow the app theme
 * (`html[data-theme]`), the same source the theme store and the token blocks use.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
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

describe('index.html pre-paint theme script', () => {
  const originalMatchMedia = window.matchMedia;
  const root = document.documentElement;

  afterEach(() => {
    vi.restoreAllMocks();
    window.matchMedia = originalMatchMedia;
    localStorage.removeItem('theme');
    root.setAttribute('data-theme', 'light');
    root.removeAttribute('data-theme-controller');
  });

  /** Replaces matchMedia with a stub that reads only `matches` and `media`, hence the cast. */
  function stubMatchMedia(osDark: boolean) {
    window.matchMedia = ((query: string) => ({
      matches: osDark && query === '(prefers-color-scheme: dark)',
      media: query,
    })) as unknown as typeof window.matchMedia;
  }

  /** Runs the script from a clean root and returns the attributes it set. */
  function attributesAfterRunning() {
    root.removeAttribute('data-theme');
    root.removeAttribute('data-theme-controller');
    new Function(prePaintScript)();
    return [root.getAttribute('data-theme'), root.getAttribute('data-theme-controller')];
  }

  function themeAfterRunning(stored: string | null, osDark: boolean): string | null {
    if (stored === null) localStorage.removeItem('theme');
    else localStorage.setItem('theme', stored);
    stubMatchMedia(osDark);
    const [theme, controller] = attributesAfterRunning();
    expect(controller).toBe(theme);
    return theme;
  }

  it('is found in index.html', () => {
    expect(prePaintScript).not.toBe('');
  });

  it.each([
    ['dark', false, 'dark'],
    ['light', true, 'light'],
    [null, true, 'dark'],
    [null, false, 'light'],
    ['garbage', true, 'dark'],
    ['garbage', false, 'light'],
  ] as const)('stored %s with OS dark %s gives %s, as the store would', (stored, osDark, want) => {
    expect(themeAfterRunning(stored, osDark)).toBe(want);
  });

  it('gives light when matchMedia is unavailable and nothing is stored', () => {
    localStorage.removeItem('theme');
    // Simulates an environment without matchMedia
    window.matchMedia = undefined as unknown as typeof window.matchMedia;
    expect(attributesAfterRunning()).toEqual(['light', 'light']);
  });

  it('gives light when reading the stored theme throws, even if the OS prefers dark', () => {
    stubMatchMedia(true);
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage blocked');
    });
    expect(attributesAfterRunning()).toEqual(['light', 'light']);
  });
});
