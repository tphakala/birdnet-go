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
  fakeAudioNodeChain,
  settle,
  type FakeMediaController,
} from '../../../../test/fakeMediaElement';
import {
  NEVER_SILENT_SEQUENCES,
  exhaustLoadRetries,
  runPlaySequence,
} from '../../../../test/playSequences';
import { MAX_AUDIO_LOAD_RETRIES, AUDIO_RETRY_DELAY_MS, CANPLAY_TIMEOUT_MS } from '$lib/utils/audio';
import { loggers } from '$lib/utils/logger';
import { mockAudioProbe, probeCalls } from '../../../../test/audioProbeMock';
import { attachAudioGraphWhenRunning } from '$lib/utils/audioNodes';
import {
  clearSpectrogramSourceMarks,
  isSpectrogramSourceKnownMissing,
  markSpectrogramSourceMissing,
} from '$lib/utils/spectrogramStatus';
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
const LOADING_LABEL = 'media.audio.loading';
const PLAY_ERROR_TEXT = 'media.audio.playError';
const LOAD_ERROR_TEXT = 'media.audio.error';
const DENOISE_LABEL = 'components.audioPlayer.processing.denoise';
const DENOISE_LIGHT_TEXT = 'components.audioPlayer.processing.denoiseLight';
const DENOISE_OFF_TEXT = 'components.audioPlayer.processing.denoiseOff';
// Debounce before a processing request starts (PROCESS_DEBOUNCE_MS in AudioPlayer.svelte)
const PROCESSING_DEBOUNCE_MS = 300;
const PROCESSED_BLOB_URL = 'blob:processed-clip';
const BATS_BLOB_URL = 'blob:audible-bats-clip';
const BATS_TITLE = 'media.audio.audibleBats.title';
const BATS_ENABLE_TEXT = 'media.audio.audibleBats.enable';
// Wider than MIN_CONTROLS_WIDTH_PX so the player shows its controls
const WIDE_CONTAINER_PX = 400;

