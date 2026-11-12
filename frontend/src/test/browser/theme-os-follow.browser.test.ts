/**
 * Browser Mode Test: the theme store follows real prefers-color-scheme change events until the
 * user picks a theme. jsdom has no matchMedia, so the unit test runs against a hand-written stub;
 * this one proves the listener is wired to a real MediaQueryList.
 */
import { afterEach, expect, it } from 'vitest';
import { cdp } from 'vitest/browser';
import { get } from 'svelte/store';

import { PREFERS_DARK_QUERY } from '$lib/utils/theme-resolver.js';
import { theme } from '$lib/stores/theme';

type Scheme = 'dark' | 'light';

/** Emulates the OS colour scheme and resolves once a MediaQueryList has reported the change. */
async function emulate(value: Scheme) {
  const list = window.matchMedia(PREFERS_DARK_QUERY);
  // The emulated value only fires an event when it differs from the current one
  if (list.matches === (value === 'dark')) return;
  const seen = new Promise<void>(resolve =>
    list.addEventListener('change', () => resolve(), { once: true })
  );
  await cdp().send('Emulation.setEmulatedMedia', {
    features: [{ name: 'prefers-color-scheme', value }],
  });
  await seen;
}

let cleanup: (() => void) | undefined;

afterEach(async () => {
  cleanup?.();
  cleanup = undefined;
  localStorage.removeItem('theme');
  await cdp().send('Emulation.setEmulatedMedia', { features: [] });
});

it('real OS changes are followed until a theme is chosen', async () => {
  localStorage.removeItem('theme');
  await emulate('light');
  cleanup = theme.initialize();
  expect(get(theme)).toBe('light');

  await emulate('dark');
  expect(get(theme)).toBe('dark');
  expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
  expect(localStorage.getItem('theme')).toBeNull();

  theme.toggle();
  expect(get(theme)).toBe('light');

  // The last emulated scheme is the opposite of the chosen theme
  await emulate('light');
  await emulate('dark');
  expect(get(theme)).toBe('light');
  expect(localStorage.getItem('theme')).toBe('light');
});
