/**
 * Audio utility functions for playback control.
 */

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
