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
import { removeStoredValue, setStoredValue } from '$lib/utils/storage';
import {
  SPECTROGRAM_REASON,
  clearSpectrogramSourceMarks,
  isSpectrogramSourceKnownMissing,
} from '$lib/utils/spectrogramStatus';
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

type Loader = ReturnType<typeof createSpectrogramLoader>;

// Loaders made through makeLoader are destroyed after each test: the parked-loader
// registry is module state, so a loader left parked by a failed assertion would
// fire into the next test's mocks.
const madeLoaders: Loader[] = [];
function makeLoader(config: Parameters<typeof createSpectrogramLoader>[0] = {}): Loader {
  const loader = createSpectrogramLoader(config);
  madeLoaders.push(loader);
  return loader;
}

// Stubs document.visibilityState; the afterEach below restores jsdom's own value.
function setVisibility(visibility: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => visibility,
  });
}

// Answers status and generate requests by URL, so a test does not depend on exact poll counts.
function routeFetch(
  status: () => Promise<unknown>,
  generate: () => Promise<unknown> = () =>
    Promise.resolve(mockJsonResponse({ data: { status: 'queued' } }, 202))
) {
  mockFetch.mockImplementation((url: string) =>
    url.includes('/generate') ? generate() : status()
  );
}

function statusAnswers(status: string) {
  return () => Promise.resolve(mockJsonResponse({ data: { status } }));
}

function statusAnswersWithReason(status: string, reason: string) {
  return () => Promise.resolve(mockJsonResponse({ data: { status, reason } }));
}

function fetchCount(kind: 'status' | 'generate'): number {
  return mockFetch.mock.calls.filter(([url]) => String(url).includes(`/${kind}?`)).length;
}

// Hides the page long enough for every pending cycle timer to fire and park, then shows it again.
async function hideAndShow(target: EventTarget = document, eventName = 'visibilitychange') {
  setVisibility('hidden');
  await vi.advanceTimersByTimeAsync(1000);
  setVisibility('visible');
  target.dispatchEvent(new Event(eventName));
  await flushAll();
}

