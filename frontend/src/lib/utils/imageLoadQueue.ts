/**
 * Image Load Queue
 *
 * Limits concurrent image loads to prevent HTTP/1.1 connection exhaustion.
 * Browsers limit connections to 6 per domain under HTTP/1.1. With SSE
 * connections and other requests, loading many spectrograms simultaneously
 * causes requests to queue indefinitely in the browser.
 *
 * @example
 * ```typescript
 * // In component mount
 * const handle = acquireSlot();
 * const acquired = await handle.promise;
 * if (acquired) {
 *   // Set image src
 *   img.src = spectrogramUrl;
 * }
 *
 * // On load/error/destroy
 * releaseSlot();
 *
 * // On component destroy while waiting
 * handle.cancel();
 * ```
 */

import { loggers } from '$lib/utils/logger';

const logger = loggers.ui;

/** Set to true to enable queue debug logging */
const DEBUG_QUEUE = false;

function debugLog(message: string, data?: Record<string, unknown>) {
  if (DEBUG_QUEUE) {
    logger.debug(`[ImageQueue] ${message}`, data);
  }
}

/** Maximum concurrent image loads - adjust this value to tune performance */
export const MAX_CONCURRENT_IMAGE_LOADS = 2;

/** Handle returned by acquireSlot for managing the slot request */
export interface SlotHandle {
  /** Promise that resolves to true when slot acquired, false if cancelled */
  promise: Promise<boolean>;
  /** Cancel the slot request (safe to call if already resolved) */
  cancel: () => void;
}

/** Queue statistics for debugging */
export interface QueueStats {
  /** Number of currently active slots */
  active: number;
  /** Number of requests waiting in queue */
  queued: number;
  /** Maximum concurrent slots allowed */
  maxConcurrent: number;
}

interface QueuedRequest {
  resolve: (acquired: boolean) => void;
  cancelled: boolean;
}

// Module-level state (singleton)
let activeCount = 0;
const waitQueue: QueuedRequest[] = [];

/**
 * Request a slot for loading an image.
 *
 * @returns Handle with promise and cancel function
 */
export function acquireSlot(): SlotHandle {
  let request: QueuedRequest | undefined;

  const promise = new Promise<boolean>(resolve => {
    if (activeCount < MAX_CONCURRENT_IMAGE_LOADS) {
      activeCount++;
      debugLog('Slot acquired immediately', { active: activeCount });
      resolve(true);
      return;
    }

    // Queue the request
    request = { resolve, cancelled: false };
    waitQueue.push(request);
    debugLog('Request queued', { queued: waitQueue.length });
  });

  const cancel = () => {
    if (request && !request.cancelled) {
      request.cancelled = true;
      // Remove from queue
      const index = waitQueue.indexOf(request);
      if (index !== -1) {
        waitQueue.splice(index, 1);
      }
      request.resolve(false);
    }
  };

  return { promise, cancel };
}

/**
 * Release a slot after image load completes or fails.
 * Safe to call even if no slot was acquired.
 */
export function releaseSlot(): void {
  debugLog('Slot released', { active: activeCount, queued: waitQueue.length });

  // Process next queued request if any
  while (waitQueue.length > 0) {
    const next = waitQueue.shift();
    if (next && !next.cancelled) {
      // Transfer slot to next request (activeCount stays same)
      debugLog('Slot transferred to queued request', { active: activeCount, queued: waitQueue.length });
      next.resolve(true);
      return;
    }
  }

  // No queued requests, just decrement
  if (activeCount > 0) {
    activeCount--;
    debugLog('Active count decremented', { active: activeCount });
  }
}

/**
 * Get current queue statistics for debugging.
 */
export function getQueueStats(): QueueStats {
  return {
    active: activeCount,
    queued: waitQueue.length,
    maxConcurrent: MAX_CONCURRENT_IMAGE_LOADS,
  };
}

/**
 * Reset queue state. Only for testing.
 */
export function resetQueue(): void {
  activeCount = 0;
  waitQueue.length = 0;
}
