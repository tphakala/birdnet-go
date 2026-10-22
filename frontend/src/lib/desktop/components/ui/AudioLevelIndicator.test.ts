import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { deferred, type Deferred } from '../../../../test/async-helpers';
import { waitForNextTick } from '../../../../test/constants';
import { BUFFERING_STRATEGY } from './hls-config';

// The global i18n mock returns the key for untranslated strings, so t() here
// yields exactly the text the component renders.
const HLS_LOAD_FAILED = t('media.audio.hlsLoadFailed');
const HLS_NOT_SUPPORTED = t('media.audio.hlsNotSupported');
const STREAM_ERROR = t('media.audio.streamError');
const START_LABEL = t('media.audio.startPlayback');
const STOP_LABEL = t('media.audio.stopPlayback');

const ERROR_STATUS_HIDE_DELAY_MS = 3000;

const mocks = vi.hoisted(() => ({
  tryLoadHls: vi.fn(),
  canPlayHls: vi.fn(),
  fetchWithCSRF: vi.fn(),
  sse: null as { onmessage: ((event: { data: string }) => void) | null } | null,
}));

vi.mock('./hls-loader', () => ({
  tryLoadHls: mocks.tryLoadHls,
  canPlayHls: mocks.canPlayHls,
}));

vi.mock('$lib/utils/api', () => ({
  fetchWithCSRF: mocks.fetchWithCSRF,
}));

vi.mock('$lib/stores/appState.svelte', () => ({
  hasLiveAudioAccess: () => true,
}));

vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class {
    onopen = null;
    onerror = null;
    onmessage: ((event: { data: string }) => void) | null = null;
    constructor() {
      mocks.sse = this;
    }
    close() {}
  },
}));

import AudioLevelIndicator from './AudioLevelIndicator.svelte';

const SOURCE_A = 'source-a';
const SOURCE_B = 'source-b';

// FakeHls is the HLS.js class stand-in; the constructor spy shows whether the
// component went on to set up a player.
const hlsConstructed = vi.fn();
type HlsHandler = (event: string, data?: unknown) => void;
// hlsHandlers holds the handler the component registered for each HLS.js event.
let hlsHandlers = new Map<string, HlsHandler>();
// emitHls fires an HLS.js event at the component's handler, failing when none is registered.
function emitHls(event: string, data?: unknown) {
  const handler = hlsHandlers.get(event);
  if (!handler) throw new Error(`no HLS.js handler registered for ${event}`);
  handler(event, data);
}
// HEARTBEAT_INTERVAL_MS mirrors HEARTBEAT_INTERVAL in AudioLevelIndicator.svelte.
const HEARTBEAT_INTERVAL_MS = 20000;
// heartbeatCalls counts keep-alive heartbeats, not the ?disconnect=true ones.
function heartbeatCalls(): number {
  return mocks.fetchWithCSRF.mock.calls.filter(([url]) => url === '/api/v2/streams/hls/heartbeat')
    .length;
}
// disconnectCalls counts the ?disconnect=true heartbeats.
function disconnectCalls(): number {
  return mocks.fetchWithCSRF.mock.calls.filter(
    ([url]) => url === '/api/v2/streams/hls/heartbeat?disconnect=true'
  ).length;
}

class FakeHls {
  static Events = { ERROR: 'e', MEDIA_ATTACHED: 'a', MANIFEST_PARSED: 'm', FRAG_BUFFERED: 'f' };
  static ErrorTypes = { MEDIA_ERROR: 'mediaError' };
  static isSupported = () => true;
  constructor() {
    hlsConstructed();
  }
  on(event: string, handler: HlsHandler) {
    hlsHandlers.set(event, handler);
  }
  attachMedia() {}
  loadSource() {}
  destroy() {}
}

type StartResponse = { stream_token: string; playlist_url: string };

// startCalls[i] is the i-th POST to a /start endpoint, in call order.
let startCalls: { sourceId: string; response: Deferred<StartResponse> }[];

function stopCallsFor(sourceId: string): number {
  return mocks.fetchWithCSRF.mock.calls.filter(
    ([url]) => url === `/api/v2/streams/hls/${sourceId}/stop`
  ).length;
}

function startResponse(): StartResponse {
  return { stream_token: 'token', playlist_url: '/playlist.m3u8' };
}

