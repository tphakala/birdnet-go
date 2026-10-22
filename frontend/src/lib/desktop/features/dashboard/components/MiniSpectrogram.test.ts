import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import { t } from '$lib/i18n';

const HLS_LOAD_FAILED = t('media.audio.hlsLoadFailed');
const HLS_NOT_SUPPORTED = t('media.audio.hlsNotSupported');
const PLAY_LABEL = t('media.audio.play');

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
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('shows a retryable message when HLS.js fails to load, then hides it', async () => {
    mocks.tryLoadHls.mockResolvedValue(null);
    mocks.canPlayHls.mockReturnValue(false);
    render(MiniSpectrogram);
    vi.useFakeTimers({ toFake: ['setTimeout'] });

    await fireEvent.click(screen.getByRole('button', { name: PLAY_LABEL }));

    expect(await screen.findByText(HLS_LOAD_FAILED)).toBeInTheDocument();
    expect(screen.queryByText(HLS_NOT_SUPPORTED)).toBeNull();
    expect(startRequests()).toBe(0);
    // The play button is usable again for a retry.
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
});
