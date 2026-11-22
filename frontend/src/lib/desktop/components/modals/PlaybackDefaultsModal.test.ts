/**
 * PlaybackDefaultsModal: a draft of the station's playback defaults that is
 * written only by Save, with only the changed keys.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { get } from 'svelte/store';
import { createComponentTestFactory, renderTyped } from '../../../../test/render-helpers';
import { deferred } from '../../../../test/async-helpers';
import { seedSavedRealtime } from '../../../../test/settings-store-helpers';
import { settingsActions, settingsStore, type DenoiseSettings } from '$lib/stores/settings';
import { settingsAPI } from '$lib/utils/settingsApi.js';
import { toastActions } from '$lib/stores/toast';
import type { PlaybackDefaultValues } from '$lib/utils/playbackDefaults';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import PlaybackDefaultsModal from './PlaybackDefaultsModal.svelte';
import StackHost from './PlaybackDefaultsModal.stack.test.svelte';

const GAIN_LABEL = 'settings.userInterface.audioPlayback.defaultGain';
const DENOISE_LABEL = 'settings.userInterface.audioPlayback.denoiseLabel';
const HIGH_PASS_LABEL = 'settings.userInterface.audioPlayback.highPassLabel';
const SAVE = 'settings.actions.save';
// The shared test setup translates this key
const CANCEL = 'Cancel';
const USE_CURRENT = 'components.audioPlayer.defaults.useCurrent';
const NO_CHANGES = 'settings.actions.noChangesToSave';
const UNSAVED = 'settings.actions.unsavedChanges';
const SAVING = 'settings.actions.saving';
const SAVE_FAILED = 'components.audioPlayer.defaults.saveFailed';
const UNAVAILABLE = 'components.audioPlayer.defaults.unavailable';
const RETRY = 'common.ui.retry';
const PRECEDENCE = 'settings.userInterface.audioPlayback.denoiseExportPrecedence';

const initialStore = get(settingsStore);

interface SavedDashboard {
  defaultAudioGain?: number;
  playbackDenoise?: DenoiseSettings;
  playback?: { highPassHz: number };
}

function seedSettings(dashboard: SavedDashboard, exportDenoise = false, loaded = true) {
  seedSavedRealtime(
    {
      dashboard: { ...dashboard },
      audio: { export: { denoise: { enabled: exportDenoise, strength: 'light' } } },
    },
    loaded
  );
}

const CURRENT: PlaybackDefaultValues = { gainDb: 7.5, denoise: 'heavy', highPassHz: 400 };

function renderDialog(
  overrides: Partial<{
    current: PlaybackDefaultValues;
    onClose: () => void;
    onSaved: (changed: Partial<PlaybackDefaultValues>) => void;
  }> = {}
) {
  const onClose = overrides.onClose ?? vi.fn();
  const onSaved = overrides.onSaved ?? vi.fn();
  const view = renderTyped(PlaybackDefaultsModal, {
    props: { isOpen: true, current: overrides.current ?? CURRENT, onClose, onSaved },
  });
  return { ...view, onClose, onSaved };
}

const dialog = () => screen.getByRole('dialog');
const saveButton = () => within(dialog()).getByRole('button', { name: new RegExp(SAVE) });
const slider = (label: string) => within(dialog()).getByLabelText<HTMLInputElement>(label);

async function moveSlider(label: string, value: number) {
  await fireEvent.input(slider(label), { target: { value: String(value) } });
}

describe('PlaybackDefaultsModal', () => {
  const originalPatch = vi.mocked(settingsAPI.patchSection).getMockImplementation();
  const originalLoad = vi.mocked(settingsAPI.load).getMockImplementation();

  beforeEach(() => {
    settingsStore.set(initialStore);
    seedSettings({
      defaultAudioGain: 6,
      playbackDenoise: { enabled: false, strength: 'medium' },
      playback: { highPassHz: 0 },
    });
  });

  afterEach(() => {
    vi.mocked(settingsAPI.patchSection).mockReset();
    if (originalPatch) vi.mocked(settingsAPI.patchSection).mockImplementation(originalPatch);
    vi.mocked(settingsAPI.load).mockReset();
    if (originalLoad) vi.mocked(settingsAPI.load).mockImplementation(originalLoad);
    vi.mocked(toastActions.success).mockClear();
    vi.mocked(toastActions.error).mockClear();
    // Only the vi.spyOn spies; the shared module mocks keep their implementations
    vi.restoreAllMocks();
    settingsStore.set(initialStore);
  });

  it('renders one control per default, starting from the saved values', () => {
    renderDialog();

    expect(slider(GAIN_LABEL).value).toBe('6');
    expect(slider(HIGH_PASS_LABEL).value).toBe('20');
    expect(within(dialog()).getByRole('combobox', { name: DENOISE_LABEL })).toBeInTheDocument();
  });

  it('keeps Save blocked with its reason until a value changes', async () => {
    renderDialog();

    expect(saveButton()).toHaveAttribute('aria-disabled', 'true');
    expect(saveButton()).toHaveAccessibleDescription(NO_CHANGES);

    await moveSlider(GAIN_LABEL, 9);

    expect(saveButton()).not.toHaveAttribute('aria-disabled');
    expect(within(dialog()).getByText(UNSAVED)).toBeInTheDocument();
  });

  it('saves only the changed keys, once, then toasts, reports them and closes', async () => {
    const { onClose, onSaved } = renderDialog();

    await moveSlider(HIGH_PASS_LABEL, 300);
    await fireEvent.click(saveButton());

    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(settingsAPI.patchSection).toHaveBeenCalledTimes(1);
    expect(settingsAPI.patchSection).toHaveBeenCalledWith('dashboard', {
      playback: { highPassHz: 300 },
    });
    expect(onSaved).toHaveBeenCalledWith({ highPassHz: 300 });
    expect(toastActions.success).toHaveBeenCalledWith('components.audioPlayer.defaults.saved');
    expect(get(settingsStore).originalData.realtime?.dashboard?.playback?.highPassHz).toBe(300);
  });

  it('shows the saving state in the footer while the request runs', async () => {
    const pending = deferred<Record<string, never>>();
    vi.mocked(settingsAPI.patchSection).mockImplementation(() => pending.promise);
    const { onClose } = renderDialog();

    await moveSlider(GAIN_LABEL, 9);
    const save = saveButton();
    await fireEvent.click(save);

    await waitFor(() => expect(save).toHaveAttribute('aria-busy', 'true'));
    expect(save).toHaveAccessibleName(SAVING);
    expect(save).toHaveAccessibleDescription(SAVING);
    expect(within(dialog()).queryByText(UNSAVED)).toBeNull();
    pending.resolve({});
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });

  it('blocks closing and editing while the save runs', async () => {
    const pending = deferred<Record<string, never>>();
    vi.mocked(settingsAPI.patchSection).mockImplementation(() => pending.promise);
    const { onClose } = renderDialog();

    await moveSlider(GAIN_LABEL, 9);
    const save = saveButton();
    await fireEvent.click(save);
    await waitFor(() => expect(save).toHaveAttribute('aria-busy', 'true'));

    await fireEvent.click(within(dialog()).getByRole('button', { name: CANCEL }));
    await fireEvent.keyDown(document, { key: 'Escape' });
    await fireEvent.click(within(dialog()).getByRole('button', { name: USE_CURRENT }));
    expect(onClose).not.toHaveBeenCalled();
    expect(slider(GAIN_LABEL)).toBeDisabled();
    expect(within(dialog()).getByRole('combobox', { name: DENOISE_LABEL })).toBeDisabled();
    expect(slider(GAIN_LABEL).value).toBe('9');

    pending.resolve({});
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(settingsAPI.patchSection).toHaveBeenCalledWith('dashboard', { defaultAudioGain: 9 });
  });

  it('starts again from the saved values when reopened after Cancel', async () => {
    const view = createComponentTestFactory(PlaybackDefaultsModal).render({
      props: { isOpen: true, current: CURRENT, onClose: vi.fn(), onSaved: vi.fn() },
    });

    await moveSlider(GAIN_LABEL, 9);
    await view.rerender({ isOpen: false });
    await view.rerender({ isOpen: true });

    expect(slider(GAIN_LABEL).value).toBe('6');
    expect(saveButton()).toHaveAttribute('aria-disabled', 'true');
  });

  it('leaves the denoise default alone when copying while export denoise is on', async () => {
    seedSettings({ playbackDenoise: { enabled: true, strength: 'light' } }, true);
    const { onClose } = renderDialog();

    await fireEvent.click(within(dialog()).getByRole('button', { name: USE_CURRENT }));
    await fireEvent.click(saveButton());

    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(settingsAPI.patchSection).toHaveBeenCalledTimes(1);
    const [, patch] = vi.mocked(settingsAPI.patchSection).mock.calls[0];
    expect(patch).not.toHaveProperty('playbackDenoise');
  });

  it('holds a page unload only while the dialog is open with changes', async () => {
    const view = createComponentTestFactory(PlaybackDefaultsModal).render({
      props: { isOpen: true, current: CURRENT, onClose: vi.fn(), onSaved: vi.fn() },
    });
    const unload = () => {
      const event = new Event('beforeunload', { cancelable: true });
      window.dispatchEvent(event);
      return event.defaultPrevented;
    };

    expect(unload()).toBe(false);
    await moveSlider(GAIN_LABEL, 9);
    expect(unload()).toBe(true);
    await view.rerender({ isOpen: false });
    expect(unload()).toBe(false);
  });

  it('keeps the dialog open with the draft and an alert when the save fails', async () => {
    // Rejects at once, ahead of the tick the "Saving..." announcement waits for
    const saveSection = vi
      .spyOn(settingsActions, 'saveSection')
      .mockRejectedValue(new Error('Server said no.'));
    const { onClose, onSaved } = renderDialog();

    await moveSlider(GAIN_LABEL, -6.5);
    await fireEvent.click(saveButton());

    const alert = await within(dialog()).findByRole('alert');
    expect(alert).toHaveTextContent(`${SAVE_FAILED} Server said no.`);
    expect(slider(GAIN_LABEL).value).toBe('-6.5');
    expect(saveButton()).not.toHaveAttribute('aria-disabled');
    // The "Saving..." announcement must not land after the error
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(within(dialog()).getByRole('status').textContent).toBe('');
    expect(onClose).not.toHaveBeenCalled();
    expect(onSaved).not.toHaveBeenCalled();
    expect(saveSection).toHaveBeenCalledTimes(1);
  });

  it('does not report back to a player that unmounted while the save ran', async () => {
    const pending = deferred<Record<string, never>>();
    vi.mocked(settingsAPI.patchSection).mockImplementation(() => pending.promise);
    const { onClose, onSaved, unmount } = renderDialog();

    await moveSlider(GAIN_LABEL, 9);
    await fireEvent.click(saveButton());
    unmount();
    pending.resolve({});

    await waitFor(() => expect(toastActions.success).toHaveBeenCalled());
    expect(onSaved).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('reports a failed save in a toast when the dialog was closed while it ran', async () => {
    const pending = deferred<Record<string, never>>();
    vi.mocked(settingsAPI.patchSection).mockImplementation(() => pending.promise);
    const view = createComponentTestFactory(PlaybackDefaultsModal).render({
      props: { isOpen: true, current: CURRENT, onClose: vi.fn(), onSaved: vi.fn() },
    });

    await moveSlider(GAIN_LABEL, 9);
    await fireEvent.click(saveButton());
    await view.rerender({ isOpen: false });
    pending.reject(new Error('Server said no.'));

    await waitFor(() =>
      expect(toastActions.error).toHaveBeenCalledWith(`${SAVE_FAILED} Server said no.`)
    );
  });

  it('reports a failed save in a toast when the dialog unmounted while it ran', async () => {
    const pending = deferred<Record<string, never>>();
    vi.mocked(settingsAPI.patchSection).mockImplementation(() => pending.promise);
    const { unmount } = renderDialog();

    await moveSlider(GAIN_LABEL, 9);
    await fireEvent.click(saveButton());
    unmount();
    pending.reject(new Error('Server said no.'));

    await waitFor(() =>
      expect(toastActions.error).toHaveBeenCalledWith(`${SAVE_FAILED} Server said no.`)
    );
  });

  it('shows a failed save inline, not as a toast, while the dialog is open', async () => {
    vi.mocked(settingsAPI.patchSection).mockRejectedValue(new Error('Server said no.'));
    renderDialog();

    await moveSlider(GAIN_LABEL, 9);
    await fireEvent.click(saveButton());

    expect(await within(dialog()).findByRole('alert')).toBeInTheDocument();
    expect(toastActions.error).not.toHaveBeenCalled();
  });

  it('disables the denoise default with the reason while export denoise is on', () => {
    seedSettings({ playbackDenoise: { enabled: true, strength: 'light' } }, true);
    renderDialog();

    const select = within(dialog()).getByRole('combobox', { name: DENOISE_LABEL });
    expect(select).toBeDisabled();
    expect(select).toHaveAccessibleDescription(PRECEDENCE);
  });

  it('names only gain and high-pass in the Use current values help while export denoise is on', () => {
    seedSettings({ playbackDenoise: { enabled: true, strength: 'light' } }, true);
    renderDialog();

    expect(within(dialog()).getByRole('button', { name: USE_CURRENT })).toHaveAccessibleDescription(
      'components.audioPlayer.defaults.useCurrentHelpNoDenoise'
    );
  });

  it('copies the player values into the draft without saving them', async () => {
    renderDialog();

    await fireEvent.click(within(dialog()).getByRole('button', { name: USE_CURRENT }));

    expect(slider(GAIN_LABEL).value).toBe('7.5');
    expect(slider(HIGH_PASS_LABEL).value).toBe('400');
    expect(within(dialog()).getByRole('combobox', { name: DENOISE_LABEL })).toHaveTextContent(
      'components.audioPlayer.processing.denoiseHeavy'
    );
    expect(settingsAPI.patchSection).not.toHaveBeenCalled();
  });

  it('closes on Cancel without a request', async () => {
    const { onClose } = renderDialog();

    await moveSlider(GAIN_LABEL, 9);
    await fireEvent.click(within(dialog()).getByRole('button', { name: CANCEL }));

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(settingsAPI.patchSection).not.toHaveBeenCalled();
  });

  it('does not close on a backdrop click while the draft has changes', async () => {
    const { onClose } = renderDialog();

    await moveSlider(GAIN_LABEL, 9);
    await fireEvent.click(dialog());

    expect(onClose).not.toHaveBeenCalled();
  });

  it('offers Retry when settings are not loaded, then fills the draft once they load', async () => {
    seedSettings({}, false, false);
    vi.mocked(settingsAPI.load).mockImplementation(async () => {
      const realtime = {
        ...initialStore.originalData.realtime,
        dashboard: {
          ...initialStore.originalData.realtime?.dashboard,
          defaultAudioGain: -3,
          playback: { highPassHz: 250 },
        },
      };
      // A partial settings payload: only what the dialog reads
      return { ...initialStore.originalData, realtime } as unknown as Awaited<
        ReturnType<typeof settingsAPI.load>
      >;
    });
    renderDialog();

    expect(within(dialog()).queryByLabelText(GAIN_LABEL)).toBeNull();
    expect(saveButton()).toHaveAttribute('aria-disabled', 'true');
    expect(saveButton()).toHaveAccessibleDescription(UNAVAILABLE);

    await fireEvent.click(within(dialog()).getByRole('button', { name: RETRY }));

    await waitFor(() => expect(slider(GAIN_LABEL).value).toBe('-3'));
    expect(slider(HIGH_PASS_LABEL).value).toBe('250');
    expect(saveButton()).toHaveAccessibleDescription(NO_CHANGES);
    expect(settingsAPI.patchSection).not.toHaveBeenCalled();
  });
});

describe('PlaybackDefaultsModal Retry', () => {
  const originalLoad = vi.mocked(settingsAPI.load).getMockImplementation();

  beforeEach(() => {
    settingsStore.set(initialStore);
    seedSettings({}, false, false);
  });

  afterEach(() => {
    vi.mocked(settingsAPI.load).mockReset();
    if (originalLoad) vi.mocked(settingsAPI.load).mockImplementation(originalLoad);
    settingsStore.set(initialStore);
  });

  it('keeps the unavailable state and Retry when the load fails again', async () => {
    vi.mocked(settingsAPI.load).mockRejectedValue(new Error('offline'));
    renderDialog();

    await fireEvent.click(within(dialog()).getByRole('button', { name: RETRY }));

    await waitFor(() =>
      expect(within(dialog()).getByRole('status')).toHaveTextContent(UNAVAILABLE)
    );
    expect(within(dialog()).getByRole('button', { name: RETRY })).toBeInTheDocument();
    expect(within(dialog()).queryByLabelText(GAIN_LABEL)).toBeNull();
  });

  it('leaves focus where the user moved it while Retry loads', async () => {
    const pending = deferred<Awaited<ReturnType<typeof settingsAPI.load>>>();
    vi.mocked(settingsAPI.load).mockImplementation(() => pending.promise);
    renderDialog();
    const retry = within(dialog()).getByRole('button', { name: RETRY });
    retry.focus();
    await fireEvent.click(retry);

    const cancel = within(dialog()).getByRole('button', { name: CANCEL });
    cancel.focus();
    const payload: Awaited<ReturnType<typeof settingsAPI.load>> = JSON.parse(
      JSON.stringify(initialStore.originalData)
    );
    pending.resolve(payload);

    await waitFor(() => expect(within(dialog()).getByLabelText(GAIN_LABEL)).toBeInTheDocument());
    // The focus move, when there is one, follows a tick after the fields render
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(cancel).toHaveFocus();
  });

  it('moves focus to the first field when it went with the Retry button', async () => {
    renderDialog();
    const retry = within(dialog()).getByRole('button', { name: RETRY });
    retry.focus();

    await fireEvent.click(retry);

    await waitFor(() =>
      expect(within(dialog()).getByRole('button', { name: USE_CURRENT })).toHaveFocus()
    );
  });
});

describe('PlaybackDefaultsModal Accessibility', () => {
  beforeEach(() => {
    settingsStore.set(initialStore);
    seedSettings({ defaultAudioGain: 0 });
  });

  afterEach(() => settingsStore.set(initialStore));

  it('has no axe violations with the fields shown', async () => {
    renderDialog();
    expect(slider(GAIN_LABEL)).toBeInTheDocument();

    await expectNoA11yViolations(dialog());
  });

  it('has no axe violations while settings are not loaded', async () => {
    seedSettings({}, false, false);
    renderDialog();
    expect(within(dialog()).getByRole('button', { name: RETRY })).toBeInTheDocument();

    await expectNoA11yViolations(dialog());
  });
});

describe('PlaybackDefaultsModal over another Modal', () => {
  const user = userEvent.setup();

  beforeEach(() => {
    settingsStore.set(initialStore);
    seedSettings({ defaultAudioGain: 0 });
  });

  afterEach(() => settingsStore.set(initialStore));

  async function openFromLowerModal(onCloseLower = vi.fn()) {
    renderTyped(StackHost, { props: { onCloseLower } });
    const opener = screen.getByRole('button', { name: 'Open defaults' });
    opener.focus();
    await fireEvent.click(opener);
    const upper = await screen.findByRole('dialog', {
      name: 'components.audioPlayer.defaults.title',
    });
    await waitFor(() => expect(upper.contains(document.activeElement)).toBe(true));
    return { opener, upper, onCloseLower };
  }

  it('is not laid out inside the lower dialog', async () => {
    const { upper } = await openFromLowerModal();

    const lower = screen.getByRole('dialog', { name: 'Lower modal' });
    expect(lower.contains(upper)).toBe(false);
  });

  it('keeps Tab inside itself', async () => {
    const { upper } = await openFromLowerModal();

    for (let i = 0; i < 12; i++) {
      await user.tab();
      expect(upper.contains(document.activeElement)).toBe(true);
    }
  });

  it('closes alone on Escape and returns focus to the opener', async () => {
    const { opener, onCloseLower } = await openFromLowerModal();

    await user.keyboard('{Escape}');

    await waitFor(() => expect(opener).toHaveFocus());
    expect(onCloseLower).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog', { name: 'Lower modal' })).toBeInTheDocument();
  });
});
