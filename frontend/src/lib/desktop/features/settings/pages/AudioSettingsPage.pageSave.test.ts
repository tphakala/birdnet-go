/**
 * The sound card tab saves with the page: edits go into the settings form, the
 * Save Changes bar persists them with one full-form PUT, and an invalid source
 * blocks Save with the reason whichever tab is open. Runs over the real settings
 * store with a mocked settings API; the sound card manager is a stand-in whose
 * buttons send changed source lists.
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { api } from '$lib/utils/api';
import { navigation } from '$lib/stores/navigation.svelte';
import {
  settingsStore,
  settingsActions,
  hasUnsavedChanges,
  settingsValidationErrors,
} from '$lib/stores/settings';
import type { AudioSourceConfig } from '$lib/stores/settings';
import { settingsAPI } from '$lib/utils/settingsApi.js';
import { serveConfirmedSettings } from '../../../../../test/settings-store-helpers';
import { serverSettings } from '../../../../../test/settings-helpers';
import AudioSettingsPage from './AudioSettingsPage.svelte';
import { SETTINGS_CARD_HEADER_CLASSES } from '../components/SettingsCard.svelte';

const TAB_STORAGE_KEY = 'birdnet-audio-settings-active-tab';
const SAVE_KEY = 'settings.actions.save';
const TAB_NOTE_KEY = 'settings.autoSave.tabNote';
const BLOCKED_KEY = 'settings.actions.validationBlocked';
const SOUND_CARD_ISSUE_KEY = 'audio-soundcards-invalid';
const SOUND_CARD_REASON_KEY = 'settings.audio.validation.soundCards';
const RECORDING_TAB_KEY = 'settings.audio.tabs.recording';

// The Streams tab still saves as it changes through the auto-save engine.
const { commitSetting } = vi.hoisted(() => ({ commitSetting: vi.fn() }));
vi.mock('$lib/stores/settingsAutoSave.svelte', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/stores/settingsAutoSave.svelte')>()),
  commitSetting,
}));

vi.mock('$lib/utils/settingsApi.js', () => ({
  settingsAPI: {
    load: vi.fn(),
    save: vi.fn().mockResolvedValue(undefined),
    patchSection: vi.fn().mockResolvedValue({}),
  },
}));
vi.mock('$lib/stores/toast.js', () => ({ toastActions: { success: vi.fn(), error: vi.fn() } }));

vi.mock('$lib/desktop/components/forms/StreamManager.svelte', async () => ({
  default: (await import('../../../../../test/fixtures/MockStreamManager.svelte')).default,
}));
vi.mock('$lib/desktop/components/forms/SoundCardManager.svelte', async () => ({
  default: (await import('../../../../../test/fixtures/MockSoundCardManager.svelte')).default,
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

const STREAM = { name: 'Old', url: 'rtsp://old', enabled: true, type: 'rtsp' };
const SOURCE = { name: 'Garden', device: 'hw:1,0', gain: 0, models: ['birdnet'] };

function seedStore(sources: AudioSourceConfig[]) {
  const settings = serverSettings();
  const audio = settings.realtime?.audio;
  if (!audio) throw new Error('serverSettings() has no realtime.audio section');
  audio.sources = sources;
  settingsStore.set({
    formData: globalThis.structuredClone(settings),
    originalData: globalThis.structuredClone(settings),
    isLoading: false,
    isSaving: false,
    activeSection: 'audio',
    error: null,
    dataLoaded: true,
  });
  // A server response carries the defaults the store's coercion fills in on the
  // first write, so take the coerced form as the saved state: a source edit then
  // changes only the sources.
  settingsActions.updateSection('realtime', {});
  settingsStore.update(state => ({
    ...state,
    originalData: globalThis.structuredClone(state.formData),
  }));
}

function formSources(): AudioSourceConfig[] {
  return get(settingsStore).formData.realtime?.audio?.sources ?? [];
}

function renderTab(tab: string, sources: AudioSourceConfig[] = [SOURCE]) {
  seedStore(sources);
  localStorage.setItem(TAB_STORAGE_KEY, tab);
  return render(AudioSettingsPage);
}

const saveButton = () =>
  screen.getByRole<HTMLButtonElement>('button', { name: 'settings.actions.saveAriaLabel' });

describe('AudioSettingsPage sound card tab saves with the page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    serveConfirmedSettings();
  });

  afterEach(() => {
    localStorage.removeItem(TAB_STORAGE_KEY);
    // Selecting a tab writes the URL, which the next test would start from.
    navigation.navigate('/');
  });

  it('shows the Save Changes bar and no tab note', async () => {
    renderTab('soundcard');

    expect(await screen.findByRole('button', { name: 'commit-sources' })).toBeInTheDocument();
    expect(screen.getByText(SAVE_KEY)).toBeInTheDocument();
    expect(screen.queryByText(TAB_NOTE_KEY)).toBeNull();
  });

  it('writes an edit into the form without a request, and Save sends one full-form PUT', async () => {
    renderTab('soundcard');

    await fireEvent.click(await screen.findByRole('button', { name: 'commit-sources' }));

    expect(formSources()).toEqual([{ ...SOURCE, gain: 1 }]);
    expect(get(hasUnsavedChanges)).toBe(true);
    expect(settingsAPI.patchSection).not.toHaveBeenCalled();
    expect(settingsAPI.save).not.toHaveBeenCalled();

    await fireEvent.click(saveButton());
    await waitFor(() => expect(settingsAPI.save).toHaveBeenCalledOnce());
    expect(settingsAPI.patchSection).not.toHaveBeenCalled();
    expect(vi.mocked(settingsAPI.save).mock.calls[0][0].realtime?.audio?.sources).toEqual([
      { ...SOURCE, gain: 1 },
    ]);
  });

  it.each([
    ['gain', 'commit-sources', 'revert-sources'],
    ['name', 'rename-source', 'rename-back'],
  ])('leaves nothing unsaved after a %s edit and its inverse', async (_field, edit, back) => {
    renderTab('soundcard');

    await fireEvent.click(await screen.findByRole('button', { name: edit }));
    expect(get(hasUnsavedChanges)).toBe(true);
    await fireEvent.click(screen.getByRole('button', { name: back }));

    expect(get(hasUnsavedChanges)).toBe(false);
    expect(saveButton()).toHaveAttribute('aria-disabled', 'true');
  });

  it('keeps the audio section keys the tab does not edit when a source is written', async () => {
    renderTab('soundcard');
    const before = globalThis.structuredClone(get(settingsStore).formData.realtime?.audio);

    await fireEvent.click(await screen.findByRole('button', { name: 'commit-sources' }));

    expect({ ...get(settingsStore).formData.realtime?.audio, sources: undefined }).toEqual({
      ...before,
      sources: undefined,
    });
  });

  it('blocks Save with the reason for an invalid name, also from the recording tab', async () => {
    renderTab('soundcard');

    await fireEvent.click(await screen.findByRole('button', { name: 'blank-name' }));
    expect(await screen.findByText(BLOCKED_KEY)).toBeInTheDocument();
    expect(screen.getByText(SOUND_CARD_REASON_KEY)).toBeInTheDocument();
    expect(saveButton()).toHaveAttribute('aria-disabled', 'true');

    // Only the active tab is mounted, yet the rule still applies on another tab.
    await fireEvent.click(screen.getByRole('tab', { name: RECORDING_TAB_KEY }));
    expect(await screen.findByText(BLOCKED_KEY)).toBeInTheDocument();
    expect(screen.getByText(SOUND_CARD_REASON_KEY)).toBeInTheDocument();
    expect(saveButton()).toHaveAttribute('aria-disabled', 'true');
    await fireEvent.click(saveButton());
    expect(settingsAPI.save).not.toHaveBeenCalled();
  });

  it('lifts the block when the name is fixed', async () => {
    renderTab('soundcard');
    await fireEvent.click(await screen.findByRole('button', { name: 'blank-name' }));
    await screen.findByText(SOUND_CARD_REASON_KEY);

    await fireEvent.click(screen.getByRole('button', { name: 'rename-source' }));

    await waitFor(() => expect(screen.queryByText(SOUND_CARD_REASON_KEY)).toBeNull());
    expect(saveButton()).not.toHaveAttribute('aria-disabled');
  });

  it('does not block Save on an invalid value the saved configuration already has', async () => {
    // A stored oddity must not stop a save on another tab: only what the user changed counts.
    renderTab('soundcard', [
      { ...SOURCE, name: 'Mic' },
      { ...SOURCE, name: 'mic', device: 'hw:2,0' },
    ]);

    await fireEvent.click(await screen.findByRole('button', { name: 'commit-sources' }));

    expect(get(hasUnsavedChanges)).toBe(true);
    expect(screen.queryByText(SOUND_CARD_REASON_KEY)).toBeNull();
  });

  it('clears the sound card block when the page unmounts', async () => {
    const view = renderTab('soundcard');
    await fireEvent.click(await screen.findByRole('button', { name: 'blank-name' }));
    await waitFor(() => expect(get(settingsValidationErrors)).toContain(SOUND_CARD_ISSUE_KEY));

    view.unmount();

    expect(get(settingsValidationErrors)).not.toContain(SOUND_CARD_ISSUE_KEY);
  });

  it('disables the manager while a save runs and says why', async () => {
    renderTab('soundcard');
    const state = await screen.findByTestId('manager-state');
    expect(state).toHaveAttribute('data-disabled', 'false');

    settingsStore.update(current => ({ ...current, isSaving: true }));

    await waitFor(() => expect(state).toHaveAttribute('data-disabled', 'true'));
    expect(state).toHaveTextContent('settings.actions.saving');
  });

  it('remounts the manager when the form is reset', async () => {
    renderTab('soundcard');
    await fireEvent.click(await screen.findByRole('button', { name: 'commit-sources' }));
    const before = screen.getByTestId('manager-state').getAttribute('data-mount');

    await fireEvent.click(screen.getByRole('button', { name: 'settings.actions.resetAriaLabel' }));

    await waitFor(() =>
      expect(screen.getByTestId('manager-state').getAttribute('data-mount')).not.toBe(before)
    );
  });

  it('keeps the Streams tab on the auto-save engine with its tab note and no save bar', async () => {
    renderTab('streams');

    expect(await screen.findByText(TAB_NOTE_KEY)).toBeInTheDocument();
    expect(screen.queryByText(SAVE_KEY)).toBeNull();

    await fireEvent.click(await screen.findByRole('button', { name: 'commit-streams' }));
    expect(commitSetting).toHaveBeenCalledExactlyOnceWith(
      'rtsp',
      { streams: [{ ...STREAM, gain: 1 }] },
      { field: 'rtsp:streams:row:gain', debounceMs: 400 }
    );
  });

  it('keeps the empty state on screen while Refresh reloads the devices after an empty first load', async () => {
    await renderTab('soundcard', []);
    await screen.findByText('settings.audio.emptyStates.soundCard.title');

    // The reload stays pending, so the page is mid-refresh when it is read.
    vi.mocked(api.get).mockReturnValueOnce(new Promise(() => {}));
    await fireEvent.click(
      screen.getByRole('button', { name: 'settings.audio.emptyStates.soundCard.refresh' })
    );

    expect(screen.getByText('settings.audio.emptyStates.soundCard.title')).toBeInTheDocument();
    expect(screen.queryByText('settings.audio.loading')).toBeNull();
  });

  it('shows the skeleton, not the empty state, while Retry reloads after a failed first load', async () => {
    vi.mocked(api.get).mockRejectedValueOnce(new Error('offline'));
    await renderTab('soundcard', []);
    const retry = await screen.findByRole('button', {
      name: 'settings.audio.emptyStates.soundCard.retry',
    });

    vi.mocked(api.get).mockReturnValueOnce(new Promise(() => {}));
    await fireEvent.click(retry);

    expect(await screen.findByText('settings.audio.loading')).toBeInTheDocument();
    expect(screen.queryByText('settings.audio.emptyStates.soundCard.title')).toBeNull();
  });

  it('shows the loading skeleton before the first device load completes', async () => {
    vi.mocked(api.get).mockReturnValueOnce(new Promise(() => {}));
    await renderTab('soundcard', []);

    const status = await screen.findByText('settings.audio.loading');
    expect(status).toBeInTheDocument();
    // The skeleton header keeps the loaded card's header spacing and divider
    expect(status.parentElement).toHaveClass(...SETTINGS_CARD_HEADER_CLASSES.split(/\s+/));
  });
});
