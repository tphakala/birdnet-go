/**
 * spectrogramDefaults.ts
 *
 * Resolves detection-card spectrogram size and raw mode.
 * Reads savedDashboardSettings once authenticated settings have loaded;
 * otherwise reads /api/v2/app/config spectrogramDefaults so guests and initial
 * mounts request the configured size immediately without a fallback restart.
 */

import { get } from 'svelte/store';
import { appState } from '$lib/stores/appState.svelte';
import {
  DEFAULT_SPECTROGRAM_SETTINGS,
  savedDashboardSettings,
  settingsDataLoaded,
  type SpectrogramPreRender,
  type SpectrogramSize,
} from '$lib/stores/settings';

export interface SpectrogramDefaultValues {
  size: SpectrogramSize;
  raw: boolean;
}

const VALID_SPECTROGRAM_SIZES = new Set<string>(['sm', 'md', 'lg', 'xl']);

export function parseSpectrogramSize(value: unknown): SpectrogramSize | undefined {
  return typeof value === 'string' && VALID_SPECTROGRAM_SIZES.has(value)
    ? (value as SpectrogramSize)
    : undefined;
}

export function resolveSpectrogramDefaults(
  settingsLoaded: boolean,
  settingsSpec?: Pick<SpectrogramPreRender, 'size' | 'raw'>,
  appSpec?: { size?: unknown; raw?: boolean }
): SpectrogramDefaultValues {
  if (settingsLoaded) {
    return {
      size: parseSpectrogramSize(settingsSpec?.size) ?? DEFAULT_SPECTROGRAM_SETTINGS.size,
      raw: settingsSpec?.raw ?? DEFAULT_SPECTROGRAM_SETTINGS.raw,
    };
  }
  return {
    size: parseSpectrogramSize(appSpec?.size) ?? DEFAULT_SPECTROGRAM_SETTINGS.size,
    raw: appSpec?.raw ?? DEFAULT_SPECTROGRAM_SETTINGS.raw,
  };
}

/** Synchronous reader for createSpectrogramLoader dynamic configuration getters. */
export function getSpectrogramDefaults(): SpectrogramDefaultValues {
  return resolveSpectrogramDefaults(
    get(settingsDataLoaded),
    get(savedDashboardSettings)?.spectrogram,
    appState.spectrogramDefaults
  );
}
