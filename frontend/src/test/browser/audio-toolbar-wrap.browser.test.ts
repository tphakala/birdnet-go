/**
 * Browser Mode Test: the audio toolbar wraps inside a narrow host
 *
 * jsdom lays nothing out, so only a real browser shows whether the bar wraps
 * instead of running past its host, whether every control keeps a 44 px hit
 * area, and whether the Export menu is clamped into the viewport. The host is
 * 660 px wide, the bar's width beside an open sidebar on a 1024 px tablet.
 *
 * Global CSS is not loaded here and t() returns the message key, so the labels
 * are long keys; nothing below depends on the label text.
 *
 * Usage:
 *   npm run test:browser
 */

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';
import type { ComponentProps } from 'svelte';

import AudioToolbar from '$lib/desktop/components/media/AudioToolbar.svelte';
import { DENOISE_OPTIONS } from '$lib/utils/denoisePresets';
import { AUDIO_TOOLBAR_GLOBAL_CSS, audioToolbarProps } from '../audioToolbarProps';

const PAGE_WIDTH_PX = 1400;
const PAGE_HEIGHT_PX = 900;
const SHORT_PAGE_HEIGHT_PX = 400;
const HOST_WIDTH_PX = 660;
const TARGET_PX = 44;
const MIN_FONT_PX = 12;
// How far the Export button is pushed past the right edge of the viewport
const PAST_EDGE_PX = 20;

type ToolbarProps = ComponentProps<typeof AudioToolbar>;

function el<T extends Element = HTMLElement>(selector: string): T {
  const found = document.querySelector<T>(selector);
  if (!found) throw new Error(`${selector} not rendered`);
  return found;
}

function bar(): HTMLElement {
  return el('.audio-toolbar');
}

/** Right edge of the bar's content box, in viewport coordinates. */
function barContentRight(): number {
  const style = getComputedStyle(bar());
  return (
    bar().getBoundingClientRect().right -
    parseFloat(style.paddingRight) -
    parseFloat(style.borderRightWidth)
  );
}

function hitAreas(): Record<string, Element> {
  const buttons = Array.from(document.querySelectorAll('.audio-toolbar .toolbar-btn'));
  return {
    play: el('.play-btn'),
    skip: buttons[1],
    loop: buttons[2],
    clear: el('.selection-controls .toolbar-btn'),
    denoise: el('.denoise-dropdown > .toolbar-btn'),
    normalize: el('.processing-controls > .toolbar-btn'),
    export: el('.export-btn'),
    seek: el('.progress-bar'),
    gain: el('.gain-slider'),
  };
}

async function openExportMenu(): Promise<HTMLElement> {
  el('.export-btn').click();
  await expect.poll(() => document.querySelector('.export-menu')).not.toBeNull();
  const menu = el('.export-menu');
  // The enter transition scales the menu; measure once it has settled
  await expect.poll(() => menu.getAnimations().length).toBe(0);
  return menu;
}

async function closeExportMenu(): Promise<void> {
  el('.export-btn').click();
  await expect.poll(() => document.querySelector('.export-menu')).toBeNull();
}

