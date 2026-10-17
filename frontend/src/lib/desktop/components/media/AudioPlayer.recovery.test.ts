/**
 * Recovery tests for AudioPlayer: a play press must never be a silent no-op,
 * the error overlay must not block the controls, and the Play/Pause state must
 * follow the element across source resets. Kept apart from AudioPlayer.test.ts
 * because it mocks audioContextManager and audioNodes and models the media
 * element's load algorithm (src assignment pauses without a pause event).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, fireEvent, cleanup, render } from '@testing-library/svelte';
import {
  installFakeMediaElement,
  MEDIA_ERR_NETWORK,
  type FakeMediaController,
} from '../../../../test/fakeMediaElement';
import { MAX_AUDIO_LOAD_RETRIES, AUDIO_RETRY_DELAY_MS, CANPLAY_TIMEOUT_MS } from '$lib/utils/audio';
import { loggers } from '$lib/utils/logger';
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

const PLAY_LABEL = 'media.audio.play';
const PAUSE_LABEL = 'media.audio.pause';
const PLAY_ERROR_TEXT = 'media.audio.playError';
const LOAD_ERROR_TEXT = 'media.audio.error';
const DENOISE_LABEL = 'components.audioPlayer.processing.denoise';
const DENOISE_LIGHT_TEXT = 'components.audioPlayer.processing.denoiseLight';
const DENOISE_OFF_TEXT = 'components.audioPlayer.processing.denoiseOff';
// Debounce before a processing request starts (PROCESS_DEBOUNCE_MS in AudioPlayer.svelte)
const PROCESSING_DEBOUNCE_MS = 300;
const PROCESSED_BLOB_URL = 'blob:processed-clip';

function fakeChain(): ReturnType<typeof attachAudioGraphWhenRunning> {
  return {
    source: { connect: vi.fn(), disconnect: vi.fn() },
    gain: { gain: { value: 1 }, connect: vi.fn(), disconnect: vi.fn() },
    highPass: { frequency: { value: 20 }, connect: vi.fn(), disconnect: vi.fn() },
  } as unknown as ReturnType<typeof attachAudioGraphWhenRunning>; // minimal node fakes
}

describe('AudioPlayer recovery', () => {
  let ctl: FakeMediaController;
  const mockedAttach = vi.mocked(attachAudioGraphWhenRunning);

  async function settle() {
    await vi.advanceTimersByTimeAsync(0);
  }

  function mount(props: Record<string, unknown> = {}) {
    return render(AudioPlayer, {
      props: { audioUrl: '/audio/test.mp3', detectionId: 'test-123', ...props },
    });
  }

  function playButton(container: HTMLElement): HTMLButtonElement {
    const el = container.querySelector<HTMLButtonElement>('button[id^="playPause"]');
    if (!el) throw new Error('play/pause button not found');
    return el;
  }

  async function press(container: HTMLElement) {
    await fireEvent.click(playButton(container));
    await settle();
  }

  async function exhaustRetries() {
    for (let i = 0; i < MAX_AUDIO_LOAD_RETRIES; i++) {
      ctl.failLoad();
      await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
    }
    ctl.failLoad();
    await settle();
  }

  beforeEach(() => {
    vi.useFakeTimers();
    ctl = installFakeMediaElement();
    mockedAttach.mockReset();
    mockedAttach.mockImplementation(() => fakeChain());
    vi.mocked(loggers.audio.error).mockClear();
    vi.mocked(loggers.audio.warn).mockClear();
  });

  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    ctl.restore();
  });

  it('clears the play error when play is pressed again', async () => {
    const { container } = mount();
    ctl.rejectNextPlay('NotAllowedError');
    await press(container);
    expect(screen.getByText(PLAY_ERROR_TEXT)).toBeInTheDocument();

    await press(container);

    expect(screen.queryByText(PLAY_ERROR_TEXT)).not.toBeInTheDocument();
    expect(ctl.paused).toBe(false);
  });

  it('does not show an error for an interrupted play()', async () => {
    const { container } = mount();
    ctl.rejectNextPlay('AbortError');

    await press(container);

    expect(screen.queryByText(PLAY_ERROR_TEXT)).not.toBeInTheDocument();
    expect(loggers.audio.error).not.toHaveBeenCalled();
  });

  it('renders the load error as a non-blocking alert', async () => {
    const { container } = mount();
    await exhaustRetries();

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent(LOAD_ERROR_TEXT);
    expect(alert.className).toContain('pointer-events-none');
    expect(playButton(container).disabled).toBe(false);
  });

  it('reloads the clip when play is pressed after load retries are exhausted', async () => {
    const { container } = mount();
    await exhaustRetries();
    const loadsBefore = ctl.load.mock.calls.length;

    await press(container);

    expect(ctl.load.mock.calls.length).toBe(loadsBefore + 1);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    ctl.completeLoad();
    await settle();
    expect(ctl.paused).toBe(false);
  });

  it('queues play when the reload hits a transient error and plays once loaded', async () => {
    const { container } = mount();
    await exhaustRetries();
    ctl.setPlayMode('hold');

    await fireEvent.click(playButton(container));
    await settle();
    ctl.failLoad();
    await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
    ctl.completeLoad();
    await settle();

    expect(ctl.paused).toBe(false);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows Play and plays again after a retry reloads a playing clip', async () => {
    const { container } = mount();
    await press(container);
    expect(screen.getByLabelText(PAUSE_LABEL)).toBeInTheDocument();

    ctl.failLoad(MEDIA_ERR_NETWORK);
    await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
    ctl.completeLoad();
    await settle();
    expect(screen.getByLabelText(PLAY_LABEL)).toBeInTheDocument();
    const playsBefore = ctl.play.mock.calls.length;

    await press(container);

    expect(ctl.play.mock.calls.length).toBe(playsBefore + 1);
    expect(ctl.paused).toBe(false);
  });

  it('shows Play and plays again after processing is switched off mid-playback', async () => {
    const originalFetch = globalThis.fetch;
    const originalCreateObjectURL = URL.createObjectURL;
    const originalRevokeObjectURL = URL.revokeObjectURL;
    URL.createObjectURL = vi.fn(() => PROCESSED_BLOB_URL);
    URL.revokeObjectURL = vi.fn();
    globalThis.fetch = vi.fn().mockImplementation(async () => ({
      ok: true,
      status: 200,
      blob: async () => new Blob(['processed']),
      headers: new Headers(),
    }));

    try {
      mount({ enableClipExtraction: true, showSpectrogram: false });
      await settle();

      // Switch denoise on and let the processed clip load.
      await fireEvent.click(screen.getByLabelText(DENOISE_LABEL));
      await fireEvent.click(screen.getByText(DENOISE_LIGHT_TEXT));
      await vi.advanceTimersByTimeAsync(PROCESSING_DEBOUNCE_MS);
      await settle();
      ctl.completeLoad();
      await settle();

      // Play the processed clip.
      await fireEvent.click(screen.getByLabelText(PLAY_LABEL));
      await settle();
      expect(screen.getByLabelText(PAUSE_LABEL)).toBeInTheDocument();

      // Switch denoise off: the source resets to the original and playback stops.
      await fireEvent.click(screen.getByLabelText(DENOISE_LABEL));
      await fireEvent.click(screen.getByText(DENOISE_OFF_TEXT));
      await vi.advanceTimersByTimeAsync(PROCESSING_DEBOUNCE_MS);
      await settle();
      ctl.completeLoad();
      await settle();
      expect(screen.getByLabelText(PLAY_LABEL)).toBeInTheDocument();

      const playsBefore = ctl.play.mock.calls.length;
      await fireEvent.click(screen.getByLabelText(PLAY_LABEL));
      await settle();
      expect(ctl.play.mock.calls.length).toBe(playsBefore + 1);
    } finally {
      globalThis.fetch = originalFetch;
      URL.createObjectURL = originalCreateObjectURL;
      URL.revokeObjectURL = originalRevokeObjectURL;
    }
  });

  it('still plays when audio graph setup throws', async () => {
    mockedAttach.mockImplementation(() => {
      throw new DOMException('already connected', 'InvalidStateError');
    });
    const { container } = mount();

    await press(container);

    expect(ctl.play).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(PLAY_ERROR_TEXT)).not.toBeInTheDocument();
    expect(loggers.audio.warn).toHaveBeenCalled();
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
      const { container } = mount();
      const clickOutcomes: boolean[] = [];
      const enabledBeforeClick: boolean[] = [];

      for (const step of steps) {
        const loadsBefore = ctl.load.mock.calls.length;
        switch (step) {
          case 'click': {
            enabledBeforeClick.push(!playButton(container).disabled);
            await press(container);
            // The element plays, or a load was started, or the error is visible
            // while the element is loadable again (the next click plays).
            const loadGrew = ctl.load.mock.calls.length > loadsBefore;
            const errorVisibleOnLoadableElement =
              screen.queryByText(PLAY_ERROR_TEXT) !== null && ctl.error === null;
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
            // The reload's loadstart shows a loading state that ends with the
            // safety timeout when the test does not load the clip.
            await vi.advanceTimersByTimeAsync(CANPLAY_TIMEOUT_MS);
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
      expect(enabledBeforeClick.every(Boolean)).toBe(true);
      expect(clickOutcomes.every(Boolean)).toBe(true);
    });
  });
});
