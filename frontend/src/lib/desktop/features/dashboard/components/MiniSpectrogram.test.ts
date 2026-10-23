import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { deferred } from '../../../../../test/async-helpers';
import { waitForNextTick } from '../../../../../test/constants';
import {
  installFakeMediaElement,
  type FakeMediaController,
} from '../../../../../test/fakeMediaElement';

const HLS_LOAD_FAILED = t('media.audio.hlsLoadFailed');
const HLS_NOT_SUPPORTED = t('media.audio.hlsNotSupported');
const PLAY_LABEL = t('media.audio.play');
const STOP_LABEL = t('media.audio.stop');
// STORAGE_KEY mirrors the auto-start preference key in MiniSpectrogram.svelte.
const STORAGE_KEY = 'birdnet-spectrogram-active';

const ERROR_HIDE_DELAY_MS = 3000;
const SOURCE_ID = 'source-a';

const mocks = vi.hoisted(() => ({
  tryLoadHls: vi.fn(),
  canPlayHls: vi.fn(),
  fetchWithCSRF: vi.fn(),
}));

vi.mock('$lib/desktop/components/ui/hls-loader', () => ({
  tryLoadHls: mocks.tryLoadHls,
  canPlayHls: mocks.canPlayHls,
}));

vi.mock('$lib/utils/api', () => ({
  fetchWithCSRF: mocks.fetchWithCSRF,
}));

vi.mock('$lib/stores/appState.svelte', () => ({
  appState: { liveSpectrogram: false },
  hasLiveAudioAccess: () => true,
}));

// The discovery stream reports one source as soon as a handler is attached.
vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class {
    onerror = null;
    #onmessage: ((event: { data: string }) => void) | null = null;
    set onmessage(handler: ((event: { data: string }) => void) | null) {
      this.#onmessage = handler;
      queueMicrotask(() =>
        handler?.({
          data: JSON.stringify({ type: 'audio-level', levels: { [SOURCE_ID]: {} } }),
        })
      );
    }
    get onmessage() {
      return this.#onmessage;
    }
    close() {}
  },
}));

import MiniSpectrogram from './MiniSpectrogram.svelte';

function startRequests(): number {
  return mocks.fetchWithCSRF.mock.calls.filter(([url]) => String(url).endsWith('/start')).length;
}

