import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { appState } from '$lib/stores/appState.svelte';
import { settingsStore } from '$lib/stores/settings';
import {
  PLAYBACK_GAIN_MAX_DB,
  PLAYBACK_GAIN_MIN_DB,
  PLAYBACK_HIGH_PASS_MAX_HZ,
  PLAYBACK_HIGH_PASS_MIN_HZ,
  clampPlaybackGain,
  getPlaybackDefaults,
  highPassFromStored,
  highPassToStored,
} from './playbackDefaults';

describe('clampPlaybackGain', () => {
  it.each([
    { name: 'in range', input: 6, want: 6 },
    { name: 'negative in range', input: -6.5, want: -6.5 },
    { name: 'rounds to the half step', input: 6.3, want: 6.5 },
    { name: 'minimum', input: PLAYBACK_GAIN_MIN_DB, want: PLAYBACK_GAIN_MIN_DB },
    { name: 'below the minimum', input: -20, want: PLAYBACK_GAIN_MIN_DB },
    { name: 'above the maximum', input: 40, want: PLAYBACK_GAIN_MAX_DB },
    { name: 'NaN', input: NaN, want: 0 },
    { name: 'infinity', input: Infinity, want: 0 },
    { name: 'a string', input: '6', want: 0 },
    { name: 'undefined', input: undefined, want: 0 },
  ])('$name', ({ input, want }) => {
    expect(clampPlaybackGain(input)).toBe(want);
  });

  it('returns a plain zero, never negative zero, for a small negative gain', () => {
    expect(Object.is(clampPlaybackGain(-0.1), 0)).toBe(true);
    expect(Object.is(clampPlaybackGain(-0.2), 0)).toBe(true);
  });
});

describe('highPassFromStored and highPassToStored', () => {
  it.each([
    { name: 'off', stored: 0, shown: PLAYBACK_HIGH_PASS_MIN_HZ },
    { name: 'below the floor', stored: 19, shown: PLAYBACK_HIGH_PASS_MIN_HZ },
    { name: 'the floor', stored: 20, shown: PLAYBACK_HIGH_PASS_MIN_HZ },
    { name: 'in range', stored: 300, shown: 300 },
    { name: 'the maximum', stored: PLAYBACK_HIGH_PASS_MAX_HZ, shown: PLAYBACK_HIGH_PASS_MAX_HZ },
    { name: 'above the maximum', stored: 9000, shown: PLAYBACK_HIGH_PASS_MAX_HZ },
    { name: 'NaN', stored: NaN, shown: PLAYBACK_HIGH_PASS_MIN_HZ },
    { name: 'a string', stored: '300', shown: PLAYBACK_HIGH_PASS_MIN_HZ },
  ])('reads $name as the cutoff a player starts with', ({ stored, shown }) => {
    expect(highPassFromStored(stored)).toBe(shown);
  });

  it.each([
    { name: 'the floor is stored as off', cutoff: 20, want: 0 },
    { name: 'below the floor is stored as off', cutoff: 15, want: 0 },
    { name: 'zero is stored as off', cutoff: 0, want: 0 },
    { name: 'an active cutoff is kept', cutoff: 300, want: 300 },
    {
      name: 'the maximum is kept',
      cutoff: PLAYBACK_HIGH_PASS_MAX_HZ,
      want: PLAYBACK_HIGH_PASS_MAX_HZ,
    },
    { name: 'above the maximum is clamped', cutoff: 9000, want: PLAYBACK_HIGH_PASS_MAX_HZ },
    { name: 'NaN is stored as off', cutoff: NaN, want: 0 },
  ])('$name', ({ cutoff, want }) => {
    expect(highPassToStored(cutoff)).toBe(want);
  });
});

describe('getPlaybackDefaults', () => {
  const initial = get(settingsStore);

  function loadSettings(dashboard: Record<string, unknown>, unsaved: Record<string, unknown> = {}) {
    settingsStore.update(store => {
      const withDashboard = (values: Record<string, unknown>) => ({
        ...store.originalData.realtime,
        dashboard: { ...store.originalData.realtime?.dashboard, ...values },
      });
      return {
        ...store,
        dataLoaded: true,
        originalData: { ...store.originalData, realtime: withDashboard(dashboard) },
        formData: { ...store.formData, realtime: withDashboard({ ...dashboard, ...unsaved }) },
      } as unknown as typeof store;
    });
  }

  beforeEach(() => {
    settingsStore.set(initial);
    appState.playbackDefaults = { gainDb: 0, highPassHz: 0 };
    appState.playbackDenoise = '';
  });

  afterEach(() => {
    settingsStore.set(initial);
    appState.playbackDefaults = { gainDb: 0, highPassHz: 0 };
    appState.playbackDenoise = '';
  });

  it('starts from zero gain, no filter and no denoise when nothing is loaded', () => {
    expect(getPlaybackDefaults()).toEqual({
      gainDb: 0,
      denoise: '',
      highPassHz: PLAYBACK_HIGH_PASS_MIN_HZ,
    });
  });

  it('gives a guest the values /app/config carried', () => {
    appState.playbackDefaults = { gainDb: -6.5, highPassHz: 300 };
    appState.playbackDenoise = 'heavy';

    expect(getPlaybackDefaults()).toEqual({ gainDb: -6.5, denoise: 'heavy', highPassHz: 300 });
  });

  it('reads the saved settings once they are loaded, ahead of /app/config', () => {
    appState.playbackDefaults = { gainDb: 12, highPassHz: 900 };
    loadSettings({
      defaultAudioGain: -3,
      playback: { highPassHz: 250 },
      playbackDenoise: { enabled: true, strength: 'light' },
    });

    expect(getPlaybackDefaults()).toEqual({ gainDb: -3, denoise: 'light', highPassHz: 250 });
  });

  it('reads saved settings, not unsaved form edits', () => {
    loadSettings(
      { defaultAudioGain: 6, playback: { highPassHz: 250 } },
      { defaultAudioGain: 18, playback: { highPassHz: 1000 } }
    );

    expect(getPlaybackDefaults()).toMatchObject({ gainDb: 6, highPassHz: 250 });
  });

  it('shows a stored 0 as the floor', () => {
    loadSettings({ defaultAudioGain: 0, playback: { highPassHz: 0 } });

    expect(getPlaybackDefaults().highPassHz).toBe(PLAYBACK_HIGH_PASS_MIN_HZ);
  });
});
