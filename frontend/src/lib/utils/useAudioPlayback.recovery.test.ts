/**
 * Recovery tests for useAudioPlayback: a play press must never be a silent
 * no-op after a load failure, an interrupted play(), or a retry that resets
 * the source. Uses the fake media element, which models the load algorithm
 * (src assignment pauses the element without a pause event).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { waitFor, cleanup } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../test/render-helpers';
import {
  installFakeMediaElement,
  MEDIA_ERR_NETWORK,
  type FakeMediaController,
} from '../../test/fakeMediaElement';
import { MAX_AUDIO_LOAD_RETRIES, AUDIO_RETRY_DELAY_MS, PLAY_END_DELAY_MS } from '$lib/utils/audio';
import { loggers } from '$lib/utils/logger';
import type { AudioPlaybackState } from './useAudioPlayback.svelte';
import Wrapper from './UseAudioPlaybackWrapper.test.svelte';

vi.mock('$lib/utils/audioContextManager', () => ({
  getAudioContext: vi.fn().mockResolvedValue({ state: 'running' }),
  isAudioContextSupported: vi.fn().mockReturnValue(true),
  releaseAudioContext: vi.fn(),
}));

vi.mock('$lib/utils/audioNodes', () => ({
  attachAudioGraphWhenRunning: vi.fn().mockReturnValue({
    source: { connect: vi.fn(), disconnect: vi.fn() },
    gain: { gain: { value: 1 }, connect: vi.fn(), disconnect: vi.fn() },
    highPass: { frequency: { value: 20 }, connect: vi.fn(), disconnect: vi.fn() },
  }),
  disconnectAudioNodes: vi.fn(),
}));

const PLAY_ERROR_KEY = 'media.audio.playError';

describe('useAudioPlayback recovery', () => {
  let ctl: FakeMediaController;
  let capturedState: AudioPlaybackState | null = null;
  const wrapperTest = createComponentTestFactory(Wrapper);

  function getState(): AudioPlaybackState {
    if (capturedState === null) {
      throw new Error('capturedState is null, did you forget to await renderAndWait?');
    }
    return capturedState;
  }

  async function settle() {
    await vi.advanceTimersByTimeAsync(0);
  }

  async function renderAndWait(options: { onPlayEnd?: () => void } = {}) {
    wrapperTest.render({
      options: { audioUrl: '/audio/test.mp3', detectionId: 'test-123', ...options },
      onState: (s: AudioPlaybackState) => {
        capturedState = s;
      },
    });
    await waitFor(() => {
      expect(capturedState).not.toBeNull();
    });
  }

  /** Fail every retry until the budget is spent and the error shows. */
  async function exhaustRetries() {
    for (let i = 0; i < MAX_AUDIO_LOAD_RETRIES; i++) {
      ctl.failLoad();
      await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
    }
    ctl.failLoad();
    await settle();
  }

  async function toggle() {
    await getState().togglePlayPause();
    await settle();
  }

  beforeEach(() => {
    vi.useFakeTimers();
    capturedState = null;
    ctl = installFakeMediaElement();
    vi.mocked(loggers.audio.error).mockClear();
  });

  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    ctl.restore();
    capturedState = null;
  });

  it('retries a 503 preload and plays once the clip is ready', async () => {
    await renderAndWait();

    ctl.failLoad();
    await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
    expect(ctl.load).toHaveBeenCalledTimes(1);

    ctl.completeLoad();
    await settle();
    expect(getState().isLoading).toBe(false);

    await toggle();
    expect(ctl.play).toHaveBeenCalledTimes(1);
    expect(ctl.paused).toBe(false);
  });

  it('reloads the clip when play is pressed after load retries are exhausted', async () => {
    await renderAndWait();
    await exhaustRetries();
    expect(getState().error).not.toBeNull();
    const loadsBefore = ctl.load.mock.calls.length;

    await toggle();

    expect(ctl.load.mock.calls.length).toBe(loadsBefore + 1);
    expect(getState().error).toBeNull();
    ctl.completeLoad();
    await settle();
    expect(ctl.paused).toBe(false);
  });

  it('resumes a reload that hits a transient error once the clip loads', async () => {
    await renderAndWait();
    await exhaustRetries();
    ctl.setPlayMode('hold');

    const pressed = getState().togglePlayPause();
    await settle();
    ctl.failLoad();
    await pressed;
    await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
    ctl.completeLoad();
    await settle();

    expect(ctl.paused).toBe(false);
    expect(getState().error).toBeNull();
  });

  it('does not swallow a play rejection when no retry is pending', async () => {
    await renderAndWait();
    ctl.failLoad();
    await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
    ctl.rejectNextPlay('NotAllowedError');

    await toggle();

    expect(getState().error).toBe(PLAY_ERROR_KEY);
  });

  it('treats an interrupted play() as a non-error', async () => {
    await renderAndWait();
    ctl.rejectNextPlay('AbortError');

    await toggle();

    expect(getState().error).toBeNull();
    expect(loggers.audio.error).not.toHaveBeenCalled();
  });

  it('clears isLoading on loadedmetadata without waiting for canplay', async () => {
    await renderAndWait();
    ctl.dispatch('loadstart');
    await settle();
    expect(getState().isLoading).toBe(true);

    ctl.completeLoad({ canplay: false });
    await settle();

    expect(getState().isLoading).toBe(false);
  });

  it('resumes a queued play on loadedmetadata when canplay never fires', async () => {
    await renderAndWait();
    await exhaustRetries();
    ctl.setPlayMode('hold');
    const pressed = getState().togglePlayPause();
    await settle();
    ctl.failLoad();
    await pressed;
    await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);

    ctl.completeLoad({ canplay: false });
    await settle();

    expect(ctl.paused).toBe(false);
  });

  it('plays a queued intent only once when loadedmetadata and canplay both fire', async () => {
    await renderAndWait();
    await exhaustRetries();
    ctl.setPlayMode('hold');
    const pressed = getState().togglePlayPause();
    await settle();
    ctl.failLoad();
    await pressed;
    await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
    const playsBefore = ctl.play.mock.calls.length;

    ctl.completeLoad();
    await settle();

    expect(ctl.play.mock.calls.length).toBe(playsBefore + 1);
  });

  it('marks playback stopped when a retry reloads a playing clip', async () => {
    const onPlayEnd = vi.fn();
    await renderAndWait({ onPlayEnd });
    await toggle();
    expect(getState().isPlaying).toBe(true);

    ctl.failLoad(MEDIA_ERR_NETWORK);
    await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);

    expect(getState().isPlaying).toBe(false);
    expect(onPlayEnd).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(PLAY_END_DELAY_MS);
    expect(onPlayEnd).toHaveBeenCalledTimes(1);
  });

  describe('a play click is never a silent no-op', () => {
    type Step =
      | 'click'
      | 'exhaust'
      | 'failLoad'
      | 'advanceRetry'
      | 'completeLoad'
      | 'rejectNextNotAllowed'
      | 'failNetworkWhilePlaying'
      | 'metadataOnly';

    interface Sequence {
      name: string;
      steps: Step[];
    }

    const SEQUENCES: Sequence[] = [
      { name: 'click', steps: ['click'] },
      { name: 'exhaust, click', steps: ['exhaust', 'click'] },
      {
        name: 'exhaust, click, failLoad, advanceRetry, completeLoad, click',
        steps: ['exhaust', 'click', 'failLoad', 'advanceRetry', 'completeLoad', 'click'],
      },
      { name: 'failLoad, click', steps: ['failLoad', 'click'] },
      {
        name: 'failLoad, advanceRetry, rejectNextNotAllowed, click',
        steps: ['failLoad', 'advanceRetry', 'rejectNextNotAllowed', 'click'],
      },
      {
        name: 'click, failNetworkWhilePlaying, advanceRetry, completeLoad, click',
        steps: ['click', 'failNetworkWhilePlaying', 'advanceRetry', 'completeLoad', 'click'],
      },
      { name: 'metadataOnly, click', steps: ['metadataOnly', 'click'] },
      {
        name: 'exhaust, rejectNextNotAllowed, click, click',
        steps: ['exhaust', 'rejectNextNotAllowed', 'click', 'click'],
      },
    ];

    it.each(SEQUENCES)('after: $name', async ({ steps }) => {
      await renderAndWait();
      const clickOutcomes: boolean[] = [];

      for (const step of steps) {
        const loadsBefore = ctl.load.mock.calls.length;
        switch (step) {
          case 'click': {
            await toggle();
            // The element plays, or a load was started, or the error is visible
            // while the element is loadable again (the next click plays).
            const loadGrew = ctl.load.mock.calls.length > loadsBefore;
            const errorVisibleOnLoadableElement = getState().error !== null && ctl.error === null;
            clickOutcomes.push(!ctl.paused || loadGrew || errorVisibleOnLoadableElement);
            break;
          }
          case 'exhaust':
            await exhaustRetries();
            break;
          case 'failLoad':
            ctl.failLoad();
            await settle();
            break;
          case 'advanceRetry':
            await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
            break;
          case 'completeLoad':
            ctl.completeLoad();
            await settle();
            break;
          case 'rejectNextNotAllowed':
            ctl.rejectNextPlay('NotAllowedError');
            break;
          case 'failNetworkWhilePlaying':
            ctl.failLoad(MEDIA_ERR_NETWORK);
            await settle();
            break;
          case 'metadataOnly':
            ctl.completeLoad({ canplay: false });
            await settle();
            break;
        }
      }

      expect(clickOutcomes.length).toBeGreaterThan(0);
      expect(clickOutcomes.every(Boolean)).toBe(true);
    });
  });
});
