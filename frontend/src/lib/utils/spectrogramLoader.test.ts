import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { resetBasePath, setBasePath } from './urlHelpers';

// Default: acquireSlot resolves immediately with true
const mockAcquireSlot = vi.fn(() => ({
  promise: Promise.resolve(true),
  cancel: vi.fn(),
}));
const mockReleaseSlot = vi.fn();
const mockGetQueueStats = vi.fn(() => ({ active: 3, queued: 2, maxConcurrent: 3 }));

vi.mock('$lib/utils/imageLoadQueue', () => ({
  get acquireSlot() {
    return mockAcquireSlot;
  },
  get releaseSlot() {
    return mockReleaseSlot;
  },
  get getQueueStats() {
    return mockGetQueueStats;
  },
  MAX_CONCURRENT_IMAGE_LOADS: 3,
}));

// Mock CSRF token
vi.mock('$lib/utils/api', () => ({
  getCsrfToken: () => 'test-csrf-token',
}));

// Mock logger
vi.mock('$lib/utils/logger', () => ({
  loggers: {
    ui: {
      debug: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
  },
}));

// Mock fetch globally
const mockFetch = vi.fn();
vi.stubGlobal('fetch', mockFetch);

import { loggers } from '$lib/utils/logger';
import {
  createSpectrogramLoader,
  LATE_POLL_TIMER_WARN_MS,
  LONG_SLOT_HOLD_WARN_MS,
  SLOW_GENERATE_FETCH_WARN_MS,
  SLOW_SLOT_WAIT_WARN_MS,
  SLOW_STATUS_FETCH_WARN_MS,
  SPECTROGRAM_DIAGNOSTICS_STORAGE_KEY,
} from './spectrogramLoader.svelte';

// Helper to create mock JSON response
function mockJsonResponse(data: Record<string, unknown>, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(data),
  };
}

// Helper to flush all microtasks and pending timers
async function flushAll() {
  await vi.advanceTimersByTimeAsync(0);
}

