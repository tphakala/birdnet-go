import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import type { Writable } from 'svelte/store';
import AudioSettingsPage from './AudioSettingsPage.svelte';

// The Processing tab is the one the denoise section lives on; the page restores
// the last active tab from this key.
const TAB_STORAGE_KEY = 'birdnet-audio-settings-active-tab';

// Tests run without translations, so the text shown is the key.
const ENABLE_LABEL = 'settings.audio.exportDenoise.enable';
const WARNING_PERMANENT = 'settings.audio.exportDenoise.warningPermanent';
const LOSSY_NOTE = 'settings.audio.exportDenoise.lossyNote';

vi.mock('$lib/desktop/components/forms/StreamManager.svelte');
vi.mock('$lib/desktop/components/forms/SoundCardManager.svelte');

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
  const audioSettings = writable<unknown>(null);
  const rtspSettings = writable<unknown>(null);
  const realtimeSettings = writable<unknown>(null);
  const extendedCaptureSettings = writable<unknown>(null);
  const settingsActions = { updateSection: vi.fn() };
  return {
    settingsStore,
    audioSettings,
    rtspSettings,
    realtimeSettings,
    extendedCaptureSettings,
    settingsActions,
    hasUnsavedChanges: writable(false),
    defaultQuietHoursConfig: {},
  };
});

type ExportOverrides = { type?: string; denoise?: unknown };

async function renderProcessingTab(exportOverrides: ExportOverrides) {
  // The module is mocked above with writable stores; the real types are readable.
  const { audioSettings, settingsActions } = (await import('$lib/stores/settings')) as unknown as {
    audioSettings: Writable<unknown>;
    settingsActions: { updateSection: ReturnType<typeof vi.fn> };
  };
  audioSettings.set({
    sources: [],
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
      ...exportOverrides,
    },
  });
  localStorage.setItem(TAB_STORAGE_KEY, 'processing');
  render(AudioSettingsPage);
  return settingsActions.updateSection;
}

describe('AudioSettingsPage clip denoise', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    global.fetch = vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve([]) });
  });

  afterEach(() => {
    localStorage.removeItem(TAB_STORAGE_KEY);
  });

  it('writes the whole denoise object when the switch is turned on', async () => {
    const updateSection = await renderProcessingTab({});

    await fireEvent.click(await screen.findByLabelText(ENABLE_LABEL));

    await waitFor(() => expect(updateSection).toHaveBeenCalled());
    const [section, data] = updateSection.mock.calls[0] as [
      string,
      { audio: { export: { denoise: unknown; type: string } } },
    ];
    expect(section).toBe('realtime');
    expect(data.audio.export.denoise).toEqual({ enabled: true, strength: 'light-medium' });
    // The rest of the export object travels with it, the update merges shallowly.
    expect(data.audio.export.type).toBe('wav');
  });

  it('keeps the strength when the switch is turned off', async () => {
    const updateSection = await renderProcessingTab({
      denoise: { enabled: true, strength: 'heavy' },
    });

    await fireEvent.click(await screen.findByLabelText(ENABLE_LABEL));

    await waitFor(() => expect(updateSection).toHaveBeenCalled());
    const data = updateSection.mock.calls[0][1] as {
      audio: { export: { denoise: unknown } };
    };
    expect(data.audio.export.denoise).toEqual({ enabled: false, strength: 'heavy' });
  });

  it('shows the permanence warning only while the switch is on', async () => {
    await renderProcessingTab({ denoise: { enabled: true, strength: 'light-medium' } });

    expect(await screen.findByText(WARNING_PERMANENT)).toBeTruthy();
    expect(screen.getByText('settings.audio.exportDenoise.warningImported')).toBeTruthy();
  });

  it('hides the warning while the switch is off', async () => {
    await renderProcessingTab({});

    await screen.findByLabelText(ENABLE_LABEL);
    expect(screen.queryByText(WARNING_PERMANENT)).toBeNull();
  });

  it.each([
    ['mp3', true],
    ['aac', true],
    ['opus', true],
    ['flac', false],
    ['wav', false],
  ])('shows the lossy-format note for %s: %s', async (type, shown) => {
    await renderProcessingTab({ type, denoise: { enabled: true, strength: 'light-medium' } });

    await screen.findByText(WARNING_PERMANENT);
    expect(screen.queryByText(LOSSY_NOTE) !== null).toBe(shown);
  });
});
