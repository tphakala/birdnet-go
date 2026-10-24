/**
 * Audio utility functions for playback control.
 */

// =============================================================================
// Timing Constants
// =============================================================================

/** Delay before signaling play end after pause/stop (prevents UI flicker) */
export const PLAY_END_DELAY_MS = 3000;

/** Safety timeout for iOS Safari canplay event fallback */
export const CANPLAY_TIMEOUT_MS = 3000;

/** Progress update interval for smooth playhead animation */
export const PROGRESS_UPDATE_INTERVAL_MS = 50;

/** Minimum width in pixels to show full audio controls */
export const MIN_CONTROLS_WIDTH_PX = 175;

/** Maximum number of retries when audio fails to load (e.g., still being encoded) */
export const MAX_AUDIO_LOAD_RETRIES = 3;

/** Delay between audio load retries in milliseconds */
export const AUDIO_RETRY_DELAY_MS = 2000;

/**
 * Background retry delays in ms for a dashboard card whose clip is still being
 * exported. Spans the backend's pending-export window (about 90 s total). The
 * entry count is the retry limit. A media element cannot read the HTTP status, so
 * the first failure of a load of the original clip is also checked with
 * probeAudioSource: a 404 ends the retries and a 503 with a longer Retry-After than
 * the scheduled delay lengthens that delay. Every other failure retries blind.
 */
export const DASHBOARD_AUDIO_RETRY_DELAYS_MS: readonly number[] = [
  2000, 4000, 8000, 16000, 30000, 30000,
];

/** Range header value that asks for the first byte of a clip only. */
const AUDIO_PROBE_RANGE = 'bytes=0-0';

/** HTTP status the audio endpoint answers for a clip that is gone for good. */
const HTTP_NOT_FOUND = 404;

/** HTTP status the audio endpoint answers for a clip that may still be written. */
const HTTP_SERVICE_UNAVAILABLE = 503;

/** Milliseconds in a second, for converting a Retry-After header. */
const MS_PER_SECOND = 1000;

/** Longest Retry-After honoured, in ms; a larger or hostile value is capped to this. */
export const MAX_AUDIO_PROBE_RETRY_AFTER_MS = 30000;

/** DOMException name a browser uses when play() is interrupted by a load or pause */
const ABORT_ERROR_NAME = 'AbortError';

// =============================================================================
// Playback Speed
// =============================================================================

/**
 * Playback speed options available in the UI.
 * These are discrete steps chosen for accessibility:
 * - 0.5x and 0.75x slow down audio, lowering pitch for high-frequency calls
 * - 1.0x is normal speed
 * - 1.25x and 1.5x speed up for quick review
 */
export const SPEED_OPTIONS = [0.5, 0.75, 1.0, 1.25, 1.5] as const;

/**
 * Default playback speed (normal speed).
 */
export const DEFAULT_PLAYBACK_SPEED = 1.0;

/**
 * Apply playback rate to an audio element with pitch preservation disabled.
 *
 * Disabling preservesPitch creates the "tape slow-down" effect where
 * slower playback lowers pitch, making high-frequency bird calls
 * more audible for users with reduced high-frequency hearing.
 *
 * Includes vendor prefixes for cross-browser compatibility.
 *
 * @param audio - The HTMLAudioElement to modify
 * @param rate - Playback rate multiplier (e.g., 0.5 for half speed, 1.5 for 1.5x)
 */
export function applyPlaybackRate(audio: HTMLAudioElement, rate: number): void {
  audio.playbackRate = rate;

  // Disable pitch preservation for accessibility - slower = lower pitch
  // Use type assertion for vendor-prefixed properties
  const audioWithPitch = audio as HTMLAudioElement & {
    preservesPitch?: boolean;
    mozPreservesPitch?: boolean;
    webkitPreservesPitch?: boolean;
  };

  audioWithPitch.preservesPitch = false;
  audioWithPitch.mozPreservesPitch = false;
  audioWithPitch.webkitPreservesPitch = false;
}

