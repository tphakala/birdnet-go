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
 * `Settings.EffectivePlaybackDenoise`.
 */
export function getPlaybackDenoiseDefault(): string {
  if (get(settingsDataLoaded)) {
    const realtime: PlaybackDenoiseSources | undefined = get(settingsStore).formData.realtime;
    if (realtime?.audio?.export?.denoise?.enabled) return '';
    const playback = realtime?.dashboard?.playbackDenoise;
    return playback?.enabled && isDenoisePresetId(playback.strength) ? playback.strength : '';
  }
  // Not every test double of appState carries the field.
  return (appState as { playbackDenoise?: string }).playbackDenoise ?? '';
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
 * The url with a `t` cache-busting query parameter set as a parameter of its
 * own, so a url that already carries `denoise` stays valid for a reload that
 * must bypass a cached failure.
 */
export function withCacheBuster(url: string): string {
  const parsed = new URL(url, window.location.origin);
  parsed.searchParams.set('t', String(Date.now()));
  return `${parsed.pathname}${parsed.search}`;
}
