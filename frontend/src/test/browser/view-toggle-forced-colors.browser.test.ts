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
import { cdp, userEvent } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import ViewToggle from '$lib/desktop/components/ui/ViewToggle.svelte';

const CHECKED_SELECTOR = '[role="radio"][aria-checked="true"]';
const UNCHECKED_SELECTOR = '[role="radio"][aria-checked="false"]';

const backgroundOf = (element: Element): string => getComputedStyle(element).backgroundColor;
const colorOf = (element: Element): string => getComputedStyle(element).color;

/** Appends an element painted with system colours, to read what the browser resolves them to. */
function systemColorReference(): HTMLElement {
  const reference = document.createElement('div');
  reference.style.cssText =
    'forced-color-adjust: none; background-color: Highlight; color: HighlightText;';
  document.body.append(reference);
  return reference;
}

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
  // The reference is the system selection pair the checked button must paint.
  const reference = systemColorReference();
  try {
    await vi.waitFor(() => expect(backgroundOf(checked)).toBe(backgroundOf(reference)));
    expect(colorOf(checked)).toBe(colorOf(reference));
    expect(backgroundOf(unchecked)).not.toBe(backgroundOf(reference));
  } finally {
    reference.remove();
  }
});

it('draws the keyboard focus ring of the checked view in the selection text colour', async () => {
  await render(ViewToggle, { props: { view: 'table', onViewChange: vi.fn() } });
  const checked = document.querySelector(CHECKED_SELECTOR);
  if (!checked) throw new Error('view toggle radios not rendered');

  // Tab gives keyboard focus, so :focus-visible applies; the checked radio is the group's one Tab stop.
  await userEvent.keyboard('{Tab}');
  expect(document.activeElement).toBe(checked);

  // On the Highlight fill the shared ring's author colours would vanish; the ring must use the
  // selection text colour and drop the inner shadow band.
  const reference = systemColorReference();
  try {
    await vi.waitFor(() => expect(getComputedStyle(checked).outlineColor).toBe(colorOf(reference)));
    expect(getComputedStyle(checked).boxShadow).toBe('none');
  } finally {
    reference.remove();
  }
});
