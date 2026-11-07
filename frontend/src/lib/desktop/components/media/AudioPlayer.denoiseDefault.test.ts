/**
 * Playback denoise default in AudioPlayer: the toolbar starts on the default, the
 * first play loads the GET denoise URL, and toolbar states that equal the default
 * return to that URL instead of posting /process. Uses the fake media element,
 * which models the load algorithm.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, fireEvent, cleanup, render } from '@testing-library/svelte';
import {
  installFakeMediaElement,
  fakeAudioNodeChain,
  settle,
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

// The default the player reads when it mounts.
const playbackDefault = vi.hoisted(() => ({ preset: 'light-medium' }));
vi.mock('$lib/utils/playbackDenoise', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/playbackDenoise')>()),
  getPlaybackDenoiseDefault: () => playbackDefault.preset,
}));

const PRESET = 'light-medium';
const DETECTION_ID = '4242';
const STORED_URL = `/api/v2/audio/${DETECTION_ID}`;
const DENOISED_URL = `${STORED_URL}?denoise=${PRESET}`;
const PLAY_LABEL = 'media.audio.play';
const DENOISE_LABEL = 'components.audioPlayer.processing.denoise';
const NORMALIZE_LABEL = 'components.audioPlayer.processing.normalize';
const DENOISE_LIGHT_MEDIUM_TEXT = 'components.audioPlayer.processing.denoiseLightMedium';
const DENOISE_OFF_TEXT = 'components.audioPlayer.processing.denoiseOff';
const DENOISE_HEAVY_TEXT = 'components.audioPlayer.processing.denoiseHeavy';
const BATS_TITLE = 'media.audio.audibleBats.title';
const BATS_ENABLE_TEXT = 'media.audio.audibleBats.enable';
const PROCESSING_DEBOUNCE_MS = 300;
const PROCESSED_BLOB_URL = 'blob:processed-clip';
const BATS_BLOB_URL = 'blob:audible-bats-clip';
const WIDE_CONTAINER_PX = 400;

describe('AudioPlayer playback denoise default', () => {
  let ctl: FakeMediaController;
  const originalFetch = globalThis.fetch;
  const originalCreateObjectURL = URL.createObjectURL;
  const originalRevokeObjectURL = URL.revokeObjectURL;
  const originalOffsetWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth');
  let fetchMock: ReturnType<typeof vi.fn>;

  function mount(props: Record<string, unknown> = {}) {
    return render(AudioPlayer, {
      props: {
        audioUrl: STORED_URL,
        detectionId: DETECTION_ID,
        enableClipExtraction: true,
        showSpectrogram: false,
        ...props,
      },
    });
  }

  function elementPath(): string {
    const url = new URL(ctl.element.src);
    return `${url.pathname}${url.search}`;
  }

  async function press() {
    await fireEvent.click(screen.getByLabelText(PLAY_LABEL));
    await settle();
  }

  async function chooseDenoise(text: string) {
    await fireEvent.click(screen.getByLabelText(DENOISE_LABEL));
    await fireEvent.click(screen.getByText(text));
    await vi.advanceTimersByTimeAsync(PROCESSING_DEBOUNCE_MS);
    await settle();
  }

  async function toggleNormalize() {
    await fireEvent.click(screen.getByLabelText(NORMALIZE_LABEL));
    await vi.advanceTimersByTimeAsync(PROCESSING_DEBOUNCE_MS);
    await settle();
  }

  function postedUrls(): string[] {
    return fetchMock.mock.calls
      .map(call => String(call[0]))
      .filter(url => url.includes('/process') || url.includes('/audible-bats'));
  }

  beforeEach(() => {
    vi.useFakeTimers();
    playbackDefault.preset = PRESET;
    ctl = installFakeMediaElement();
    vi.mocked(attachAudioGraphWhenRunning).mockReset();
    vi.mocked(attachAudioGraphWhenRunning).mockImplementation(() => fakeAudioNodeChain());
    URL.createObjectURL = vi.fn(() => PROCESSED_BLOB_URL);
    URL.revokeObjectURL = vi.fn();
    fetchMock = vi.fn().mockImplementation(async () => ({
      ok: true,
      status: 200,
      blob: async () => new Blob(['processed']),
      headers: new Headers(),
    }));
    globalThis.fetch = fetchMock as unknown as typeof globalThis.fetch;
  });

  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    ctl.restore();
    globalThis.fetch = originalFetch;
    URL.createObjectURL = originalCreateObjectURL;
    URL.revokeObjectURL = originalRevokeObjectURL;
    if (originalOffsetWidth) {
      Object.defineProperty(HTMLElement.prototype, 'offsetWidth', originalOffsetWidth);
    }
  });

  it('starts the toolbar on the default and loads the stored clip until a play', async () => {
    mount();
    await settle();

    expect(screen.getByText(DENOISE_LIGHT_MEDIUM_TEXT)).toBeInTheDocument();
    expect(elementPath()).toBe(STORED_URL);
    expect(postedUrls()).toHaveLength(0);
  });

  it('plays the GET denoise URL without posting any processing request', async () => {
    mount();
    ctl.completeLoad();

    await press();

    expect(elementPath()).toBe(DENOISED_URL);
    expect(ctl.paused).toBe(false);
    expect(postedUrls()).toHaveLength(0);
  });

  it('plays the stored clip after the toolbar is set to Off', async () => {
    mount();
    ctl.completeLoad();

    await chooseDenoise(DENOISE_OFF_TEXT);
    ctl.completeLoad();
    await settle();
    await press();

    expect(elementPath()).toBe(STORED_URL);
    expect(postedUrls()).toHaveLength(0);
  });

  it('keeps the stored clip when no default is set', async () => {
    playbackDefault.preset = '';
    mount();
    ctl.completeLoad();

    await press();

    expect(elementPath()).toBe(STORED_URL);
  });

  it('returns to the GET denoise URL when normalize is switched on and off again', async () => {
    mount();
    ctl.completeLoad();
    await press();
    expect(elementPath()).toBe(DENOISED_URL);

    await toggleNormalize();
    expect(postedUrls()).toHaveLength(2);
    expect(ctl.element.src).toBe(PROCESSED_BLOB_URL);

    await toggleNormalize();
    expect(elementPath()).toBe(DENOISED_URL);
    // No further processing request, neither audio nor spectrogram.
    expect(postedUrls()).toHaveLength(2);
  });

  it('returns to the GET denoise URL when the default is picked again after another strength', async () => {
    mount();
    ctl.completeLoad();

    await chooseDenoise(DENOISE_HEAVY_TEXT);
    expect(ctl.element.src).toBe(PROCESSED_BLOB_URL);
    const postsAfterHeavy = postedUrls().length;

    await chooseDenoise(DENOISE_LIGHT_MEDIUM_TEXT);
    expect(elementPath()).toBe(DENOISED_URL);
    expect(postedUrls()).toHaveLength(postsAfterHeavy);
  });

  describe('with audible bats active', () => {
    beforeEach(() => {
      Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
        configurable: true,
        get: () => WIDE_CONTAINER_PX,
      });
      URL.createObjectURL = vi.fn((blob: Blob | MediaSource) =>
        blob instanceof Blob && blob.size > 0 && blob.type === 'bats'
          ? BATS_BLOB_URL
          : PROCESSED_BLOB_URL
      );
      fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
        const isBats = String(input).includes('/audible-bats');
        return {
          ok: true,
          status: 200,
          blob: async () =>
            new Blob([isBats ? 'bats' : 'processed'], { type: isBats ? 'bats' : '' }),
          headers: new Headers(),
        };
      });
    });

    async function activateAudibleBats() {
      await fireEvent.click(screen.getByLabelText(BATS_TITLE));
      await fireEvent.click(screen.getByText(BATS_ENABLE_TEXT));
      await settle();
    }

    it('turns audible bats off before a denoise change replaces the source', async () => {
      mount({ modelType: 'bat' });
      ctl.completeLoad();
      await activateAudibleBats();
      expect(ctl.element.src).toBe(BATS_BLOB_URL);

      await chooseDenoise(DENOISE_HEAVY_TEXT);

      expect(ctl.element.src).toBe(PROCESSED_BLOB_URL);
      expect(URL.revokeObjectURL).toHaveBeenCalledWith(BATS_BLOB_URL);
    });

    it('turns audible bats off before a normalize change replaces the source', async () => {
      mount({ modelType: 'bat' });
      ctl.completeLoad();
      await activateAudibleBats();
      expect(ctl.element.src).toBe(BATS_BLOB_URL);

      await toggleNormalize();

      expect(ctl.element.src).toBe(PROCESSED_BLOB_URL);
      expect(URL.revokeObjectURL).toHaveBeenCalledWith(BATS_BLOB_URL);
    });
  });
});