describe('spectrogramLoader', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    mockFetch.mockReset();
    resetBasePath();
  });

  afterEach(() => {
    vi.useRealTimers();
    resetBasePath();
  });

  describe('initial state', () => {
    it('starts in idle state with no URL', () => {
      const loader = createSpectrogramLoader();
      expect(loader.state).toBe('idle');
      expect(loader.spectrogramUrl).toBe('');
      expect(loader.showSpinner).toBe(false);
      expect(loader.error).toBe(false);
      expect(loader.isGenerating).toBe(false);
      expect(loader.isQueued).toBe(false);
      loader.destroy();
    });
  });

  describe('status exists — immediate load', () => {
    it('acquires slot and sets URL when status is exists', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));

      const loader = createSpectrogramLoader({ size: 'md', raw: true });
      loader.start(42);
      await flushAll();

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/v2/spectrogram/42/status?size=md&raw=true',
        expect.objectContaining({ signal: expect.any(AbortSignal) })
      );
      expect(mockAcquireSlot).toHaveBeenCalled();
      expect(loader.spectrogramUrl).toBe('/api/v2/spectrogram/42?size=md&raw=true');
      expect(loader.state).toBe('loading');
      loader.destroy();
    });

    it('transitions to loaded on handleImageLoad', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));

      const loader = createSpectrogramLoader();
      loader.start(42);
      await flushAll();
      loader.handleImageLoad();

      expect(loader.state).toBe('loaded');
      expect(loader.showSpinner).toBe(false);
      expect(mockReleaseSlot).toHaveBeenCalled();
      loader.destroy();
    });

    it('uses the configured app base path for status and image URLs', async () => {
      setBasePath('/proxy');
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));

      const loader = createSpectrogramLoader({ size: 'md', raw: true });
      loader.start(42);
      await flushAll();

      expect(mockFetch).toHaveBeenCalledWith(
        '/proxy/api/v2/spectrogram/42/status?size=md&raw=true',
        expect.objectContaining({ signal: expect.any(AbortSignal) })
      );
      expect(loader.spectrogramUrl).toBe('/proxy/api/v2/spectrogram/42?size=md&raw=true');
      loader.destroy();
    });
  });

  describe('status not_started — triggers generation', () => {
    it('calls POST generate then starts polling', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'not_started' } }));
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'queued' } }, 202));

      const loader = createSpectrogramLoader({ initialPollIntervalMs: 1000 });
      loader.start(42);
      await flushAll();

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/v2/spectrogram/42/generate?size=md&raw=true',
        expect.objectContaining({
          method: 'POST',
          headers: { 'X-CSRF-Token': 'test-csrf-token' },
        })
      );
      expect(loader.state).toBe('polling');
      expect(loader.isQueued).toBe(true);
      expect(loader.isGenerating).toBe(false);
      loader.destroy();
    });

    it('skips generation and loads when generate returns exists', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'not_started' } }));
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }, 200));

      const loader = createSpectrogramLoader();
      loader.start(42);
      await flushAll();

      expect(mockAcquireSlot).toHaveBeenCalled();
      expect(loader.spectrogramUrl).toContain('/api/v2/spectrogram/42');
      loader.destroy();
    });
  });

  describe('isQueued vs isGenerating', () => {
    it('reports isQueued when server status is queued', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'queued' } }));

      const loader = createSpectrogramLoader({ initialPollIntervalMs: 1000 });
      loader.start(42);
      await flushAll();

      expect(loader.state).toBe('polling');
      expect(loader.isQueued).toBe(true);
      expect(loader.isGenerating).toBe(false);
      loader.destroy();
    });

    it('reports isGenerating when server status is generating', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));

      const loader = createSpectrogramLoader({ initialPollIntervalMs: 1000 });
      loader.start(42);
      await flushAll();

      expect(loader.state).toBe('polling');
      expect(loader.isQueued).toBe(false);
      expect(loader.isGenerating).toBe(true);
      loader.destroy();
    });

    it('transitions from isQueued to isGenerating as server progresses', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'queued' } }));

      const loader = createSpectrogramLoader({ initialPollIntervalMs: 100 });
      loader.start(42);
      await flushAll();

      expect(loader.isQueued).toBe(true);
      expect(loader.isGenerating).toBe(false);

      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));
      vi.advanceTimersByTime(100);
      await flushAll();

      expect(loader.isQueued).toBe(false);
      expect(loader.isGenerating).toBe(true);
      loader.destroy();
    });
  });

  describe('polling', () => {
    it('polls with exponential backoff until exists', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));

      const loader = createSpectrogramLoader({
        initialPollIntervalMs: 100,
        maxPollIntervalMs: 400,
      });
      loader.start(42);
      await flushAll();
      expect(loader.state).toBe('polling');

      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));
      vi.advanceTimersByTime(100);
      await flushAll();

      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));
      vi.advanceTimersByTime(200);
      await flushAll();

      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));
      vi.advanceTimersByTime(400);
      await flushAll();

      expect(mockAcquireSlot).toHaveBeenCalled();
      loader.destroy();
    });

    it('retries on failed status then errors after max attempts', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));

      const loader = createSpectrogramLoader({
        initialPollIntervalMs: 100,
        maxPollIntervalMs: 200,
        maxPollAttempts: 2,
      });
      loader.start(42);
      await flushAll();

      // First failed — retries by re-triggering generation
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'failed' } }));
      vi.advanceTimersByTime(100);
      await flushAll();
      expect(loader.state).toBe('polling');

      // Generation trigger returns queued, poll again
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'queued' } }, 202));
      await vi.advanceTimersByTimeAsync(200);
      await flushAll();

      // Second failed — still within budget, retries again
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'failed' } }));
      await vi.advanceTimersByTimeAsync(200);
      await flushAll();

      // Generation trigger returns queued
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'queued' } }, 202));
      await vi.advanceTimersByTimeAsync(200);
      await flushAll();

      // Third poll — exceeds maxPollAttempts, gives up
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'failed' } }));
      await vi.advanceTimersByTimeAsync(200);
      await flushAll();

      expect(loader.state).toBe('error');
      expect(loader.error).toBe(true);
      loader.destroy();
    });

    it('gives up after maxPollAttempts', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));

      const loader = createSpectrogramLoader({
        initialPollIntervalMs: 10,
        maxPollIntervalMs: 10,
        maxPollAttempts: 2,
      });
      loader.start(42);
      await flushAll();

      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));
      vi.advanceTimersByTime(10);
      await flushAll();

      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));
      vi.advanceTimersByTime(10);
      await flushAll();

      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));
      vi.advanceTimersByTime(10);
      await flushAll();

      expect(loader.state).toBe('error');
      expect(loader.error).toBe(true);
      loader.destroy();
    });
  });

  describe('permanent failure handling', () => {
    it('does not restart for same detection after server-reported failure exhausts retries', async () => {
      // Use maxPollAttempts: 1 so the first failed status during checkStatus
      // triggers a retry, then the second failed during polling exhausts budget.
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'failed' } }));

      const loader = createSpectrogramLoader({
        initialPollIntervalMs: 10,
        maxPollIntervalMs: 10,
        maxPollAttempts: 1,
      });
      loader.start(42);
      await flushAll();

      // First failed — retries (poll budget allows 1 attempt)
      expect(loader.state).toBe('polling');

      // Generation trigger
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'queued' } }, 202));
      await vi.advanceTimersByTimeAsync(20);
      await flushAll();

      // Poll returns failed again — budget exhausted
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'failed' } }));
      await vi.advanceTimersByTimeAsync(20);
      await flushAll();

      expect(loader.state).toBe('error');
      const fetchCallCount = mockFetch.mock.calls.length;

      // Re-starting same detection should be a no-op (permanent failure)
      loader.start(42);
      await flushAll();

      expect(mockFetch.mock.calls.length).toBe(fetchCallCount);
      expect(loader.state).toBe('error');
      loader.destroy();
    });

    it('allows restart for a different detection after failure', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'failed' } }));

      const loader = createSpectrogramLoader();
      loader.start(42);
      await flushAll();

      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));
      loader.start(99);
      await flushAll();

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/v2/spectrogram/99/status?size=md&raw=true',
        expect.objectContaining({ signal: expect.any(AbortSignal) })
      );
      loader.destroy();
    });

    it('allows restart after transient error (network failure)', async () => {
      mockFetch.mockRejectedValue(new Error('Network error'));

      const loader = createSpectrogramLoader({ maxFetchRetries: 0 });
      loader.start(42);
      await flushAll();

      expect(loader.state).toBe('error');

      mockFetch.mockReset();
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));

      loader.start(42);
      await flushAll();

      expect(mockFetch).toHaveBeenCalledTimes(1);
      loader.destroy();
    });
  });

  describe('image error handling', () => {
    it('retries on image error with cache buster', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));

      const loader = createSpectrogramLoader({
        maxImageRetries: 2,
        imageRetryDelays: [50, 100],
      });
      loader.start(42);
      await flushAll();

      expect(loader.spectrogramUrl).toBe('/api/v2/spectrogram/42?size=md&raw=true');

      // Slot is released immediately on error, then re-acquired after delay
      loader.handleImageError();
      expect(mockReleaseSlot).toHaveBeenCalled();

      await vi.advanceTimersByTimeAsync(50);
      await flushAll();

      // After retry, acquireAndLoad re-acquires slot and sets URL with cache-buster
      expect(loader.spectrogramUrl).toContain('/api/v2/spectrogram/42?size=md&raw=true&t=');
      expect(mockAcquireSlot).toHaveBeenCalledTimes(2); // initial + retry
      loader.destroy();
    });

    it('transitions to error after max image retries', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));

      const loader = createSpectrogramLoader({
        maxImageRetries: 1,
        imageRetryDelays: [10],
      });
      loader.start(42);
      await flushAll();

      // First error releases slot and schedules retry
      loader.handleImageError();
      expect(mockReleaseSlot).toHaveBeenCalled();

      await vi.advanceTimersByTimeAsync(10);
      await flushAll();

      // Second error exhausts retries
      mockReleaseSlot.mockClear();
      loader.handleImageError();

      expect(loader.state).toBe('error');
      expect(loader.error).toBe(true);
      // Slot released on error (before exhausting retries check)
      expect(mockReleaseSlot).toHaveBeenCalled();
      loader.destroy();
    });
  });

  describe('cleanup and cancellation', () => {
    it('cancels pending fetch on destroy', async () => {
      let capturedSignal: AbortSignal | undefined;
      mockFetch.mockImplementationOnce((_url: string, opts: { signal: AbortSignal }) => {
        capturedSignal = opts.signal;
        return new Promise(() => {});
      });

      const loader = createSpectrogramLoader();
      loader.start(42);
      await flushAll();

      loader.destroy();
      expect(capturedSignal?.aborted).toBe(true);
    });

    it('cancels slot acquisition on stop', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));

      const cancelFn = vi.fn();
      let slotResolver: ((v: boolean) => void) | undefined;
      mockAcquireSlot.mockReturnValueOnce({
        promise: new Promise<boolean>(resolve => {
          slotResolver = resolve;
        }),
        cancel: cancelFn,
      });

      const loader = createSpectrogramLoader();
      loader.start(42);
      await flushAll();

      expect(loader.state).toBe('acquiring-slot');

      loader.stop();
      expect(cancelFn).toHaveBeenCalled();

      if (slotResolver) slotResolver(true);
      await flushAll();

      expect(loader.state).not.toBe('loading');
      loader.destroy();
    });

    it('keeps loaded image visible after stop', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));

      const loader = createSpectrogramLoader();
      loader.start(42);
      await flushAll();
      loader.handleImageLoad();

      expect(loader.state).toBe('loaded');
      expect(loader.spectrogramUrl).toBeTruthy();

      loader.stop();
      expect(loader.state).toBe('loaded');
      expect(loader.spectrogramUrl).toBeTruthy();
      loader.destroy();
    });
  });

  describe('detection change', () => {
    it('restarts for new detection ID', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));

      const loader = createSpectrogramLoader({ initialPollIntervalMs: 1000 });
      loader.start(42);
      await flushAll();
      expect(loader.state).toBe('polling');

      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));
      loader.start(99);
      await flushAll();

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/v2/spectrogram/99/status?size=md&raw=true',
        expect.objectContaining({ signal: expect.any(AbortSignal) })
      );
      loader.destroy();
    });

    it('is a no-op for same detection already loading', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));

      const loader = createSpectrogramLoader({ initialPollIntervalMs: 1000 });
      loader.start(42);
      await flushAll();

      const fetchCallCount = mockFetch.mock.calls.length;
      loader.start(42);
      await flushAll();

      expect(mockFetch.mock.calls.length).toBe(fetchCallCount);
      loader.destroy();
    });
  });

  describe('spinner delay', () => {
    it('does not show spinner before delay', () => {
      mockFetch.mockImplementation(() => new Promise(() => {}));

      const loader = createSpectrogramLoader();
      loader.start(42);

      expect(loader.showSpinner).toBe(false);
      loader.destroy();
    });

    it('shows spinner after 150ms delay', async () => {
      mockFetch.mockImplementation(() => new Promise(() => {}));

      const loader = createSpectrogramLoader();
      loader.start(42);

      vi.advanceTimersByTime(150);
      expect(loader.showSpinner).toBe(true);
      loader.destroy();
    });
  });

  describe('fetch error handling', () => {
    it('retries status check on network error', async () => {
      mockFetch.mockRejectedValueOnce(new Error('Network error'));
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));

      const loader = createSpectrogramLoader({ maxFetchRetries: 2 });
      loader.start(42);

      await flushAll();
      vi.advanceTimersByTime(500);
      await flushAll();

      expect(mockFetch).toHaveBeenCalledTimes(2);
      expect(mockAcquireSlot).toHaveBeenCalled();
      loader.destroy();
    });

    it('transitions to error after max fetch retries', async () => {
      mockFetch.mockRejectedValue(new Error('Network error'));

      const loader = createSpectrogramLoader({ maxFetchRetries: 1 });
      loader.start(42);

      await flushAll();
      vi.advanceTimersByTime(500);
      await flushAll();

      expect(loader.state).toBe('error');
      expect(loader.error).toBe(true);
      loader.destroy();
    });
  });

  // The diagnostics move the wall clock with vi.setSystemTime, which leaves the
  // fake timers alone, to stand in for a throttled timer, a request queued in
  // the browser, or a held image slot.
  describe('diagnostics', () => {
    const warn = vi.mocked(loggers.ui.warn);
    // For checks that exactly one diagnostic fired, not just that one did.
    const warnMessages = () => warn.mock.calls.map(call => String(call[0]));

    // Starts a loader on detection 42 that is polling every 100 ms.
    async function startPolling() {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));
      const loader = createSpectrogramLoader({ initialPollIntervalMs: 100 });
      loader.start(42);
      await flushAll();
      return loader;
    }

    beforeEach(() => {
      localStorage.setItem(SPECTROGRAM_DIAGNOSTICS_STORAGE_KEY, 'true');
    });

    afterEach(() => {
      localStorage.removeItem(SPECTROGRAM_DIAGNOSTICS_STORAGE_KEY);
    });

    it('stays quiet with the diagnostics turned off, even for a long slot hold', async () => {
      localStorage.removeItem(SPECTROGRAM_DIAGNOSTICS_STORAGE_KEY);
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));
      const loader = createSpectrogramLoader();
      loader.start(42);
      await flushAll();

      vi.setSystemTime(Date.now() + LONG_SLOT_HOLD_WARN_MS + 500);
      loader.handleImageLoad();

      expect(warn).not.toHaveBeenCalled();
      loader.destroy();
    });

    it('warns when a poll timer fires late', async () => {
      const loader = await startPolling();

      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));
      vi.setSystemTime(Date.now() + LATE_POLL_TIMER_WARN_MS + 1000);
      vi.advanceTimersByTime(100);
      await flushAll();

      expect(warn).toHaveBeenCalledWith(
        'Spectrogram poll timer fired late',
        null,
        expect.objectContaining({ detectionId: 42, intervalMs: 100 }),
        'spectrogram-poll-late'
      );
      loader.destroy();
    });

    it('stays quiet for an on-time poll, a fast status request and a quick image', async () => {
      const loader = await startPolling();

      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));
      vi.advanceTimersByTime(100);
      await flushAll();
      expect(loader.state).toBe('loading');
      loader.handleImageLoad();

      expect(warn).not.toHaveBeenCalled();
      loader.destroy();
    });

    it('warns when a status poll request is slow', async () => {
      const loader = await startPolling();

      mockFetch.mockImplementationOnce(() => {
        vi.setSystemTime(Date.now() + SLOW_STATUS_FETCH_WARN_MS + 500);
        return Promise.resolve(mockJsonResponse({ data: { status: 'generating' } }));
      });
      vi.advanceTimersByTime(100);
      await flushAll();

      expect(warn).toHaveBeenCalledWith(
        'Spectrogram status request was slow',
        null,
        expect.objectContaining({ detectionId: 42, httpStatus: 200 }),
        'spectrogram-status-slow'
      );
      loader.destroy();
    });

    it('warns when the first status check is slow', async () => {
      mockFetch.mockImplementationOnce(() => {
        vi.setSystemTime(Date.now() + SLOW_STATUS_FETCH_WARN_MS + 500);
        return Promise.resolve(mockJsonResponse({ data: { status: 'exists' } }));
      });
      const loader = createSpectrogramLoader();
      loader.start(42);
      await flushAll();

      expect(warn).toHaveBeenCalledWith(
        'Spectrogram status request was slow',
        null,
        expect.objectContaining({ detectionId: 42, httpStatus: 200 }),
        'spectrogram-status-slow'
      );
      loader.destroy();
    });

    it.each([
      ['a non-OK status', mockJsonResponse({ error: 'busy' }, 503), 503],
      ['an OK status that ends the cycle', mockJsonResponse({ data: { status: 'exists' } }), 200],
    ])('warns when the generate request is slow, with %s', async (_label, response, httpStatus) => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'not_started' } }));
      mockFetch.mockImplementationOnce(() => {
        vi.setSystemTime(Date.now() + SLOW_GENERATE_FETCH_WARN_MS + 500);
        return Promise.resolve(response);
      });
      const loader = createSpectrogramLoader();
      loader.start(42);
      await flushAll();

      expect(warn).toHaveBeenCalledWith(
        'Spectrogram generate request was slow',
        null,
        expect.objectContaining({ detectionId: 42, httpStatus }),
        'spectrogram-generate-slow'
      );
      loader.destroy();
    });

    it('warns with queue stats when the image slot wait is long', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));
      mockAcquireSlot.mockImplementationOnce(() => {
        vi.setSystemTime(Date.now() + SLOW_SLOT_WAIT_WARN_MS + 500);
        return { promise: Promise.resolve(true), cancel: vi.fn() };
      });
      const loader = createSpectrogramLoader();
      loader.start(42);
      await flushAll();

      expect(warn).toHaveBeenCalledWith(
        'Spectrogram waited long for an image load slot',
        null,
        expect.objectContaining({ detectionId: 42, active: 3, queued: 2, maxConcurrent: 3 }),
        'spectrogram-slot-wait'
      );
      loader.destroy();
    });

    it('warns when an image holds its slot for a long time before loading', async () => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));
      const loader = createSpectrogramLoader();
      loader.start(42);
      await flushAll();
      expect(loader.state).toBe('loading');

      vi.setSystemTime(Date.now() + LONG_SLOT_HOLD_WARN_MS + 500);
      loader.handleImageLoad();

      expect(warnMessages()).toEqual(['Spectrogram image held its load slot for a long time']);
      expect(mockReleaseSlot).toHaveBeenCalledTimes(1);
      loader.destroy();
    });
  });
});
