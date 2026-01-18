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