/**
 * Convert decibels to linear gain value.
 *
 * @param db - Gain in decibels
 * @returns Linear gain multiplier
 */
export function dbToGain(db: number): number {
  return Math.pow(10, db / 20);
}

/**
 * Whether a play() rejection is only an interruption (a load() or src change
 * or pause() arrived before playback started), not a playback failure.
 * Structural so it works for DOMException in browsers and in jsdom.
 *
 * @param err - the value a play() promise rejected with
 * @returns true when the rejection is an AbortError
 */
export function isPlayInterruptedError(err: unknown): boolean {
  return typeof err === 'object' && err !== null && 'name' in err && err.name === ABORT_ERROR_NAME;
}

/**
 * Translation key for a play button's accessible label. A loading clip reads as
 * loading, never as Pause, even when the element already reports playing.
 *
 * @param isLoading - whether the clip is loading or reloading
 * @param isPlaying - whether the element is playing
 * @returns the i18n key to pass to t()
 */
export function playButtonLabelKey(
  isLoading: boolean,
  isPlaying: boolean
): 'media.audio.loading' | 'media.audio.pause' | 'media.audio.play' {
  if (isLoading) return 'media.audio.loading';
  return isPlaying ? 'media.audio.pause' : 'media.audio.play';
}

// =============================================================================
// Load failure probe
// =============================================================================

/**
 * What a media element's failed load was caused by, as the audio endpoint answers
 * a one byte probe of the same URL.
 * - `missing`: 404, the clip is gone for good; retrying cannot help.
 * - `pending`: 503, the clip may still be written; retry after `retryAfterMs`
 *   (undefined when the server sent no usable Retry-After).
 * - `unknown`: any other status or a network error; keep the blind retries.
 */
export type AudioProbeOutcome =
  { kind: 'missing' } | { kind: 'pending'; retryAfterMs?: number } | { kind: 'unknown' };

/** Reads a Retry-After header in seconds as capped milliseconds; undefined when unusable. */
function parseRetryAfterMs(header: string | null): number | undefined {
  if (header === null || !/^\d+$/.test(header.trim())) return undefined;
  const ms = Number(header.trim()) * MS_PER_SECOND;
  return Math.min(ms, MAX_AUDIO_PROBE_RETRY_AFTER_MS);
}

/**
 * A media element cannot read the HTTP status of a failed load, so this asks the
 * audio endpoint once, with a GET for the first byte (the audio routes do not
 * answer HEAD). The body is cancelled unread. Never rejects: an abort or network
 * error yields `unknown`, so callers check their signal before acting.
 *
 * @param url - the audio URL whose element load failed
 * @param signal - aborts the request when the caller moves on
 * @returns how the server classifies the failure
 */
export async function probeAudioSource(
  url: string,
  signal: AbortSignal
): Promise<AudioProbeOutcome> {
  try {
    const response = await fetch(url, { headers: { Range: AUDIO_PROBE_RANGE }, signal });
    // Cancel before reading anything: a server that ignores Range must not stream the clip.
    void response.body?.cancel().catch(() => undefined);
    if (response.status === HTTP_NOT_FOUND) return { kind: 'missing' };
    if (response.status === HTTP_SERVICE_UNAVAILABLE) {
      return {
        kind: 'pending',
        retryAfterMs: parseRetryAfterMs(response.headers.get('Retry-After')),
      };
    }
    return { kind: 'unknown' };
  } catch {
    return { kind: 'unknown' };
  }
}

/**
 * The delay before the retry that follows a `pending` probe: the server's
 * Retry-After when it is longer than the scheduled delay, never shorter.
 */
export function pendingRetryDelayMs(outcome: AudioProbeOutcome, scheduledDelayMs: number): number {
  return outcome.kind === 'pending' && outcome.retryAfterMs !== undefined
    ? Math.max(scheduledDelayMs, outcome.retryAfterMs)
    : scheduledDelayMs;
}
