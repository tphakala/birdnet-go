/**
 * Spectrogram Loader State Machine
 *
 * Manages the full lifecycle of loading a spectrogram for a detection card:
 * 1. Check status via lightweight JSON endpoint (no queue slot)
 * 2. Trigger generation if needed (fire-and-forget POST)
 * 3. Poll status with exponential backoff until ready
 * 4. Acquire image queue slot (guaranteed fast - file exists on disk)
 * 5. Provide URL for img.src
 *
 * Designed to prevent head-of-line blocking under HTTP/1.1 constraints.
 * Status checks and generation triggers don't use queue slots.
 * Only the final image fetch (confirmed ready on disk) uses a slot.
 */

import {
  acquireSlot,
  getQueueStats,
  releaseSlot,
  type SlotHandle,
} from '$lib/utils/imageLoadQueue';
import { getCsrfToken } from '$lib/utils/api';
import { getStoredValue } from '$lib/utils/storage';
import { loggers } from '$lib/utils/logger';
import { buildAppUrl } from '$lib/utils/urlHelpers';

const logger = loggers.ui;

// Diagnostics for a card whose spinner is held up by the browser rather than by
// the server: a late poll timer, a slow status or generate request, a long wait for an image
// slot, or an image that keeps its slot too long. They are off by default; turn
// them on in one browser with localStorage.setItem('birdnet-debug-spectrogram',
// 'true'), which takes effect without a reload. Each one then logs a throttled
// warning, which production builds keep.

/** localStorage key that turns on the loader diagnostics in this browser. */
export const SPECTROGRAM_DIAGNOSTICS_STORAGE_KEY = 'birdnet-debug-spectrogram';

/** A poll timer firing this much later than scheduled was throttled, typically in a background tab. */
export const LATE_POLL_TIMER_WARN_MS = 2000;
/** A status request taking longer than this most likely waited in the browser's connection queue. */
export const SLOW_STATUS_FETCH_WARN_MS = 3000;
/** A generate request taking longer than this most likely waited in the browser's connection queue. */
export const SLOW_GENERATE_FETCH_WARN_MS = 3000;
/** Waiting longer than this for an image load slot means the slots are held. */
export const SLOW_SLOT_WAIT_WARN_MS = 5000;
/** An image holding its load slot longer than this before load or error fired. */
export const LONG_SLOT_HOLD_WARN_MS = 10000;

/** Logger throttle keys, one per kind of diagnostic warning. */
const POLL_LATE_THROTTLE_KEY = 'spectrogram-poll-late';
const STATUS_SLOW_THROTTLE_KEY = 'spectrogram-status-slow';
const GENERATE_SLOW_THROTTLE_KEY = 'spectrogram-generate-slow';
const SLOT_WAIT_THROTTLE_KEY = 'spectrogram-slot-wait';
const SLOT_HOLD_THROTTLE_KEY = 'spectrogram-slot-hold';

/**
 * Logs a throttled diagnostic warning when elapsedMs exceeds limitMs and the
 * diagnostics are turned on in this browser.
 */
function warnIfOver(
  elapsedMs: number,
  limitMs: number,
  message: string,
  context: Record<string, unknown>,
  throttleKey: string
): void {
  if (elapsedMs <= limitMs) return;
  const enabled = getStoredValue(
    SPECTROGRAM_DIAGNOSTICS_STORAGE_KEY,
    false,
    (v): v is boolean => typeof v === 'boolean'
  );
  if (enabled) {
    logger.warn(message, null, context, throttleKey);
  }
}

/** Loading states for the spectrogram loader state machine */
export type SpectrogramLoadState =
  | 'idle'
  | 'checking'
  | 'triggering'
  | 'polling'
  | 'acquiring-slot'
  | 'loading'
  | 'loaded'
  | 'error';

