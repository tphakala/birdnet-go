/**
 * connectionState.svelte.ts
 *
 * Centralized connection state management using Svelte 5 runes.
 * Tracks whether the backend is reachable via SSE heartbeat watchdog.
 * When offline, polls GET /api/v2/ping to detect recovery.
 *
 * Usage:
 *   import { connectionState, markOnline } from '$lib/stores/connectionState.svelte';
 *
 *   // Reactive access in components:
 *   {#if !connectionState.isOnline}
 *     <OfflineBanner />
 *   {/if}
 *
 *   // Guard background polling:
 *   if (!connectionState.isOnline) return;
 */
import { buildAppUrl } from '$lib/utils/urlHelpers';
import { getLogger } from '$lib/utils/logger';
import { restartInProgress } from '$lib/stores/restart.svelte';

const logger = getLogger('connectionState');

/** Watchdog timeout: 35s (slightly over 2x the 15s SSE heartbeat interval) */
const HEARTBEAT_TIMEOUT_MS = 35_000;

/** Shortened watchdog timeout after an explicit SSE error */
const ERROR_TIMEOUT_MS = 5_000;

/** Ping polling interval during offline state */
const PING_INTERVAL_MS = 5_000;

/** Ping request timeout */
const PING_TIMEOUT_MS = 5_000;

/** Ping endpoint */
const PING_ENDPOINT = '/api/v2/ping';

/**
 * Reactive connection state
 */
export const connectionState = $state({
  /** Whether the backend is currently reachable */
  isOnline: true,
  /** Timestamp (ms) of the last successful backend contact */
  lastContact: Date.now(),
});

/** Watchdog timer ID */
let watchdogTimer: ReturnType<typeof setTimeout> | null = null;

/** Ping polling timer ID */
let pingTimer: ReturnType<typeof setInterval> | null = null;

/** Active ping request controller (for aborting in-flight requests) */
let activePingController: AbortController | null = null;

/** Whether the watchdog has been activated (after app init) */
let activated = false;

/** Whether the error timeout has already been armed for the current burst */
let errorTimeoutArmed = false;

/**
 * Check if the backend is online (non-reactive, for use in polling guards).
 */
export function isBackendOnline(): boolean {
  return connectionState.isOnline;
}

/**
 * Mark the backend as online. Called from:
 * - fetchWithCSRF on successful response
 * - Ping polling on successful response
 * - SSE heartbeat/message received
 */
export function markOnline(): void {
  connectionState.lastContact = Date.now();
  errorTimeoutArmed = false;
  if (!connectionState.isOnline) {
    logger.info('Backend connectivity restored');
    connectionState.isOnline = true;
    stopPingPolling();

    // If reconnecting after a restart, reload the page to get fresh state
    if (restartInProgress.value) {
      logger.info('Reloading page after restart reconnection');
      restartInProgress.value = false;
      window.location.reload();
      return;
    }
  }
  resetWatchdog(HEARTBEAT_TIMEOUT_MS);
}

/**
 * Mark the backend as offline. Starts ping polling for recovery.
 */
function markOffline(): void {
  if (connectionState.isOnline) {
    logger.warn('Backend connectivity lost');
    connectionState.isOnline = false;
    startPingPolling();
  }
}

/**
 * Reset the watchdog timer with the given timeout.
 * If the timer expires without being reset, the backend is marked offline.
 */
function resetWatchdog(timeoutMs: number): void {
  if (!activated) return;

  if (watchdogTimer !== null) {
    clearTimeout(watchdogTimer);
  }

  watchdogTimer = setTimeout(() => {
    watchdogTimer = null;
    logger.warn('SSE heartbeat watchdog expired', {
      timeoutMs,
      lastContact: connectionState.lastContact,
    });
    markOffline();
  }, timeoutMs);
}

/**
 * Called when any SSE message is received (heartbeat, notification, toast, etc.).
 * Resets the watchdog timer and marks the backend as online.
 */
export function onSSEActivity(): void {
  if (!activated) return;
  markOnline();
}

/**
 * Called when an explicit SSE error/disconnect occurs.
 * Shortens the watchdog timeout for faster offline detection.
 * Uses a boolean guard so the shortened timeout only fires once per
 * disconnect burst, preventing repeated timer resets from delaying
 * offline detection.
 */
export function onSSEError(): void {
  if (!activated) return;
  if (!connectionState.isOnline) return;
  if (errorTimeoutArmed) return;
  errorTimeoutArmed = true;
  resetWatchdog(ERROR_TIMEOUT_MS);
}

/**
 * Start polling the ping endpoint to detect recovery.
 */
function startPingPolling(): void {
  if (pingTimer !== null) return; // Already polling

  logger.info('Starting ping polling for recovery');

  // Poll immediately, then on interval
  void pingOnce();
  pingTimer = setInterval(() => {
    void pingOnce();
  }, PING_INTERVAL_MS);
}

/**
 * Stop ping polling.
 */
function stopPingPolling(): void {
  if (pingTimer !== null) {
    clearInterval(pingTimer);
    pingTimer = null;
    logger.info('Stopped ping polling');
  }
}

/**
 * Make a single ping request.
 * Uses a module-level AbortController so deactivateWatchdog() can cancel in-flight requests.
 */
async function pingOnce(): Promise<void> {
  activePingController?.abort();
  const controller = new AbortController();
  activePingController = controller;
  const timeoutId = setTimeout(() => controller.abort(), PING_TIMEOUT_MS);

  try {
    const response = await fetch(buildAppUrl(PING_ENDPOINT), {
      method: 'GET',
      signal: controller.signal,
      credentials: 'same-origin',
    });

    if (activated && response.ok) {
      markOnline();
    }
  } catch {
    // Ping failed — stay offline, will retry on next interval
  } finally {
    clearTimeout(timeoutId);
    if (activePingController === controller) {
      activePingController = null;
    }
  }
}

/**
 * Activate the watchdog. Called once after app initialization completes.
 * Must not be called before appState.initialized === true.
 */
export function activateWatchdog(): void {
  if (activated) return;
  activated = true;
  connectionState.lastContact = Date.now();
  resetWatchdog(HEARTBEAT_TIMEOUT_MS);
  logger.info('Connection watchdog activated');
}

/**
 * Deactivate and clean up all timers. For use in tests or teardown.
 */
export function deactivateWatchdog(): void {
  activated = false;
  errorTimeoutArmed = false;
  if (watchdogTimer !== null) {
    clearTimeout(watchdogTimer);
    watchdogTimer = null;
  }
  stopPingPolling();
  activePingController?.abort();
  activePingController = null;
}