describe('MiniSpectrogram HLS player availability', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    mocks.fetchWithCSRF.mockResolvedValue({});
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    // Unmounting keeps the auto-start preference, so clear it between tests.
    localStorage.removeItem(STORAGE_KEY);
  });

  let media: FakeMediaController | null = null;

  afterEach(() => {
    cleanup();
    media?.restore();
    media = null;
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('shows the load failure message when HLS.js fails to load, then hides it', async () => {
    mocks.tryLoadHls.mockResolvedValue(null);
    mocks.canPlayHls.mockReturnValue(false);
    render(MiniSpectrogram);
    vi.useFakeTimers({ toFake: ['setTimeout'] });

    await fireEvent.click(screen.getByRole('button', { name: PLAY_LABEL }));

    expect(await screen.findByText(HLS_LOAD_FAILED)).toBeInTheDocument();
    expect(screen.queryByText(HLS_NOT_SUPPORTED)).toBeNull();
    expect(startRequests()).toBe(0);
    // The play button is enabled again.
    expect(screen.getByRole('button', { name: PLAY_LABEL })).toBeEnabled();

    await vi.advanceTimersByTimeAsync(ERROR_HIDE_DELAY_MS);
    await waitFor(() => expect(screen.queryByText(HLS_LOAD_FAILED)).toBeNull());
  });

  it('shows "not supported" when HLS.js loaded but no player exists', async () => {
    mocks.tryLoadHls.mockResolvedValue({ isSupported: () => false });
    mocks.canPlayHls.mockReturnValue(false);
    render(MiniSpectrogram);

    await fireEvent.click(screen.getByRole('button', { name: PLAY_LABEL }));

    expect(await screen.findByText(HLS_NOT_SUPPORTED)).toBeInTheDocument();
    expect(screen.queryByText(HLS_LOAD_FAILED)).toBeNull();
    expect(startRequests()).toBe(0);
  });

  it('starts the stream and builds an HLS.js player when HLS.js is available', async () => {
    const constructed = vi.fn();
    class FakeHls {
      static Events = { MANIFEST_PARSED: 'm', ERROR: 'e' };
      static isSupported = () => true;
      constructor() {
        constructed();
      }
      on() {}
      loadSource() {}
      attachMedia() {}
      destroy() {}
    }
    mocks.tryLoadHls.mockResolvedValue(FakeHls);
    mocks.canPlayHls.mockReturnValue(true);
    mocks.fetchWithCSRF.mockResolvedValue({
      stream_token: 'token',
      playlist_url: '/playlist.m3u8',
    });
    render(MiniSpectrogram);

    await fireEvent.click(screen.getByRole('button', { name: PLAY_LABEL }));

    await waitFor(() => expect(constructed).toHaveBeenCalledTimes(1));
    expect(startRequests()).toBe(1);
    expect(screen.queryByText(HLS_LOAD_FAILED)).toBeNull();
    expect(screen.queryByText(HLS_NOT_SUPPORTED)).toBeNull();
  });

  it('plays natively when HLS.js failed to load and the browser plays HLS', async () => {
    const controller = installFakeMediaElement();
    media = controller;
    mocks.tryLoadHls.mockResolvedValue(null);
    mocks.canPlayHls.mockReturnValue(true);
    mocks.fetchWithCSRF.mockResolvedValue({
      stream_token: 'token',
      playlist_url: '/playlist.m3u8',
    });
    render(MiniSpectrogram);

    await fireEvent.click(screen.getByRole('button', { name: PLAY_LABEL }));

    await waitFor(() => expect(controller.play).toHaveBeenCalledTimes(1));
    expect(controller.element.getAttribute('src')).toBe('/playlist.m3u8');
  });

  it('does not start a server stream when unmounted while HLS.js loads', async () => {
    const load = deferred<null>();
    mocks.tryLoadHls.mockReturnValue(load.promise);
    mocks.canPlayHls.mockReturnValue(true);
    render(MiniSpectrogram);

    await fireEvent.click(screen.getByRole('button', { name: PLAY_LABEL }));
    await waitFor(() => expect(mocks.tryLoadHls).toHaveBeenCalledTimes(1));
    cleanup();
    load.resolve(null);
    await waitForNextTick();

    expect(startRequests()).toBe(0);
  });

  it('keeps the auto-start preference on unmount and clears it on stop', async () => {
    const controller = installFakeMediaElement();
    media = controller;
    mocks.tryLoadHls.mockResolvedValue(null);
    mocks.canPlayHls.mockReturnValue(true);
    mocks.fetchWithCSRF.mockResolvedValue({
      stream_token: 'token',
      playlist_url: '/playlist.m3u8',
    });
    render(MiniSpectrogram);
    await fireEvent.click(screen.getByRole('button', { name: PLAY_LABEL }));
    await waitFor(() => expect(localStorage.getItem(STORAGE_KEY)).toBe('true'));

    cleanup();
    expect(localStorage.getItem(STORAGE_KEY)).toBe('true');

    // The next mount starts on its own and the stop button clears the preference.
    render(MiniSpectrogram);
    await fireEvent.click(await screen.findByRole('button', { name: STOP_LABEL }));
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it('clears the auto-start preference on a fatal HLS error', async () => {
    const handlers = new Map<string, (event: unknown, data: unknown) => void>();
    class FakeHls {
      static Events = { MANIFEST_PARSED: 'm', ERROR: 'e' };
      static isSupported = () => true;
      on(event: string, handler: (event: unknown, data: unknown) => void) {
        handlers.set(event, handler);
      }
      loadSource() {}
      attachMedia() {}
      destroy() {}
    }
    const controller = installFakeMediaElement();
    media = controller;
    mocks.tryLoadHls.mockResolvedValue(FakeHls);
    mocks.canPlayHls.mockReturnValue(true);
    mocks.fetchWithCSRF.mockResolvedValue({
      stream_token: 'token',
      playlist_url: '/playlist.m3u8',
    });
    render(MiniSpectrogram);
    await fireEvent.click(screen.getByRole('button', { name: PLAY_LABEL }));
    await waitFor(() => expect(handlers.has(FakeHls.Events.MANIFEST_PARSED)).toBe(true));
    handlers.get(FakeHls.Events.MANIFEST_PARSED)?.({}, {});
    await waitFor(() => expect(localStorage.getItem(STORAGE_KEY)).toBe('true'));

    handlers.get(FakeHls.Events.ERROR)?.({}, { fatal: true, type: 'networkError', details: 'x' });

    expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
  });
});
