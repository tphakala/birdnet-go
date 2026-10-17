import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/svelte';
import { loggers } from '$lib/utils/logger';
import { DASHBOARD_AUDIO_RETRY_DELAYS_MS, PLAY_END_DELAY_MS } from '$lib/utils/audio';
import {
  installFakeMediaElement,
  MEDIA_ERR_NETWORK,
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
const PLAY_LABEL = 'media.audio.play';
const PAUSE_LABEL = 'media.audio.pause';
const LOADING_LABEL = 'media.audio.loading';

function fakeChain(): ReturnType<typeof attachAudioGraphWhenRunning> {
  return {
    source: { connect: vi.fn(), disconnect: vi.fn() },
    gain: { gain: { value: 1 }, connect: vi.fn(), disconnect: vi.fn() },
    highPass: { frequency: { value: 20 }, connect: vi.fn(), disconnect: vi.fn() },
  } as unknown as ReturnType<typeof attachAudioGraphWhenRunning>; // minimal node fakes
}

describe('PlayOverlay', () => {
  let ctl: FakeMediaController;
  const mockedAttach = vi.mocked(attachAudioGraphWhenRunning);

  async function settle() {
    await vi.advanceTimersByTimeAsync(0);
  }

  async function advanceRetry(index: number) {
    const delay = DASHBOARD_AUDIO_RETRY_DELAYS_MS.at(index);
    if (delay === undefined) throw new Error(`no retry delay at index ${index}`);
    await vi.advanceTimersByTimeAsync(delay);
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
    mockedAttach.mockImplementation(() => fakeChain());
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

    for (let i = 0; i < DASHBOARD_AUDIO_RETRY_DELAYS_MS.length; i++) {
      ctl.failLoad();
      await advanceRetry(i);
    }
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

    for (let i = 1; i < DASHBOARD_AUDIO_RETRY_DELAYS_MS.length; i++) {
      ctl.failLoad();
      await advanceRetry(i);
    }
    ctl.failLoad();
    await settle();
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

  describe('loading label', () => {
    it('does not read Pause while a click waits for a failed clip to reload', async () => {
      mount();
      ctl.failLoad();
      ctl.setPlayMode('hold');

      await click();

      expect(button().getAttribute('aria-label')).toBe(LOADING_LABEL);
      expect(button().disabled).toBe(false);
    });
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
      | 'exhaust';

    interface Sequence {
      name: string;
      steps: Step[];
      /** The element must end up playing */
      endsPlaying?: boolean;
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
        steps: ['failLoad', 'holdMode', 'click', 'failLoad', 'advance', 'completeLoad'],
        endsPlaying: true,
      },
      { name: 'failLoad, advance, click', steps: ['failLoad', 'advance', 'click'] },
      {
        name: 'hold, click during preload, failLoad during context wait, advance, completeLoad',
        steps: ['holdMode', 'clickNoFlush', 'failLoad', 'advance', 'completeLoad'],
        endsPlaying: true,
      },
    ];

    it.each(PO_SEQUENCES)(
      'the card stays playable after: $name',
      async ({ steps, endsPlaying }) => {
        mount();
        let retryIndex = 0;
        // True after a click made in hold mode, until the load completes: the only
        // window in which a timer may call play() on its own.
        let clickArmedPlay = false;
        let held = false;

        for (const step of steps) {
          const playsBefore = ctl.play.mock.calls.length;
          const loadsBefore = ctl.load.mock.calls.length;
          let isClick = false;

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
              for (let i = 0; i < DASHBOARD_AUDIO_RETRY_DELAYS_MS.length; i++) {
                ctl.failLoad();
                await advanceRetry(i);
              }
              ctl.failLoad();
              break;
            case 'click':
              isClick = true;
              retryIndex = 0;
              await click();
              clickArmedPlay = held;
              break;
            case 'clickNoFlush':
              retryIndex = 0;
              button().click();
              clickArmedPlay = held;
              break;
          }

          expect(button().disabled).toBe(false);
          const loadGrew = ctl.load.mock.calls.length > loadsBefore;
          const clickLeftUsableState =
            !ctl.paused || loadGrew || (alertShown() && ctl.error === null);
          const playCalledByStep = ctl.play.mock.calls.length > playsBefore;
          // A click never ends as a silent no-op, and no other step starts playback
          // unless a held click armed it.
          expect(isClick ? clickLeftUsableState : true).toBe(true);
          expect(
            isClick || step === 'clickNoFlush' || clickArmedPlay ? false : playCalledByStep
          ).toBe(false);
        }

        await settle();
        expect(endsPlaying ? !ctl.paused : true).toBe(true);
      }
    );
  });
});