/** Configuration for the spectrogram loader */
export interface SpectrogramLoaderConfig {
  size?: string;
  raw?: boolean;
  initialPollIntervalMs?: number;
  maxPollIntervalMs?: number;
  maxPollAttempts?: number;
  maxImageRetries?: number;
  imageRetryDelays?: number[];
  maxFetchRetries?: number;
}

/** Status values returned by the backend */
type SpectrogramStatus =
  'exists' | 'generated' | 'not_started' | 'queued' | 'generating' | 'failed';

const DEFAULT_CONFIG: Required<SpectrogramLoaderConfig> = {
  size: 'md',
  raw: true,
  initialPollIntervalMs: 1000,
  maxPollIntervalMs: 8000,
  maxPollAttempts: 60,
  maxImageRetries: 3,
  imageRetryDelays: [500, 1000, 2000],
  maxFetchRetries: 2,
};

/**
 * Creates a spectrogram loader for a single detection card.
 *
 * Call `start(detectionId)` when the card becomes visible.
 * Call `stop()` when hidden or destroyed.
 * Read `state`, `spectrogramUrl`, `showSpinner`, `error` for UI binding.
 *
 * Logs the browser-side diagnostics described above when they are turned on
 * (SPECTROGRAM_DIAGNOSTICS_STORAGE_KEY).
 */
