/**
 * Browser Mode Test: the audio toolbar wraps inside a narrow host
 *
 * jsdom lays nothing out, so only a real browser shows whether the bar wraps
 * instead of running past its host, whether every control keeps its hit
 * area (32 px for a mouse, 44 px for touch), and whether the Export menu is clamped into the viewport. The host is
 * 660 px wide, the bar's width beside an open sidebar on a 1024 px tablet; the
 * 200 percent text case uses a wider host.
 *
 * Global CSS is not loaded here and t() returns the message key, so the labels
 * are long keys. No assertion reads label text, but the 200 percent host width
 * is chosen so the doubled keys fit, so longer keys can make that case fail.
 *
 * Usage:
 *   npm run test:browser
 */

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { cdp, page } from 'vitest/browser';
import type { ComponentProps } from 'svelte';

import AudioToolbar from '$lib/desktop/components/media/AudioToolbar.svelte';
import { DENOISE_OPTIONS } from '$lib/utils/denoisePresets';
import { audioToolbarProps } from '../audioToolbarProps';
import { injectTestStyles } from './injectTestStyles';

const PAGE_WIDTH_PX = 1400;
const PAGE_HEIGHT_PX = 900;
const SHORT_PAGE_HEIGHT_PX = 400;
const HOST_WIDTH_PX = 660;
// Wide enough for the long message keys the labels show here once doubled
const ZOOMED_HOST_WIDTH_PX = 900;
// Hit-area floor for a fine pointer (mouse, trackpad) and a coarse one (touch)
const FINE_TARGET_PX = 32;
const COARSE_TARGET_PX = 44;
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

/** Names of the controls and text that end past the bar's content box. */
function controlsPastBar(): string[] {
  const contentRight = barContentRight();
  const controls: Record<string, Element> = {
    ...hitAreas(),
    time: el('.playback-time'),
    range: el('.time-range'),
    gainValue: el('.control-value'),
  };
  return Object.entries(controls)
    .filter(([, control]) => control.getBoundingClientRect().right > contentRight + 0.5)
    .map(([name]) => name);
}

function hitAreas(): Record<string, Element> {
  return {
    play: el('.play-btn'),
    skip: el('[aria-label="components.audioPlayer.processing.skipToStart"]'),
    loop: el('[aria-label="media.audio.loop"]'),
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
    style = injectTestStyles();
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

    expect(controlsPastBar()).toEqual([]);
    expect(bar().scrollWidth).toBeLessThanOrEqual(bar().clientWidth);
  });

  it.each([
    ['a mouse', false, FINE_TARGET_PX],
    ['touch', true, COARSE_TARGET_PX],
  ] as const)('sizes every control for %s', async (_pointer, touch, target) => {
    // Touch emulation is what makes (pointer: coarse) match in Chromium
    const session = cdp();
    await session.send('Emulation.setTouchEmulationEnabled', { enabled: touch });
    try {
      expect(window.matchMedia('(pointer: coarse)').matches).toBe(touch);
      await renderToolbar();

      const tooSmall = Object.entries(hitAreas())
        .filter(([, control]) => {
          const rect = control.getBoundingClientRect();
          return rect.width < target || rect.height < target;
        })
        .map(([name]) => name);

      expect(tooSmall).toEqual([]);
      // An icon button is exactly the floor, so the mouse size stays compact
      const play = el('.play-btn').getBoundingClientRect();
      expect([play.width, play.height]).toEqual([target, target]);
    } finally {
      await session.send('Emulation.setTouchEmulationEnabled', { enabled: false });
    }
  });

  it('uses text of at least 12 px', async () => {
    await renderToolbar();
    const tooSmall = (selectors: string[]) =>
      selectors.filter(
        selector => parseFloat(getComputedStyle(el(selector)).fontSize) < MIN_FONT_PX
      );

    const inBar = tooSmall([
      '.playback-time',
      '.time-range',
      '.control-value',
      '.denoise-label-current',
      '.export-btn',
    ]);
    await openExportMenu();
    const inExportMenu = tooSmall(['.export-option']);
    await closeExportMenu();
    el('.denoise-dropdown > .toolbar-btn').click();
    await expect.poll(() => document.querySelector('.denoise-menu')).not.toBeNull();
    const inDenoiseMenu = tooSmall(['.denoise-option']);

    expect([...inBar, ...inExportMenu, ...inDenoiseMenu]).toEqual([]);
  });

  it('keeps every control inside the bar at 200 percent text', async () => {
    host.style.width = `${ZOOMED_HOST_WIDTH_PX}px`;
    document.documentElement.style.fontSize = '200%';
    try {
      await renderToolbar();

      expect(controlsPastBar()).toEqual([]);
      expect(bar().scrollWidth).toBeLessThanOrEqual(bar().clientWidth);
    } finally {
      document.documentElement.style.removeProperty('font-size');
    }
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
