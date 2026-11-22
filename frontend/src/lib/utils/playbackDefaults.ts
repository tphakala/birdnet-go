/**
 * playbackDefaults.ts
 *
 * The values a clip player starts with (gain, denoise, high-pass cutoff), their
 * ranges and the mapping of the high-pass cutoff to the value the station
 * stores. Signed-in users read the defaults from the settings store; guests
 * never load that store and read them from /app/config instead.
 */

import { get } from 'svelte/store';
import { t, type TranslationKey } from '$lib/i18n';
import { appState } from '$lib/stores/appState.svelte';
import {
  settingsDataLoaded,
  settingsStore,
  type Dashboard,
  type DenoiseSettings,
  type RealtimeSettings,
} from '$lib/stores/settings';
import {
  DEFAULT_DENOISE_PRESET,
  DENOISE_OPTIONS,
  isDenoisePresetId,
  type DenoiseOption,
} from '$lib/utils/denoisePresets';
import { getPlaybackDenoiseDefault, storedPlaybackDenoise } from '$lib/utils/playbackDenoise';

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

/** Clamp a finite number into [min, max]. A NaN stays NaN, so callers pass numbers they have checked. */
export function clampToRange(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

/** Clamp a gain to the default's range and step; anything that is not a number is 0. */
export function clampPlaybackGain(db: unknown): number {
  if (typeof db !== 'number' || !Number.isFinite(db)) return 0;
  const stepped = Math.round(db / PLAYBACK_GAIN_STEP_DB) * PLAYBACK_GAIN_STEP_DB;
  // Math.round(-0.2) is -0, which would print as "-0 dB".
  return clampToRange(stepped, PLAYBACK_GAIN_MIN_DB, PLAYBACK_GAIN_MAX_DB) || 0;
}

/**
 * A gain as text: signed when positive, no trailing ".0", a space before the
 * unit ("+7.5 dB", "0 dB", "-6.5 dB"). Negative zero and a value that is not
 * finite read as "0".
 */
export function formatPlaybackGain(db: number, unit: string): string {
  const value = Number.isFinite(db) ? db || 0 : 0;
  return `${value > 0 ? '+' : ''}${value} ${unit}`;
}

/** A cutoff rounded to whole Hz and held to the player's range; anything that is not a number is the floor. */
function clampHighPass(hz: unknown): number {
  if (typeof hz !== 'number' || !Number.isFinite(hz)) return PLAYBACK_HIGH_PASS_MIN_HZ;
  return clampToRange(Math.round(hz), PLAYBACK_HIGH_PASS_MIN_HZ, PLAYBACK_HIGH_PASS_MAX_HZ);
}

/**
 * Stored default to the cutoff a player starts with: held to the player's range
 * and snapped to the slider's step, so the thumb, its label and the filter agree
 * (a hand-edited 21 Hz starts at the floor, 333 Hz at 330). Anything below the
 * floor is the floor.
 */
export function highPassFromStored(hz: unknown): number {
  const cutoff = clampHighPass(hz);
  const steps = Math.round((cutoff - PLAYBACK_HIGH_PASS_MIN_HZ) / PLAYBACK_HIGH_PASS_STEP_HZ);
  return clampToRange(
    PLAYBACK_HIGH_PASS_MIN_HZ + steps * PLAYBACK_HIGH_PASS_STEP_HZ,
    PLAYBACK_HIGH_PASS_MIN_HZ,
    PLAYBACK_HIGH_PASS_MAX_HZ
  );
}

/**
 * Player cutoff (or an already stored value) to the stored default: the floor is
 * stored as "off". Other values are kept as they are, off the slider's step
 * included, because the server keeps them.
 */
export function highPassToStored(hz: unknown): number {
  const cutoff = clampHighPass(hz);
  return cutoff <= PLAYBACK_HIGH_PASS_MIN_HZ ? PLAYBACK_HIGH_PASS_STORED_OFF_HZ : cutoff;
}

/**
 * What a player starts with: the saved settings once they are loaded, else the
 * values /app/config gave a guest. Reads saved settings, not unsaved form edits,
 * so it agrees with what the server holds. The denoise value is the effective
 * playback default, export-stage denoise precedence included.
 */
export function getPlaybackDefaults(): PlaybackDefaultValues {
  const denoise = getPlaybackDenoiseDefault();
  if (get(settingsDataLoaded)) {
    return { ...readPlaybackDefaults(get(settingsStore).originalData.realtime), denoise };
  }
  return {
    gainDb: clampPlaybackGain(appState.playbackDefaults.gainDb),
    denoise,
    highPassHz: highPassFromStored(appState.playbackDefaults.highPassHz),
  };
}

/** A default gain as text in the UI's unit ("+6 dB", "-6.5 dB"). */
export function formatPlaybackGainLabel(db: number): string {
  return formatPlaybackGain(db, t('settings.userInterface.audioPlayback.defaultGainUnit'));
}

/** A cutoff as text: the floor reads "Off", any other value carries the unit ("300 Hz"). */
export function formatPlaybackHighPass(hz: number): string {
  return hz <= PLAYBACK_HIGH_PASS_MIN_HZ
    ? t('settings.userInterface.audioPlayback.highPassOff')
    : `${hz} ${t('settings.userInterface.audioPlayback.highPassUnit')}`;
}

interface PlaybackDefaultItemBase {
  labelKey: TranslationKey;
  helpKey: TranslationKey;
}

/**
 * One row of the playback defaults dialog. The dialog renders one control per
 * `kind`, so a new default is a new entry here (plus its field in
 * PlaybackDefaultValues, its line in readPlaybackDefaults, changedPlaybackDefaults
 * and playbackDefaultsPatch, and its stored key), not a layout change.
 */
export type PlaybackDefaultItem =
  | (PlaybackDefaultItemBase & {
      kind: 'slider';
      key: 'gainDb' | 'highPassHz';
      min: number;
      max: number;
      step: number;
      /** The value as the slider shows and announces it. */
      format: (value: number) => string;
    })
  | (PlaybackDefaultItemBase & {
      kind: 'select';
      key: 'denoise';
      options: readonly DenoiseOption[];
    });

/** The playback defaults, in the order the dialog shows them. */
export const PLAYBACK_DEFAULT_ITEMS: readonly PlaybackDefaultItem[] = [
  {
    kind: 'slider',
    key: 'gainDb',
    labelKey: 'settings.userInterface.audioPlayback.defaultGain',
    helpKey: 'settings.userInterface.audioPlayback.defaultGainHelpText',
    min: PLAYBACK_GAIN_MIN_DB,
    max: PLAYBACK_GAIN_MAX_DB,
    step: PLAYBACK_GAIN_STEP_DB,
    format: formatPlaybackGainLabel,
  },
  {
    kind: 'select',
    key: 'denoise',
    labelKey: 'settings.userInterface.audioPlayback.denoiseLabel',
    helpKey: 'settings.userInterface.audioPlayback.denoiseHelpText',
    options: DENOISE_OPTIONS,
  },
  {
    kind: 'slider',
    key: 'highPassHz',
    labelKey: 'settings.userInterface.audioPlayback.highPassLabel',
    helpKey: 'settings.userInterface.audioPlayback.highPassHelpText',
    min: PLAYBACK_HIGH_PASS_MIN_HZ,
    max: PLAYBACK_HIGH_PASS_MAX_HZ,
    step: PLAYBACK_HIGH_PASS_STEP_HZ,
    format: formatPlaybackHighPass,
  },
];

/**
 * The saved defaults as the values the dialog edits, snapped to the sliders.
 * The denoise value is the stored choice: export-stage denoise precedence is not
 * applied here, because the dialog shows what is stored and explains the
 * precedence beside it.
 */
export function readPlaybackDefaults(
  realtime: RealtimeSettings | undefined
): PlaybackDefaultValues {
  const dashboard = realtime?.dashboard;
  return {
    gainDb: clampPlaybackGain(dashboard?.defaultAudioGain),
    denoise: storedPlaybackDenoise(dashboard?.playbackDenoise),
    highPassHz: highPassFromStored(dashboard?.playback?.highPassHz),
  };
}

/** The keys of `draft` whose value differs from `saved`, with the draft's values. */
export function changedPlaybackDefaults(
  draft: PlaybackDefaultValues,
  saved: PlaybackDefaultValues
): Partial<PlaybackDefaultValues> {
  const changed: Partial<PlaybackDefaultValues> = {};
  if (draft.gainDb !== saved.gainDb) changed.gainDb = draft.gainDb;
  if (draft.denoise !== saved.denoise) changed.denoise = draft.denoise;
  if (draft.highPassHz !== saved.highPassHz) changed.highPassHz = draft.highPassHz;
  return changed;
}

/**
 * The dashboard PATCH body for the changed keys only. Turning denoise off keeps
 * the saved strength, so the Settings page still shows it. `playback` is sent
 * whole, because the store merges a section patch one level deep.
 */
export function playbackDefaultsPatch(
  changed: Partial<PlaybackDefaultValues>,
  saved: Partial<Dashboard> | undefined
): Partial<Dashboard> {
  const patch: Partial<Dashboard> = {};
  if (changed.gainDb !== undefined) {
    patch.defaultAudioGain = clampPlaybackGain(changed.gainDb);
  }
  if (changed.denoise !== undefined) {
    const savedStrength = saved?.playbackDenoise?.strength;
    const keptStrength = isDenoisePresetId(savedStrength) ? savedStrength : DEFAULT_DENOISE_PRESET;
    const denoise: DenoiseSettings = isDenoisePresetId(changed.denoise)
      ? { enabled: true, strength: changed.denoise }
      : { enabled: false, strength: keptStrength };
    patch.playbackDenoise = denoise;
  }
  if (changed.highPassHz !== undefined) {
    patch.playback = { ...saved?.playback, highPassHz: highPassToStored(changed.highPassHz) };
  }
  return patch;
}