describe('spectrogramLoader', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    mockFetch.mockReset();
    resetBasePath();
  });

  afterEach(() => {
    for (const loader of madeLoaders.splice(0)) loader.destroy();
    Reflect.deleteProperty(document, 'visibilityState');
    vi.useRealTimers();
    clearSpectrogramSourceMarks();
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

    // A dead clip answers `failed`, or `not_started` once the server evicts the failed
    // entry, so the generate budget has to hold on both.
    it.each([
      [
        'failed',
        async () => {
          await vi.advanceTimersByTimeAsync(2000);
        },
      ],
      [
        'not_started',
        async () => {
          await hideAndShow();
          await hideAndShow();
        },
      ],
    ])(
      'sends at most maxGenerateRequests generate requests when the status is %s, then errors',
      async (status, drive) => {
        routeFetch(statusAnswers(status));
        const loader = makeLoader({
          initialPollIntervalMs: 10,
          maxPollIntervalMs: 40,
          maxGenerateRequests: 2,
        });
        loader.start(42);
        await flushAll();
        await drive();

        expect(fetchCount('generate')).toBe(2);
        expect(loader.state).toBe('error');
        expect(loader.error).toBe(true);

        const fetchCallCount = mockFetch.mock.calls.length;
        loader.start(42);
        document.dispatchEvent(new Event('visibilitychange'));
        await vi.advanceTimersByTimeAsync(1000);
        expect(mockFetch.mock.calls.length).toBe(fetchCallCount);
      }
    );

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
        maxGenerateRequests: 1,
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

  describe('hidden page and request budget', () => {
    it.each([
      ['visibilitychange', document],
      ['pageshow', window],
    ])('resumes a parked poll with a status check on %s', async (eventName, target) => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'generating' } }));
      const loader = makeLoader({ initialPollIntervalMs: 100 });
      loader.start(42);
      await flushAll();
      expect(fetchCount('status')).toBe(1);

      setVisibility('hidden');
      await vi.advanceTimersByTimeAsync(10 * 60 * 1000);
      expect(fetchCount('status')).toBe(1);

      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));
      setVisibility('visible');
      target.dispatchEvent(new Event(eventName));
      await flushAll();

      expect(fetchCount('status')).toBe(2);
      expect(loader.state).toBe('loading');
    });

    it('keeps polling when the status request fails right after a resume', async () => {
      let answer = statusAnswers('generating');
      routeFetch(() => answer());
      const loader = makeLoader({ initialPollIntervalMs: 100 });
      loader.start(42);
      await flushAll();

      setVisibility('hidden');
      await vi.advanceTimersByTimeAsync(10_000);

      // The network is not back yet when the page becomes visible.
      answer = () => Promise.reject(new TypeError('network not ready'));
      setVisibility('visible');
      document.dispatchEvent(new Event('visibilitychange'));
      await flushAll();
      expect(loader.state).toBe('polling');
      expect(loader.error).toBe(false);

      answer = statusAnswers('exists');
      await vi.advanceTimersByTimeAsync(10_000);
      expect(loader.state).toBe('loading');
    });

    it('asks for generation again when a poll finds the status back at not_started', async () => {
      let statusCalls = 0;
      routeFetch(() => {
        statusCalls++;
        return statusAnswers(statusCalls === 1 ? 'generating' : 'not_started')();
      });
      const loader = makeLoader({ initialPollIntervalMs: 100 });
      loader.start(42);
      await flushAll();
      expect(fetchCount('generate')).toBe(0);

      await vi.advanceTimersByTimeAsync(150);
      expect(fetchCount('generate')).toBe(1);
    });

    it('keeps hide and show cycles within the poll budget', async () => {
      routeFetch(statusAnswers('generating'));
      const loader = makeLoader({ initialPollIntervalMs: 100, maxPollAttempts: 2 });
      loader.start(42);
      await flushAll();

      for (let i = 0; i < 5; i++) {
        await hideAndShow();
      }

      // The initial check is not a cycle status check.
      expect(fetchCount('status') - 1).toBeLessThanOrEqual(2);
      expect(loader.state).toBe('error');
    });

    it('parks a failed re-trigger while hidden and sends the generate when visible', async () => {
      routeFetch(statusAnswers('failed'));
      const loader = makeLoader({ initialPollIntervalMs: 100, maxGenerateRequests: 1 });
      loader.start(42);
      await flushAll();

      setVisibility('hidden');
      await vi.advanceTimersByTimeAsync(10_000);
      expect(fetchCount('generate')).toBe(0);

      setVisibility('visible');
      document.dispatchEvent(new Event('visibilitychange'));
      await flushAll();
      expect(fetchCount('generate')).toBe(1);
    });

    it('makes only the first status check when started in a hidden page', async () => {
      routeFetch(statusAnswers('generating'));
      setVisibility('hidden');
      const loader = makeLoader({ initialPollIntervalMs: 100 });
      loader.start(42);
      await vi.advanceTimersByTimeAsync(10 * 60 * 1000);
      expect(fetchCount('status')).toBe(1);

      setVisibility('visible');
      document.dispatchEvent(new Event('visibilitychange'));
      await flushAll();
      expect(fetchCount('status')).toBe(2);
    });

    // Spies on the listener registration of document and window; restores them even when an assertion fails.
    async function withListenerSpies(
      body: (calls: (target: 'add' | 'remove', type: string) => number) => Promise<void>
    ) {
      const spies = {
        add: [vi.spyOn(document, 'addEventListener'), vi.spyOn(window, 'addEventListener')],
        remove: [
          vi.spyOn(document, 'removeEventListener'),
          vi.spyOn(window, 'removeEventListener'),
        ],
      };
      try {
        await body((target, type) =>
          spies[target].reduce(
            (sum, spy) => sum + spy.mock.calls.filter(([eventType]) => eventType === type).length,
            0
          )
        );
      } finally {
        [...spies.add, ...spies.remove].forEach(spy => spy.mockRestore());
      }
    }

    // Starts three loaders that park on a hidden page.
    async function parkThreeLoaders() {
      const parked = [makeLoader(), makeLoader(), makeLoader()];
      parked.forEach((loader, i) => loader.start(40 + i));
      await flushAll();
      setVisibility('hidden');
      await vi.advanceTimersByTimeAsync(10_000);
      return parked;
    }

    it('keeps one visibility listener pair for all parked loaders and removes it after the last stops', async () => {
      routeFetch(statusAnswers('generating'));
      await withListenerSpies(async calls => {
        const parked = await parkThreeLoaders();
        expect(calls('add', 'visibilitychange')).toBe(1);
        expect(calls('add', 'pageshow')).toBe(1);

        parked[0].destroy();
        parked[1].destroy();
        expect(calls('remove', 'visibilitychange')).toBe(0);
        expect(calls('remove', 'pageshow')).toBe(0);
        parked[2].destroy();
        expect(calls('remove', 'visibilitychange')).toBe(1);
        expect(calls('remove', 'pageshow')).toBe(1);

        const fetchCallCount = mockFetch.mock.calls.length;
        setVisibility('visible');
        document.dispatchEvent(new Event('visibilitychange'));
        await flushAll();
        expect(mockFetch.mock.calls.length).toBe(fetchCallCount);
      });
    });

    it('resumes every parked loader on one visible event and removes the listeners', async () => {
      routeFetch(statusAnswers('generating'));
      await withListenerSpies(async calls => {
        await parkThreeLoaders();
        expect(fetchCount('status')).toBe(3);

        setVisibility('visible');
        document.dispatchEvent(new Event('visibilitychange'));
        await flushAll();

        expect(fetchCount('status')).toBe(6);
        expect(calls('remove', 'visibilitychange')).toBe(1);
        expect(calls('remove', 'pageshow')).toBe(1);
      });
    });

    it('stops at the default of four generate requests for a source that keeps failing', async () => {
      routeFetch(statusAnswers('failed'));
      const loader = makeLoader();
      loader.start(42);
      await vi.advanceTimersByTimeAsync(60_000);

      expect(fetchCount('generate')).toBe(4);
      expect(loader.state).toBe('error');
    });

    it('doubles the re-trigger delay per generate request up to maxPollIntervalMs', async () => {
      routeFetch(statusAnswers('failed'));
      const loader = makeLoader({
        initialPollIntervalMs: 100,
        maxPollIntervalMs: 300,
        maxGenerateRequests: 4,
      });
      loader.start(42);
      await flushAll();

      // The first re-trigger waits 100 ms, the second 200 ms and the third is clamped to 300 ms.
      const generateTimes: [number, number][] = [
        [99, 0],
        [1, 1],
        [299, 1],
        [1, 2],
        [399, 2],
        [1, 3],
      ];
      for (const [advanceMs, expected] of generateTimes) {
        await vi.advanceTimersByTimeAsync(advanceMs);
        expect(fetchCount('generate')).toBe(expected);
      }
    });

    it.each([
      ['stop', 700, 1],
      ['stop then start', 700, 2],
    ])('ends the status check backoff after %s', async (label, advanceMs, expectedFetches) => {
      let statusCalls = 0;
      routeFetch(() => {
        statusCalls++;
        return statusCalls === 1
          ? Promise.reject(new Error('Network error'))
          : Promise.resolve(mockJsonResponse({ data: { status: 'generating' } }));
      });
      const loader = makeLoader({ maxFetchRetries: 2 });
      loader.start(42);
      await flushAll();

      await vi.advanceTimersByTimeAsync(100);
      loader.stop();
      if (label === 'stop then start') loader.start(42);
      await vi.advanceTimersByTimeAsync(advanceMs);

      expect(fetchCount('status')).toBe(expectedFetches);
    });
  });

  describe('status reason', () => {
    const PLAIN_IMAGE_URL = '/api/v2/spectrogram/42?size=md&raw=true';
    // Longer than every poll and re-trigger delay, so a loader still cycling would show it.
    const LONG_WAIT_MS = 120_000;

    it.each([
      ['failed', SPECTROGRAM_REASON.sourceInvalid],
      ['not_started', SPECTROGRAM_REASON.sourceInvalid],
    ])('fails at once on %s with %s and sends no generate request', async (status, reason) => {
      routeFetch(statusAnswersWithReason(status, reason));
      const loader = makeLoader();
      loader.start(42);
      await vi.advanceTimersByTimeAsync(LONG_WAIT_MS);

      expect(fetchCount('generate')).toBe(0);
      expect(fetchCount('status')).toBe(1);
      expect(loader.state).toBe('error');
      expect(loader.error).toBe(true);
    });

    it('stays failed across stop and start for the same detection', async () => {
      routeFetch(statusAnswersWithReason('failed', SPECTROGRAM_REASON.sourceInvalid));
      const loader = makeLoader();
      loader.start(42);
      await flushAll();
      const callsAfterFailure = mockFetch.mock.calls.length;

      loader.stop();
      loader.start(42);
      await vi.advanceTimersByTimeAsync(LONG_WAIT_MS);

      expect(mockFetch.mock.calls.length).toBe(callsAfterFailure);
      expect(loader.state).toBe('error');
      expect(loader.error).toBe(true);
    });

    it('still starts a different detection after a terminal failure', async () => {
      routeFetch(statusAnswersWithReason('failed', SPECTROGRAM_REASON.sourceInvalid));
      const loader = makeLoader();
      loader.start(42);
      await flushAll();

      routeFetch(statusAnswers('exists'));
      loader.start(99);
      await flushAll();

      expect(loader.error).toBe(false);
      expect(loader.spectrogramUrl).toBe('/api/v2/spectrogram/99?size=md&raw=true');
    });

    it('loads the plain image once instead of generating for a missing source, and marks the clip', async () => {
      routeFetch(statusAnswersWithReason('not_started', SPECTROGRAM_REASON.sourceMissing));
      const loader = makeLoader();
      loader.start(42);
      await flushAll();

      expect(fetchCount('generate')).toBe(0);
      expect(loader.spectrogramUrl).toBe(PLAIN_IMAGE_URL);
      expect(loader.state).toBe('loading');
      expect(isSpectrogramSourceKnownMissing(42)).toBe(true);

      // A render the server kept shows after all.
      loader.handleImageLoad();
      expect(loader.state).toBe('loaded');
      expect(loader.error).toBe(false);
    });

    it('fails permanently when the plain image of a missing source fails, without image retries', async () => {
      routeFetch(statusAnswersWithReason('not_started', SPECTROGRAM_REASON.sourceMissing));
      const loader = makeLoader();
      loader.start(42);
      await flushAll();
      const slotsAcquired = mockAcquireSlot.mock.calls.length;

      loader.handleImageError();
      await vi.advanceTimersByTimeAsync(LONG_WAIT_MS);

      expect(loader.state).toBe('error');
      expect(loader.error).toBe(true);
      expect(mockAcquireSlot.mock.calls.length).toBe(slotsAcquired);
      expect(fetchCount('generate')).toBe(0);

      // A scroll out and back must not reset the verdict.
      const callsAfterFailure = mockFetch.mock.calls.length;
      loader.stop();
      loader.start(42);
      await flushAll();
      expect(mockFetch.mock.calls.length).toBe(callsAfterFailure);
      expect(loader.state).toBe('error');
      expect(loader.error).toBe(true);
    });

    it('applies a missing source reported by a poll the same way', async () => {
      let answers = 0;
      routeFetch(() =>
        Promise.resolve(
          mockJsonResponse({
            data:
              answers++ === 0
                ? { status: 'generating' }
                : { status: 'not_started', reason: SPECTROGRAM_REASON.sourceMissing },
          })
        )
      );
      const loader = makeLoader();
      loader.start(42);
      await vi.advanceTimersByTimeAsync(2000);

      expect(fetchCount('generate')).toBe(0);
      expect(loader.spectrogramUrl).toBe(PLAIN_IMAGE_URL);
    });

    it('tries the plain image once for a missing source reported as failed, without generating', async () => {
      routeFetch(statusAnswersWithReason('failed', SPECTROGRAM_REASON.sourceMissing));
      const loader = makeLoader();
      loader.start(42);
      await vi.advanceTimersByTimeAsync(LONG_WAIT_MS);

      expect(fetchCount('generate')).toBe(0);
      expect(loader.spectrogramUrl).toBe(PLAIN_IMAGE_URL);
      expect(loader.state).toBe('loading');
    });

    // A poll that finds the first answer `generating` and the second `failed` with a reason.
    async function startAndPollFailed(reason: string): Promise<Loader> {
      let answers = 0;
      routeFetch(() =>
        Promise.resolve(
          mockJsonResponse({
            data: answers++ === 0 ? { status: 'generating' } : { status: 'failed', reason },
          })
        )
      );
      const loader = makeLoader();
      loader.start(42);
      await vi.advanceTimersByTimeAsync(LONG_WAIT_MS);
      return loader;
    }

    it('ends a poll that answers failed with source_invalid without generating', async () => {
      const loader = await startAndPollFailed(SPECTROGRAM_REASON.sourceInvalid);

      expect(fetchCount('generate')).toBe(0);
      expect(fetchCount('status')).toBe(2);
      expect(loader.state).toBe('error');
    });

    it('tries the plain image when a poll answers failed with source_missing', async () => {
      const loader = await startAndPollFailed(SPECTROGRAM_REASON.sourceMissing);

      expect(fetchCount('generate')).toBe(0);
      expect(fetchCount('status')).toBe(2);
      expect(loader.spectrogramUrl).toBe(PLAIN_IMAGE_URL);
    });

    it('keeps retrying past the generate budget while the server is busy', async () => {
      routeFetch(statusAnswersWithReason('failed', SPECTROGRAM_REASON.busy));
      const loader = makeLoader();
      loader.start(42);
      await vi.advanceTimersByTimeAsync(LONG_WAIT_MS);

      expect(fetchCount('generate')).toBeGreaterThan(4);
      expect(loader.error).toBe(false);
    });

    it.each([SPECTROGRAM_REASON.sourcePending, SPECTROGRAM_REASON.renderFailed])(
      'keeps the generate budget for a failed status with %s',
      async reason => {
        routeFetch(statusAnswersWithReason('failed', reason));
        const loader = makeLoader();
        loader.start(42);
        await vi.advanceTimersByTimeAsync(LONG_WAIT_MS);

        expect(fetchCount('generate')).toBe(4);
        expect(loader.state).toBe('error');
      }
    );

    it.each([SPECTROGRAM_REASON.notGenerated, SPECTROGRAM_REASON.sourcePending])(
      'still asks for generation on not_started with %s',
      async reason => {
        routeFetch(statusAnswersWithReason('not_started', reason));
        const loader = makeLoader();
        loader.start(42);
        await flushAll();

        expect(fetchCount('generate')).toBe(1);
      }
    );
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
      setStoredValue(SPECTROGRAM_DIAGNOSTICS_STORAGE_KEY, true);
    });

    afterEach(() => {
      removeStoredValue(SPECTROGRAM_DIAGNOSTICS_STORAGE_KEY);
    });

    it('stays quiet with the diagnostics turned off, even for a long slot hold', async () => {
      removeStoredValue(SPECTROGRAM_DIAGNOSTICS_STORAGE_KEY);
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

    it.each([
      ['load', (l: ReturnType<typeof createSpectrogramLoader>) => l.handleImageLoad()],
      ['error', (l: ReturnType<typeof createSpectrogramLoader>) => l.handleImageError()],
      ['stop', (l: ReturnType<typeof createSpectrogramLoader>) => l.stop()],
    ])('names %s as what released a long slot hold', async (releasedBy, release) => {
      mockFetch.mockResolvedValueOnce(mockJsonResponse({ data: { status: 'exists' } }));
      const loader = createSpectrogramLoader();
      loader.start(42);
      await flushAll();

      vi.setSystemTime(Date.now() + LONG_SLOT_HOLD_WARN_MS + 500);
      release(loader);

      expect(warn).toHaveBeenCalledWith(
        'Spectrogram image held its load slot for a long time',
        null,
        expect.objectContaining({ detectionId: 42, releasedBy }),
        'spectrogram-slot-hold'
      );
      loader.destroy();
    });
  });
});