export function createSpectrogramLoader(userConfig: SpectrogramLoaderConfig = {}) {
  const config = { ...DEFAULT_CONFIG, ...userConfig };

  // Reactive state (Svelte 5 runes)
  let state = $state<SpectrogramLoadState>('idle');
  let spectrogramUrl = $state('');
  let showSpinner = $state(false);
  let hasError = $state(false);
  let currentDetectionId = $state<number | undefined>(undefined);
  let serverStatus = $state<SpectrogramStatus | undefined>(undefined);

  // Internal state (not reactive)
  let pollTimer: ReturnType<typeof setTimeout> | undefined;
  let spinnerTimer: ReturnType<typeof setTimeout> | undefined;
  let pollAttempts = 0;
  let currentPollInterval: number = config.initialPollIntervalMs;
  let imageRetryCount = 0;
  let slotHandle: SlotHandle | undefined;
  let hasSlot = false;
  let slotAcquiredAt = 0;
  let abortController: AbortController | undefined;
  let destroyed = false;

  // --- Internal helpers ---

  function clearTimers(): void {
    if (pollTimer) {
      clearTimeout(pollTimer);
      pollTimer = undefined;
    }
    if (spinnerTimer) {
      clearTimeout(spinnerTimer);
      spinnerTimer = undefined;
    }
  }

  function abortPending(): void {
    if (abortController) {
      abortController.abort();
      abortController = undefined;
    }
  }

  function releaseCurrentSlot(): void {
    if (hasSlot) {
      const heldMs = Date.now() - slotAcquiredAt;
      warnIfOver(
        heldMs,
        LONG_SLOT_HOLD_WARN_MS,
        'Spectrogram image held its load slot for a long time',
        { detectionId: currentDetectionId, heldMs, state },
        SLOT_HOLD_THROTTLE_KEY
      );
      releaseSlot();
      hasSlot = false;
    }
    if (slotHandle) {
      slotHandle.cancel();
      slotHandle = undefined;
    }
  }

  function resetInternalState(): void {
    pollAttempts = 0;
    currentPollInterval = config.initialPollIntervalMs;
    imageRetryCount = 0;
  }

  function startSpinnerDelay(): void {
    spinnerTimer = setTimeout(() => {
      if (state !== 'loaded' && state !== 'error' && state !== 'idle') {
        showSpinner = true;
      }
    }, 150);
  }

  function buildStatusUrl(detectionId: number): string {
    return buildAppUrl(
      `/api/v2/spectrogram/${detectionId}/status?size=${config.size}&raw=${String(config.raw)}`
    );
  }

  function buildGenerateUrl(detectionId: number): string {
    return buildAppUrl(
      `/api/v2/spectrogram/${detectionId}/generate?size=${config.size}&raw=${String(config.raw)}`
    );
  }

  function buildImageUrl(detectionId: number): string {
    let url = buildAppUrl(
      `/api/v2/spectrogram/${detectionId}?size=${config.size}&raw=${String(config.raw)}`
    );
    if (imageRetryCount > 0) {
      url += `&t=${String(Date.now())}`;
    }
    return url;
  }

  function isStale(forDetectionId: number): boolean {
    return destroyed || currentDetectionId !== forDetectionId;
  }

  // --- State machine transitions ---

  async function checkStatus(detectionId: number): Promise<void> {
    if (isStale(detectionId)) return;

    state = 'checking';

    let fetchRetries = 0;
    while (fetchRetries <= config.maxFetchRetries) {
      try {
        abortController = new AbortController();
        const fetchStart = Date.now();
        const response = await fetch(buildStatusUrl(detectionId), {
          signal: abortController.signal,
        });
        const fetchMs = Date.now() - fetchStart;
        warnIfOver(
          fetchMs,
          SLOW_STATUS_FETCH_WARN_MS,
          'Spectrogram status request was slow',
          { detectionId, fetchMs, httpStatus: response.status },
          STATUS_SLOW_THROTTLE_KEY
        );

        if (isStale(detectionId)) return;

        if (!response.ok) {
          throw new Error(`Status check failed: ${String(response.status)}`);
        }

        const json = (await response.json()) as { data: { status: string } };
        const status = json.data.status as SpectrogramStatus;
        serverStatus = status;

        switch (status) {
          case 'exists':
          case 'generated':
            await acquireAndLoad(detectionId);
            return;

          case 'not_started':
            await triggerGeneration(detectionId);
            return;

          case 'queued':
          case 'generating':
            startPolling(detectionId);
            return;

          case 'failed':
            // Treat as transient if we still have poll budget — the audio file
            // may not have been written yet when generation was attempted.
            // Retry the full generation cycle after a delay.
            if (pollAttempts < config.maxPollAttempts) {
              pollAttempts++;
              logger.debug('Spectrogram generation failed, retrying (audio may not be ready)', {
                detectionId,
                attempt: pollAttempts,
              });
              currentPollInterval = Math.min(
                config.initialPollIntervalMs * Math.pow(2, Math.min(pollAttempts, 4)),
                config.maxPollIntervalMs
              );
              pollTimer = setTimeout(() => {
                if (isStale(detectionId)) return;
                void triggerGeneration(detectionId);
              }, currentPollInterval);
              state = 'polling';
              return;
            }
            logger.warn('Spectrogram generation failed on server after retries', { detectionId });
            state = 'error';
            hasError = true;
            showSpinner = false;
            return;

          default:
            logger.warn('Unknown spectrogram status', { detectionId, status });
            await triggerGeneration(detectionId);
            return;
        }
      } catch (err: unknown) {
        if (err instanceof DOMException && err.name === 'AbortError') return;
        if (isStale(detectionId)) return;

        fetchRetries++;
        if (fetchRetries > config.maxFetchRetries) {
          logger.error('Status check failed after retries', { detectionId, error: err });
          state = 'error';
          hasError = true;
          showSpinner = false;
          return;
        }
        await new Promise(resolve => setTimeout(resolve, 500 * fetchRetries));
        if (isStale(detectionId)) return;
      }
    }
  }

  async function triggerGeneration(detectionId: number): Promise<void> {
    if (isStale(detectionId)) return;

    state = 'triggering';
    abortController = new AbortController();

    try {
      const csrfToken = getCsrfToken();
      const headers: Record<string, string> = {};
      if (csrfToken) {
        headers['X-CSRF-Token'] = csrfToken;
      }

      const fetchStart = Date.now();
      const response = await fetch(buildGenerateUrl(detectionId), {
        method: 'POST',
        headers,
        signal: abortController.signal,
      });
      const fetchMs = Date.now() - fetchStart;
      warnIfOver(
        fetchMs,
        SLOW_GENERATE_FETCH_WARN_MS,
        'Spectrogram generate request was slow',
        { detectionId, fetchMs, httpStatus: response.status },
        GENERATE_SLOW_THROTTLE_KEY
      );

      if (isStale(detectionId)) return;

      if (response.ok || response.status === 202) {
        const json = (await response.json()) as { data: { status: string } };
        const status = json.data.status as SpectrogramStatus;
        serverStatus = status;

        if (status === 'exists') {
          await acquireAndLoad(detectionId);
          return;
        }
      }

      startPolling(detectionId);
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      if (isStale(detectionId)) return;

      logger.debug('Generation trigger failed, falling back to polling', {
        detectionId,
        error: err,
      });
      startPolling(detectionId);
    }
  }

  function startPolling(detectionId: number): void {
    if (isStale(detectionId)) return;

    state = 'polling';
    currentPollInterval = config.initialPollIntervalMs;
    // Note: pollAttempts is NOT reset here — it accumulates across the entire
    // start() lifecycle so that failed-status retries properly count toward
    // the maxPollAttempts budget. It is reset in resetInternalState() when
    // start() is called with a new detection or after a fresh user action.
    schedulePoll(detectionId);
  }

  function schedulePoll(detectionId: number): void {
    if (isStale(detectionId)) return;

    const interval = currentPollInterval;
    const scheduledAt = Date.now();
    pollTimer = setTimeout(() => {
      const lateMs = Date.now() - scheduledAt - interval;
      warnIfOver(
        lateMs,
        LATE_POLL_TIMER_WARN_MS,
        'Spectrogram poll timer fired late',
        { detectionId, intervalMs: interval, lateMs, visibility: document.visibilityState },
        POLL_LATE_THROTTLE_KEY
      );
      void pollOnce(detectionId);
    }, interval);
  }

  async function pollOnce(detectionId: number): Promise<void> {
    if (isStale(detectionId)) return;

    pollAttempts++;

    if (pollAttempts > config.maxPollAttempts) {
      logger.warn('Spectrogram polling timed out', { detectionId, attempts: pollAttempts });
      serverStatus = 'failed';
      state = 'error';
      hasError = true;
      showSpinner = false;
      return;
    }

    abortController = new AbortController();

    try {
      const fetchStart = Date.now();
      const response = await fetch(buildStatusUrl(detectionId), {
        signal: abortController.signal,
      });
      const fetchMs = Date.now() - fetchStart;
      warnIfOver(
        fetchMs,
        SLOW_STATUS_FETCH_WARN_MS,
        'Spectrogram status request was slow',
        { detectionId, fetchMs, httpStatus: response.status },
        STATUS_SLOW_THROTTLE_KEY
      );

      if (isStale(detectionId)) return;

      if (response.ok) {
        const json = (await response.json()) as { data: { status: string } };
        const status = json.data.status as SpectrogramStatus;
        serverStatus = status;

        if (status === 'exists' || status === 'generated') {
          await acquireAndLoad(detectionId);
          return;
        }

        if (status === 'failed') {
          // Don't give up immediately — audio file may not have been ready.
          // Re-trigger generation if we still have poll budget.
          if (pollAttempts < config.maxPollAttempts) {
            logger.debug('Spectrogram failed during poll, re-triggering generation', {
              detectionId,
              attempt: pollAttempts,
            });
            currentPollInterval = Math.min(
              config.initialPollIntervalMs * Math.pow(2, Math.min(pollAttempts, 4)),
              config.maxPollIntervalMs
            );
            pollTimer = setTimeout(() => {
              if (isStale(detectionId)) return;
              void triggerGeneration(detectionId);
            }, currentPollInterval);
            return;
          }
          logger.warn('Spectrogram generation failed after retries', { detectionId });
          state = 'error';
          hasError = true;
          showSpinner = false;
          return;
        }
      }
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      if (isStale(detectionId)) return;
      logger.debug('Poll fetch error, will retry', { detectionId, error: err });
    }

    currentPollInterval = Math.min(currentPollInterval * 2, config.maxPollIntervalMs);
    schedulePoll(detectionId);
  }

  async function acquireAndLoad(detectionId: number): Promise<void> {
    if (isStale(detectionId)) return;

    state = 'acquiring-slot';

    const waitStart = Date.now();
    slotHandle = acquireSlot();
    const acquired = await slotHandle.promise;
    if (acquired) {
      const waitMs = Date.now() - waitStart;
      warnIfOver(
        waitMs,
        SLOW_SLOT_WAIT_WARN_MS,
        'Spectrogram waited long for an image load slot',
        { detectionId, waitMs, ...getQueueStats() },
        SLOT_WAIT_THROTTLE_KEY
      );
    }

    // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- state can change during await
    if (!acquired || isStale(detectionId) || state !== 'acquiring-slot') {
      if (acquired) {
        releaseSlot();
      }
      return;
    }

    hasSlot = true;
    slotAcquiredAt = Date.now();
    state = 'loading';
    spectrogramUrl = buildImageUrl(detectionId);
  }

  // --- Public API ---

  function start(detectionId: number): void {
    if (destroyed) return;

    // Same detection already in progress or loaded — no-op
    if (currentDetectionId === detectionId && state !== 'idle' && state !== 'error') {
      return;
    }

    // Same detection with permanent server failure — don't retry endlessly
    if (currentDetectionId === detectionId && state === 'error' && serverStatus === 'failed') {
      return;
    }

    // New detection or restart after transient error
    stop();
    currentDetectionId = detectionId;
    resetInternalState();
    hasError = false;
    showSpinner = false;
    spectrogramUrl = '';
    serverStatus = undefined;
    startSpinnerDelay();
    void checkStatus(detectionId);
  }

  function stop(): void {
    clearTimers();
    abortPending();
    releaseCurrentSlot();

    if (state !== 'loaded') {
      state = 'idle';
      showSpinner = false;
    }
  }

  function handleImageLoad(): void {
    state = 'loaded';
    showSpinner = false;
    imageRetryCount = 0;
    clearTimers();
    releaseCurrentSlot();
  }

  function handleImageError(): void {
    const detectionId = currentDetectionId;
    if (detectionId === undefined) return;

    // Release slot immediately so other cards can load during retry delay
    releaseCurrentSlot();

    if (imageRetryCount < config.maxImageRetries) {
      const delay =
        config.imageRetryDelays[Math.min(imageRetryCount, config.imageRetryDelays.length - 1)];
      imageRetryCount++;

      logger.debug('Spectrogram image load failed, retrying', {
        detectionId,
        retryCount: imageRetryCount,
        delay,
      });

      // Re-acquire slot through normal flow on retry
      pollTimer = setTimeout(() => {
        if (isStale(detectionId)) return;
        void acquireAndLoad(detectionId);
      }, delay);
    } else {
      logger.warn('Spectrogram image load failed after retries', { detectionId });
      state = 'error';
      hasError = true;
      showSpinner = false;
    }
  }

  function destroy(): void {
    destroyed = true;
    stop();
    currentDetectionId = undefined;
  }

  return {
    get state() {
      return state;
    },
    get spectrogramUrl() {
      return spectrogramUrl;
    },
    get showSpinner() {
      return showSpinner;
    },
    get error() {
      return hasError;
    },
    /** True when spectrogram is queued, waiting for a backend generation slot */
    get isQueued() {
      return (
        (state === 'polling' || state === 'triggering') &&
        (serverStatus === 'queued' || serverStatus === 'not_started')
      );
    },
    /** True when the backend is actively generating the spectrogram */
    get isGenerating() {
      return (state === 'polling' || state === 'triggering') && serverStatus === 'generating';
    },

    start,
    stop,
    handleImageLoad,
    handleImageError,
    destroy,
  };
}
