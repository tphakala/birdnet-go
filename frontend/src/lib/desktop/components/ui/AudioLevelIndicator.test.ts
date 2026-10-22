import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { deferred, type Deferred } from '../../../../test/async-helpers';

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
class FakeHls {
  static Events = { ERROR: 'e', MEDIA_ATTACHED: 'a', MANIFEST_PARSED: 'm', FRAG_BUFFERED: 'f' };
  static ErrorTypes = { MEDIA_ERROR: 'mediaError' };
  static isSupported = () => true;
  constructor() {
    hlsConstructed();
  }
  on() {}
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

  it('reports a failed HLS.js load as retryable and hides the message', async () => {
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
});
