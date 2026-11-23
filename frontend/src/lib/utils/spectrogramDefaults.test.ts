import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { get } from 'svelte/store';
import { appState } from '$lib/stores/appState.svelte';
import { DEFAULT_SPECTROGRAM_SETTINGS, settingsStore } from '$lib/stores/settings';
import { seedSavedRealtime } from '../../test/settings-store-helpers';
import { getSpectrogramDefaults, resolveSpectrogramDefaults } from './spectrogramDefaults';

describe('spectrogramDefaults', () => {
  const initialStore = get(settingsStore);
  const initialAppDefaults = { ...appState.spectrogramDefaults };

  beforeEach(() => {
    settingsStore.set(initialStore);
    appState.spectrogramDefaults = { ...initialAppDefaults };
  });

  afterEach(() => {
    settingsStore.set(initialStore);
    appState.spectrogramDefaults = initialAppDefaults;
  });

  it('uses /app/config defaults before settings load (including for guests)', () => {
    appState.spectrogramDefaults = { size: 'lg', raw: false };
    expect(getSpectrogramDefaults()).toEqual({ size: 'lg', raw: false });
  });

  it('prefers dashboardSettings once settingsDataLoaded is true', () => {
    appState.spectrogramDefaults = { size: 'sm', raw: true };
    seedSavedRealtime({
      dashboard: {
        spectrogram: {
          ...DEFAULT_SPECTROGRAM_SETTINGS,
          size: 'xl',
          raw: false,
        },
      },
    });

    expect(getSpectrogramDefaults()).toEqual({ size: 'xl', raw: false });
  });

  it('falls back to md and raw=true when neither source has valid size', () => {
    expect(resolveSpectrogramDefaults(false, undefined, { size: 'invalid' })).toEqual({
      size: 'md',
      raw: true,
    });
  });
});
