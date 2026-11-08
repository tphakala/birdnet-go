/**
 * Browser Mode Test: an active toolbar button keeps its active colours on hover
 *
 * jsdom does not compute cascade specificity, so only a real browser shows
 * whether the hover rule outranks the active rule and leaves white text on the
 * grey hover background.
 *
 * Usage:
 *   npm run test:browser
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { userEvent } from 'vitest/browser';

import AudioToolbar from '$lib/desktop/components/media/AudioToolbar.svelte';

const PRIMARY_COLOR = 'rgb(10, 20, 30)';
const HOVER_COLOR = 'rgb(200, 200, 200)';
const CLIP_DURATION_SECONDS = 29;

function toolbarProps(overrides: Record<string, unknown> = {}) {
  return {
    isPlaying: false,
    isLoading: false,
    currentTime: 0,
    duration: CLIP_DURATION_SECONDS,
    progress: 0,
    loop: false,
    onPlayPause: vi.fn(),
    onSeek: vi.fn(),
    onLoopToggle: vi.fn(),
    selectionStart: null,
    selectionEnd: null,
    hasSelection: false,
    gainDb: 0,
    denoise: '',
    normalize: false,
    isProcessing: false,
    isPlayingSelection: false,
    isExtracting: false,
    extractionError: null,
    onPlaySelection: vi.fn(),
    onStopSelection: vi.fn(),
    onSkipToSelection: vi.fn(),
    onClearSelection: vi.fn(),
    onGainChange: vi.fn(),
    onDenoiseChange: vi.fn(),
    onNormalizeToggle: vi.fn(),
    onExport: vi.fn(),
    ...overrides,
  };
}

function backgroundOf(element: Element): string {
  return getComputedStyle(element).backgroundColor;
}

describe('AudioToolbar hover styling in a real browser', () => {
  let noTransitions: HTMLStyleElement;

  beforeEach(() => {
    document.documentElement.style.setProperty('--color-primary', PRIMARY_COLOR);
    document.documentElement.style.setProperty('--color-base-300', HOVER_COLOR);
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
  });

  it('keeps an active toolbar button on its active colours while hovered', async () => {
    await render(AudioToolbar, { props: toolbarProps({ loop: true }) });

    const active = document.querySelector('[aria-pressed="true"]');
    if (!active) throw new Error('active toolbar button not rendered');
    await userEvent.hover(active);

    expect(backgroundOf(active)).toBe(PRIMARY_COLOR);
  });

  it('still greys an inactive toolbar button on hover', async () => {
    await render(AudioToolbar, { props: toolbarProps() });

    const inactive = document.querySelector('[aria-pressed="false"]:not(:disabled)');
    if (!inactive) throw new Error('inactive toolbar button not rendered');
    await userEvent.hover(inactive);

    expect(backgroundOf(inactive)).toBe(HOVER_COLOR);
  });
});
