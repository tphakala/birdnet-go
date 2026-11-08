/**
 * playbackDenoise.ts
 *
 * The playback denoise default: a denoise preset every player and download asks
 * the server for, so stored clips stay unchanged while they are heard denoised.
 * Signed-in users read it from the settings store; guests never load that store
 * and read the server's resolved value from /app/config instead.
 */

import { get } from 'svelte/store';
import { appState } from '$lib/stores/appState.svelte';
import { settingsDataLoaded, settingsStore, type DenoiseSettings } from '$lib/stores/settings';
import { isDenoisePresetId } from '$lib/utils/denoisePresets';
import { buildAppUrl } from '$lib/utils/urlHelpers';

/** The parts of the realtime settings the default depends on, each possibly absent. */
interface PlaybackDenoiseSources {
  audio?: { export?: { denoise?: DenoiseSettings } };
  dashboard?: { playbackDenoise?: DenoiseSettings };
}

/**
 * The preset to apply to playback and downloads by default, or '' when none
 * applies. Export-stage denoise takes precedence: clips saved denoised are not
 * denoised again, so the default is '' while it is on. Mirrors the server's
 * `Settings.EffectivePlaybackDenoise` and reads the saved settings, not unsaved
 * form edits, so it agrees with what the server applies.
 */
export function getPlaybackDenoiseDefault(): string {
  if (get(settingsDataLoaded)) {
    const realtime: PlaybackDenoiseSources | undefined = get(settingsStore).originalData.realtime;
    if (realtime?.audio?.export?.denoise?.enabled) return '';
    const playback = realtime?.dashboard?.playbackDenoise;
    return playback?.enabled && isDenoisePresetId(playback.strength) ? playback.strength : '';
  }
  return appState.playbackDenoise || '';
}

/**
 * URL of a detection's audio clip: the stored clip, or the denoised WAV at the
 * given preset when it is not empty.
 */
export function audioUrlFor(detectionId: string | number, preset: string): string {
  const url = buildAppUrl(`/api/v2/audio/${encodeURIComponent(String(detectionId))}`);
  return preset ? `${url}?denoise=${encodeURIComponent(preset)}` : url;
}

/**
 * Point the element at src and keep its position. Meant for the first play, run
 * synchronously in the click handler so the play call keeps its user gesture.
 */
export function swapSourceKeepingPosition(element: HTMLAudioElement, src: string): void {
  const position = element.currentTime;
  element.src = src;
  if (position > 0) element.currentTime = position;
}

/**
 * The url with a `t` cache-busting query parameter set as a parameter of its
 * own, so a url that already carries `denoise` stays valid for a reload that
 * must bypass a cached failure.
 */
export function withCacheBuster(url: string): string {
  const parsed = new URL(url, window.location.origin);
  parsed.searchParams.set('t', String(Date.now()));
  return `${parsed.pathname}${parsed.search}`;
}