describe('AudioPlayer recovery', () => {
  let ctl: FakeMediaController;
  const mockedAttach = vi.mocked(attachAudioGraphWhenRunning);

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

  beforeEach(() => {
    vi.useFakeTimers();
    ctl = installFakeMediaElement();
    mockedAttach.mockReset();
    mockedAttach.mockImplementation(() => fakeAudioNodeChain());
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
    await exhaustLoadRetries(ctl);

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent(LOAD_ERROR_TEXT);
    expect(alert.className).toContain('pointer-events-none');
    expect(playButton(container).disabled).toBe(false);
  });

  it('reloads the clip when play is pressed after load retries are exhausted', async () => {
    const { container } = mount();
    await exhaustLoadRetries(ctl);
    const loadsBefore = ctl.load.mock.calls.length;

    await press(container);

    expect(ctl.load.mock.calls.length).toBe(loadsBefore + 1);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    ctl.completeLoad();
    await settle();
    expect(ctl.paused).toBe(false);
  });

  it('does not read Pause while a reload after a failure is loading', async () => {
    const { container } = mount();
    await exhaustLoadRetries(ctl);

    await press(container);

    expect(playButton(container).getAttribute('aria-label')).toBe(LOADING_LABEL);
    ctl.completeLoad();
    await settle();
    expect(playButton(container).getAttribute('aria-label')).toBe(PAUSE_LABEL);
  });

  it('queues play when the reload hits a transient error and plays once loaded', async () => {
    const { container } = mount();
    await exhaustLoadRetries(ctl);
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

  it('cancels a pending retry when play is pressed and keeps playing past the old timer', async () => {
    const { container } = mount();
    ctl.failLoad();
    const loadsBefore = ctl.load.mock.calls.length;

    await press(container);
    expect(ctl.load.mock.calls.length).toBe(loadsBefore + 1);
    await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);

    expect(ctl.load.mock.calls.length).toBe(loadsBefore + 1);
    expect(ctl.paused).toBe(false);
  });

  it('reloads instead of pausing when play is pressed on a playing element in the error state', async () => {
    const { container } = mount();
    await press(container);
    ctl.failLoad(MEDIA_ERR_NETWORK);
    const loadsBefore = ctl.load.mock.calls.length;
    const playsBefore = ctl.play.mock.calls.length;

    await press(container);

    expect(ctl.load.mock.calls.length).toBe(loadsBefore + 1);
    expect(ctl.play.mock.calls.length).toBe(playsBefore + 1);
  });

  it('drops the queued play and keeps the error when retries run out', async () => {
    const { container } = mount();
    ctl.setPlayMode('hold');
    await fireEvent.click(playButton(container));
    await settle();
    ctl.failLoad();
    for (let i = 1; i < MAX_AUDIO_LOAD_RETRIES; i++) {
      await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
      ctl.failLoad();
    }
    await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
    ctl.failLoad();
    await settle();
    expect(screen.getByRole('alert')).toHaveTextContent(LOAD_ERROR_TEXT);
    expect(screen.getByLabelText(PLAY_LABEL)).toBeInTheDocument();
    const playsBefore = ctl.play.mock.calls.length;

    ctl.completeLoad();
    await settle();

    expect(ctl.play.mock.calls.length).toBe(playsBefore);
  });

  describe('audible bats swap', () => {
    const originalFetch = globalThis.fetch;
    const originalOffsetWidth = Object.getOwnPropertyDescriptor(
      HTMLElement.prototype,
      'offsetWidth'
    );
    const originalCreateObjectURL = URL.createObjectURL;
    const originalRevokeObjectURL = URL.revokeObjectURL;

    beforeEach(() => {
      // The controls (and the bats button) render only above a minimum width.
      Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
        configurable: true,
        get: () => WIDE_CONTAINER_PX,
      });
      URL.createObjectURL = vi.fn(() => BATS_BLOB_URL);
      URL.revokeObjectURL = vi.fn();
      globalThis.fetch = vi.fn().mockImplementation(async () => ({
        ok: true,
        status: 200,
        blob: async () => new Blob(['bats']),
        headers: new Headers(),
      }));
    });

    afterEach(() => {
      clearSpectrogramSourceMarks();
      globalThis.fetch = originalFetch;
      URL.createObjectURL = originalCreateObjectURL;
      URL.revokeObjectURL = originalRevokeObjectURL;
      if (originalOffsetWidth) {
        Object.defineProperty(HTMLElement.prototype, 'offsetWidth', originalOffsetWidth);
      }
    });

    async function activateAudibleBats() {
      await fireEvent.click(screen.getByLabelText(BATS_TITLE));
      await fireEvent.click(screen.getByText(BATS_ENABLE_TEXT));
      await settle();
    }

    it('does not probe a failing audible-bats source', async () => {
      mount({ modelType: 'bat' });
      await activateAudibleBats();

      ctl.failLoad();
      await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);

      expect(probeCalls()).toHaveLength(0);
    });

    it('keeps retrying an audible-bats source while the original is reported gone', async () => {
      markSpectrogramSourceMissing('test-123');
      mount({ modelType: 'bat' });
      await activateAudibleBats();
      const loadsBefore = ctl.load.mock.calls.length;

      ctl.failLoad();
      await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);

      expect(ctl.load.mock.calls.length).toBe(loadsBefore + 1);
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });

    it('resumes playback on the swapped source', async () => {
      const { container } = mount({ modelType: 'bat' });
      await press(container);
      expect(ctl.paused).toBe(false);
      const playsBefore = ctl.play.mock.calls.length;

      await activateAudibleBats();
      expect(ctl.paused).toBe(true);
      ctl.completeLoad();
      await settle();

      expect(ctl.play.mock.calls.length).toBe(playsBefore + 1);
      expect(ctl.paused).toBe(false);
    });

    it('keeps a queued play across the swap', async () => {
      const { container } = mount({ modelType: 'bat' });
      ctl.failLoad();
      ctl.setPlayMode('hold');
      await fireEvent.click(playButton(container));
      await settle();
      ctl.failLoad();
      // Playback is not running while the retry waits.
      ctl.dispatch('pause');
      const playsBefore = ctl.play.mock.calls.length;

      await activateAudibleBats();
      ctl.completeLoad();
      await settle();

      expect(ctl.play.mock.calls.length).toBe(playsBefore + 1);
      expect(ctl.paused).toBe(false);
    });
  });

  it('drops a queued play when processing starts', async () => {
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
      ctl.failLoad();
      ctl.setPlayMode('hold');
      await fireEvent.click(screen.getByLabelText(PLAY_LABEL));
      await settle();
      ctl.failLoad();
      ctl.dispatch('pause');
      const playsBefore = ctl.play.mock.calls.length;

      await fireEvent.click(screen.getByLabelText(DENOISE_LABEL));
      await fireEvent.click(screen.getByText(DENOISE_LIGHT_TEXT));
      await vi.advanceTimersByTimeAsync(PROCESSING_DEBOUNCE_MS);
      await settle();
      ctl.completeLoad();
      await settle();

      expect(ctl.play.mock.calls.length).toBe(playsBefore);
    } finally {
      globalThis.fetch = originalFetch;
      URL.createObjectURL = originalCreateObjectURL;
      URL.revokeObjectURL = originalRevokeObjectURL;
    }
  });

  it.each(NEVER_SILENT_SEQUENCES)(
    'a play click is never a silent no-op after: $name',
    async ({ steps }) => {
      const { container } = mount();

      await runPlaySequence(steps, ctl, {
        click: async () => {
          const loadsBefore = ctl.load.mock.calls.length;
          expect(playButton(container).disabled).toBe(false);
          await press(container);
          // The element plays, or a load was started, or the error is visible
          // while the element is loadable again (the next click plays).
          const loadGrew = ctl.load.mock.calls.length > loadsBefore;
          const errorVisibleOnLoadableElement =
            screen.queryByText(PLAY_ERROR_TEXT) !== null && ctl.error === null;
          expect(!ctl.paused || loadGrew || errorVisibleOnLoadableElement).toBe(true);
        },
        advanceRetry: async () => {
          await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
          // The reload's loadstart shows a loading state that ends with the
          // safety timeout when the test does not load the clip.
          await vi.advanceTimersByTimeAsync(CANPLAY_TIMEOUT_MS);
        },
      });
    }
  );

  describe('spectrogram retry and error state', () => {
    const SPECTROGRAM_ALT = 'components.audio.spectrogramAlt';
    const SPECTROGRAM_UNAVAILABLE_TEXT = 'components.audio.spectrogramUnavailable';
    // Longer than the sum of all retry backoffs (500 + 1000 + 2000 + 4000 ms)
    const PAST_ALL_BACKOFFS_MS = 10_000;
    // First entry of SPECTROGRAM_RETRY_DELAYS in AudioPlayer.svelte
    const SPECTROGRAM_FIRST_RETRY_DELAY_MS = 500;
    // SPECTROGRAM_POLL_INTERVAL in AudioPlayer.svelte
    const POLL_INTERVAL_MS = 2000;
    // MAX_POLL_DURATION in AudioPlayer.svelte
    const MAX_POLL_DURATION_MS = 5 * 60 * 1000;
    const SERVER_ERROR_STATUS = 500;
    const NOT_FOUND_STATUS = 404;
    // Failed status answers before the endpoint recovers, enough to outlast the image retries
    const STATUS_ERRORS_BEFORE_RECOVERY = 8;
    let originalFetch: typeof globalThis.fetch;

    function statusResponse(status: string, httpStatus = 200, reason?: string): Response {
      return new Response(JSON.stringify({ status, ...(reason ? { reason } : {}) }), {
        status: httpStatus,
        headers: { 'Content-Type': 'application/json' },
      });
    }

    // Answers the status endpoint from a queue, one entry per request.
    function stubStatusFetch(responses: Response[]) {
      const stub = vi.fn(async () => {
        const next = responses.shift();
        if (!next) throw new Error('unexpected extra status request');
        return next;
      });
      globalThis.fetch = stub;
      return stub;
    }

    function spectrogramImg(): HTMLImageElement {
      return screen.getByAltText<HTMLImageElement>(SPECTROGRAM_ALT);
    }

    beforeEach(() => {
      originalFetch = globalThis.fetch;
    });

    afterEach(() => {
      globalThis.fetch = originalFetch;
      clearSpectrogramSourceMarks();
    });

    describe('status reason', () => {
      it.each([
        ['not_started', 'source_missing'],
        ['not_started', 'source_invalid'],
      ])(
        'ends every retry within one tick for a failed image and %s with %s',
        async (status, reason) => {
          const stub = stubStatusFetch([statusResponse(status, 200, reason)]);
          mount();

          await fireEvent.error(spectrogramImg());
          await vi.advanceTimersByTimeAsync(0);

          expect(screen.getByText(SPECTROGRAM_UNAVAILABLE_TEXT)).toBeInTheDocument();
          await vi.advanceTimersByTimeAsync(PAST_ALL_BACKOFFS_MS + MAX_POLL_DURATION_MS);
          expect(spectrogramImg().getAttribute('src')).not.toContain('retry=');
          expect(stub).toHaveBeenCalledTimes(1);
        }
      );

      it.each(['source_pending', 'busy', 'render_failed'])(
        'keeps the capped image retries for a failed status with %s',
        async reason => {
          const stub = stubStatusFetch([statusResponse('failed', 200, reason)]);
          mount();

          await fireEvent.error(spectrogramImg());
          await vi.advanceTimersByTimeAsync(SPECTROGRAM_FIRST_RETRY_DELAY_MS);

          expect(spectrogramImg().getAttribute('src')).toContain('retry=1');
          expect(screen.queryByText(SPECTROGRAM_UNAVAILABLE_TEXT)).not.toBeInTheDocument();
          expect(stub).toHaveBeenCalledTimes(1);
        }
      );

      it('marks the clip as known missing for source_missing only', async () => {
        stubStatusFetch([statusResponse('not_started', 200, 'source_missing')]);
        mount();
        await fireEvent.error(spectrogramImg());
        await vi.advanceTimersByTimeAsync(0);
        expect(isSpectrogramSourceKnownMissing('test-123')).toBe(true);

        cleanup();
        clearSpectrogramSourceMarks();
        stubStatusFetch([statusResponse('failed', 200, 'source_invalid')]);
        mount({ detectionId: 'other-1' });
        await fireEvent.error(spectrogramImg());
        await vi.advanceTimersByTimeAsync(0);
        expect(isSpectrogramSourceKnownMissing('other-1')).toBe(false);
      });

      it('marks the clip as known missing when failed carries source_missing', async () => {
        stubStatusFetch([statusResponse('failed', 200, 'source_missing')]);
        mount();

        await fireEvent.error(spectrogramImg());
        await vi.advanceTimersByTimeAsync(0);

        expect(isSpectrogramSourceKnownMissing('test-123')).toBe(true);
      });

      it('ends the image retries when a later poll reports a terminal reason', async () => {
        stubStatusFetch([
          statusResponse('generating'),
          statusResponse('not_started', 200, 'source_missing'),
        ]);
        mount();
        await fireEvent.error(spectrogramImg());
        await vi.advanceTimersByTimeAsync(SPECTROGRAM_FIRST_RETRY_DELAY_MS);
        expect(spectrogramImg().getAttribute('src')).toContain('retry=1');

        await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
        await vi.advanceTimersByTimeAsync(PAST_ALL_BACKOFFS_MS);

        expect(screen.getByText(SPECTROGRAM_UNAVAILABLE_TEXT)).toBeInTheDocument();
        expect(spectrogramImg().getAttribute('src')).not.toContain('retry=2');
      });

      it.each(['source_pending', 'not_generated'])(
        'keeps the image retries going for not_started with %s',
        async reason => {
          stubStatusFetch([statusResponse('not_started', 200, reason)]);
          mount();

          await fireEvent.error(spectrogramImg());
          await vi.advanceTimersByTimeAsync(SPECTROGRAM_FIRST_RETRY_DELAY_MS);

          expect(spectrogramImg().getAttribute('src')).toContain('retry=1');
          expect(screen.queryByText(SPECTROGRAM_UNAVAILABLE_TEXT)).not.toBeInTheDocument();
        }
      );
    });

    it('stops the image retries when the status poll reports failed', async () => {
      stubStatusFetch([statusResponse('failed')]);
      mount();

      await fireEvent.error(spectrogramImg());
      await vi.advanceTimersByTimeAsync(PAST_ALL_BACKOFFS_MS);

      expect(spectrogramImg().getAttribute('src')).not.toContain('retry=');
      expect(screen.getByText(SPECTROGRAM_UNAVAILABLE_TEXT)).toBeInTheDocument();
    });

    it('clears the unavailable overlay when the image loads after a failure', async () => {
      stubStatusFetch([statusResponse('failed')]);
      mount();
      await fireEvent.error(spectrogramImg());
      await vi.advanceTimersByTimeAsync(0);
      expect(screen.getByText(SPECTROGRAM_UNAVAILABLE_TEXT)).toBeInTheDocument();

      await fireEvent.load(spectrogramImg());

      expect(screen.queryByText(SPECTROGRAM_UNAVAILABLE_TEXT)).not.toBeInTheDocument();
      expect(spectrogramImg().classList.contains('invisible')).toBe(false);
    });

    it('starts a fresh request with retry count 0 when the detection changes after a failure', async () => {
      stubStatusFetch([statusResponse('not_started')]);
      const { rerender } = mount();
      // Exhaust the retries so the loader ends in the error state
      for (let i = 0; i < 5; i++) {
        await fireEvent.error(spectrogramImg());
        await vi.advanceTimersByTimeAsync(PAST_ALL_BACKOFFS_MS);
      }
      expect(screen.getByText(SPECTROGRAM_UNAVAILABLE_TEXT)).toBeInTheDocument();
      expect(spectrogramImg().getAttribute('src')).toContain('retry=');

      await rerender({ audioUrl: '/audio/next.mp3', detectionId: 'next-456' });

      const src = spectrogramImg().getAttribute('src') ?? '';
      expect(src).toContain('/api/v2/spectrogram/next-456');
      expect(src).not.toContain('retry=');
      expect(screen.queryByText(SPECTROGRAM_UNAVAILABLE_TEXT)).not.toBeInTheDocument();
    });

    it('stops polling after the maximum poll duration when every status request fails', async () => {
      const stub = vi.fn(async () => statusResponse('', SERVER_ERROR_STATUS));
      globalThis.fetch = stub;
      mount();
      await fireEvent.error(spectrogramImg());

      await vi.advanceTimersByTimeAsync(MAX_POLL_DURATION_MS + POLL_INTERVAL_MS * 2);
      const callsAtLimit = stub.mock.calls.length;
      await vi.advanceTimersByTimeAsync(MAX_POLL_DURATION_MS);

      // Polling kept going until the limit, then stopped for good
      expect(callsAtLimit).toBeGreaterThan(MAX_POLL_DURATION_MS / POLL_INTERVAL_MS / 2);
      expect(stub.mock.calls.length).toBe(callsAtLimit);
    });

    it('keeps polling after the status request throws a network error', async () => {
      const stub = vi
        .fn()
        .mockRejectedValueOnce(new TypeError('network down'))
        .mockResolvedValueOnce(statusResponse('failed'));
      globalThis.fetch = stub;
      mount();

      await fireEvent.error(spectrogramImg());
      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);

      expect(stub).toHaveBeenCalledTimes(2);
      expect(screen.getByText(SPECTROGRAM_UNAVAILABLE_TEXT)).toBeInTheDocument();
    });

    it('does not restart the image retries when failed arrives while a retry is in flight', async () => {
      let answer: (r: Response) => void = () => {};
      globalThis.fetch = vi.fn(
        () =>
          new Promise<Response>(resolve => {
            answer = resolve;
          })
      );
      mount();
      await fireEvent.error(spectrogramImg());
      // The first retry fires and its image request is now in flight
      await vi.advanceTimersByTimeAsync(SPECTROGRAM_FIRST_RETRY_DELAY_MS);
      expect(spectrogramImg().getAttribute('src')).toContain('retry=1');

      answer(statusResponse('failed'));
      await vi.advanceTimersByTimeAsync(0);
      // The in-flight image request fails after the status answer
      await fireEvent.error(spectrogramImg());
      await vi.advanceTimersByTimeAsync(PAST_ALL_BACKOFFS_MS);

      expect(spectrogramImg().getAttribute('src')).toContain('retry=1');
      expect(spectrogramImg().getAttribute('src')).not.toContain('retry=2');
      expect(screen.getByText(SPECTROGRAM_UNAVAILABLE_TEXT)).toBeInTheDocument();
    });

    it('retries again after a failed status once a later attempt starts', async () => {
      stubStatusFetch([statusResponse('failed'), statusResponse('generating')]);
      mount();
      await fireEvent.error(spectrogramImg());
      await vi.advanceTimersByTimeAsync(0);
      // A late load ends the failed attempt; the next failure begins a new one
      await fireEvent.load(spectrogramImg());
      await fireEvent.error(spectrogramImg());
      await vi.advanceTimersByTimeAsync(SPECTROGRAM_FIRST_RETRY_DELAY_MS);

      expect(spectrogramImg().getAttribute('src')).toContain('retry=1');
    });

    it('ends polling at a permanent client error from the status endpoint', async () => {
      const stub = vi.fn(async () => statusResponse('', NOT_FOUND_STATUS));
      globalThis.fetch = stub;
      mount();

      await fireEvent.error(spectrogramImg());
      await vi.advanceTimersByTimeAsync(MAX_POLL_DURATION_MS);

      expect(stub).toHaveBeenCalledTimes(1);
    });

    it('still reloads the image when the status recovers after the image retries ran out', async () => {
      const stub = stubStatusFetch([
        ...Array.from({ length: STATUS_ERRORS_BEFORE_RECOVERY }, () =>
          statusResponse('', SERVER_ERROR_STATUS)
        ),
        statusResponse('exists'),
      ]);
      mount();
      await fireEvent.error(spectrogramImg());
      // Run every image retry out at its backoff; each retry request fails in turn
      for (const backoffMs of [500, 1000, 2000, 4000]) {
        await vi.advanceTimersByTimeAsync(backoffMs);
        await fireEvent.error(spectrogramImg());
      }
      expect(screen.getByText(SPECTROGRAM_UNAVAILABLE_TEXT)).toBeInTheDocument();
      // The status endpoint has not recovered yet, so the poll is still running
      expect(stub.mock.calls.length).toBeLessThan(STATUS_ERRORS_BEFORE_RECOVERY);

      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS * STATUS_ERRORS_BEFORE_RECOVERY);

      expect(stub).toHaveBeenCalledTimes(STATUS_ERRORS_BEFORE_RECOVERY + 1);
      expect(spectrogramImg().getAttribute('src')).toContain('cache=1');
    });

    it('does not keep polling when a cancelled poll rejects with a non-abort error', async () => {
      let fail: (e: Error) => void = () => {};
      const stub = vi.fn(
        () =>
          new Promise<Response>((_resolve, reject) => {
            fail = reject;
          })
      );
      globalThis.fetch = stub;
      const { unmount } = mount();
      await fireEvent.error(spectrogramImg());
      expect(stub).toHaveBeenCalledTimes(1);

      unmount();
      fail(new TypeError('network down'));
      await vi.advanceTimersByTimeAsync(MAX_POLL_DURATION_MS);

      expect(stub).toHaveBeenCalledTimes(1);
    });

    it('drops the generating overlay when the image loads', async () => {
      stubStatusFetch([statusResponse('generating')]);
      mount();
      await fireEvent.error(spectrogramImg());
      await vi.advanceTimersByTimeAsync(0);
      expect(screen.getByText('components.audio.generating')).toBeInTheDocument();

      await fireEvent.load(spectrogramImg());

      expect(screen.queryByText('components.audio.generating')).not.toBeInTheDocument();
    });

    it('keeps polling after a failed status request and reloads the image once it exists', async () => {
      const stub = stubStatusFetch([
        statusResponse('', SERVER_ERROR_STATUS),
        statusResponse('generating'),
        statusResponse('exists'),
      ]);
      mount();

      await fireEvent.error(spectrogramImg());
      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS * 3);

      expect(stub).toHaveBeenCalledTimes(3);
      expect(spectrogramImg().getAttribute('src')).toContain('cache=1');
    });
  });
  describe('probe before retry', () => {
    const MISSING_STATUS = 404;
    const PENDING_STATUS = 503;
    const PARTIAL_STATUS = 206;
    const RETRY_AFTER_SECONDS = 9;
    // Far longer than every retry delay, so a stray retry would show.
    const LONG_WAIT_MS = AUDIO_RETRY_DELAY_MS * 10;
    let restoreFetch: (() => void) | undefined;

    afterEach(() => {
      restoreFetch?.();
      restoreFetch = undefined;
      clearSpectrogramSourceMarks();
    });

    it('sends one ranged probe and no retries when the clip answers 404', async () => {
      restoreFetch = mockAudioProbe({ status: MISSING_STATUS });
      mount();

      ctl.failLoad();
      await vi.advanceTimersByTimeAsync(LONG_WAIT_MS);

      expect(probeCalls()).toHaveLength(1);
      expect(probeCalls()[0]?.[0]).toBe('/audio/test.mp3');
      expect(ctl.load).not.toHaveBeenCalled();
      expect(screen.getByRole('alert')).toHaveTextContent(LOAD_ERROR_TEXT);
    });

    it('retries after Retry-After when the clip answers 503', async () => {
      restoreFetch = mockAudioProbe({
        status: PENDING_STATUS,
        headers: { 'Retry-After': String(RETRY_AFTER_SECONDS) },
      });
      mount();

      ctl.failLoad();
      await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
      expect(ctl.load).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(RETRY_AFTER_SECONDS * 1000 - AUDIO_RETRY_DELAY_MS);

      expect(ctl.load).toHaveBeenCalledTimes(1);
      expect(probeCalls()).toHaveLength(1);
    });

    it('keeps the scheduled retry when the probe answers 206', async () => {
      restoreFetch = mockAudioProbe({ status: PARTIAL_STATUS });
      mount();

      ctl.failLoad();
      await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);

      expect(probeCalls()).toHaveLength(1);
      expect(ctl.load).toHaveBeenCalledTimes(1);
    });

    it('sends no request and no retry for a clip the server reported gone', async () => {
      restoreFetch = mockAudioProbe({ status: PARTIAL_STATUS });
      markSpectrogramSourceMissing('test-123');
      mount();

      ctl.failLoad();
      await vi.advanceTimersByTimeAsync(LONG_WAIT_MS);

      expect(probeCalls()).toHaveLength(0);
      expect(ctl.load).not.toHaveBeenCalled();
      expect(screen.getByRole('alert')).toHaveTextContent(LOAD_ERROR_TEXT);
    });

    it('ignores a 404 that arrives after the retry already fired', async () => {
      restoreFetch = mockAudioProbe({ status: MISSING_STATUS, delayMs: LONG_WAIT_MS });
      mount();

      ctl.failLoad();
      await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
      expect(ctl.load).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(LONG_WAIT_MS);

      expect(probeCalls()[0]?.[1].signal?.aborted).toBe(true);
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });

    it('probes only the first failure of a cycle and again after the budget resets', async () => {
      restoreFetch = mockAudioProbe({ status: PARTIAL_STATUS });
      const { container } = mount();

      await exhaustLoadRetries(ctl);
      expect(probeCalls()).toHaveLength(1);

      await press(container);
      ctl.failLoad();
      await settle();

      expect(probeCalls()).toHaveLength(2);
    });

    it('aborts the probe when the clip loads', async () => {
      restoreFetch = mockAudioProbe({ status: MISSING_STATUS, delayMs: LONG_WAIT_MS });
      mount();

      ctl.failLoad();
      ctl.completeLoad();

      expect(probeCalls()[0]?.[1].signal?.aborted).toBe(true);
    });

    it('ignores a probe answer for a clip that is no longer the current source', async () => {
      const probeDelayMs = AUDIO_RETRY_DELAY_MS / 4;
      restoreFetch = mockAudioProbe({ status: MISSING_STATUS, delayMs: probeDelayMs });
      const { rerender } = mount();

      ctl.failLoad();
      await rerender({ audioUrl: '/audio/other.mp3', detectionId: 'other-1' });
      await vi.advanceTimersByTimeAsync(probeDelayMs * 2);

      expect(probeCalls()).toHaveLength(1);
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });

    it('aborts the probe when the player unmounts', async () => {
      restoreFetch = mockAudioProbe({ status: MISSING_STATUS, delayMs: LONG_WAIT_MS });
      mount();

      ctl.failLoad();
      cleanup();

      expect(probeCalls()[0]?.[1].signal?.aborted).toBe(true);
    });
  });
});
