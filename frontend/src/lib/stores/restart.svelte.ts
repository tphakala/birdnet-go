/**
 * restart.svelte.ts
 *
 * Reactive store for server restart state.
 * Fetches restart availability and pending restart-required status from the backend.
 * Includes recovery polling that detects when the server is back after a restart
 * and triggers a page reload to restore the UI.
 */
import { api, ApiError } from '$lib/utils/api';
import { buildAppUrl } from '$lib/utils/urlHelpers';
import { getLogger } from '$lib/utils/logger';

const logger = getLogger('restartStore');

/** Delay before starting recovery polls (server needs time to shut down) */
const RECOVERY_INITIAL_DELAY_MS = 3000;

/** Interval between recovery ping attempts */
const RECOVERY_POLL_INTERVAL_MS = 2000;

/** Timeout for each recovery ping request */
const RECOVERY_POLL_TIMEOUT_MS = 3000;

interface RestartStatus {
  binary_restart_available: boolean;
  container_restart_available: boolean;
  restart_required: boolean;
  restart_reasons: string[];
}

/** Reactive restart state */
export const restartState = $state<RestartStatus>({
  binary_restart_available: true,
  container_restart_available: false,
  restart_required: false,
  restart_reasons: [],
});

/** Whether a restart is currently being initiated */
export const restartInProgress = $state({ value: false });

/** Recovery polling timer references */
let recoveryDelayTimer: ReturnType<typeof setTimeout> | null = null;
let recoveryPollTimer: ReturnType<typeof setInterval> | null = null;

/** Fetch restart status from the backend */
export async function fetchRestartStatus(): Promise<void> {
  try {
    const data = await api.get<RestartStatus>('/api/v2/system/restart-status');
    Object.assign(restartState, data);
    // If the server is reachable, any in-progress restart has completed.
    if (restartInProgress.value) {
      stopRecoveryPolling();
      restartInProgress.value = false;
    }
  } catch (error) {
    logger.error('Failed to fetch restart status', error);
  }
}

/**
 * Start polling /api/v2/ping to detect when the server is back after a restart.
 * Waits an initial delay for the server to shut down, then polls until a
 * successful response triggers a full page reload.
 */
function startRecoveryPolling(): void {
  stopRecoveryPolling();

  recoveryDelayTimer = setTimeout(() => {
    recoveryDelayTimer = null;

    recoveryPollTimer = setInterval(async () => {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), RECOVERY_POLL_TIMEOUT_MS);

      try {
        const response = await fetch(buildAppUrl('/api/v2/ping'), {
          signal: controller.signal,
          credentials: 'same-origin',
        });

        if (response.ok) {
          logger.info('Server recovered after restart, reloading page');
          stopRecoveryPolling();
          restartInProgress.value = false;
          window.location.reload();
        }
      } catch {
        // Server still down — keep polling
      } finally {
        clearTimeout(timeout);
      }
    }, RECOVERY_POLL_INTERVAL_MS);
  }, RECOVERY_INITIAL_DELAY_MS);
}

/** Stop recovery polling and clean up timers */
function stopRecoveryPolling(): void {
  if (recoveryDelayTimer !== null) {
    clearTimeout(recoveryDelayTimer);
    recoveryDelayTimer = null;
  }
  if (recoveryPollTimer !== null) {
    clearInterval(recoveryPollTimer);
    recoveryPollTimer = null;
  }
}

/** Shared restart request logic */
async function requestRestart(endpoint: string, logMessage: string): Promise<boolean> {
  try {
    restartInProgress.value = true;
    await api.post(endpoint);
    startRecoveryPolling();
    return true;
  } catch (error) {
    if (error instanceof ApiError) {
      if (!error.isNetworkError) {
        // HTTP error (403, 500): request was rejected, abort restart
        restartInProgress.value = false;
        logger.error(logMessage, error);
      } else {
        // Network error: server shut down before responding, restart likely succeeded
        startRecoveryPolling();
        logger.info('Server connection lost after restart request, starting recovery polling');
      }
    } else {
      // Unexpected client/runtime error: abort restart
      restartInProgress.value = false;
      logger.error(logMessage, error);
    }
    return false;
  }
}

/** Request a binary restart */
export function requestBinaryRestart(): Promise<boolean> {
  return requestRestart('/api/v2/control/restart-server', 'Failed to request binary restart');
}

/** Request a container restart */
export function requestContainerRestart(): Promise<boolean> {
  return requestRestart('/api/v2/control/restart-container', 'Failed to request container restart');
}
