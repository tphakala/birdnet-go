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
// RATE_RESOLVE_TIMEOUT_MS in AudioPlayer.svelte
const RATE_RESOLVE_TIMEOUT_MS = 1500;
// Longer than every spectrogram retry delay in AudioPlayer.svelte (500 to 4000 ms)
const PAST_RETRY_DELAY_MS = 5000;
// One initial load plus MAX_SPECTROGRAM_RETRIES (4) in AudioPlayer.svelte
const SPECTROGRAM_LOAD_ATTEMPTS = 5;
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
    ctl = installFakeMediaElement();
    detailRequests = [];
    originalFetch = fetchMock().getMockImplementation();
    // Detail requests stay pending until the test answers them; the request
    // rejects with an AbortError when its signal aborts, as fetch does.
    fetchMock().mockImplementation((url, init) => {
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

      expect(labels(container)).toEqual(['160k', '120k', '80k', '40k']);
      expect(labels(container)).not.toContain('128k');
      const top = container.querySelector<HTMLElement>('.freq-label');
      expect(parseFloat(top?.style.bottom ?? '')).toBeCloseTo((160 / 192) * 100, 2);
      expect(imageSrc()).toContain(`sr=${RATE_384K}`);
      expect(detailCalls()).toHaveLength(0);
    });

    it('labels a 192 kHz bat clip up to 80 kHz', async () => {
      const { container } = mount({ modelType: 'bat', clipSampleRate: RATE_192K });
      await settle();

      expect(labels(container)[0]).toBe('80k');
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
      expect(labels(container)[0]).toBe('80k');

      await rerender({ modelType: 'bat', clipSampleRate: RATE_384K });
      await settle();

      expect(labels(container)[0]).toBe('160k');
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
      expect(labels(container)).toEqual([]);
      expect(screen.queryByText(SPECTROGRAM_UNAVAILABLE)).toBeNull();
    });
  });

  describe('rate resolve', () => {
    it('fetches the detection once and waits for it before requesting the image', async () => {
      const { container } = mount({ modelType: 'bat' });
      await settle();

      expect(detailCalls()).toEqual([expect.stringContaining(`${DETECTION_ENDPOINT}42`)]);
      expect(imageSrc()).toBeNull();
      expect(labels(container)).toEqual([]);

      detailRequests[0]?.resolve({ id: 42, clipSampleRate: RATE_384K });
      await settle();

      expect(imageSrc()).toContain(`sr=${RATE_384K}`);
      expect(labels(container)).toEqual(['160k', '120k', '80k', '40k']);
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
      expect(labels(container)[0]).toBe('80k');
    });

    it('aborts the request on unmount', async () => {
      const { unmount } = mount({ modelType: 'bat' });
      await settle();

      unmount();

      expect(detailRequests[0]?.signal?.aborted).toBe(true);
    });
  });
});
