import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte';
import { loggers } from '$lib/utils/logger';
import { DASHBOARD_AUDIO_RETRY_DELAYS_MS, PLAY_END_DELAY_MS } from '$lib/utils/audio';
import {
  installFakeMediaElement,
  FAKE_MEDIA_DURATION_SECONDS,
  MEDIA_ERR_NETWORK,
  fakeAudioNodeChain,
  settle,
  type FakeMediaController,
} from '../../../../../test/fakeMediaElement';
import { attachAudioGraphWhenRunning } from '$lib/utils/audioNodes';
import PlayOverlay from './PlayOverlay.svelte';

vi.mock('$lib/utils/audioContextManager', () => ({
  getAudioContext: vi.fn().mockResolvedValue({ state: 'running' }),
  isAudioContextSupported: vi.fn().mockReturnValue(true),
  releaseAudioContext: vi.fn(),
}));

vi.mock('$lib/utils/audioNodes', () => ({
  attachAudioGraphWhenRunning: vi.fn(),
  disconnectAudioNodes: vi.fn(),
}));

const DETECTION_ID = 4242;
const RETRY_QUERY_MARKER = '?t=';
const BATS_SRC = 'blob:audible-bats-clip';
// The slider is stubbed to SLIDER_WIDTH_PX wide, so a click at x seeks to x / width of the clip.
const SLIDER_WIDTH_PX = 200;
const SEEK_CLICK_X = 100;
const DRAG_CLICK_X = 50;
const SEEK_EXPECTED_SECONDS = FAKE_MEDIA_DURATION_SECONDS * (SEEK_CLICK_X / SLIDER_WIDTH_PX);
const DRAG_EXPECTED_SECONDS = FAKE_MEDIA_DURATION_SECONDS * (DRAG_CLICK_X / SLIDER_WIDTH_PX);
const PLAY_LABEL = 'media.audio.play';
const PAUSE_LABEL = 'media.audio.pause';
const LOADING_LABEL = 'media.audio.loading';

