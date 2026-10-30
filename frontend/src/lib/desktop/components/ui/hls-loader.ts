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
 * the same import. A failed import is forgotten so a later call imports again;
 * browsers that refetch a failed module recover, while Chromium keeps the
 * failure until the page is reloaded.
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

/** MIME type a browser with native HLS playback (Safari, iOS) reports support for. */
const HLS_MIME_TYPE = 'application/vnd.apple.mpegurl';

/**
 * Reports whether live HLS audio can play here: through HLS.js when it loaded and
 * the browser supports it, or natively. HlsClass is tryLoadHls()'s result.
 */
export function canPlayHls(HlsClass: typeof Hls | null): boolean {
  if (HlsClass?.isSupported()) return true;
  return document.createElement('audio').canPlayType(HLS_MIME_TYPE) !== '';
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
