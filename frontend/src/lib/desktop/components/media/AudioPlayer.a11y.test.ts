/**
 * Keyboard and screen reader behaviour of the large AudioPlayer: seeking from the
 * toolbar slider and from the inline progress bar (a slider as well) by key, and
 * showing the overlay controls on keyboard focus or while a popover is open. Uses the
 * fake media element.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, fireEvent, cleanup, render } from '@testing-library/svelte';
import {
  installFakeMediaElement,
  fakeAudioNodeChain,
  settle,
  FAKE_MEDIA_DURATION_SECONDS,
  type FakeMediaController,
} from '../../../../test/fakeMediaElement';
import { attachAudioGraphWhenRunning } from '$lib/utils/audioNodes';
import AudioPlayer from './AudioPlayer.svelte';
import { SEEK_PAGE_STEP_SECONDS, SEEK_STEP_SECONDS } from '$lib/utils/seek';

vi.mock('$lib/utils/audioContextManager', () => ({
  getAudioContext: vi.fn().mockResolvedValue({ state: 'running' }),
  isAudioContextSupported: vi.fn().mockReturnValue(true),
  releaseAudioContext: vi.fn(),
}));

vi.mock('$lib/utils/audioNodes', () => ({
  attachAudioGraphWhenRunning: vi.fn(),
  disconnectAudioNodes: vi.fn(),
}));

const DETECTION_ID = '4242';
const SEEK_LABEL = 'media.audio.seek';
const SETTINGS_LABEL = 'media.audio.settings';
const BATS_LABEL = 'media.audio.audibleBats.title';
const OVERLAY_TEST_ID = 'audio-overlay-controls';
const HIDDEN_CLASS = 'opacity-0';
const FOCUS_VISIBLE_CLASS = 'has-[:focus-visible]:opacity-100';
// The old reveal matched any focus, so a mouse click left the controls on screen.
const ANY_FOCUS_CLASS = 'focus-within:opacity-100';
// offsetWidth the player container reports, wide enough to show every control
const WIDE_CONTAINER_PX = 400;
// Width the inline progress bar's rect reports in the click tests
const PROGRESS_BAR_WIDTH_PX = 400;
// A position that is neither the start nor the end of the fake clip
const INITIAL_POSITION_SECONDS = 7;
// How far past the end of the fake clip a stale position is in the clamping test
const PAST_END_SECONDS = 30;

describe('AudioPlayer Accessibility', () => {
  let ctl: FakeMediaController;
  const originalFetch = globalThis.fetch;
  const originalOffsetWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth');
  const originalTouchStart = Object.getOwnPropertyDescriptor(window, 'ontouchstart');

  function mount(props: Record<string, unknown> = {}) {
    return render(AudioPlayer, {
      props: {
        audioUrl: `/api/v2/audio/${DETECTION_ID}`,
        detectionId: DETECTION_ID,
        enableClipExtraction: true,
        showSpectrogram: false,
        ...props,
      },
    });
  }

  function inlineBar(container: HTMLElement): HTMLElement {
    const bar = container.querySelector<HTMLElement>('[role="slider"][id^="progress"]');
    if (!bar) throw new Error('inline progress bar not rendered');
    return bar;
  }

  /** Replace the element's currentTime with a getter that reads 0 and a spy for writes. */
  function spyOnCurrentTimeWrites() {
    const setCurrentTime = vi.fn();
    Object.defineProperty(ctl.element, 'currentTime', {
      configurable: true,
      get: () => 0,
      set: setCurrentTime,
    });
    return setCurrentTime;
  }

  function seekSlider(): HTMLElement {
    return screen.getByRole('slider', { name: SEEK_LABEL });
  }

  function overlayControls(): HTMLElement {
    return screen.getByTestId(OVERLAY_TEST_ID);
  }
  beforeEach(() => {
    vi.useFakeTimers();
    // jsdom reports touch support, which makes the player skip the hover-only fade.
    Reflect.deleteProperty(window, 'ontouchstart');
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
      configurable: true,
      get: () => WIDE_CONTAINER_PX,
    });
    ctl = installFakeMediaElement();
    vi.mocked(attachAudioGraphWhenRunning).mockReset();
    vi.mocked(attachAudioGraphWhenRunning).mockImplementation(() => fakeAudioNodeChain());
    globalThis.fetch = vi.fn().mockRejectedValue(new Error('network disabled in test'));
  });

  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    ctl.restore();
    globalThis.fetch = originalFetch;
    if (originalOffsetWidth) {
      Object.defineProperty(HTMLElement.prototype, 'offsetWidth', originalOffsetWidth);
    }
    if (originalTouchStart) {
      Object.defineProperty(window, 'ontouchstart', originalTouchStart);
    }
  });

  it('seeks from the new position on every key press while paused', async () => {
    mount();
    ctl.completeLoad();
    await settle();
    const slider = seekSlider();
    slider.focus();

    await fireEvent.keyDown(slider, { key: 'ArrowRight' });
    await fireEvent.keyDown(slider, { key: 'ArrowRight' });
    expect(ctl.element.currentTime).toBe(2 * SEEK_STEP_SECONDS);
    expect(seekSlider()).toHaveAttribute('aria-valuenow', String(2 * SEEK_STEP_SECONDS));

    await fireEvent.keyDown(slider, { key: 'End' });
    expect(ctl.element.currentTime).toBe(FAKE_MEDIA_DURATION_SECONDS);

    await fireEvent.keyDown(slider, { key: 'Home' });
    expect(ctl.element.currentTime).toBe(0);
  });

  it('does not write the playback position from the inline bar until the duration is known', async () => {
    const { container } = mount({ enableClipExtraction: false });
    await settle();
    const bar = inlineBar(container);
    vi.spyOn(bar, 'getBoundingClientRect').mockReturnValue(
      new DOMRect(0, 0, PROGRESS_BAR_WIDTH_PX, 0)
    );
    const setCurrentTime = spyOnCurrentTimeWrites();

    await fireEvent.click(bar, { clientX: PROGRESS_BAR_WIDTH_PX / 2 });

    expect(setCurrentTime).not.toHaveBeenCalled();
  });

  describe('inline progress bar as a slider', () => {
    it('names the bar and reports the position as value text', async () => {
      const { container } = mount({ enableClipExtraction: false });
      ctl.completeLoad();
      await settle();
      const bar = inlineBar(container);

      bar.focus();
      expect(bar).toHaveFocus();
      expect(bar).toHaveAccessibleName('media.audio.seek');
      expect(bar).toHaveAttribute('aria-valuemin', '0');
      expect(bar).toHaveAttribute('aria-valuemax', String(FAKE_MEDIA_DURATION_SECONDS));
      expect(bar).toHaveAttribute('aria-valuenow', '0');
      expect(bar).toHaveAttribute('aria-valuetext', '0:00 of 2:00');
      expect(bar).not.toHaveAttribute('aria-disabled');
    });

    it.each([
      ['ArrowRight', SEEK_STEP_SECONDS],
      ['ArrowUp', SEEK_STEP_SECONDS],
      ['PageUp', SEEK_PAGE_STEP_SECONDS],
      ['End', FAKE_MEDIA_DURATION_SECONDS],
    ])('seeks by time on %s and keeps the page from scrolling', async (key, expected) => {
      const { container } = mount({ enableClipExtraction: false });
      ctl.completeLoad();
      await settle();
      const bar = inlineBar(container);

      const notPrevented = await fireEvent.keyDown(bar, { key });

      expect(ctl.element.currentTime).toBe(expected);
      expect(bar).toHaveAttribute('aria-valuenow', String(expected));
      expect(notPrevented).toBe(false);
    });

    it('keeps each press moving while paused and clamps at the start', async () => {
      const { container } = mount({ enableClipExtraction: false });
      ctl.completeLoad();
      await settle();
      const bar = inlineBar(container);

      await fireEvent.keyDown(bar, { key: 'ArrowRight' });
      await fireEvent.keyDown(bar, { key: 'ArrowRight' });
      expect(ctl.element.currentTime).toBe(2 * SEEK_STEP_SECONDS);
      await fireEvent.keyDown(bar, { key: 'Home' });
      await fireEvent.keyDown(bar, { key: 'ArrowLeft' });
      expect(ctl.element.currentTime).toBe(0);
    });

    it('does not seek on Enter, and on Space it does not seek or scroll the page', async () => {
      const { container } = mount({ enableClipExtraction: false });
      ctl.completeLoad();
      await settle();
      const bar = inlineBar(container);
      ctl.element.currentTime = INITIAL_POSITION_SECONDS;

      const enterNotPrevented = await fireEvent.keyDown(bar, { key: 'Enter' });
      const spaceNotPrevented = await fireEvent.keyDown(bar, { key: ' ' });

      expect(ctl.element.currentTime).toBe(INITIAL_POSITION_SECONDS);
      expect(enterNotPrevented).toBe(true);
      expect(spaceNotPrevented).toBe(false);
    });

    it('leaves a seek key held with a modifier to the browser', async () => {
      const { container } = mount({ enableClipExtraction: false });
      ctl.completeLoad();
      await settle();
      const bar = inlineBar(container);

      const notPrevented = await fireEvent.keyDown(bar, { key: 'ArrowRight', ctrlKey: true });

      expect(ctl.element.currentTime).toBe(0);
      expect(notPrevented).toBe(true);
    });

    it('does not seek on a click when the bar has no width', async () => {
      const { container } = mount({ enableClipExtraction: false });
      ctl.completeLoad();
      await settle();
      const bar = inlineBar(container);
      ctl.element.currentTime = INITIAL_POSITION_SECONDS;
      vi.spyOn(bar, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 0, 0));

      await fireEvent.click(bar, { clientX: 0 });

      expect(ctl.element.currentTime).toBe(INITIAL_POSITION_SECONDS);
    });

    it('steps from the end of the clip when the position is past the duration', async () => {
      const { container } = mount({ enableClipExtraction: false });
      ctl.completeLoad();
      await settle();
      const bar = inlineBar(container);
      ctl.element.currentTime = FAKE_MEDIA_DURATION_SECONDS + PAST_END_SECONDS;
      await fireEvent(ctl.element, new Event('timeupdate'));

      expect(bar).toHaveAttribute('aria-valuenow', String(FAKE_MEDIA_DURATION_SECONDS));
      expect(bar).toHaveAttribute('aria-valuetext', '2:00 of 2:00');
      await fireEvent.keyDown(bar, { key: 'ArrowLeft' });
      expect(ctl.element.currentTime).toBe(FAKE_MEDIA_DURATION_SECONDS - SEEK_STEP_SECONDS);
    });

    it.each([Number.NaN, Number.POSITIVE_INFINITY])(
      'reports an empty, disabled range when the media reports a duration of %s',
      async reported => {
        const { container } = mount({ enableClipExtraction: false });
        ctl.completeLoad();
        await settle();
        const bar = inlineBar(container);
        Object.defineProperty(ctl.element, 'duration', { configurable: true, get: () => reported });
        await fireEvent(ctl.element, new Event('loadedmetadata'));

        expect(bar).toHaveAttribute('aria-valuemax', '0');
        expect(bar).toHaveAttribute('aria-disabled', 'true');
        expect(bar).not.toHaveAttribute('aria-valuetext');
      }
    );

    it('is disabled and inert until the duration is known', async () => {
      const { container } = mount({ enableClipExtraction: false });
      await settle();
      const bar = inlineBar(container);
      const setCurrentTime = spyOnCurrentTimeWrites();

      const notPrevented = await fireEvent.keyDown(bar, { key: 'End' });

      expect(bar).toHaveAttribute('aria-disabled', 'true');
      expect(bar).toHaveAttribute('aria-valuemax', '0');
      expect(bar).not.toHaveAttribute('aria-valuetext');
      expect(setCurrentTime).not.toHaveBeenCalled();
      expect(notPrevented).toBe(true);
    });
  });

  describe('overlay controls', () => {
    it('start hidden and reveal on keyboard focus only', async () => {
      mount();
      await settle();

      expect(overlayControls()).toHaveClass(HIDDEN_CLASS);
      // jsdom evaluates no focus variant; the class is what the browser acts on.
      expect(overlayControls()).toHaveClass(FOCUS_VISIBLE_CLASS);
      expect(overlayControls()).not.toHaveClass(ANY_FOCUS_CLASS);
    });

    it.each([
      ['audio settings', {}, SETTINGS_LABEL, []],
      ['audible bats', { modelType: 'bat' }, BATS_LABEL, []],
      ['audio settings, then audible bats', { modelType: 'bat' }, BATS_LABEL, [SETTINGS_LABEL]],
      ['audible bats, then audio settings', { modelType: 'bat' }, SETTINGS_LABEL, [BATS_LABEL]],
    ])(
      'stay visible while %s is open and drop the open reveal after Escape',
      async (_name, props, lastOpened, openedBefore) => {
        mount(props);
        await settle();

        for (const label of [...openedBefore, lastOpened]) {
          await fireEvent.click(screen.getByRole('button', { name: label }));
        }
        await settle();
        expect(overlayControls()).not.toHaveClass(HIDDEN_CLASS);

        await fireEvent.keyDown(document, { key: 'Escape' });
        await settle();
        expect(overlayControls()).toHaveClass(HIDDEN_CLASS);
      }
    );
  });

  it('reveals the inline controls on keyboard focus only', async () => {
    const { container } = mount({ enableClipExtraction: false });
    await settle();

    const inline = container.querySelector('.rounded-b-md');
    expect(inline).toHaveClass(HIDDEN_CLASS);
    expect(inline).toHaveClass(FOCUS_VISIBLE_CLASS);
    expect(inline).not.toHaveClass(ANY_FOCUS_CLASS);
  });
});
