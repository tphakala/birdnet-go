/**
 * On-demand loader for HLS.js
 *
 * HLS.js is only needed once the user starts a live audio stream, but the
 * components that start one (the header audio level indicator and the
 * dashboard mini spectrogram) are on every page. Loading it here on first use
 * keeps it in its own chunk instead of the main bundle.
 */

import type Hls from 'hls.js';
import { loggers } from '$lib/utils/logger';

const logger = loggers.audio;

let hlsModule: Promise<typeof Hls> | null = null;

/**
 * Loads HLS.js once and returns its class. Concurrent and later calls share
 * the same import; a failed import is forgotten so the next call retries it.
 */
export function loadHls(): Promise<typeof Hls> {
  hlsModule ??= import('hls.js').then(
    module => module.default,
    (error: unknown) => {
      hlsModule = null;
      throw error;
    }
  );
  return hlsModule;
}

/**
 * Loads HLS.js like loadHls, but resolves to null when the import fails (for
 * example offline, or a chunk name left stale by an upgrade while the page was
 * open), so the caller can fall back to native HLS playback.
 */
export async function tryLoadHls(): Promise<typeof Hls | null> {
  try {
    return await loadHls();
  } catch (error) {
    logger.warn('HLS.js failed to load, falling back to native HLS', error);
    return null;
  }
}
