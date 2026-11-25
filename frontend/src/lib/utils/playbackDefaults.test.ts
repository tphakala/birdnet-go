import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { appState } from '$lib/stores/appState.svelte';
import { settingsStore } from '$lib/stores/settings';
import type { Dashboard, RealtimeSettings } from '$lib/stores/settings';
import {
  PLAYBACK_DEFAULT_ITEMS,
  changedPlaybackDefaults,
  clampPlaybackGain,
  formatPlaybackGain,
  formatPlaybackHighPass,
  getPlaybackDefaults,
  highPassFromStored,
  highPassToStored,
  playbackDefaultsPatch,
  readPlaybackDefaults,
  type PlaybackDefaultValues,
} from './playbackDefaults';

describe('clampPlaybackGain', () => {
  it.each([
    { name: 'in range', input: 6, want: 6 },
    { name: 'negative in range', input: -6.5, want: -6.5 },
    { name: 'rounds to the half step', input: 6.3, want: 6.5 },
    { name: 'minimum', input: -12, want: -12 },
    { name: 'below the minimum', input: -20, want: -12 },
    { name: 'above the maximum', input: 40, want: 24 },
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

describe('formatPlaybackGain', () => {
  it.each([
    { name: 'zero', db: 0, want: '0 dB' },
    { name: 'a positive whole value', db: 6, want: '+6 dB' },
    { name: 'a positive half step', db: 7.5, want: '+7.5 dB' },
    { name: 'a negative half step', db: -6.5, want: '-6.5 dB' },
    { name: 'the minimum', db: -12, want: '-12 dB' },
    { name: 'negative zero', db: -0, want: '0 dB' },
    { name: 'infinity', db: Infinity, want: '0 dB' },
    { name: 'negative infinity', db: -Infinity, want: '0 dB' },
    { name: 'not a number', db: NaN, want: '0 dB' },
  ])('formats $name', ({ db, want }) => {
    expect(formatPlaybackGain(db, 'dB')).toBe(want);
  });

  it('puts the given unit after a space', () => {
    expect(formatPlaybackGain(3, 'desibeliä')).toBe('+3 desibeliä');
  });
});

describe('highPassFromStored and highPassToStored', () => {
  it.each([
    { name: 'off', stored: 0, shown: 20 },
    { name: 'below the floor', stored: 19, shown: 20 },
    { name: 'the floor', stored: 20, shown: 20 },
    { name: 'in range', stored: 300, shown: 300 },
    { name: 'just above the floor, snapped down', stored: 21, shown: 20 },
    { name: 'below the halfway point, snapped down', stored: 24, shown: 20 },
    { name: 'the halfway point, snapped up', stored: 25, shown: 30 },
    { name: 'off the grid in the middle of the range', stored: 333, shown: 330 },
    { name: 'the maximum', stored: 5000, shown: 5000 },
    { name: 'above the maximum', stored: 9000, shown: 5000 },
    { name: 'NaN', stored: NaN, shown: 20 },
    { name: 'a string', stored: '300', shown: 20 },
  ])('reads $name as the cutoff a player starts with', ({ stored, shown }) => {
    expect(highPassFromStored(stored)).toBe(shown);
  });

  it.each([
    { name: 'the floor is stored as off', cutoff: 20, want: 0 },
    { name: 'below the floor is stored as off', cutoff: 15, want: 0 },
    { name: 'zero is stored as off', cutoff: 0, want: 0 },
    { name: 'an active cutoff is kept', cutoff: 300, want: 300 },
    { name: 'just above the floor is kept, not snapped', cutoff: 21, want: 21 },
    { name: 'the lowest cutoff the slider offers is kept', cutoff: 30, want: 30 },
    { name: 'a fraction at the floor is stored as off', cutoff: 20.4, want: 0 },
    {
      name: 'the maximum is kept',
      cutoff: 5000,
      want: 5000,
    },
    { name: 'above the maximum is clamped', cutoff: 9000, want: 5000 },
    { name: 'NaN is stored as off', cutoff: NaN, want: 0 },
  ])('$name', ({ cutoff, want }) => {
    expect(highPassToStored(cutoff)).toBe(want);
  });
});

describe('getPlaybackDefaults', () => {
  const initial = get(settingsStore);

  // The loaded store is seeded with only the dashboard section these tests read,
  // not a full form type.
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
      highPassHz: 20,
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

    expect(getPlaybackDefaults().highPassHz).toBe(20);
  });
});

describe('formatPlaybackHighPass', () => {
  it.each([
    { name: 'the floor', hz: 20, want: 'settings.userInterface.audioPlayback.highPassOff' },
    { name: 'below the floor', hz: 0, want: 'settings.userInterface.audioPlayback.highPassOff' },
    { name: 'not a number', hz: NaN, want: 'settings.userInterface.audioPlayback.highPassOff' },
    { name: 'infinity', hz: Infinity, want: 'settings.userInterface.audioPlayback.highPassOff' },
    {
      name: 'an active cutoff',
      hz: 300,
      want: '300 settings.userInterface.audioPlayback.highPassUnit',
    },
  ])('formats $name', ({ hz, want }) => {
    expect(formatPlaybackHighPass(hz)).toBe(want);
  });
});

describe('PLAYBACK_DEFAULT_ITEMS', () => {
  it('lists gain, denoise and high-pass, each slider with its own format', () => {
    expect(PLAYBACK_DEFAULT_ITEMS.map(item => item.key)).toEqual([
      'gainDb',
      'denoise',
      'highPassHz',
    ]);
    const formatted = PLAYBACK_DEFAULT_ITEMS.flatMap(item =>
      item.kind === 'slider' ? [item.format(item.key === 'gainDb' ? -6.5 : 20)] : []
    );
    expect(formatted).toEqual([
      '-6.5 settings.userInterface.audioPlayback.defaultGainUnit',
      'settings.userInterface.audioPlayback.highPassOff',
    ]);
  });
});

describe('readPlaybackDefaults', () => {
  // Only the dashboard section is read; the rest of the realtime settings is not needed here.
  const realtime = (dashboard: Partial<Dashboard>) =>
    ({ dashboard }) as unknown as RealtimeSettings;

  it('starts from zero gain, off and the floor when nothing is stored', () => {
    expect(readPlaybackDefaults(undefined)).toEqual({ gainDb: 0, denoise: '', highPassHz: 20 });
    expect(readPlaybackDefaults(realtime({}))).toEqual({ gainDb: 0, denoise: '', highPassHz: 20 });
  });

  it('reads the stored values, snapped to the sliders', () => {
    expect(
      readPlaybackDefaults(
        realtime({
          defaultAudioGain: -6.5,
          playbackDenoise: { enabled: true, strength: 'heavy' },
          playback: { highPassHz: 333 },
        })
      )
    ).toEqual({ gainDb: -6.5, denoise: 'heavy', highPassHz: 330 });
  });

  it('reads a disabled or unknown denoise strength as off', () => {
    expect(
      readPlaybackDefaults(realtime({ playbackDenoise: { enabled: false, strength: 'heavy' } }))
        .denoise
    ).toBe('');
    expect(
      readPlaybackDefaults(realtime({ playbackDenoise: { enabled: true, strength: 'loud' } }))
        .denoise
    ).toBe('');
  });

  it('shows the stored choice even while export denoise is on', () => {
    const settings = {
      dashboard: { playbackDenoise: { enabled: true, strength: 'light' } },
      audio: { export: { denoise: { enabled: true } } },
    } as unknown as RealtimeSettings;
    expect(readPlaybackDefaults(settings).denoise).toBe('light');
  });
});

describe('changedPlaybackDefaults', () => {
  const saved: PlaybackDefaultValues = { gainDb: 0, denoise: '', highPassHz: 20 };

  it('is empty when nothing differs', () => {
    expect(changedPlaybackDefaults({ ...saved }, saved)).toEqual({});
  });

  it('lists only the keys that differ', () => {
    expect(changedPlaybackDefaults({ ...saved, denoise: 'light', highPassHz: 300 }, saved)).toEqual(
      { denoise: 'light', highPassHz: 300 }
    );
  });
});

describe('playbackDefaultsPatch', () => {
  const saved: Partial<Dashboard> = {
    defaultAudioGain: 6,
    playbackDenoise: { enabled: true, strength: 'heavy' },
    playback: { highPassHz: 300 },
  };

  it('sends only the changed keys', () => {
    expect(playbackDefaultsPatch({ gainDb: -6.5 }, saved)).toEqual({ defaultAudioGain: -6.5 });
  });

  it('keeps the saved strength when denoise is turned off', () => {
    expect(playbackDefaultsPatch({ denoise: '' }, saved)).toEqual({
      playbackDenoise: { enabled: false, strength: 'heavy' },
    });
  });

  it('falls back to the default strength when none is saved', () => {
    expect(playbackDefaultsPatch({ denoise: '' }, undefined)).toEqual({
      playbackDenoise: { enabled: false, strength: 'light-medium' },
    });
  });

  it('turns denoise on with the chosen strength', () => {
    expect(playbackDefaultsPatch({ denoise: 'light' }, saved)).toEqual({
      playbackDenoise: { enabled: true, strength: 'light' },
    });
  });

  it('sends the playback object whole and stores the floor as off', () => {
    expect(playbackDefaultsPatch({ highPassHz: 20 }, saved)).toEqual({
      playback: { highPassHz: 0 },
    });
    expect(playbackDefaultsPatch({ highPassHz: 500 }, undefined)).toEqual({
      playback: { highPassHz: 500 },
    });
  });
});
