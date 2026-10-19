/**
 * Frequency axis and clip sample rate tests for AudioPlayer: the bat axis runs
 * from 0 to half the clip's sample rate, the spectrogram URL carries the rate
 * (sr) only when it is known, and a bat detection without a rate resolves it
 * once through the single-detection endpoint before the image is requested.
 * Kept apart from AudioPlayer.test.ts because it replaces the global fetch
 * mock for the detection endpoint.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, fireEvent, cleanup, render } from '@testing-library/svelte';
import type { ComponentProps } from 'svelte';
import {
  installFakeMediaElement,
  settle,
  type FakeMediaController,
} from '../../../../test/fakeMediaElement';
import { clearClipSampleRateCache } from '$lib/utils/clipSampleRateLookup';
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

const SPECTROGRAM_ALT = 'components.audio.spectrogramAlt';
const SPECTROGRAM_UNAVAILABLE = 'components.audio.spectrogramUnavailable';
const DETECTION_ENDPOINT = '/api/v2/detections/';
const SPECTROGRAM_ENDPOINT = '/api/v2/spectrogram/';
// SPECTROGRAM_PROBE_TIMEOUT_MS in AudioPlayer.svelte
const PROBE_TIMEOUT_MS = 8000;
// The server waits this long for missing audio before it answers 404 (media.go audioWaitTimeout)
const SERVER_MISSING_AUDIO_WAIT_MS = 5000;
// First spectrogram retry delay in AudioPlayer.svelte
const FIRST_RETRY_DELAY_MS = 500;
// CLIP_RATE_LOOKUP_TIMEOUT_MS in $lib/utils/clipSampleRateLookup
const RATE_RESOLVE_TIMEOUT_MS = 1500;
// Longer than every spectrogram retry delay in AudioPlayer.svelte (500 to 4000 ms)
const PAST_RETRY_DELAY_MS = 5000;
// One initial load plus MAX_SPECTROGRAM_RETRIES (4) in AudioPlayer.svelte
const SPECTROGRAM_LOAD_ATTEMPTS = 5;
// Longer than the spinner delay of the spectrogram loader in AudioPlayer.svelte (150 ms)
const SPINNER_DELAY_MS = 200;
const RATE_384K = 384_000;
const RATE_192K = 192_000;
const BIRD_RATE = 48_000;
const BIRD_LABELS = ['12k', '10k', '8k', '6k', '5k', '4k', '3k', '2k', '1k'];

type FetchImpl = typeof globalThis.fetch;
type AudioPlayerProps = ComponentProps<typeof AudioPlayer>;

interface PendingDetailRequest {
  url: string;
  signal: AbortSignal | undefined;
  resolve: (body: unknown, status?: number) => void;
  reject: (error: Error) => void;
}

function jsonResponse(body: unknown, status = 200): Response {
  // A partial Response: the api client reads only these members.
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: '',
    headers: new Headers({ 'content-type': 'application/json' }),
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response;
}

describe('AudioPlayer frequency axis and clip sample rate', () => {
  let ctl: FakeMediaController;
  let originalFetch: FetchImpl | undefined;
  let detailRequests: PendingDetailRequest[];
  // Answers the status check of an image URL that carries sr; undefined leaves
  // it to the shared fetch mock (200).
  let probeResponder: ((signal: AbortSignal | undefined) => Promise<Response>) | undefined;
  let probeCalls: string[];
  let probeSignals: (AbortSignal | undefined)[];
  // Body of the spectrogram /status answer; undefined leaves it to the shared mock.
  let statusBody: Record<string, unknown> | undefined;
  // When set, the spectrogram /status request fails on the network.
  let statusFails: boolean;
  // When set, the spectrogram /status request never answers until aborted.
  let statusHangs: boolean;

  const fetchMock = () => vi.mocked(globalThis.fetch);

  // render infers the props type from the component, so every prop stays type
  // checked; renderTyped is not used because its result types rerender as never.
  function mount(props: Partial<AudioPlayerProps> = {}) {
    return render(AudioPlayer, {
      props: { audioUrl: '/audio/test.mp3', detectionId: '42', ...props },
    });
  }

  function labels(container: HTMLElement): string[] {
    return Array.from(container.querySelectorAll('.freq-label')).map(el => el.textContent.trim());
  }

  // Fires the image load event, then reads the axis labels: the bat axis is
  // drawn only over a loaded image.
  async function loadedLabels(container: HTMLElement): Promise<string[]> {
    const img = screen.queryByAltText(SPECTROGRAM_ALT);
    if (img) await fireEvent.load(img);
    await settle();
    return labels(container);
  }

  function imageSrc(): string | null {
    return screen.queryByAltText(SPECTROGRAM_ALT)?.getAttribute('src') ?? null;
  }

  function detailCalls(): string[] {
    return fetchMock()
      .mock.calls.map(call => String(call[0]))
      .filter(url => url.includes(DETECTION_ENDPOINT));
  }

  beforeEach(() => {
    vi.useFakeTimers();
    // The rate lookup is shared across players: start every test without it.
    clearClipSampleRateCache();
    ctl = installFakeMediaElement();
    detailRequests = [];
    probeCalls = [];
    probeSignals = [];
    originalFetch = fetchMock().getMockImplementation();
    // Detail requests stay pending until the test answers them; the request
    // rejects with an AbortError when its signal aborts, as fetch does.
    probeResponder = undefined;
    statusBody = undefined;
    statusFails = false;
    statusHangs = false;
    fetchMock().mockImplementation((url, init) => {
      if (
        statusHangs &&
        String(url).includes(SPECTROGRAM_ENDPOINT) &&
        String(url).includes('/status')
      ) {
        return new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () =>
            reject(new DOMException('The operation was aborted.', 'AbortError'))
          );
        });
      }
      if (
        statusFails &&
        String(url).includes(SPECTROGRAM_ENDPOINT) &&
        String(url).includes('/status')
      ) {
        return Promise.reject(new TypeError('Failed to fetch'));
      }
      if (
        statusBody &&
        String(url).includes(SPECTROGRAM_ENDPOINT) &&
        String(url).includes('/status')
      ) {
        return Promise.resolve(jsonResponse({ data: statusBody }));
      }
      // jsdom never fetches <img> sources, so every fetch of a spectrogram image
      // URL (not /status) is the component's status check.
      if (String(url).includes(SPECTROGRAM_ENDPOINT) && !String(url).includes('/status')) {
        probeCalls.push(String(url));
        probeSignals.push(init?.signal ?? undefined);
        if (probeResponder) return probeResponder(init?.signal ?? undefined);
      }
      if (String(url).includes(DETECTION_ENDPOINT)) {
        return new Promise<Response>((resolve, reject) => {
          const signal = init?.signal ?? undefined;
          signal?.addEventListener('abort', () =>
            reject(new DOMException('The operation was aborted.', 'AbortError'))
          );
          detailRequests.push({
            url: String(url),
            signal,
            resolve: (body, status = 200) => resolve(jsonResponse(body, status)),
            reject,
          });
        });
      }
      return originalFetch
        ? originalFetch(url, init)
        : Promise.reject(new Error(`Unmocked fetch call to: ${String(url)}`));
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    ctl.restore();
    fetchMock().mockReset();
    if (originalFetch) fetchMock().mockImplementation(originalFetch);
  });

  describe('frequency overlay', () => {
    it('labels a 384 kHz bat clip from 0 to 192 kHz', async () => {
      const { container } = mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      expect(await loadedLabels(container)).toEqual(['160k', '120k', '80k', '40k']);
      expect(await loadedLabels(container)).not.toContain('128k');
      const top = container.querySelector<HTMLElement>('.freq-label');
      expect(parseFloat(top?.style.bottom ?? '')).toBeCloseTo((160 / 192) * 100, 2);
      expect(imageSrc()).toContain(`sr=${RATE_384K}`);
      expect(detailCalls()).toHaveLength(0);
    });

    it('labels a 192 kHz bat clip up to 80 kHz', async () => {
      const { container } = mount({ modelType: 'bat', clipSampleRate: RATE_192K });
      await settle();

      expect((await loadedLabels(container))[0]).toBe('80k');
      expect(imageSrc()).toContain(`sr=${RATE_192K}`);
    });

    it('keeps the bird axis and URL whatever the clip rate', async () => {
      const { container } = mount({ modelType: 'bird', clipSampleRate: BIRD_RATE });
      await settle();

      expect(labels(container)).toEqual(BIRD_LABELS);
      expect(imageSrc()).not.toContain('sr=');
      expect(detailCalls()).toHaveLength(0);
    });

    it('updates the axis and URL when the rate prop changes', async () => {
      const { container, rerender } = mount({ modelType: 'bat', clipSampleRate: RATE_192K });
      await settle();
      expect((await loadedLabels(container))[0]).toBe('80k');

      await rerender({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      expect((await loadedLabels(container))[0]).toBe('160k');
      expect(imageSrc()).toContain(`sr=${RATE_384K}`);
    });

    it('drops the rate and the axis when the exact render is not available', async () => {
      const { container } = mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      for (let i = 0; i < SPECTROGRAM_LOAD_ATTEMPTS; i++) {
        const img = screen.getByAltText(SPECTROGRAM_ALT);
        expect(img.getAttribute('src')).toContain(`sr=${RATE_384K}`);
        await fireEvent.error(img);
        await vi.advanceTimersByTimeAsync(PAST_RETRY_DELAY_MS);
      }

      // The image is requested again without sr, so the server may answer with
      // any render it has; no axis is drawn over it and no error is shown.
      expect(imageSrc()).not.toBeNull();
      expect(imageSrc()).not.toContain('sr=');
      expect(screen.queryByText(SPECTROGRAM_UNAVAILABLE)).toBeNull();
      expect(await loadedLabels(container)).toEqual([]);
    });

    it('shows the error placeholder when the image without the rate fails too', async () => {
      const { container } = mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      // Exhaust the retries with the rate, then with the fallback URL without it.
      for (let i = 0; i < SPECTROGRAM_LOAD_ATTEMPTS * 2; i++) {
        const img = screen.queryByAltText(SPECTROGRAM_ALT);
        if (!img) break;
        await fireEvent.error(img);
        await vi.advanceTimersByTimeAsync(PAST_RETRY_DELAY_MS);
      }

      expect(screen.getByText(SPECTROGRAM_UNAVAILABLE)).toBeInTheDocument();
      expect(imageSrc()).not.toContain('sr=');
      expect(labels(container)).toEqual([]);
    });
  });

  describe('exact render status check', () => {
    const notFound = () => Promise.resolve(jsonResponse({ error: 'no render' }, 404));
    // Answers 404 after the server's wait for missing audio, or rejects when aborted first.
    const slowNotFound = (signal: AbortSignal | undefined) =>
      new Promise<Response>((resolve, reject) => {
        const timer = setTimeout(
          () => resolve(jsonResponse({ error: 'no render' }, 404)),
          SERVER_MISSING_AUDIO_WAIT_MS
        );
        signal?.addEventListener('abort', () => {
          clearTimeout(timer);
          reject(new DOMException('The operation was aborted.', 'AbortError'));
        });
      });
    const neverAnswers = (signal: AbortSignal | undefined) =>
      new Promise<Response>((_resolve, reject) => {
        signal?.addEventListener('abort', () =>
          reject(new DOMException('The operation was aborted.', 'AbortError'))
        );
      });

    it('falls back to the image without sr after one failed attempt when the server says 404', async () => {
      probeResponder = notFound;
      const { container } = mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await settle();

      expect(probeCalls).toHaveLength(1);
      expect(probeCalls[0]).toContain(`sr=${RATE_384K}`);
      expect(imageSrc()).not.toContain('sr=');
      expect(imageSrc()).not.toContain('retry=');
      expect(screen.queryByText(SPECTROGRAM_UNAVAILABLE)).toBeNull();
      // The fallback image has loaded: still no axis over it.
      expect(await loadedLabels(container)).toEqual([]);
    });

    it('keeps the rate while the status poll reports a queued generation', async () => {
      // The 404 arrives at once, before the component's own status poll has
      // answered, so the decision cannot rely on that poll's state.
      probeResponder = notFound;
      statusBody = { status: 'queued', queuePosition: 2, message: '' };
      mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await settle();

      expect(probeCalls).toHaveLength(1);
      expect(imageSrc()).toContain(`sr=${RATE_384K}`);
    });

    it.each(['generating', 'generated', 'completed', 'exists'])(
      'keeps the rate when the generation status is %s',
      async generationStatus => {
        probeResponder = notFound;
        statusBody = { status: generationStatus, queuePosition: 0, message: '' };
        mount({ modelType: 'bat', clipSampleRate: RATE_384K });
        await settle();

        await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
        await settle();

        expect(imageSrc()).toContain(`sr=${RATE_384K}`);
      }
    );

    it('falls back on a 404 even when the generation status cannot be read', async () => {
      probeResponder = notFound;
      statusFails = true;
      mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await settle();

      expect(imageSrc()).not.toContain('sr=');
    });

    it('falls back at the timeout when the generation status read hangs', async () => {
      probeResponder = notFound;
      statusHangs = true;
      mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await vi.advanceTimersByTimeAsync(PROBE_TIMEOUT_MS - 1);
      expect(probeSignals[0]?.aborted).toBe(false);

      await vi.advanceTimersByTimeAsync(1);
      await settle();

      expect(probeSignals[0]?.aborted).toBe(true);
      expect(imageSrc()).not.toContain('sr=');
    });

    it('keeps retrying with sr when the status check answers 503', async () => {
      const cancelBody = vi.fn(() => Promise.resolve());
      probeResponder = () =>
        Promise.resolve(
          Object.assign(jsonResponse({ error: 'busy' }, 503), { body: { cancel: cancelBody } })
        );
      mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await vi.advanceTimersByTimeAsync(FIRST_RETRY_DELAY_MS);

      // Only the status was read: the body is dropped.
      expect(cancelBody).toHaveBeenCalledTimes(1);
      expect(imageSrc()).toContain(`sr=${RATE_384K}`);
      expect(imageSrc()).toContain('retry=1');
    });

    it('keeps retrying with sr when the status check times out', async () => {
      probeResponder = neverAnswers;
      mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await vi.advanceTimersByTimeAsync(PROBE_TIMEOUT_MS - 1);
      expect(probeSignals[0]?.aborted).toBe(false);
      await vi.advanceTimersByTimeAsync(1);
      expect(probeSignals[0]?.aborted).toBe(true);
      await vi.advanceTimersByTimeAsync(FIRST_RETRY_DELAY_MS);

      expect(imageSrc()).toContain(`sr=${RATE_384K}`);
      expect(imageSrc()).toContain('retry=');
    });

    it('falls back as soon as a slow 404 arrives, well before the retries run out', async () => {
      probeResponder = slowNotFound;
      const { container } = mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      const img = screen.getByAltText(SPECTROGRAM_ALT);
      await fireEvent.error(img);
      await vi.advanceTimersByTimeAsync(SERVER_MISSING_AUDIO_WAIT_MS);
      await settle();

      // The retries alone would need 500 + 1000 + 2000 + 4000 ms before falling back.
      expect(imageSrc()).not.toContain('sr=');
      expect(probeCalls).toHaveLength(1);
      expect(await loadedLabels(container)).toEqual([]);
    });

    it('drops a pending status check when the detection changes', async () => {
      probeResponder = slowNotFound;
      const { container, rerender } = mount({
        modelType: 'bat',
        clipSampleRate: RATE_384K,
        detectionId: '42',
      });
      await settle();
      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await settle();

      await rerender({ modelType: 'bat', clipSampleRate: RATE_192K, detectionId: '43' });
      await settle();
      expect(probeSignals[0]?.aborted).toBe(true);
      await vi.advanceTimersByTimeAsync(SERVER_MISSING_AUDIO_WAIT_MS);

      // The new detection keeps its own rate and axis.
      expect(imageSrc()).toContain('/api/v2/spectrogram/43');
      expect(imageSrc()).toContain(`sr=${RATE_192K}`);
      expect((await loadedLabels(container))[0]).toBe('80k');
    });

    it('ignores a 404 that arrives while the spectrogram is hidden', async () => {
      probeResponder = slowNotFound;
      const { rerender } = mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();
      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await settle();

      // Hiding the spectrogram drops its URL without the reset effect running.
      await rerender({ modelType: 'bat', clipSampleRate: RATE_384K, showSpectrogram: false });
      await vi.advanceTimersByTimeAsync(SERVER_MISSING_AUDIO_WAIT_MS);
      await rerender({ modelType: 'bat', clipSampleRate: RATE_384K, showSpectrogram: true });
      await settle();

      expect(imageSrc()).toContain(`sr=${RATE_384K}`);
    });

    it('drops a pending status check on unmount', async () => {
      probeResponder = slowNotFound;
      const { unmount } = mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();
      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await settle();

      unmount();

      expect(probeSignals[0]?.aborted).toBe(true);
    });

    it('draws no bat axis while the image is still loading', async () => {
      const { container } = mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      expect(imageSrc()).toContain(`sr=${RATE_384K}`);
      expect(labels(container)).toEqual([]);
      expect(await loadedLabels(container)).toEqual(['160k', '120k', '80k', '40k']);
    });

    it('ignores a 404 that arrives after the image has loaded', async () => {
      probeResponder = slowNotFound;
      const { container } = mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await vi.advanceTimersByTimeAsync(FIRST_RETRY_DELAY_MS);
      // The retry loads before the status check answers.
      await fireEvent.load(screen.getByAltText(SPECTROGRAM_ALT));
      await vi.advanceTimersByTimeAsync(SERVER_MISSING_AUDIO_WAIT_MS);

      expect(probeSignals[0]?.aborted).toBe(true);
      expect(imageSrc()).toContain(`sr=${RATE_384K}`);
      expect(labels(container)).toEqual(['160k', '120k', '80k', '40k']);
    });

    it('makes no status check for a bird image', async () => {
      probeResponder = notFound;
      mount({ modelType: 'bird', clipSampleRate: BIRD_RATE });
      await settle();
      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await vi.advanceTimersByTimeAsync(FIRST_RETRY_DELAY_MS);

      expect(probeCalls).toHaveLength(0);
      expect(imageSrc()).toContain('retry=1');
    });

    it('makes no status check for a bat image without a rate', async () => {
      probeResponder = notFound;
      mount({ modelType: 'bat' });
      await settle();
      detailRequests[0].resolve({ id: 42 });
      await settle();

      expect(imageSrc()).not.toContain('sr=');
      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await vi.advanceTimersByTimeAsync(FIRST_RETRY_DELAY_MS);

      expect(probeCalls).toHaveLength(0);
      expect(imageSrc()).toContain('retry=1');
    });
  });

  describe('state kept per detection', () => {
    it('does not reuse the resolved rate of the previous detection', async () => {
      const { rerender } = mount({ modelType: 'bat', detectionId: '42' });
      await settle();
      detailRequests[0].resolve({ id: 42, clipSampleRate: RATE_384K });
      await settle();
      expect(imageSrc()).toContain(`sr=${RATE_384K}`);

      await rerender({ modelType: 'bat', detectionId: '43' });
      await settle();

      // Detection 43 waits for its own lookup instead of using 42's rate.
      expect(detailCalls()).toHaveLength(2);
      expect(detailRequests[1]?.url).toContain(`${DETECTION_ENDPOINT}43`);
      expect(imageSrc() ?? '').not.toContain(`sr=${RATE_384K}`);
    });

    it('uses the rate and the axis again after the previous detection fell back', async () => {
      probeResponder = () => Promise.resolve(jsonResponse({ error: 'no render' }, 404));
      const { container, rerender } = mount({
        modelType: 'bat',
        clipSampleRate: RATE_384K,
        detectionId: '42',
      });
      await settle();
      await fireEvent.error(screen.getByAltText(SPECTROGRAM_ALT));
      await settle();
      expect(imageSrc()).not.toContain('sr=');

      await rerender({ modelType: 'bat', clipSampleRate: RATE_192K, detectionId: '43' });
      await settle();

      expect(imageSrc()).toContain('/api/v2/spectrogram/43');
      expect(imageSrc()).toContain(`sr=${RATE_192K}`);
      expect((await loadedLabels(container))[0]).toBe('80k');
    });
  });

  describe('rate resolve', () => {
    it.each([0, -RATE_384K, Number.NaN, RATE_384K + 0.5])(
      'treats a clipSampleRate prop of %s as unknown and resolves the rate',
      async rate => {
        const { container } = mount({ modelType: 'bat', clipSampleRate: rate });
        await settle();

        expect(detailCalls()).toHaveLength(1);
        expect(imageSrc()).toBeNull();

        detailRequests[0].resolve({ id: 42, clipSampleRate: RATE_192K });
        await settle();

        expect(imageSrc()).toContain(`sr=${RATE_192K}`);
        expect(imageSrc()).not.toContain(`sr=${String(rate)}`);
        expect((await loadedLabels(container))[0]).toBe('80k');
      }
    );

    it('shows the loading state while the rate is resolved', async () => {
      const { container } = mount({ modelType: 'bat', responsive: true });
      await settle();

      const player = container.firstElementChild;
      expect(player?.classList.contains('spectrogram-skeleton')).toBe(true);
      expect(container.querySelector('.sr-only[role="status"]')?.textContent.trim()).toBe(
        'components.audio.spectrogramLoading'
      );
      // The spinner follows the loader's delay, as for any spectrogram load.
      await vi.advanceTimersByTimeAsync(SPINNER_DELAY_MS);
      const spinner = container.querySelector('.loading-spinner');
      expect(spinner).not.toBeNull();
      // Only the status region announces the state; the spinner is visual.
      expect(spinner?.getAttribute('aria-hidden')).toBe('true');
    });

    it('fetches the detection once and waits for it before requesting the image', async () => {
      const { container } = mount({ modelType: 'bat' });
      await settle();

      expect(detailCalls()).toEqual([expect.stringContaining(`${DETECTION_ENDPOINT}42`)]);
      expect(imageSrc()).toBeNull();
      expect(labels(container)).toEqual([]);

      detailRequests[0]?.resolve({ id: 42, clipSampleRate: RATE_384K });
      await settle();

      expect(imageSrc()).toContain(`sr=${RATE_384K}`);
      expect(await loadedLabels(container)).toEqual(['160k', '120k', '80k', '40k']);
      expect(detailCalls()).toHaveLength(1);
    });

    it.each<[string, (req: PendingDetailRequest) => void]>([
      ['a network error', req => req.reject(new TypeError('Failed to fetch'))],
      ['a 404', req => req.resolve({ error: 'not found' }, 404)],
      ['a response without the field', req => req.resolve({ id: 42 })],
      ['an invalid rate', req => req.resolve({ id: 42, clipSampleRate: 'fast' })],
    ])('shows the image without sr and without an axis after %s', async (_name, answer) => {
      const { container } = mount({ modelType: 'bat' });
      await settle();

      expect(detailRequests).toHaveLength(1);
      answer(detailRequests[0]);
      await settle();

      expect(imageSrc()).not.toBeNull();
      expect(imageSrc()).not.toContain('sr=');
      expect(labels(container)).toEqual([]);
    });

    it('gives up on the resolve after the client timeout', async () => {
      const { container } = mount({ modelType: 'bat' });
      await settle();
      expect(imageSrc()).toBeNull();

      await vi.advanceTimersByTimeAsync(RATE_RESOLVE_TIMEOUT_MS - 1);
      expect(imageSrc()).toBeNull();

      await vi.advanceTimersByTimeAsync(1);
      await settle();

      expect(detailRequests[0]?.signal?.aborted).toBe(true);
      expect(imageSrc()).not.toBeNull();
      expect(imageSrc()).not.toContain('sr=');
      expect(labels(container)).toEqual([]);
    });

    it('makes no request for a bat with a rate or for a bird', async () => {
      mount({ modelType: 'bat', clipSampleRate: RATE_384K });
      mount({ modelType: 'bird', detectionId: '43' });
      mount({ modelType: '', detectionId: '44' });
      await settle();

      expect(detailCalls()).toHaveLength(0);
    });

    it('renders the spectrogram status region before any spectrogram is requested', async () => {
      const { container } = mount({ modelType: 'bat', showSpectrogram: false });
      await settle();

      const region = container.querySelector('.sr-only[role="status"]');
      expect(region).not.toBeNull();
      expect(region?.textContent.trim()).toBe('');
    });

    it('makes no request when the spectrogram is hidden', async () => {
      mount({ modelType: 'bat', showSpectrogram: false });
      await settle();

      expect(detailCalls()).toHaveLength(0);
    });

    it('aborts the first request when the detection changes and resolves the second', async () => {
      const { container, rerender } = mount({ modelType: 'bat', detectionId: '42' });
      await settle();

      await rerender({ modelType: 'bat', detectionId: '43' });
      await settle();

      expect(detailRequests).toHaveLength(2);
      expect(detailRequests[0]?.signal?.aborted).toBe(true);
      expect(detailRequests[1]?.url).toContain(`${DETECTION_ENDPOINT}43`);

      detailRequests[1]?.resolve({ id: 43, clipSampleRate: RATE_192K });
      await settle();

      expect(imageSrc()).toContain('/api/v2/spectrogram/43');
      expect(imageSrc()).toContain(`sr=${RATE_192K}`);
      expect((await loadedLabels(container))[0]).toBe('80k');
    });

    it('shares one detail request between two players of the same detection', async () => {
      const first = mount({ modelType: 'bat' });
      const second = mount({ modelType: 'bat' });
      await settle();

      expect(detailCalls()).toHaveLength(1);
      detailRequests[0].resolve({ id: 42, clipSampleRate: RATE_384K });
      await settle();

      for (const { container } of [first, second]) {
        const img = container.querySelector('img');
        expect(img?.getAttribute('src')).toContain(`sr=${RATE_384K}`);
      }
    });

    it('keeps the shared request when one of two players unmounts', async () => {
      const leaving = mount({ modelType: 'bat' });
      const staying = mount({ modelType: 'bat' });
      await settle();

      leaving.unmount();
      expect(detailRequests[0]?.signal?.aborted).toBe(false);

      detailRequests[0].resolve({ id: 42, clipSampleRate: RATE_384K });
      await settle();
      expect(staying.container.querySelector('img')?.getAttribute('src')).toContain(
        `sr=${RATE_384K}`
      );
    });

    it('asks again after the only player unmounted before the answer', async () => {
      const { unmount } = mount({ modelType: 'bat' });
      await settle();
      unmount();

      mount({ modelType: 'bat' });
      await settle();

      expect(detailCalls()).toHaveLength(2);
      expect(detailRequests[0]?.signal?.aborted).toBe(true);
    });

    it('aborts the request on unmount', async () => {
      const { unmount } = mount({ modelType: 'bat' });
      await settle();

      unmount();

      expect(detailRequests[0]?.signal?.aborted).toBe(true);
    });
  });
});
