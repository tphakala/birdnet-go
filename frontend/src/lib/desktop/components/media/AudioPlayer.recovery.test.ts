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
import { AUDIO_RETRY_DELAY_MS, CANPLAY_TIMEOUT_MS } from '$lib/utils/audio';
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
const LOADING_LABEL = 'media.audio.loading';
const PLAY_ERROR_TEXT = 'media.audio.playError';
const LOAD_ERROR_TEXT = 'media.audio.error';
const DENOISE_LABEL = 'components.audioPlayer.processing.denoise';
const DENOISE_LIGHT_TEXT = 'components.audioPlayer.processing.denoiseLight';
const DENOISE_OFF_TEXT = 'components.audioPlayer.processing.denoiseOff';
// Debounce before a processing request starts (PROCESS_DEBOUNCE_MS in AudioPlayer.svelte)
const PROCESSING_DEBOUNCE_MS = 300;
const PROCESSED_BLOB_URL = 'blob:processed-clip';

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
});
