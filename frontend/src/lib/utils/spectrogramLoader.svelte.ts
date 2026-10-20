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
 *
 * The poll cycle pauses while the page is hidden and resumes the step that was
 * due (a status check that may be followed by a generate request, or the
 * generate request of a re-trigger) when the page is visible again. Generate
 * requests per start() are capped by maxGenerateRequests.
 *
 * The status `reason` refines that: a terminal reason (source_missing,
 * source_invalid) sends no generate request. source_invalid fails the
 * detection at once; source_missing first loads the plain image once, for a
 * render the server kept, and fails when that image fails. The failure
 * outlives stop() and start(). A `busy` failure is retried with backoff
 * outside the generate budget.
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
import {
  SPECTROGRAM_REASON,
  isTerminalSpectrogramReason,
  markSpectrogramSourceMissing,
  parseSpectrogramStatus,
} from '$lib/utils/spectrogramStatus';

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

/**
 * Generate requests a loader sends per start(): the first plus three re-triggers.
 * The server waits out the capture window before it reports `failed`, so a
 * `failed` is either short-lived (file not ready, render interrupted), which a few
 * retries cover, or permanent (source clip missing), which more retries only repeat.
 */
const DEFAULT_MAX_GENERATE_REQUESTS = 4;

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

function isPageHidden(): boolean {
  return document.visibilityState === 'hidden';
}

// Loaders whose poll cycle is parked while the page is hidden. One shared pair of
// listeners serves all of them and is attached only while at least one is parked,
// so many cards cost one listener and a skipped destroy() cannot leak one.
const parkedLoaderResumers = new Set<() => void>();
let visibilityListenersAttached = false;

function attachVisibilityListeners(): void {
  document.addEventListener('visibilitychange', resumeParkedLoaders);
  // A back/forward cache restore may not fire visibilitychange.
  window.addEventListener('pageshow', resumeParkedLoaders);
  visibilityListenersAttached = true;
}

function detachVisibilityListeners(): void {
  document.removeEventListener('visibilitychange', resumeParkedLoaders);
  window.removeEventListener('pageshow', resumeParkedLoaders);
  visibilityListenersAttached = false;
}

/** Resumes every parked loader once the page is visible; the event also fires on hide. */
function resumeParkedLoaders(): void {
  if (isPageHidden()) return;
  // Clear first so a resumer that parks again re-registers and re-attaches cleanly.
  const resumers = [...parkedLoaderResumers];
  parkedLoaderResumers.clear();
  detachVisibilityListeners();
  for (const resume of resumers) resume();
}

function addParkedLoader(resume: () => void): void {
  parkedLoaderResumers.add(resume);
  if (!visibilityListenersAttached) attachVisibilityListeners();
}

