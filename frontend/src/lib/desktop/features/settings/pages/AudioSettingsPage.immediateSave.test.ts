/**
 * The sound card tab saves each change as it is made: it has no save bar, and a
 * change goes to the auto-save engine as the audio section's sources array only.
 * The other tabs keep the save bar.
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import type { Writable } from 'svelte/store';
import AudioSettingsPage from './AudioSettingsPage.svelte';

const TAB_STORAGE_KEY = 'birdnet-audio-settings-active-tab';
const SAVE_KEY = 'settings.actions.save';
const TAB_NOTE_KEY = 'settings.autoSave.tabNote';

vi.mock('$lib/desktop/components/forms/StreamManager.svelte');
vi.mock('$lib/desktop/components/forms/SoundCardManager.svelte', async () => ({
  default: (await import('../../../../../test/fixtures/MockSoundCardManager.svelte')).default,
}));

const { commitSetting } = vi.hoisted(() => ({ commitSetting: vi.fn() }));
vi.mock('$lib/stores/settingsAutoSave.svelte', () => ({
  commitSetting,
  autoSaveStatus: vi.fn(() => ({ state: 'idle' })),
  autoSaveBusy: vi.fn(() => false),
  autoSaveFailures: vi.fn(() => 0),
}));

// The device list loads; a failed load would replace the source list with an error state.
vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: { get: vi.fn().mockResolvedValue([]) },
}));

vi.mock('$lib/stores/appState.svelte', () => ({
  appState: { csrfToken: 'test-csrf-token', initialized: true, loading: false, error: null },
  getCsrfToken: vi.fn().mockReturnValue('test-csrf-token'),
  isSentryEnabled: () => false,
  isGuestMode: vi.fn(() => false),
  refreshCsrfToken: vi.fn().mockResolvedValue(false),
}));

vi.mock('$lib/stores/settings', async () => {
  const { writable } = await vi.importActual<typeof import('svelte/store')>('svelte/store');
  const settingsStore = writable({
    isLoading: false,
    isSaving: false,
    error: null,
    activeSection: 'audio',
    originalData: { realtime: { audio: { export: {} } } },
    formData: { realtime: { audio: { export: {} } } },
  });
  return {
    settingsStore,
    audioSettings: writable<unknown>(null),
    rtspSettings: writable<unknown>(null),
    realtimeSettings: writable<unknown>(null),
    extendedCaptureSettings: writable<unknown>(null),
    settingsActions: { updateSection: vi.fn(), saveSettings: vi.fn(), resetAllSettings: vi.fn() },
    hasUnsavedChanges: writable(false),
    settingsValidationErrors: writable<string[]>([]),
    settingsValidationReasons: writable(new Map<string, string>()),
    SettingsChangedElsewhereError: class extends Error {},
    defaultQuietHoursConfig: {},
  };
});

const SOURCE = { name: 'Garden', device: 'hw:1,0', gain: 0, models: ['birdnet'] };

async function renderTab(tab: string) {
  // The module is mocked above with writable stores; the real types are readable.
  const { audioSettings } = (await import('$lib/stores/settings')) as unknown as {
    audioSettings: Writable<unknown>;
  };
  audioSettings.set({
    sources: [SOURCE],
    source: '',
    soundLevel: { enabled: false, interval: 60 },
    equalizer: { enabled: false, filters: [] },
    export: {
      enabled: true,
      path: 'clips/',
      type: 'wav',
      ultrasonicType: 'flac',
      bitrate: '96k',
      retention: { policy: 'none', maxAge: '7d', maxUsage: '80%', minClips: 10 },
      length: 15,
      preCapture: 3,
      gain: 0,
      normalization: { enabled: false, targetLUFS: -23, loudnessRange: 7, truePeak: -2 },
      denoise: { enabled: false, strength: 'light-medium' },
    },
  });
  localStorage.setItem(TAB_STORAGE_KEY, tab);
  render(AudioSettingsPage);
}

describe('AudioSettingsPage sound card tab saves immediately', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    localStorage.removeItem(TAB_STORAGE_KEY);
  });

  it('has no save bar and commits the sources array of the audio section', async () => {
    await renderTab('soundcard');

    expect(screen.getByText(TAB_NOTE_KEY)).toBeInTheDocument();
    expect(screen.queryByText(SAVE_KEY)).toBeNull();

    await fireEvent.click(await screen.findByRole('button', { name: 'commit-sources' }));
    expect(commitSetting).toHaveBeenCalledExactlyOnceWith(
      'audio',
      { sources: [{ ...SOURCE, gain: 1 }] },
      { field: 'audio:sources:row:gain', debounceMs: 400 }
    );
  });

  it('keeps the save bar on the recording tab', async () => {
    await renderTab('recording');

    expect(screen.getByText(SAVE_KEY)).toBeInTheDocument();
    expect(screen.queryByText(TAB_NOTE_KEY)).toBeNull();
  });
});
