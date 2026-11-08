/**
 * Keyboard and screen reader behaviour of the large AudioPlayer: seeking from the
 * toolbar slider by key, and keeping the overlay controls visible while they hold
 * focus or a popover is open. Uses the fake media element.
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
const FOCUS_VISIBLE_CLASS = 'focus-within:opacity-100';
const WIDE_CONTAINER_PX = 400;

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
    expect(ctl.element.currentTime).toBe(2);
    expect(seekSlider()).toHaveAttribute('aria-valuenow', '2');

    await fireEvent.keyDown(slider, { key: 'End' });
    expect(ctl.element.currentTime).toBe(FAKE_MEDIA_DURATION_SECONDS);

    await fireEvent.keyDown(slider, { key: 'Home' });
    expect(ctl.element.currentTime).toBe(0);
  });

  describe('overlay controls', () => {
    it('start hidden and are revealed by focus', async () => {
      mount();
      await settle();

      expect(overlayControls()).toHaveClass(HIDDEN_CLASS);
      // jsdom has no focus variant to evaluate; the class is what the browser acts on.
      expect(overlayControls()).toHaveClass(FOCUS_VISIBLE_CLASS);
    });

    it.each([
      ['audio settings', {}, SETTINGS_LABEL, []],
      ['audible bats', { modelType: 'bat' }, BATS_LABEL, []],
      ['audio settings, then audible bats', { modelType: 'bat' }, BATS_LABEL, [SETTINGS_LABEL]],
      ['audible bats, then audio settings', { modelType: 'bat' }, SETTINGS_LABEL, [BATS_LABEL]],
    ])(
      'stay visible while %s is open and hide again after Escape',
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

  it('reveals the inline controls when they hold focus', async () => {
    const { container } = mount({ enableClipExtraction: false });
    await settle();

    const inline = container.querySelector('.rounded-b-md');
    expect(inline).toHaveClass(HIDDEN_CLASS);
    expect(inline).toHaveClass(FOCUS_VISIBLE_CLASS);
  });
});
