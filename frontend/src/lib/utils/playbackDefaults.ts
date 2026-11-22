/**
 * playbackDefaults.ts
 *
 * The values a clip player starts with (gain, denoise, high-pass cutoff), their
 * ranges and the mapping of the high-pass cutoff to the value the station
 * stores. Signed-in users read the defaults from the settings store; guests
 * never load that store and read them from /app/config instead.
 */

import { get } from 'svelte/store';
import { appState } from '$lib/stores/appState.svelte';
import { settingsDataLoaded, settingsStore } from '$lib/stores/settings';
import { getPlaybackDenoiseDefault } from '$lib/utils/playbackDenoise';

/** Lowest default gain in dB (the player toolbar's lower bound, `conf.MinPlaybackGain`). */
export const PLAYBACK_GAIN_MIN_DB = -12;
/** Highest default gain in dB (the player toolbar's upper bound, `conf.MaxPlaybackGain`). */
export const PLAYBACK_GAIN_MAX_DB = 24;
export const PLAYBACK_GAIN_STEP_DB = 0.5;
/** The player's floor: a 20 Hz high-pass is inaudible, so it is the slider's "Off" position. */
export const PLAYBACK_HIGH_PASS_MIN_HZ = 20;
/** Highest default cutoff in Hz (`conf.MaxPlaybackHighPassHz`). */
export const PLAYBACK_HIGH_PASS_MAX_HZ = 5000;
export const PLAYBACK_HIGH_PASS_STEP_HZ = 10;
/** What the configuration stores when the default high-pass filter is off. */
export const PLAYBACK_HIGH_PASS_STORED_OFF_HZ = 0;

/** What a player starts with. */
export interface PlaybackDefaultValues {
  /** Gain in dB. */
  gainDb: number;
  /** A denoise preset id, or '' for off. */
  denoise: string;
  /** Player cutoff in Hz; PLAYBACK_HIGH_PASS_MIN_HZ is "off". */
  highPassHz: number;
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

/** Clamp a gain to the default's range and step; anything that is not a number is 0. */
export function clampPlaybackGain(db: unknown): number {
  if (typeof db !== 'number' || !Number.isFinite(db)) return 0;
  const stepped = Math.round(db / PLAYBACK_GAIN_STEP_DB) * PLAYBACK_GAIN_STEP_DB;
  // Math.round(-0.2) is -0, which would print as "-0 dB".
  return clamp(stepped, PLAYBACK_GAIN_MIN_DB, PLAYBACK_GAIN_MAX_DB) || 0;
}

/** Stored default to the cutoff a player starts with; anything below the floor is the floor. */
export function highPassFromStored(hz: unknown): number {
  if (typeof hz !== 'number' || !Number.isFinite(hz)) return PLAYBACK_HIGH_PASS_MIN_HZ;
  return clamp(Math.round(hz), PLAYBACK_HIGH_PASS_MIN_HZ, PLAYBACK_HIGH_PASS_MAX_HZ);
}

/** Player cutoff (or an already stored value) to the stored default: the floor is stored as "off". */
export function highPassToStored(hz: unknown): number {
  const cutoff = highPassFromStored(hz);
  return cutoff <= PLAYBACK_HIGH_PASS_MIN_HZ ? PLAYBACK_HIGH_PASS_STORED_OFF_HZ : cutoff;
}

/**
 * What a player starts with: the saved settings once they are loaded, else the
 * values /app/config gave a guest. Reads saved settings, not unsaved form edits,
 * so it agrees with what the server holds. The denoise value is the effective
 * playback default, export-stage denoise precedence included.
 */
export function getPlaybackDefaults(): PlaybackDefaultValues {
  if (get(settingsDataLoaded)) {
    const dashboard = get(settingsStore).originalData.realtime?.dashboard;
    return {
      gainDb: clampPlaybackGain(dashboard?.defaultAudioGain),
      denoise: getPlaybackDenoiseDefault(),
      highPassHz: highPassFromStored(dashboard?.playback?.highPassHz),
    };
  }
  return {
    gainDb: clampPlaybackGain(appState.playbackDefaults.gainDb),
    denoise: getPlaybackDenoiseDefault(),
    highPassHz: highPassFromStored(appState.playbackDefaults.highPassHz),
  };
}