function removeParkedLoader(resume: () => void): void {
  if (parkedLoaderResumers.delete(resume) && parkedLoaderResumers.size === 0) {
    detachVisibilityListeners();
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
  /** Generate requests per `start()`, the first one included. */
  maxGenerateRequests?: number;
}

/** Status values the loader knows how to act on */
const SPECTROGRAM_STATUSES = [
  'exists',
  'generated',
  'not_started',
  'queued',
  'generating',
  'failed',
] as const;

type SpectrogramStatus = (typeof SPECTROGRAM_STATUSES)[number];

function isSpectrogramStatus(value: string): value is SpectrogramStatus {
  return SPECTROGRAM_STATUSES.some(known => known === value);
}

const DEFAULT_CONFIG: Required<SpectrogramLoaderConfig> = {
  size: 'md',
  raw: true,
  initialPollIntervalMs: 1000,
  maxPollIntervalMs: 8000,
  maxPollAttempts: 60,
  maxImageRetries: 3,
  imageRetryDelays: [500, 1000, 2000],
  maxFetchRetries: 2,
  maxGenerateRequests: DEFAULT_MAX_GENERATE_REQUESTS,
};

/**
 * Creates a spectrogram loader for a single detection card.
 *
 * Call `start(detectionId)` when the card becomes visible.
 * Call `stop()` when hidden or destroyed.
 * Read `state`, `spectrogramUrl`, `showSpinner`, `error` for UI binding.
 *
 * The poll cycle pauses while the page is hidden and resumes the step that was
 * due (a status check that may be followed by a generate request, or the
 * generate request of a re-trigger) when it is visible again. Generate requests
 * per `start()` are capped by `maxGenerateRequests`.
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
  let generateRequests = 0;
  // Re-triggers after a `busy` failure; they are not counted in generateRequests.
  let busyRetries = 0;
  // Detection the server reported as terminally failed (source_missing, source_invalid).
  // Unlike the state, it survives stop() so a scroll out and back keeps the verdict.
  let terminalDetectionId: number | undefined;
  // True while the plain image of a detection whose source clip is gone is being
  // loaded: a render the server kept may still show, and a failure is final.
  let probingKeptRender = false;
  // Bumped by stop() so a status check backoff that outlives its start() ends.
  let lifecycle = 0;
  let parkedResume: (() => void) | undefined;
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

  /** Releases the image load slot; `releasedBy` names what ended the hold for the slot hold warning. */
  function releaseCurrentSlot(releasedBy: 'load' | 'error' | 'stop'): void {
    if (hasSlot) {
      const heldMs = Date.now() - slotAcquiredAt;
      warnIfOver(
        heldMs,
        LONG_SLOT_HOLD_WARN_MS,
        'Spectrogram image held its load slot for a long time',
        { detectionId: currentDetectionId, heldMs, releasedBy },
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
    generateRequests = 0;
    busyRetries = 0;
    probingKeptRender = false;
    currentPollInterval = config.initialPollIntervalMs;
    imageRetryCount = 0;
  }

  /** Ends the cycle in the sticky error state that start() will not retry for the same detection. */
  function failPermanently(
    detectionId: number,
    message: string,
    context: Record<string, unknown> = {}
  ): void {
    logger.warn(message, { detectionId, ...context });
    serverStatus = 'failed';
    state = 'error';
    hasError = true;
    showSpinner = false;
  }

  /** Ends the cycle in the error state for a reason retrying cannot change, and remembers it for this detection. */
  function failTerminally(detectionId: number, reason: string | undefined): void {
    terminalDetectionId = detectionId;
    failPermanently(detectionId, 'Spectrogram cannot be rendered, not retrying', { reason });
  }

  /**
   * Acts on a terminal reason. A missing source clip does not mean a missing image: the
   * server may still hold a render it kept, which the plain image request serves. So the
   * image is loaded once without generating; handleImageError makes its failure final.
   * An invalid clip fails at once.
   */
  function handleTerminalReason(detectionId: number, reason: string | undefined): void {
    if (reason === SPECTROGRAM_REASON.sourceMissing) {
      markSpectrogramSourceMissing(detectionId);
      logger.debug('Spectrogram source clip is gone, trying a kept render only', { detectionId });
      probingKeptRender = true;
      void acquireAndLoad(detectionId);
      return;
    }
    failTerminally(detectionId, reason);
  }

  /** Parks the cycle step that is due; the shared listener runs `resume` once the page is visible. */
  function park(detectionId: number, resume: () => void): void {
    logger.debug('Spectrogram polling paused while the page is hidden', { detectionId });
    parkedResume = resume;
    addParkedLoader(resumeFromPark);
  }

  function resumeFromPark(): void {
    const resume = parkedResume;
    parkedResume = undefined;
    if (resume === undefined || destroyed || state !== 'polling') return;
    resume();
  }

  function unpark(): void {
    if (parkedResume !== undefined) {
      parkedResume = undefined;
      removeParkedLoader(resumeFromPark);
    }
  }

  /**
   * Arms the next cycle step. If the page is hidden when the timer fires, the step
   * is parked and `resume` runs when the page is visible again, so a hidden tab
   * sends no cycle requests. The check is at fire time so an armed delay always elapses.
   */
  function scheduleCycleStep(
    detectionId: number,
    delayMs: number,
    run: () => void,
    resume: () => void
  ): void {
    if (isStale(detectionId)) return;

    pollTimer = setTimeout(() => {
      pollTimer = undefined;
      if (isStale(detectionId)) return;
      if (isPageHidden()) {
        park(detectionId, resume);
        return;
      }
      run();
    }, delayMs);
  }

  /** Counts one poll attempt against the cycle budget; false when the budget is spent. */
  function spendPollAttempt(detectionId: number): boolean {
    pollAttempts++;
    if (pollAttempts > config.maxPollAttempts) {
      failPermanently(detectionId, 'Spectrogram polling timed out', { attempts: pollAttempts });
      return false;
    }
    return true;
  }

  /**
   * Resumes a parked poll with one status poll. It keeps the poll error semantics, so a
   * fetch error right after wake-up (network not back yet) schedules the next poll
   * instead of ending in the error state.
   */
  function resumePoll(detectionId: number): void {
    void pollOnce(detectionId);
  }

  /**
   * Re-triggers generation after the server reported `failed`. The server waits out
   * the capture window before it fails, so a few retries cover short-lived failures
   * and the generate budget ends a permanent one.
   */
  function retryAfterFailedStatus(detectionId: number, reason?: string): void {
    // A busy server did not reject this detection, so its retries do not spend the
    // generate budget; the poll budget still ends the cycle.
    const busy = reason === SPECTROGRAM_REASON.busy;
    if (!busy && generateRequests >= config.maxGenerateRequests) {
      failPermanently(detectionId, 'Spectrogram generation failed on server after retries', {
        generateRequests,
      });
      return;
    }
    logger.debug('Spectrogram generation failed, retrying', { detectionId, generateRequests });
    state = 'polling';
    const attempt = busy ? busyRetries++ : generateRequests;
    const delay = Math.min(
      config.initialPollIntervalMs * Math.pow(2, attempt),
      config.maxPollIntervalMs
    );
    const trigger = () => void triggerGeneration(detectionId, !busy);
    scheduleCycleStep(detectionId, delay, trigger, trigger);
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

    const myLifecycle = lifecycle;
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

        const parsed = parseSpectrogramStatus(await response.json());
        if (!parsed) throw new Error('Status check returned no status');
        const status = parsed.status;
        serverStatus = isSpectrogramStatus(status) ? status : undefined;

        switch (status) {
          case 'exists':
          case 'generated':
            await acquireAndLoad(detectionId);
            return;

          case 'not_started':
            if (isTerminalSpectrogramReason(parsed.reason)) {
              handleTerminalReason(detectionId, parsed.reason);
              return;
            }
            await triggerGeneration(detectionId);
            return;

          case 'queued':
          case 'generating':
            startPolling(detectionId);
            return;

          case 'failed':
            if (isTerminalSpectrogramReason(parsed.reason)) {
              handleTerminalReason(detectionId, parsed.reason);
              return;
            }
            retryAfterFailedStatus(detectionId, parsed.reason);
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
        if (isStale(detectionId) || myLifecycle !== lifecycle) return;
      }
    }
  }

  /**
   * `countsAgainstBudget` is false for the re-trigger of a `busy` failure, which is
   * neither limited by nor counted in the generate budget.
   */
  async function triggerGeneration(detectionId: number, countsAgainstBudget = true): Promise<void> {
    if (isStale(detectionId)) return;

    // The single place a generate request is sent, so every path that asks for one
    // (failed, not_started, unknown status, resume) is counted.
    if (countsAgainstBudget) {
      if (generateRequests >= config.maxGenerateRequests) {
        failPermanently(detectionId, 'Spectrogram generation failed on server after retries', {
          generateRequests,
        });
        return;
      }
      generateRequests++;
    }

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
        const answer = parseSpectrogramStatus(await response.json());
        const status = answer?.status;
        serverStatus = status !== undefined && isSpectrogramStatus(status) ? status : undefined;

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
    // Note: pollAttempts and generateRequests are NOT reset here. pollAttempts
    // counts cycle status checks (timer polls and resumes) and generateRequests
    // counts generate requests across the entire start() lifecycle. Both are reset
    // in resetInternalState() when start() is called with a new detection or after
    // a fresh user action.
    schedulePoll(detectionId);
  }

  function schedulePoll(detectionId: number): void {
    const interval = currentPollInterval;
    const scheduledAt = Date.now();
    scheduleCycleStep(
      detectionId,
      interval,
      () => {
        const lateMs = Date.now() - scheduledAt - interval;
        warnIfOver(
          lateMs,
          LATE_POLL_TIMER_WARN_MS,
          'Spectrogram poll timer fired late',
          { detectionId, intervalMs: interval, lateMs, visibility: document.visibilityState },
          POLL_LATE_THROTTLE_KEY
        );
        void pollOnce(detectionId);
      },
      () => resumePoll(detectionId)
    );
  }

  async function pollOnce(detectionId: number): Promise<void> {
    if (isStale(detectionId)) return;

    if (!spendPollAttempt(detectionId)) return;

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
        const parsed = parseSpectrogramStatus(await response.json());
        if (!parsed) throw new Error('Status poll returned no status');
        const status = parsed.status;
        serverStatus = isSpectrogramStatus(status) ? status : undefined;

        if (status === 'exists' || status === 'generated') {
          await acquireAndLoad(detectionId);
          return;
        }

        if (
          (status === 'failed' || status === 'not_started') &&
          isTerminalSpectrogramReason(parsed.reason)
        ) {
          handleTerminalReason(detectionId, parsed.reason);
          return;
        }

        if (status === 'failed') {
          retryAfterFailedStatus(detectionId, parsed.reason);
          return;
        }

        // The server dropped its entry (a failed one expires), so ask for generation
        // again; the generate budget bounds this.
        if (status === 'not_started') {
          await triggerGeneration(detectionId);
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

    // Same detection the server reported as terminally failed: a scroll out and back
    // must not reset the verdict (stop() leaves the state idle).
    if (currentDetectionId === detectionId && terminalDetectionId === detectionId) {
      state = 'error';
      hasError = true;
      showSpinner = false;
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
    lifecycle++;
    unpark();
    clearTimers();
    abortPending();
    releaseCurrentSlot('stop');

    if (state !== 'loaded') {
      state = 'idle';
      showSpinner = false;
    }
  }

  function handleImageLoad(): void {
    probingKeptRender = false;
    state = 'loaded';
    showSpinner = false;
    imageRetryCount = 0;
    clearTimers();
    releaseCurrentSlot('load');
  }

  function handleImageError(): void {
    const detectionId = currentDetectionId;
    if (detectionId === undefined) return;

    // Release slot immediately so other cards can load during retry delay
    releaseCurrentSlot('error');

    // The plain image of a detection without a source clip failed: no kept render, final.
    if (probingKeptRender) {
      probingKeptRender = false;
      failTerminally(detectionId, SPECTROGRAM_REASON.sourceMissing);
      return;
    }

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
