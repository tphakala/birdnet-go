/**
 * Browser Mode Test: an active toolbar button gets its own hover colour
 *
 * jsdom does not compute cascade specificity, so only a real browser shows
 * whether the generic hover rule outranks the active rule and leaves white text
 * on the grey hover background. The active button must hover to the primary
 * hover colour instead.
 *
 * Usage:
 *   npm run test:browser
 */

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { userEvent } from 'vitest/browser';

import AudioToolbar from '$lib/desktop/components/media/AudioToolbar.svelte';
import { audioToolbarProps } from '../audioToolbarProps';

const PRIMARY_COLOR = 'rgb(10, 20, 30)';
const HOVER_COLOR = 'rgb(200, 200, 200)';
const ACTIVE_HOVER_COLOR = 'rgb(40, 50, 60)';

function backgroundOf(element: Element): string {
  return getComputedStyle(element).backgroundColor;
}

describe('AudioToolbar hover styling in a real browser', () => {
  let noTransitions: HTMLStyleElement;

  beforeEach(() => {
    document.documentElement.style.setProperty('--color-primary', PRIMARY_COLOR);
    document.documentElement.style.setProperty('--color-base-300', HOVER_COLOR);
    document.documentElement.style.setProperty('--color-primary-hover', ACTIVE_HOVER_COLOR);
    // The buttons transition all properties; without this a read right after the
    // hover sees a mid-transition value and the test can pass on the bug.
    noTransitions = document.createElement('style');
    noTransitions.textContent = '* { transition: none !important; }';
    document.head.appendChild(noTransitions);
  });

  afterEach(() => {
    noTransitions.remove();
    document.documentElement.style.removeProperty('--color-primary');
    document.documentElement.style.removeProperty('--color-base-300');
    document.documentElement.style.removeProperty('--color-primary-hover');
  });

  it('gives an active toolbar button its own hover colour, not the grey one', async () => {
    await render(AudioToolbar, { props: audioToolbarProps({ loop: true }) });

    const active = document.querySelector('[aria-pressed="true"]');
    if (!active) throw new Error('active toolbar button not rendered');
    expect(backgroundOf(active)).toBe(PRIMARY_COLOR);
    await userEvent.hover(active);

    // Hover must neither grey the active button (white text on grey was unreadable)
    // nor leave it with no hover feedback at all.
    expect(backgroundOf(active)).toBe(ACTIVE_HOVER_COLOR);
    expect(backgroundOf(active)).not.toBe(HOVER_COLOR);
    expect(getComputedStyle(active).borderColor).toBe(ACTIVE_HOVER_COLOR);
  });

  it('still greys an inactive toolbar button on hover', async () => {
    await render(AudioToolbar, { props: audioToolbarProps() });

    const inactive = document.querySelector('[aria-pressed="false"]:not(:disabled)');
    if (!inactive) throw new Error('inactive toolbar button not rendered');
    await userEvent.hover(inactive);

    expect(backgroundOf(inactive)).toBe(HOVER_COLOR);
  });
});