describe('PlayOverlay', () => {
  let ctl: FakeMediaController;
  const mockedAttach = vi.mocked(attachAudioGraphWhenRunning);

  async function advanceRetry(index: number) {
    const delay = DASHBOARD_AUDIO_RETRY_DELAYS_MS.at(index);
    if (delay === undefined) throw new Error(`no retry delay at index ${index}`);
    await vi.advanceTimersByTimeAsync(delay);
  }

  /**
   * Fail every load from `fromIndex` on, firing each retry timer, so the whole
   * budget is spent. With `finalFailure` the last reload fails too and the
   * error shows.
   */
  async function exhaustRetries(fromIndex = 0, finalFailure = true) {
    for (let i = fromIndex; i < DASHBOARD_AUDIO_RETRY_DELAYS_MS.length; i++) {
      ctl.failLoad();
      await advanceRetry(i);
    }
    if (finalFailure) {
      ctl.failLoad();
      await settle();
    }
  }

  /** jsdom has no layout, so give the seek area a width. */
  function stubSliderRect() {
    // Partial DOMRect: the card only reads left and width.
    const rect = { left: 0, width: SLIDER_WIDTH_PX } as unknown as DOMRect;
    vi.spyOn(screen.getByRole('slider'), 'getBoundingClientRect').mockReturnValue(rect);
  }

  function button(): HTMLButtonElement {
    return screen.getByRole<HTMLButtonElement>('button');
  }

  function alertShown(): boolean {
    return screen.queryByRole('alert') !== null;
  }

  async function click() {
    button().click();
    await settle();
  }

  function mount(
    props: Partial<{
      onFreezeStart: () => void;
      onFreezeEnd: () => void;
      onAudioContextAvailable: (_a: boolean) => void;
    }> = {}
  ) {
    return render(PlayOverlay, { props: { detectionId: DETECTION_ID, ...props } });
  }

  beforeEach(() => {
    vi.useFakeTimers();
    ctl = installFakeMediaElement();
    mockedAttach.mockReset();
    mockedAttach.mockImplementation(() => fakeAudioNodeChain());
  });

  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    ctl.restore();
    vi.mocked(loggers.audio.error).mockClear();
  });

  it('retries a clip that answered 503 on preload and becomes playable once ready', async () => {
    mount();

    ctl.failLoad();
    expect(alertShown()).toBe(false);
    expect(button().disabled).toBe(false);

    await advanceRetry(0);
    expect(ctl.load).toHaveBeenCalledTimes(1);
    expect(ctl.element.src).toContain(RETRY_QUERY_MARKER);

    ctl.failLoad();
    await advanceRetry(1);
    expect(ctl.load).toHaveBeenCalledTimes(2);

    ctl.completeLoad();
    expect(ctl.play).not.toHaveBeenCalled();

    await click();
    expect(ctl.play).toHaveBeenCalledTimes(1);
    expect(button().getAttribute('aria-label')).toBe(PAUSE_LABEL);
  });

  it('gives up after the retry budget and keeps the play button working', async () => {
    mount();

    await exhaustRetries(0, false);
    expect(ctl.load).toHaveBeenCalledTimes(DASHBOARD_AUDIO_RETRY_DELAYS_MS.length);
    expect(alertShown()).toBe(false);

    ctl.failLoad();
    await settle();
    expect(alertShown()).toBe(true);
    expect(button().disabled).toBe(false);

    const loadsBefore = ctl.load.mock.calls.length;
    await click();
    expect(ctl.load.mock.calls.length).toBe(loadsBefore + 1);
    expect(ctl.play).toHaveBeenCalledTimes(1);
    expect(alertShown()).toBe(false);
  });

  it('plays after a click when the clip becomes ready during the retry chain', async () => {
    mount();
    ctl.failLoad();
    ctl.setPlayMode('hold');

    await click();
    expect(ctl.load).toHaveBeenCalledTimes(1);

    ctl.failLoad();
    expect(alertShown()).toBe(false);

    await advanceRetry(0);
    expect(ctl.load).toHaveBeenCalledTimes(2);
    expect(ctl.play).toHaveBeenCalledTimes(2);

    ctl.completeLoad();
    await settle();
    expect(ctl.paused).toBe(false);
    expect(button().getAttribute('aria-label')).toBe(PAUSE_LABEL);
  });

  it('does not show an error for an interrupted play()', async () => {
    mount();
    ctl.completeLoad();
    ctl.rejectNextPlay('AbortError');

    await click();

    expect(alertShown()).toBe(false);
    expect(button().disabled).toBe(false);
    expect(loggers.audio.error).not.toHaveBeenCalled();
  });

  it('shows Play and recovers after a playback error', async () => {
    const onFreezeEnd = vi.fn();
    mount({ onFreezeEnd });
    ctl.completeLoad();

    await click();
    expect(button().getAttribute('aria-label')).toBe(PAUSE_LABEL);

    ctl.failLoad(MEDIA_ERR_NETWORK);
    await advanceRetry(0);
    expect(button().getAttribute('aria-label')).toBe(PLAY_LABEL);
    expect(onFreezeEnd).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(PLAY_END_DELAY_MS);
    expect(onFreezeEnd).toHaveBeenCalledTimes(1);

    await exhaustRetries(1);
    expect(alertShown()).toBe(true);
    expect(button().getAttribute('aria-label')).toBe(PLAY_LABEL);

    const loadsBefore = ctl.load.mock.calls.length;
    const playsBefore = ctl.play.mock.calls.length;
    await click();
    expect(ctl.load.mock.calls.length).toBe(loadsBefore + 1);
    expect(ctl.play.mock.calls.length).toBe(playsBefore + 1);
  });

  it('still plays when audio graph setup throws', async () => {
    const onAudioContextAvailable = vi.fn();
    mockedAttach.mockImplementation(() => {
      throw new Error('graph build failed');
    });
    mount({ onAudioContextAvailable });
    ctl.completeLoad();

    await click();

    expect(ctl.play).toHaveBeenCalledTimes(1);
    expect(onAudioContextAvailable).toHaveBeenCalledWith(false);
    expect(alertShown()).toBe(false);
  });

  it('stops background retries when the card unmounts', async () => {
    const { unmount } = mount();
    ctl.failLoad();

    unmount();
    const loadsAfterUnmount = ctl.load.mock.calls.length;
    for (let i = 0; i < DASHBOARD_AUDIO_RETRY_DELAYS_MS.length; i++) {
      await advanceRetry(i);
    }

    expect(ctl.load.mock.calls.length).toBe(loadsAfterUnmount);
  });

  describe('freeze callbacks', () => {
    it('pairs one freeze start with one end when a click during a retry fires two play events', async () => {
      const onFreezeStart = vi.fn();
      const onFreezeEnd = vi.fn();
      mount({ onFreezeStart, onFreezeEnd });
      ctl.failLoad();
      ctl.setPlayMode('hold');

      await click();
      ctl.failLoad();
      await advanceRetry(0);
      ctl.completeLoad();
      await settle();
      expect(ctl.play).toHaveBeenCalledTimes(2);

      ctl.dispatch('ended');
      await vi.advanceTimersByTimeAsync(PLAY_END_DELAY_MS);

      expect(onFreezeStart).toHaveBeenCalledTimes(1);
      expect(onFreezeEnd).toHaveBeenCalledTimes(1);
    });

    it('pairs one freeze start with one end for pause and resume within the grace period', async () => {
      const onFreezeStart = vi.fn();
      const onFreezeEnd = vi.fn();
      mount({ onFreezeStart, onFreezeEnd });
      ctl.completeLoad();

      await click();
      await click();
      expect(button().getAttribute('aria-label')).toBe(PLAY_LABEL);
      await click();
      expect(button().getAttribute('aria-label')).toBe(PAUSE_LABEL);

      ctl.dispatch('ended');
      await vi.advanceTimersByTimeAsync(PLAY_END_DELAY_MS);

      expect(onFreezeStart).toHaveBeenCalledTimes(1);
      expect(onFreezeEnd).toHaveBeenCalledTimes(1);
    });

    it('releases the freeze when the card unmounts while playing', async () => {
      const onFreezeStart = vi.fn();
      const onFreezeEnd = vi.fn();
      const { unmount } = mount({ onFreezeStart, onFreezeEnd });
      ctl.completeLoad();
      await click();

      unmount();

      expect(onFreezeStart).toHaveBeenCalledTimes(1);
      expect(onFreezeEnd).toHaveBeenCalledTimes(1);
    });
  });

  it('recovers a failed card from a spectrogram click after it has played', async () => {
    mount();
    ctl.completeLoad();
    await click();
    expect(ctl.paused).toBe(false);

    ctl.failLoad(MEDIA_ERR_NETWORK);
    await exhaustRetries(1);
    expect(alertShown()).toBe(true);
    // The badge must not take clicks, so they reach the spectrogram area.
    expect(screen.getByRole('alert').className).not.toContain('pointer-events-auto');

    const loadsBefore = ctl.load.mock.calls.length;
    const playsBefore = ctl.play.mock.calls.length;
    await fireEvent.mouseDown(screen.getByRole('slider'));
    await settle();

    expect(ctl.load.mock.calls.length).toBe(loadsBefore + 1);
    expect(ctl.play.mock.calls.length).toBe(playsBefore + 1);
    expect(alertShown()).toBe(false);
  });

  it('starts playback instead of seeking on the first spectrogram tap of a never-played card', async () => {
    mount();
    ctl.completeLoad();
    stubSliderRect();

    await fireEvent.mouseDown(screen.getByRole('slider'), { clientX: SEEK_CLICK_X });
    await settle();

    expect(ctl.play).toHaveBeenCalledTimes(1);
    expect(ctl.element.currentTime).toBe(0);
  });

  it('seeks and starts a drag on a spectrogram click of a healthy card that has played', async () => {
    mount();
    ctl.completeLoad();
    await click();
    expect(ctl.play).toHaveBeenCalledTimes(1);
    stubSliderRect();

    await fireEvent.mouseDown(screen.getByRole('slider'), { clientX: SEEK_CLICK_X });
    expect(ctl.element.currentTime).toBe(SEEK_EXPECTED_SECONDS);
    await fireEvent.mouseMove(document, { clientX: DRAG_CLICK_X });
    await fireEvent.mouseUp(document);

    expect(ctl.element.currentTime).toBe(DRAG_EXPECTED_SECONDS);
    expect(ctl.play).toHaveBeenCalledTimes(1);
  });

  describe('audible-bats source swap', () => {
    function swapToBats(
      rerender: (props: { detectionId: number; audibleBatsSrc: string }) => Promise<void>
    ) {
      return rerender({ detectionId: DETECTION_ID, audibleBatsSrc: BATS_SRC });
    }

    it('shows Play after the swap and resumes with one freeze while playing', async () => {
      const onFreezeStart = vi.fn();
      const onFreezeEnd = vi.fn();
      const { rerender } = mount({ onFreezeStart, onFreezeEnd });
      ctl.completeLoad();
      await click();
      expect(button().getAttribute('aria-label')).toBe(PAUSE_LABEL);
      const playsBefore = ctl.play.mock.calls.length;

      await swapToBats(rerender);
      await settle();
      expect(button().getAttribute('aria-label')).toBe(PLAY_LABEL);

      ctl.completeLoad();
      await settle();
      expect(ctl.play.mock.calls.length).toBe(playsBefore + 1);
      expect(button().getAttribute('aria-label')).toBe(PAUSE_LABEL);

      await vi.advanceTimersByTimeAsync(PLAY_END_DELAY_MS);
      expect(onFreezeStart).toHaveBeenCalledTimes(1);
      expect(onFreezeEnd).not.toHaveBeenCalled();
    });

    it('does not autoplay on an idle card', async () => {
      const { rerender } = mount();
      ctl.completeLoad();

      await swapToBats(rerender);
      await settle();
      ctl.completeLoad();
      await settle();

      expect(ctl.play).not.toHaveBeenCalled();
      expect(button().getAttribute('aria-label')).toBe(PLAY_LABEL);
    });

    it('does not autoplay when swapped during a background retry', async () => {
      const { rerender } = mount();
      ctl.failLoad();

      await swapToBats(rerender);
      await settle();
      ctl.completeLoad();
      await settle();

      expect(ctl.play).not.toHaveBeenCalled();
    });
  });

  it('keeps a queued play across an audible-bats swap during a retry delay', async () => {
    const { rerender } = mount();
    ctl.failLoad();
    ctl.setPlayMode('hold');
    await click();
    ctl.failLoad();
    // Playback is not running while the retry waits.
    ctl.dispatch('pause');
    const playsBefore = ctl.play.mock.calls.length;

    await rerender({ detectionId: DETECTION_ID, audibleBatsSrc: BATS_SRC });
    await settle();
    ctl.completeLoad();
    await settle();

    expect(ctl.play.mock.calls.length).toBe(playsBefore + 1);
    expect(ctl.paused).toBe(false);
  });

  it('fires each retry at its delay and not one millisecond earlier', async () => {
    mount();

    for (const delay of DASHBOARD_AUDIO_RETRY_DELAYS_MS) {
      ctl.failLoad();
      const loadsBefore = ctl.load.mock.calls.length;

      await vi.advanceTimersByTimeAsync(delay - 1);
      expect(ctl.load.mock.calls.length).toBe(loadsBefore);

      await vi.advanceTimersByTimeAsync(1);
      expect(ctl.load.mock.calls.length).toBe(loadsBefore + 1);
    }
  });

  it.each(['NotAllowedError', 'NotSupportedError'] as const)(
    'shows the badge for a %s rejection and the next click clears it and plays',
    async rejection => {
      mount();
      ctl.completeLoad();
      ctl.rejectNextPlay(rejection);

      await click();
      expect(alertShown()).toBe(true);
      expect(button().disabled).toBe(false);

      await click();
      expect(alertShown()).toBe(false);
      expect(ctl.paused).toBe(false);
    }
  );

  it('does not read Pause while a click waits for a failed clip to reload', async () => {
    mount();
    ctl.failLoad();
    ctl.setPlayMode('hold');

    await click();

    expect(button().getAttribute('aria-label')).toBe(LOADING_LABEL);
    expect(button().disabled).toBe(false);
  });

  describe('play button stays usable', () => {
    type Step =
      | 'failLoad'
      | 'advance'
      | 'completeLoad'
      | 'click'
      | 'clickNoFlush'
      | 'failWhilePlaying'
      | 'holdMode'
      | 'exhaust'
      | 'expectPlaying';

    interface Sequence {
      name: string;
      steps: Step[];
    }

    const PO_SEQUENCES: Sequence[] = [
      { name: 'failLoad, click', steps: ['failLoad', 'click'] },
      {
        name: 'failLoad, advance, completeLoad, click',
        steps: ['failLoad', 'advance', 'completeLoad', 'click'],
      },
      { name: 'exhausted budget, click', steps: ['exhaust', 'click'] },
      {
        name: 'click, failWhilePlaying, advance, completeLoad, click',
        steps: ['click', 'failWhilePlaying', 'advance', 'completeLoad', 'click'],
      },
      {
        name: 'failLoad, hold, click, failLoad, advance, completeLoad',
        steps: [
          'failLoad',
          'holdMode',
          'click',
          'failLoad',
          'advance',
          'completeLoad',
          'expectPlaying',
        ],
      },
      { name: 'failLoad, advance, click', steps: ['failLoad', 'advance', 'click'] },
      {
        name: 'hold, click during preload, failLoad during context wait, advance, completeLoad',
        steps: ['holdMode', 'clickNoFlush', 'failLoad', 'advance', 'completeLoad', 'expectPlaying'],
      },
    ];

    /** Click, then assert the click was not a silent no-op. */
    async function clickAndExpectNotSilent() {
      const loadsBefore = ctl.load.mock.calls.length;
      await click();
      // The element plays, or a load was started, or the error is visible while
      // the element is loadable again (the next click plays).
      const loadGrew = ctl.load.mock.calls.length > loadsBefore;
      expect(!ctl.paused || loadGrew || (alertShown() && ctl.error === null)).toBe(true);
    }

    async function expectPlaying() {
      await settle();
      expect(ctl.paused).toBe(false);
    }

    /** A step may call play() only when it is a click or a held click armed a timer. */
    function expectPlayCallsUnchangedUnless(mayPlay: boolean, playsBefore: number) {
      if (!mayPlay) expect(ctl.play.mock.calls.length).toBe(playsBefore);
    }

    it.each(PO_SEQUENCES)('the card stays playable after: $name', async ({ steps }) => {
      mount();
      let retryIndex = 0;
      // True after a click made in hold mode, until the load completes: the only
      // window in which a timer may call play() on its own.
      let clickArmedPlay = false;
      let held = false;

      for (const step of steps) {
        const playsBefore = ctl.play.mock.calls.length;

        switch (step) {
          case 'failLoad':
            ctl.failLoad();
            break;
          case 'failWhilePlaying':
            ctl.failLoad(MEDIA_ERR_NETWORK);
            break;
          case 'advance':
            await advanceRetry(retryIndex);
            retryIndex++;
            break;
          case 'completeLoad':
            ctl.completeLoad();
            clickArmedPlay = false;
            retryIndex = 0;
            await settle();
            break;
          case 'holdMode':
            ctl.setPlayMode('hold');
            held = true;
            break;
          case 'exhaust':
            await exhaustRetries();
            break;
          case 'click':
            retryIndex = 0;
            await clickAndExpectNotSilent();
            clickArmedPlay = held;
            break;
          case 'clickNoFlush':
            retryIndex = 0;
            button().click();
            clickArmedPlay = held;
            break;
          case 'expectPlaying':
            await expectPlaying();
            break;
        }

        expect(button().disabled).toBe(false);
        expectPlayCallsUnchangedUnless(
          step === 'click' || step === 'clickNoFlush' || clickArmedPlay,
          playsBefore
        );
      }
    });
  });
});
