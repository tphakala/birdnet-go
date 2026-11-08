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
  FAKE_MEDIA_DURATION_SECONDS,
  type FakeMediaController,
} from '../../../../test/fakeMediaElement';
import { attachAudioGraphWhenRunning } from '$lib/utils/audioNodes';
import { mockAudioProbe } from '../../../../test/audioProbeMock';
import { AUDIO_RETRY_DELAY_MS, MAX_AUDIO_LOAD_RETRIES } from '$lib/utils/audio';
import { DENOISE_BUTTON_NAME } from '../../../../test/audioPlayerQueries';
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
const SELECTION_START_PX = 40;
const SELECTION_END_PX = 200;

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
    await fireEvent.click(screen.getByRole('button', { name: DENOISE_BUTTON_NAME }));
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
    // The mock answers only the fields the player reads from a Response.
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

  it('does not show the processing badge for the default itself', async () => {
    mount({ enableClipExtraction: false });
    await settle();

    expect(screen.queryByText('components.audioPlayer.processing.processingActive')).toBeNull();
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

  it('switches to the GET denoise URL when a selection is played before any other play', async () => {
    const originalRect = Element.prototype.getBoundingClientRect;
    Element.prototype.getBoundingClientRect = () =>
      new DOMRect(0, 0, WIDE_CONTAINER_PX, WIDE_CONTAINER_PX);
    try {
      const { container } = mount();
      ctl.completeLoad();
      await settle();
      const player = container.firstElementChild;
      if (!(player instanceof HTMLElement)) throw new Error('player container missing');
      await fireEvent.mouseDown(player, { button: 0, clientX: SELECTION_START_PX });
      await fireEvent.mouseMove(document, { buttons: 1, clientX: SELECTION_END_PX });
      await fireEvent.mouseUp(document, { clientX: SELECTION_END_PX });

      await press();

      expect(elementPath()).toBe(DENOISED_URL);
      // Playing the selection starts at its start; a plain play would stay at 0.
      expect(ctl.element.currentTime).toBe(
        (SELECTION_START_PX / WIDE_CONTAINER_PX) * FAKE_MEDIA_DURATION_SECONDS
      );
    } finally {
      Element.prototype.getBoundingClientRect = originalRect;
    }
  });

  describe('download link of the guest overlay', () => {
    const downloadHref = () => screen.getByLabelText('media.audio.download').getAttribute('href');

    it('follows the default while it applies and the stored clip otherwise', async () => {
      mount({ enableClipExtraction: false });
      ctl.completeLoad();
      expect(downloadHref()).toBe(DENOISED_URL);

      playbackDefault.preset = '';
      cleanup();
      mount({ enableClipExtraction: false });
      expect(downloadHref()).toBe(STORED_URL);
    });
  });

  describe('when the default denoised clip fails', () => {
    const SERVER_ERROR_STATUS = 500;
    const PENDING_STATUS = 503;
    const NOT_FOUND_STATUS = 404;
    const SEEK_POSITION_SECONDS = 12;
    let restoreFetch: (() => void) | undefined;

    afterEach(() => {
      restoreFetch?.();
      restoreFetch = undefined;
    });

    it('falls back to the stored clip at the same position and keeps playing', async () => {
      restoreFetch = mockAudioProbe({ status: SERVER_ERROR_STATUS });
      mount();
      ctl.completeLoad();
      ctl.element.currentTime = SEEK_POSITION_SECONDS;
      await press();
      const playsBefore = ctl.play.mock.calls.length;

      ctl.failLoad();
      await settle();

      expect(elementPath()).toBe(STORED_URL);
      expect(ctl.element.currentTime).toBe(SEEK_POSITION_SECONDS);
      expect(ctl.play.mock.calls.length).toBe(playsBefore + 1);
      expect(screen.queryByRole('alert')).toBeNull();
    });

    it('gives the denoised clip a fresh retry budget when the first play lands during a stored clip retry', async () => {
      restoreFetch = mockAudioProbe({ status: SERVER_ERROR_STATUS });
      mount();
      // The stored clip fails its metadata load, leaving a retry pending.
      ctl.failLoad();
      await settle();
      await press();
      expect(elementPath()).toBe(DENOISED_URL);

      ctl.failLoad();
      await settle();

      // The denoised clip's first failure is probed, so the 500 falls back at once.
      expect(elementPath()).toBe(STORED_URL);
    });

    it('keeps the player paused when the denoised clip fails before any play', async () => {
      restoreFetch = mockAudioProbe({ status: SERVER_ERROR_STATUS });
      mount();
      ctl.completeLoad();
      await chooseDenoise(DENOISE_HEAVY_TEXT);
      // Picking the default again loads its denoised clip while paused.
      await chooseDenoise(DENOISE_LIGHT_MEDIUM_TEXT);
      expect(elementPath()).toBe(DENOISED_URL);
      const playsBefore = ctl.play.mock.calls.length;

      ctl.failLoad();
      await settle();

      expect(elementPath()).toBe(STORED_URL);
      expect(ctl.play.mock.calls.length).toBe(playsBefore);
    });

    it('points the download link at the stored clip after the fallback', async () => {
      restoreFetch = mockAudioProbe({ status: SERVER_ERROR_STATUS });
      // The download link belongs to the guest overlay, which has no clip extraction.
      mount({ enableClipExtraction: false });
      ctl.completeLoad();
      await press();
      expect(screen.getByLabelText('media.audio.download')).toHaveAttribute('href', DENOISED_URL);

      ctl.failLoad();
      await settle();

      expect(screen.getByLabelText('media.audio.download')).toHaveAttribute('href', STORED_URL);
    });

    it('does not ask for the denoised clip again after the fallback', async () => {
      restoreFetch = mockAudioProbe({ status: SERVER_ERROR_STATUS });
      mount();
      ctl.completeLoad();
      await press();
      ctl.failLoad();
      await settle();
      ctl.completeLoad();
      await settle();

      await toggleNormalize();
      await toggleNormalize();

      expect(elementPath()).toBe(STORED_URL);
    });

    it('retries the denoised clip on schedule while its probe has not answered, then falls back', async () => {
      restoreFetch = mockAudioProbe({
        status: SERVER_ERROR_STATUS,
        delayMs: AUDIO_RETRY_DELAY_MS * 10,
      });
      mount();
      ctl.completeLoad();
      await press();
      const loadsBefore = ctl.load.mock.calls.length;

      ctl.failLoad();
      await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);

      expect(elementPath()).toBe(DENOISED_URL);
      expect(ctl.load.mock.calls.length).toBe(loadsBefore + 1);

      // The retry dropped the slow probe; the spent budget still falls back.
      for (let i = 1; i < MAX_AUDIO_LOAD_RETRIES; i++) {
        ctl.failLoad();
        await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
      }
      ctl.failLoad();
      await settle();

      expect(elementPath()).toBe(STORED_URL);
      expect(screen.queryByRole('alert')).toBeNull();
    });

    it('ends with the load error when the denoised clip is gone (404)', async () => {
      restoreFetch = mockAudioProbe({ status: NOT_FOUND_STATUS });
      mount();
      ctl.completeLoad();
      await press();

      ctl.failLoad();
      await settle();

      expect(elementPath()).toBe(DENOISED_URL);
      expect(screen.getByRole('alert')).toBeInTheDocument();
    });

    it('keeps retrying the denoised clip while the server answers 503', async () => {
      restoreFetch = mockAudioProbe({ status: PENDING_STATUS, headers: { 'Retry-After': '1' } });
      mount();
      ctl.completeLoad();
      await press();

      ctl.failLoad();
      await vi.advanceTimersByTimeAsync(2000);

      expect(elementPath()).toBe(DENOISED_URL);
      expect(ctl.load).toHaveBeenCalled();
    });

    it('does not fall back for an explicit toolbar choice: the processing error stays', async () => {
      restoreFetch = mockAudioProbe({ status: SERVER_ERROR_STATUS });
      mount();
      ctl.completeLoad();
      await chooseDenoise(DENOISE_HEAVY_TEXT);
      expect(ctl.element.src).toBe(PROCESSED_BLOB_URL);

      for (let i = 0; i <= MAX_AUDIO_LOAD_RETRIES; i++) {
        ctl.failLoad();
        await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
      }
      await settle();

      expect(ctl.element.src).toBe(PROCESSED_BLOB_URL);
      expect(screen.getByRole('alert')).toBeInTheDocument();
    });
  });

  describe('spectrogram following the denoise strength', () => {
    const SPECTROGRAM_ALT = 'components.audio.spectrogramAlt';
    const SERVER_ERROR_STATUS = 500;
    // Longer than the last spectrogram retry delay.
    const SPECTROGRAM_RETRY_WAIT_MS = 5000;
    const MAX_SPECTROGRAM_RETRIES_TEST = 6;
    let restoreFetch: (() => void) | undefined;

    afterEach(() => {
      restoreFetch?.();
      restoreFetch = undefined;
    });

    function spectrogramSrc(): URL {
      return new URL(screen.getByAltText(SPECTROGRAM_ALT).getAttribute('src') ?? '', 'http://x');
    }

    function followingPlayer(props: Record<string, unknown> = {}) {
      return mount({ showSpectrogram: true, spectrogramFollowsDenoise: true, ...props });
    }

    function statusRequests(): string[] {
      return fetchMock.mock.calls
        .map(call => String(call[0]))
        .filter(url => url.includes('/status'));
    }

    it('carries the strength the play URL carries', async () => {
      followingPlayer();
      await settle();

      expect(spectrogramSrc().searchParams.get('denoise')).toBe(PRESET);
      ctl.completeLoad();
      await press();
      expect(new URL(ctl.element.src).searchParams.get('denoise')).toBe(PRESET);
    });

    it('follows a toolbar change and drops the parameter on Off', async () => {
      followingPlayer();
      ctl.completeLoad();

      await chooseDenoise(DENOISE_HEAVY_TEXT);
      expect(spectrogramSrc().searchParams.get('denoise')).toBe('heavy');

      await chooseDenoise(DENOISE_OFF_TEXT);
      expect(spectrogramSrc().searchParams.has('denoise')).toBe(false);
    });

    it('never carries it without the prop', async () => {
      mount({ showSpectrogram: true });
      ctl.completeLoad();
      await settle();
      expect(spectrogramSrc().searchParams.has('denoise')).toBe(false);

      await chooseDenoise(DENOISE_HEAVY_TEXT);
      expect(spectrogramSrc().searchParams.has('denoise')).toBe(false);
    });

    it('requests no processed spectrogram for a denoise-only state, and keeps it for normalize', async () => {
      followingPlayer();
      ctl.completeLoad();

      await chooseDenoise(DENOISE_HEAVY_TEXT);
      expect(postedUrls().filter(url => url.includes('/spectrogram/'))).toHaveLength(0);

      await toggleNormalize();
      expect(spectrogramSrc().searchParams.has('denoise')).toBe(false);
      expect(postedUrls().filter(url => url.includes('/spectrogram/'))).toHaveLength(1);
    });

    it('shows the denoised spectrogram again when normalize is switched off on a non-default strength', async () => {
      followingPlayer();
      ctl.completeLoad();
      await chooseDenoise(DENOISE_HEAVY_TEXT);
      await toggleNormalize();
      expect(screen.getByAltText(SPECTROGRAM_ALT).getAttribute('src')).toBe(PROCESSED_BLOB_URL);

      await toggleNormalize();

      expect(spectrogramSrc().searchParams.get('denoise')).toBe('heavy');
    });

    it('returns to the stored spectrogram when the denoised clip fails', async () => {
      restoreFetch = mockAudioProbe({ status: SERVER_ERROR_STATUS });
      followingPlayer();
      ctl.completeLoad();
      await press();
      expect(spectrogramSrc().searchParams.get('denoise')).toBe(PRESET);

      ctl.failLoad();
      await settle();

      expect(spectrogramSrc().searchParams.has('denoise')).toBe(false);
    });

    // Answers the spectrogram GET (not the /process POST) with the given status.
    function answerDenoisedSpectrogramWith(status: number, headers: Record<string, string> = {}) {
      fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const isImageRequest =
          url.includes('/api/v2/spectrogram/') && !url.includes('/process') && !init?.method;
        return {
          ok: !isImageRequest,
          status: isImageRequest ? status : 200,
          blob: async () => new Blob(['processed']),
          headers: new Headers(headers),
        };
      });
    }

    async function failSpectrogramImage() {
      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await vi.advanceTimersByTimeAsync(SPECTROGRAM_RETRY_WAIT_MS);
      await settle();
    }

    it('falls back to the stored spectrogram at once when the denoised one fails for good', async () => {
      answerDenoisedSpectrogramWith(422);
      followingPlayer();
      await settle();

      await failSpectrogramImage();

      expect(spectrogramSrc().searchParams.has('denoise')).toBe(false);
      const imageRequests = fetchMock.mock.calls.filter(call =>
        String(call[0]).includes('denoise=')
      );
      expect(imageRequests.length).toBeLessThanOrEqual(1);
    });

    it('falls back to the stored spectrogram when the denoised one keeps answering 503', async () => {
      answerDenoisedSpectrogramWith(503);
      followingPlayer();
      await settle();

      for (let attempt = 0; attempt <= MAX_SPECTROGRAM_RETRIES_TEST; attempt++) {
        if (!spectrogramSrc().searchParams.has('denoise')) break;
        await failSpectrogramImage();
      }

      expect(spectrogramSrc().searchParams.has('denoise')).toBe(false);
    });

    it('keeps the stored spectrogram for the detection and asks again for the next one', async () => {
      answerDenoisedSpectrogramWith(422);
      const { rerender } = followingPlayer();
      await settle();
      await failSpectrogramImage();
      expect(spectrogramSrc().searchParams.has('denoise')).toBe(false);

      await chooseDenoise(DENOISE_HEAVY_TEXT);
      expect(spectrogramSrc().searchParams.has('denoise')).toBe(false);

      await rerender({
        detectionId: '4343',
        audioUrl: '/api/v2/audio/4343',
        showSpectrogram: true,
        spectrogramFollowsDenoise: true,
        enableClipExtraction: true,
      });
      await settle();
      expect(spectrogramSrc().searchParams.get('denoise')).toBe('heavy');
    });

    it('keeps the denoised spectrogram on screen while the normalized one is made', async () => {
      let finishProcessing: (() => void) | undefined;
      fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.includes('/spectrogram/') && url.includes('/process') && init?.method === 'POST') {
          await new Promise<void>(resolve => {
            finishProcessing = resolve;
          });
        }
        return { ok: true, status: 200, blob: async () => new Blob(['p']), headers: new Headers() };
      });
      followingPlayer();
      ctl.completeLoad();
      await settle();

      await toggleNormalize();
      expect(spectrogramSrc().searchParams.get('denoise')).toBe(PRESET);

      finishProcessing?.();
      await settle();
      expect(screen.getByAltText(SPECTROGRAM_ALT).getAttribute('src')).toBe(PROCESSED_BLOB_URL);
    });

    it('does not poll the stored render status for the denoised variant', async () => {
      followingPlayer();
      await settle();
      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await settle();
      expect(statusRequests()).toHaveLength(0);

      cleanup();
      fetchMock.mockClear();
      mount({ showSpectrogram: true });
      await settle();
      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await settle();
      expect(statusRequests().length).toBeGreaterThan(0);
    });
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

    it('does not treat an audible-bats load failure as a denoised-clip failure', async () => {
      mount({ modelType: 'bat' });
      ctl.completeLoad();
      await press();
      await activateAudibleBats();
      expect(ctl.element.src).toBe(BATS_BLOB_URL);

      ctl.failLoad();
      await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
      await settle();

      expect(ctl.element.src).toBe(BATS_BLOB_URL);
    });

    function audibleBatsBodies(): Array<{ denoise: string }> {
      return fetchMock.mock.calls
        .filter(call => String(call[0]).includes('/audible-bats'))
        .map(call => JSON.parse(String((call[1] as RequestInit).body)));
    }

    it('requests the audible copy with the playback default', async () => {
      mount({ modelType: 'bat' });
      ctl.completeLoad();
      await activateAudibleBats();

      expect(audibleBatsBodies().map(body => body.denoise)).toEqual([PRESET]);
    });

    it('requests the audible copy with the clip override from the toolbar', async () => {
      mount({ modelType: 'bat' });
      ctl.completeLoad();
      await chooseDenoise(DENOISE_HEAVY_TEXT);
      await activateAudibleBats();

      expect(audibleBatsBodies().map(body => body.denoise)).toEqual(['heavy']);
    });

    async function failDefaultThenRequestBats(choose?: string): Promise<string[]> {
      const restoreProbe = mockAudioProbe({ status: 500 });
      const probe = globalThis.fetch;
      const batsBodies: Array<{ denoise: string }> = [];
      const wrapper = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (!String(input).includes('/audible-bats')) return probe(input, init);
        batsBodies.push(JSON.parse(String(init?.body)));
        return {
          ok: true,
          status: 200,
          blob: async () => new Blob(['bats']),
          headers: new Headers(),
        };
      });
      globalThis.fetch = wrapper as unknown as typeof globalThis.fetch;
      try {
        mount({ modelType: 'bat' });
        ctl.completeLoad();
        await press();
        ctl.failLoad();
        await settle();
        expect(elementPath()).toBe(STORED_URL);

        if (choose) await chooseDenoise(choose);
        await activateAudibleBats();

        return batsBodies.map(body => body.denoise);
      } finally {
        restoreProbe();
      }
    }

    it('requests the audible copy without denoise once the default clip failed', async () => {
      expect(await failDefaultThenRequestBats()).toEqual(['']);
    });

    it('requests the audible copy with an explicit choice made after the default clip failed', async () => {
      expect(await failDefaultThenRequestBats(DENOISE_HEAVY_TEXT)).toEqual(['heavy']);
    });

    it('drops an audible copy that was still generating when the toolbar changed', async () => {
      let resolveBats: (response: unknown) => void = () => {};
      let batsSignal: AbortSignal | undefined;
      const processedResponse = {
        ok: true,
        status: 200,
        blob: async () => new Blob(['processed']),
        headers: new Headers(),
      };
      fetchMock.mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
        if (!String(input).includes('/audible-bats')) return Promise.resolve(processedResponse);
        batsSignal = init?.signal ?? undefined;
        return new Promise(resolve => {
          resolveBats = resolve;
        });
      });
      mount({ modelType: 'bat' });
      ctl.completeLoad();
      await activateAudibleBats();

      await chooseDenoise(DENOISE_HEAVY_TEXT);
      resolveBats({
        ok: true,
        status: 200,
        blob: async () => new Blob(['bats'], { type: 'bats' }),
        headers: new Headers(),
      });
      await settle();

      expect(batsSignal?.aborted).toBe(true);
      expect(ctl.element.src).toBe(PROCESSED_BLOB_URL);
    });

    it('keeps an active audible copy when a processing result arrives after it was turned on', async () => {
      mount({ modelType: 'bat' });
      ctl.completeLoad();
      await fireEvent.click(screen.getByLabelText(NORMALIZE_LABEL));
      await activateAudibleBats();
      expect(ctl.element.src).toBe(BATS_BLOB_URL);

      await vi.advanceTimersByTimeAsync(PROCESSING_DEBOUNCE_MS);
      await settle();

      expect(ctl.element.src).toBe(BATS_BLOB_URL);
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