describe('AudioToolbar wrapping in a real browser', () => {
  let host: HTMLDivElement;
  let style: HTMLStyleElement;

  beforeEach(async () => {
    await page.viewport(PAGE_WIDTH_PX, PAGE_HEIGHT_PX);
    style = document.createElement('style');
    style.textContent = `${AUDIO_TOOLBAR_GLOBAL_CSS} * { transition: none !important; }`;
    document.head.appendChild(style);
    host = document.createElement('div');
    host.style.width = `${HOST_WIDTH_PX}px`;
    document.body.appendChild(host);
  });

  afterEach(() => {
    host.remove();
    style.remove();
  });

  async function renderToolbar(overrides: Partial<ToolbarProps> = {}) {
    return render(
      AudioToolbar,
      {
        props: audioToolbarProps({
          selectionStart: 9,
          selectionEnd: 18,
          hasSelection: true,
          denoise: 'light-medium',
          ...overrides,
        }),
      },
      { baseElement: host }
    );
  }

  it('keeps every control inside a 660 px bar', async () => {
    await renderToolbar();

    const contentRight = barContentRight();
    const controls = [
      ...Object.values(hitAreas()),
      el('.playback-time'),
      el('.time-range'),
      el('.control-value'),
    ];
    for (const control of controls) {
      expect(control.getBoundingClientRect().right).toBeLessThanOrEqual(contentRight + 0.5);
    }
    expect(bar().scrollWidth).toBeLessThanOrEqual(bar().clientWidth);
  });

  it('gives every control a 44 px hit area', async () => {
    await renderToolbar();

    const tooSmall = Object.entries(hitAreas())
      .filter(([, control]) => {
        const rect = control.getBoundingClientRect();
        return rect.width < TARGET_PX || rect.height < TARGET_PX;
      })
      .map(([name]) => name);

    expect(tooSmall).toEqual([]);
  });

  it('uses text of at least 12 px', async () => {
    await renderToolbar();

    const tooSmall = [
      '.playback-time',
      '.time-range',
      '.control-value',
      '.denoise-label-current',
      '.export-btn',
    ].filter(selector => parseFloat(getComputedStyle(el(selector)).fontSize) < MIN_FONT_PX);

    expect(tooSmall).toEqual([]);
  });

  it('stretches the seek bar to the end of its row', async () => {
    await renderToolbar();

    expect(
      Math.abs(el('.progress-bar').getBoundingClientRect().right - barContentRight())
    ).toBeLessThanOrEqual(1);
  });

  it('keeps the layout still when the selection is cleared', async () => {
    const { rerender } = await renderToolbar();
    const gainBefore = el('.gain-slider').getBoundingClientRect().toJSON();
    const heightBefore = bar().getBoundingClientRect().height;

    await rerender({ hasSelection: false, selectionStart: null, selectionEnd: null });

    expect(el('.gain-slider').getBoundingClientRect().toJSON()).toEqual(gainBefore);
    expect(bar().getBoundingClientRect().height).toBe(heightBefore);
  });

  it('keeps the Export menu inside the viewport when its button is at the right edge', async () => {
    await renderToolbar();
    const awayFromEdge = (await openExportMenu()).getBoundingClientRect().width;
    await closeExportMenu();

    const exportRight = el('.export-btn').getBoundingClientRect().right;
    host.style.marginLeft = `${window.innerWidth - exportRight + PAST_EDGE_PX}px`;
    expect(el('.export-btn').getBoundingClientRect().right).toBeGreaterThan(window.innerWidth);
    const menu = (await openExportMenu()).getBoundingClientRect();

    expect(menu.left).toBeGreaterThanOrEqual(0);
    expect(menu.right).toBeLessThanOrEqual(window.innerWidth);
    expect(menu.width).toBe(awayFromEdge);
  });

  it('keeps a tall menu inside a short viewport', async () => {
    await page.viewport(PAGE_WIDTH_PX, SHORT_PAGE_HEIGHT_PX);
    await renderToolbar();
    // Centre the Export button vertically, so neither side has room for the whole menu
    const button = el('.export-btn').getBoundingClientRect();
    host.style.marginTop = `${window.innerHeight / 2 - (button.top + button.height / 2)}px`;

    const menu = await openExportMenu();
    const rect = menu.getBoundingClientRect();

    expect(rect.top).toBeGreaterThanOrEqual(0);
    expect(rect.bottom).toBeLessThanOrEqual(window.innerHeight);
    // Capped, so the options that do not fit scroll inside the menu
    expect(menu.scrollHeight).toBeGreaterThan(menu.clientHeight);
  });

  it("keeps the Denoise button's width when the preset changes", async () => {
    const { rerender } = await renderToolbar({ denoise: '' });
    const trigger = el('.denoise-dropdown > .toolbar-btn');
    const widths = new Set<number>();

    for (const option of DENOISE_OPTIONS) {
      await rerender({ denoise: option.id });
      widths.add(trigger.getBoundingClientRect().width);
    }

    expect(widths.size).toBe(1);
  });
});
