import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { appState } from '$lib/stores/appState.svelte';
import { settingsStore } from '$lib/stores/settings';
import {
  audioUrlFor,
  getPlaybackDenoiseDefault,
  swapSourceKeepingPosition,
  withCacheBuster,
} from './playbackDenoise';

/** Publish a loaded settings store holding the given denoise settings. */
function loadSettings(options: {
  playback?: { enabled: boolean; strength: string };
  exportDenoise?: { enabled: boolean; strength: string };
  // Unsaved form edits; the saved settings in the options above must win.
  unsavedPlayback?: { enabled: boolean; strength: string };
}) {
  // The loaded store is seeded with only the sections the helper reads.
  settingsStore.update(store => {
    const realtime = store.originalData.realtime;
    const section = (playback?: { enabled: boolean; strength: string }) => ({
      ...realtime,
      audio: {
        ...realtime?.audio,
        export: {
          ...realtime?.audio?.export,
          denoise: options.exportDenoise ?? { enabled: false, strength: 'light-medium' },
        },
      },
      dashboard: { ...realtime?.dashboard, playbackDenoise: playback },
    });
    return {
      ...store,
      dataLoaded: true,
      originalData: { ...store.originalData, realtime: section(options.playback) },
      formData: {
        ...store.formData,
        realtime: section(options.unsavedPlayback ?? options.playback),
      },
    } as unknown as typeof store;
  });
}

describe('getPlaybackDenoiseDefault', () => {
  const initial = get(settingsStore);

  beforeEach(() => {
    settingsStore.set(initial);
    appState.playbackDenoise = '';
  });

  afterEach(() => {
    settingsStore.set(initial);
    appState.playbackDenoise = '';
  });

  it('is empty when neither store is populated', () => {
    expect(getPlaybackDenoiseDefault()).toBe('');
  });

  it('falls back to the app config value (guests) when settings are not loaded', () => {
    appState.playbackDenoise = 'medium';
    expect(getPlaybackDenoiseDefault()).toBe('medium');
  });

  it('prefers the settings store over the app config value once it is loaded', () => {
    appState.playbackDenoise = 'medium';
    loadSettings({ playback: { enabled: false, strength: 'heavy' } });
    expect(getPlaybackDenoiseDefault()).toBe('');
  });

  it('returns the configured strength when the setting is on', () => {
    loadSettings({ playback: { enabled: true, strength: 'heavy' } });
    expect(getPlaybackDenoiseDefault()).toBe('heavy');
  });

  it('is empty while export-stage denoise is on', () => {
    loadSettings({
      playback: { enabled: true, strength: 'heavy' },
      exportDenoise: { enabled: true, strength: 'light' },
    });
    expect(getPlaybackDenoiseDefault()).toBe('');
  });

  it('follows the saved setting, not unsaved form edits', () => {
    loadSettings({
      playback: { enabled: false, strength: 'light-medium' },
      unsavedPlayback: { enabled: true, strength: 'heavy' },
    });
    expect(getPlaybackDenoiseDefault()).toBe('');

    loadSettings({
      playback: { enabled: true, strength: 'medium' },
      unsavedPlayback: { enabled: false, strength: 'heavy' },
    });
    expect(getPlaybackDenoiseDefault()).toBe('medium');
  });

  it('is empty for an unknown strength', () => {
    loadSettings({ playback: { enabled: true, strength: 'extreme' } });
    expect(getPlaybackDenoiseDefault()).toBe('');
  });
});

describe('audioUrlFor', () => {
  it('adds no parameter for an empty preset', () => {
    expect(audioUrlFor(7, '')).toBe('/api/v2/audio/7');
  });

  it('adds the denoise parameter for a preset', () => {
    expect(audioUrlFor('7', 'light-medium')).toBe('/api/v2/audio/7?denoise=light-medium');
  });
});

describe('withCacheBuster', () => {
  const NOW_MS = 1_700_000_000_000;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW_MS);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('adds t as its own parameter to a url without a query', () => {
    expect(withCacheBuster('/api/v2/audio/1')).toBe(`/api/v2/audio/1?t=${NOW_MS}`);
  });

  it('keeps the denoise parameter and adds t after it', () => {
    expect(withCacheBuster('/api/v2/audio/1?denoise=light-medium')).toBe(
      `/api/v2/audio/1?denoise=light-medium&t=${NOW_MS}`
    );
  });

  it('replaces an earlier t instead of stacking another', () => {
    expect(withCacheBuster('/api/v2/audio/1?denoise=heavy&t=1')).toBe(
      `/api/v2/audio/1?denoise=heavy&t=${NOW_MS}`
    );
  });
});

describe('swapSourceKeepingPosition', () => {
  const POSITION_SECONDS = 12;

  /** An audio element whose currentTime records the writes. */
  function audioWithPosition(position: number) {
    const element = document.createElement('audio');
    let current = position;
    const writes: number[] = [];
    Object.defineProperty(element, 'currentTime', {
      get: () => current,
      set: (value: number) => {
        current = value;
        writes.push(value);
      },
    });
    return { element, writes };
  }

  it('points the element at the new source and restores the position', () => {
    const { element, writes } = audioWithPosition(POSITION_SECONDS);

    swapSourceKeepingPosition(element, '/api/v2/audio/1?denoise=light-medium');

    expect(element.getAttribute('src')).toBe('/api/v2/audio/1?denoise=light-medium');
    expect(writes).toEqual([POSITION_SECONDS]);
  });

  it('does not write the position when the clip is at the start', () => {
    const { element, writes } = audioWithPosition(0);

    swapSourceKeepingPosition(element, '/api/v2/audio/1');

    expect(element.getAttribute('src')).toBe('/api/v2/audio/1');
    expect(writes).toEqual([]);
  });
});
