/**
 * quietHours.svelte.ts
 *
 * Shared quiet hours status store using Svelte 5 runes.
 * Polls the backend status endpoint and provides reactive state
 * to all consumers (StreamManager, QuietHoursIndicator, etc.).
 *
 * Usage:
 *   import { quietHoursStore } from '$lib/stores/quietHours.svelte';
 *
 *   // Start polling (call once from a top-level component):
 *   quietHoursStore.startPolling();
 *
 *   // Access reactive state:
 *   quietHoursStore.status?.anyActive
 *   quietHoursStore.status?.suppressedStreams
 *
 *   // Stop polling on cleanup:
 *   quietHoursStore.stopPolling();
 */
import { api } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';

const logger = loggers.ui;

/** Polling interval in milliseconds */
const POLL_INTERVAL_MS = 30000;

export interface QuietHoursStatus {
  anyActive: boolean;
  soundCardSuppressed: boolean;
  suppressedStreams: Record<string, boolean>;
}

let status = $state<QuietHoursStatus | null>(null);
let timer: ReturnType<typeof setInterval> | null = null;
let refCount = 0;

async function fetchStatus() {
  try {
    status = await api.get<QuietHoursStatus>('/api/v2/streams/quiet-hours/status');
  } catch {
    logger.debug('Failed to fetch quiet hours status', null, {
      component: 'quietHoursStore',
    });
  }
}

/**
 * Start polling for quiet hours status.
 * Uses reference counting so multiple consumers can call startPolling/stopPolling
 * independently — polling only stops when all consumers have stopped.
 */
function startPolling() {
  refCount++;
  if (refCount === 1 && typeof window !== 'undefined') {
    fetchStatus();
    timer = setInterval(fetchStatus, POLL_INTERVAL_MS);
  }
}

/** Stop polling. Only actually stops when all consumers have called stop. */
function stopPolling() {
  refCount = Math.max(0, refCount - 1);
  if (refCount === 0 && timer !== null) {
    clearInterval(timer);
    timer = null;
  }
}

/** Force an immediate refresh of the status. */
function refresh() {
  fetchStatus();
}

export const quietHoursStore = {
  get status() {
    return status;
  },
  startPolling,
  stopPolling,
  refresh,
};