async function renderWithSources() {
  render(AudioLevelIndicator);
  mocks.sse?.onmessage?.({
    data: JSON.stringify({
      type: 'audio-level',
      levels: {
        [SOURCE_A]: { level: 10, clipping: false },
        [SOURCE_B]: { level: 10, clipping: false },
      },
    }),
  });
  await waitFor(() => expect(mocks.sse).not.toBeNull());
}

// Clicks the play/stop button of a source, opening the dropdown first when
// it is closed (the component closes it after each click).
async function clickSourceButton(sourceId: string) {
  if (!screen.queryByRole('menu')) {
    await fireEvent.click(screen.getByRole('button', { name: /media\.audio\.levelFor/ }));
  }
  const buttons = await screen.findAllByRole('button', {
    name: name => name === START_LABEL || name === STOP_LABEL,
  });
  const [first, second] = buttons;
  await fireEvent.click(sourceId === SOURCE_A ? first : second);
}

describe('AudioLevelIndicator start and stop', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    mocks.sse = null;
    startCalls = [];
    hlsHandlers = new Map();
    mocks.tryLoadHls.mockResolvedValue(FakeHls);
    mocks.canPlayHls.mockReturnValue(true);
    mocks.fetchWithCSRF.mockImplementation((url: string) => {
      const match = /^\/api\/v2\/streams\/hls\/([^/]+)\/start$/.exec(url);
      if (match) {
        const response = deferred<StartResponse>();
        startCalls.push({ sourceId: match[1], response });
        return response.promise;
      }
      return Promise.resolve({});
    });
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    vi.spyOn(console, 'error').mockImplementation(() => {});
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('reports a failed HLS.js load and hides the message', async () => {
    mocks.tryLoadHls.mockResolvedValue(null);
    mocks.canPlayHls.mockReturnValue(false);
    await renderWithSources();
    vi.useFakeTimers({ toFake: ['setTimeout'] });

    await clickSourceButton(SOURCE_A);

    expect(await screen.findByText(HLS_LOAD_FAILED)).toBeInTheDocument();
    expect(screen.queryByText(HLS_NOT_SUPPORTED)).toBeNull();
    expect(startCalls).toHaveLength(0);

    await vi.advanceTimersByTimeAsync(ERROR_STATUS_HIDE_DELAY_MS);
    await waitFor(() => expect(screen.queryByText(HLS_LOAD_FAILED)).toBeNull());
  });

  it('reports "not supported" when HLS.js loaded but no player exists', async () => {
    mocks.canPlayHls.mockReturnValue(false);
    await renderWithSources();

    await clickSourceButton(SOURCE_A);

    expect(await screen.findByText(HLS_NOT_SUPPORTED)).toBeInTheDocument();
    expect(screen.queryByText(HLS_LOAD_FAILED)).toBeNull();
    expect(startCalls).toHaveLength(0);
  });

  it('stops the stream a stale start response created', async () => {
    await renderWithSources();

    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(1));
    // The stop icon is showing while the start request is in flight.
    await clickSourceButton(SOURCE_A);
    const stopsBefore = stopCallsFor(SOURCE_A);

    startCalls[0].response.resolve(startResponse());

    await waitFor(() => expect(stopCallsFor(SOURCE_A)).toBe(stopsBefore + 1));
    expect(hlsConstructed).not.toHaveBeenCalled();
  });

  it('stops while starting instead of issuing a second start', async () => {
    await renderWithSources();

    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(1));
    await clickSourceButton(SOURCE_A);

    expect(startCalls).toHaveLength(1);
    expect(stopCallsFor(SOURCE_A)).toBeGreaterThan(0);
  });

  it('keeps the live stream when a superseded start for the same source resolves (A, B, A)', async () => {
    await renderWithSources();

    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(1));
    await clickSourceButton(SOURCE_B);
    await waitFor(() => expect(startCalls).toHaveLength(2));
    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(3));

    // The first A start is stale, but a newer start is playing source A, so
    // stopping A here would kill the live stream.
    const stopsBefore = stopCallsFor(SOURCE_A);
    startCalls[0].response.resolve(startResponse());
    await new Promise(resolve => globalThis.setTimeout(resolve, 0));
    expect(hlsConstructed).not.toHaveBeenCalled();
    expect(stopCallsFor(SOURCE_A)).toBe(stopsBefore);

    // The stale B start is not the playing source, so its stream is stopped.
    const stopsOfB = stopCallsFor(SOURCE_B);
    startCalls[1].response.resolve(startResponse());
    await waitFor(() => expect(stopCallsFor(SOURCE_B)).toBe(stopsOfB + 1));
  });

  it('does not show a stream error when a stopped start request fails', async () => {
    await renderWithSources();

    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(1));
    await clickSourceButton(SOURCE_A);

    startCalls[0].response.reject(new Error('network down'));
    await new Promise(resolve => globalThis.setTimeout(resolve, 0));

    expect(screen.queryByText(STREAM_ERROR)).toBeNull();
  });

  it('shows a stream error when the active start request fails', async () => {
    await renderWithSources();

    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(1));
    startCalls[0].response.reject(new Error('network down'));

    expect(await screen.findByText(STREAM_ERROR)).toBeInTheDocument();
  });

  it('starts again with one click after a failed start', async () => {
    await renderWithSources();

    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(1));
    startCalls[0].response.reject(new Error('network down'));
    await screen.findByText(STREAM_ERROR);

    await clickSourceButton(SOURCE_A);

    await waitFor(() => expect(startCalls).toHaveLength(2));
  });

  it('plays a started stream through HLS.js once enough fragments are buffered and sends heartbeats', async () => {
    const play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined);
    await renderWithSources();
    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(1));
    // Fake intervals only now: waitFor polls with setInterval. They must be fake before
    // the start resolves, because startHeartbeat() schedules the interval then.
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    startCalls[0].response.resolve(startResponse());
    await waitForNextTick();
    expect(hlsConstructed).toHaveBeenCalledTimes(1);

    for (let i = 1; i < BUFFERING_STRATEGY.MIN_FRAGMENTS_BEFORE_PLAY; i++) {
      emitHls(FakeHls.Events.FRAG_BUFFERED);
    }
    expect(play).not.toHaveBeenCalled();
    emitHls(FakeHls.Events.FRAG_BUFFERED);
    expect(play).toHaveBeenCalledTimes(1);

    document.getElementById('hls-audio-player')?.dispatchEvent(new Event('playing'));
    await vi.advanceTimersByTimeAsync(HEARTBEAT_INTERVAL_MS);
    expect(heartbeatCalls()).toBe(1);
  });

  it.each([
    {
      trigger: 'a fatal HLS.js error',
      message: t('media.audio.playbackError', { details: 'manifestLoadError' }),
      fail: () =>
        emitHls(FakeHls.Events.ERROR, {
          fatal: true,
          type: 'networkError',
          details: 'manifestLoadError',
        }),
    },
    {
      trigger: 'an audio element error',
      message: t('media.audio.playError'),
      fail: () => document.getElementById('hls-audio-player')?.dispatchEvent(new Event('error')),
    },
  ])('stops the stream and shows a transient error on $trigger', async ({ message, fail }) => {
    await renderWithSources();
    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(1));
    startCalls[0].response.resolve(startResponse());
    await waitFor(() => expect(hlsConstructed).toHaveBeenCalledTimes(1));
    const stopsBefore = stopCallsFor(SOURCE_A);
    vi.useFakeTimers({ toFake: ['setTimeout'] });

    fail();

    expect(stopCallsFor(SOURCE_A)).toBe(stopsBefore + 1);
    // Settle the deferred status updates without running the hide timer.
    await vi.advanceTimersByTimeAsync(0);
    expect(screen.getByText(message)).toBeInTheDocument();
    await vi.advanceTimersByTimeAsync(ERROR_STATUS_HIDE_DELAY_MS);
    expect(screen.queryByText(message)).toBeNull();
  });

  // Starts a stream whose HLS.js play() call stays pending, as when the first
  // fragments are buffered but playback has not begun yet.
  async function startWithPendingPlay() {
    const pendingPlay = deferred();
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockReturnValue(pendingPlay.promise);
    await renderWithSources();
    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(1));
    startCalls[0].response.resolve(startResponse());
    await waitFor(() => expect(hlsConstructed).toHaveBeenCalledTimes(1));
    for (let i = 0; i < BUFFERING_STRATEGY.MIN_FRAGMENTS_BEFORE_PLAY; i++) {
      emitHls(FakeHls.Events.FRAG_BUFFERED);
    }
    return pendingPlay;
  }

  it('shows no play error when a manual stop interrupts a pending HLS.js play()', async () => {
    const pendingPlay = await startWithPendingPlay();

    await clickSourceButton(SOURCE_A);
    pendingPlay.reject(new DOMException('interrupted by pause()', 'AbortError'));
    await waitForNextTick();

    expect(screen.queryByText(t('media.audio.playError'))).toBeNull();
  });

  it('keeps the fatal error toast when the interrupted HLS.js play() rejects with AbortError', async () => {
    const pendingPlay = await startWithPendingPlay();
    const message = t('media.audio.playbackError', { details: 'manifestLoadError' });
    vi.useFakeTimers({ toFake: ['setTimeout'] });

    emitHls(FakeHls.Events.ERROR, {
      fatal: true,
      type: 'networkError',
      details: 'manifestLoadError',
    });
    pendingPlay.reject(new DOMException('interrupted by pause()', 'AbortError'));
    await vi.advanceTimersByTimeAsync(0);

    expect(screen.getByText(message)).toBeInTheDocument();
    expect(screen.queryByText(t('media.audio.playError'))).toBeNull();
    await vi.advanceTimersByTimeAsync(ERROR_STATUS_HIDE_DELAY_MS);
    expect(screen.queryByText(message)).toBeNull();
  });

  it('plays natively when HLS.js failed to load and the browser plays HLS', async () => {
    mocks.tryLoadHls.mockResolvedValue(null);
    const play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined);
    await renderWithSources();
    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(1));
    startCalls[0].response.resolve(startResponse());

    await waitFor(() => expect(play).toHaveBeenCalledTimes(1));
    expect(document.getElementById('hls-audio-player')?.getAttribute('src')).toBe('/playlist.m3u8');
    expect(hlsConstructed).not.toHaveBeenCalled();
  });

  it('a second HLS check failure replaces the first message and cancels its hide timer', async () => {
    mocks.tryLoadHls.mockResolvedValue(null);
    mocks.canPlayHls.mockReturnValue(false);
    await renderWithSources();
    // clearTimeout must be faked too, or clearStatusHideTimer cannot cancel a fake timer.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });

    await clickSourceButton(SOURCE_A);
    expect(await screen.findByText(HLS_LOAD_FAILED)).toBeInTheDocument();
    await vi.advanceTimersByTimeAsync(ERROR_STATUS_HIDE_DELAY_MS / 2);

    mocks.tryLoadHls.mockResolvedValue(FakeHls);
    await clickSourceButton(SOURCE_A);
    expect(await screen.findByText(HLS_NOT_SUPPORTED)).toBeInTheDocument();

    // The first message's hide timer would have fired here if the second failure had not cancelled it.
    await vi.advanceTimersByTimeAsync(ERROR_STATUS_HIDE_DELAY_MS / 2);
    expect(screen.getByText(HLS_NOT_SUPPORTED)).toBeInTheDocument();
  });

  it('stops the server stream of a start that resolves after unmount', async () => {
    await renderWithSources();
    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(1));

    cleanup();
    startCalls[0].response.resolve(startResponse());

    await waitFor(() => expect(stopCallsFor(SOURCE_A)).toBe(1));
    expect(hlsConstructed).not.toHaveBeenCalled();
  });

  it('does not start a server stream when stopped while HLS.js loads', async () => {
    const load = deferred<typeof FakeHls | null>();
    mocks.tryLoadHls.mockReturnValue(load.promise);
    await renderWithSources();
    await clickSourceButton(SOURCE_A);
    await clickSourceButton(SOURCE_A); // the stop icon shows while the start is loading

    load.resolve(FakeHls);
    await waitForNextTick();

    expect(startCalls).toHaveLength(0);
  });

  it('sends the disconnect heartbeat when playback stops, not when it starts', async () => {
    await renderWithSources();
    await clickSourceButton(SOURCE_A);
    await waitFor(() => expect(startCalls).toHaveLength(1));
    startCalls[0].response.resolve(startResponse());
    // startHeartbeat runs once the start response has been handled.
    await waitForNextTick();
    expect(hlsConstructed).toHaveBeenCalledTimes(1);
    expect(disconnectCalls()).toBe(0);

    await clickSourceButton(SOURCE_A);

    expect(disconnectCalls()).toBe(1);
  });
});
