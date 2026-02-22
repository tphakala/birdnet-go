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

import { acquireSlot, releaseSlot, type SlotHandle } from '$lib/utils/imageLoadQueue';
import { getCsrfToken } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';

const logger = loggers.ui;

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
  | 'exists'
  | 'generated'
  | 'not_started'
  | 'queued'
  | 'generating'
  | 'failed';

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
 */
export function createSpectrogramLoader(userConfig: SpectrogramLoaderConfig = {}) {
  const config = { ...DEFAULT_CONFIG, ...userConfig };

  // Reactive state (Svelte 5 runes)
  // eslint-disable-next-line no-undef -- Svelte 5 runes are globally available in .svelte.ts files
  let state = $state<SpectrogramLoadState>('idle');
  // eslint-disable-next-line no-undef -- Svelte 5 rune
  let spectrogramUrl = $state('');
  // eslint-disable-next-line no-undef -- Svelte 5 rune
  let showSpinner = $state(false);
  // eslint-disable-next-line no-undef -- Svelte 5 rune
  let hasError = $state(false);
  // eslint-disable-next-line no-undef -- Svelte 5 rune
  let currentDetectionId = $state<number | undefined>(undefined);
  // eslint-disable-next-line no-undef -- Svelte 5 rune
  let serverStatus = $state<SpectrogramStatus | undefined>(undefined);

  // Internal state (not reactive)
  let pollTimer: ReturnType<typeof setTimeout> | undefined;
  let spinnerTimer: ReturnType<typeof setTimeout> | undefined;
  let pollAttempts = 0;
  let currentPollInterval: number = config.initialPollIntervalMs;
  let imageRetryCount = 0;
  let slotHandle: SlotHandle | undefined;
  let hasSlot = false;
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
    return `/api/v2/spectrogram/${detectionId}/status?size=${config.size}&raw=${String(config.raw)}`;
  }

  function buildGenerateUrl(detectionId: number): string {
    return `/api/v2/spectrogram/${detectionId}/generate?size=${config.size}&raw=${String(config.raw)}`;
  }

  function buildImageUrl(detectionId: number): string {
    let url = `/api/v2/spectrogram/${detectionId}?size=${config.size}&raw=${String(config.raw)}`;
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
        const response = await fetch(buildStatusUrl(detectionId), {
          signal: abortController.signal,
        });

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
            logger.warn('Spectrogram generation failed on server', { detectionId });
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

      const response = await fetch(buildGenerateUrl(detectionId), {
        method: 'POST',
        headers,
        signal: abortController.signal,
      });

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
    pollAttempts = 0;
    schedulePoll(detectionId);
  }

  function schedulePoll(detectionId: number): void {
    if (isStale(detectionId)) return;

    pollTimer = setTimeout(() => {
      void pollOnce(detectionId);
    }, currentPollInterval);
  }

  async function pollOnce(detectionId: number): Promise<void> {
    if (isStale(detectionId)) return;

    pollAttempts++;

    if (pollAttempts > config.maxPollAttempts) {
      logger.warn('Spectrogram polling timed out', { detectionId, attempts: pollAttempts });
      state = 'error';
      hasError = true;
      showSpinner = false;
      return;
    }

    abortController = new AbortController();

    try {
      const response = await fetch(buildStatusUrl(detectionId), {
        signal: abortController.signal,
      });

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
          logger.warn('Spectrogram generation failed', { detectionId });
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

    slotHandle = acquireSlot();
    const acquired = await slotHandle.promise;

    // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- state can change during await
    if (!acquired || isStale(detectionId) || state !== 'acquiring-slot') {
      if (acquired) {
        releaseSlot();
      }
      return;
    }

    hasSlot = true;
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
