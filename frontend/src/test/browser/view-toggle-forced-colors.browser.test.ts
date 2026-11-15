/**
 * Browser Mode Test: the checked view stays distinct in forced colours
 *
 * Forced colours replace every author colour with a system colour, so the checked button's
 * primary fill turns into the same page colour as its neighbour and the selected view cannot
 * be told apart. jsdom cannot evaluate the forced-colors media feature; this test emulates it
 * in a real browser and checks the computed fill.
 *
 * Usage:
 *   npx vitest run --config vitest.browser.config.ts src/test/browser/view-toggle-forced-colors.browser.test.ts
 */

import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cdp } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import ViewToggle from '$lib/desktop/components/ui/ViewToggle.svelte';

const CHECKED_SELECTOR = '[role="radio"][aria-checked="true"]';
const UNCHECKED_SELECTOR = '[role="radio"][aria-checked="false"]';

const backgroundOf = (element: Element): string => getComputedStyle(element).backgroundColor;

beforeEach(async () => {
  await cdp().send('Emulation.setEmulatedMedia', {
    features: [{ name: 'forced-colors', value: 'active' }],
  });
});

afterEach(async () => {
  await cdp().send('Emulation.setEmulatedMedia', { features: [] });
});

it('keeps the checked view distinct in forced colours', async () => {
  await render(ViewToggle, { props: { view: 'table', onViewChange: vi.fn() } });
  const checked = document.querySelector(CHECKED_SELECTOR);
  const unchecked = document.querySelector(UNCHECKED_SELECTOR);
  if (!checked || !unchecked) throw new Error('view toggle radios not rendered');

  // The unchecked button is transparent, so comparing the two alone would pass before the fix.
  // The reference is the system selection colour the checked button must paint.
  const reference = document.createElement('div');
  reference.style.cssText = 'forced-color-adjust: none; background-color: Highlight;';
  document.body.append(reference);
  try {
    await vi.waitFor(() => expect(backgroundOf(checked)).toBe(backgroundOf(reference)));
    expect(backgroundOf(unchecked)).not.toBe(backgroundOf(reference));
  } finally {
    reference.remove();
  }
});
