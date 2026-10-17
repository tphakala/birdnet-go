import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { tick } from 'svelte';
import {
  settingsActions,
  settingsStore,
  settingsValidationErrors,
  settingsValidationReasons,
} from '$lib/stores/settings';
import SettingsPageActions from './SettingsPageActions.svelte';

const initialState = get(settingsStore);

/** Make the form differ from the saved data, so there is something to save. */
function makeUnsavedChange() {
  settingsStore.update(state => ({
    ...state,
    formData: { ...state.formData, main: { ...state.formData.main, name: 'changed' } },
  }));
}

function saveButton(): HTMLElement {
  return screen.getByRole('button', { name: 'settings.actions.saveAriaLabel' });
}

describe('SettingsPageActions save blocking', () => {
  beforeEach(() => {
    settingsValidationErrors.set([]);
    settingsValidationReasons.set(new Map());
  });

  afterEach(() => {
    settingsStore.set(initialState);
    settingsValidationErrors.set([]);
    settingsValidationReasons.set(new Map());
    vi.restoreAllMocks();
  });

  it('keeps Save focusable and explains why it is blocked, listing page reasons', async () => {
    const save = vi.spyOn(settingsActions, 'saveSettings').mockResolvedValue(undefined);
    makeUnsavedChange();
    settingsValidationErrors.set(['a-key', 'b-key']);
    settingsValidationReasons.set(new Map([['a-key', 'Exceptions: Allowed Subnets is invalid']]));
    render(SettingsPageActions);

    const button = saveButton();
    expect(button).not.toBeDisabled();
    expect(button).toHaveAttribute('aria-disabled', 'true');
    const reasonId = button.getAttribute('aria-describedby');
    expect(reasonId).toBeTruthy();
    const reason = document.getElementById(reasonId ?? '');
    expect(reason).toHaveTextContent('settings.actions.validationBlocked');
    expect(reason).toHaveTextContent('Exceptions: Allowed Subnets is invalid');

    await fireEvent.click(button);
    expect(save).not.toHaveBeenCalled();
  });

  it('shows the generic line when no reason was registered', () => {
    makeUnsavedChange();
    settingsValidationErrors.set(['ebird-api-key-required']);
    render(SettingsPageActions);

    expect(screen.getByText('settings.actions.validationBlocked')).toBeInTheDocument();
    expect(screen.queryByRole('listitem')).toBeNull();
  });

  it('shows no reason and saves when nothing blocks', async () => {
    const save = vi.spyOn(settingsActions, 'saveSettings').mockResolvedValue(undefined);
    makeUnsavedChange();
    render(SettingsPageActions);

    expect(screen.queryByText('settings.actions.validationBlocked')).toBeNull();
    expect(saveButton()).not.toHaveAttribute('aria-disabled');
    await fireEvent.click(saveButton());
    expect(save).toHaveBeenCalledTimes(1);
  });

  it('describes why Save is unavailable when there is nothing to save', () => {
    render(SettingsPageActions);

    const button = saveButton();
    expect(button).toHaveAttribute('aria-disabled', 'true');
    const reasonId = button.getAttribute('aria-describedby');
    expect(reasonId).toBeTruthy();
    expect(document.getElementById(reasonId ?? '')).toHaveTextContent(
      'settings.actions.noChangesToSave'
    );
  });

  it('does not save again while a save is already running', async () => {
    const save = vi.spyOn(settingsActions, 'saveSettings').mockResolvedValue(undefined);
    makeUnsavedChange();
    settingsStore.update(state => ({ ...state, isSaving: true }));
    render(SettingsPageActions);

    const button = screen.getByRole('button', { name: 'settings.actions.savingAriaLabel' });
    expect(button).toHaveAttribute('aria-disabled', 'true');
    await fireEvent.click(button);

    expect(save).not.toHaveBeenCalled();
  });

  it('shows no validation-blocked reason when there is nothing to save', () => {
    settingsValidationErrors.set(['a-key']);
    render(SettingsPageActions);

    expect(screen.queryByText('settings.actions.validationBlocked')).toBeNull();
    expect(saveButton()).toHaveAttribute('aria-disabled', 'true');
  });
});

describe('SettingsPageActions reset', () => {
  afterEach(() => {
    settingsStore.set(initialState);
    vi.restoreAllMocks();
  });

  it('moves focus to Save after Reset removes the Reset button', async () => {
    makeUnsavedChange();
    render(SettingsPageActions);
    const reset = screen.getByRole('button', { name: 'settings.actions.resetAriaLabel' });
    reset.focus();
    expect(document.activeElement).toBe(reset);

    await fireEvent.click(reset);
    await tick();

    expect(screen.queryByRole('button', { name: 'settings.actions.resetAriaLabel' })).toBeNull();
    expect(document.activeElement).toBe(saveButton());
    expect(saveButton()).toHaveAttribute('aria-disabled', 'true');
  });

  it('calls onReset once', async () => {
    const onReset = vi.fn();
    makeUnsavedChange();
    render(SettingsPageActions, { props: { onReset } });

    await fireEvent.click(screen.getByRole('button', { name: 'settings.actions.resetAriaLabel' }));
    await tick();

    expect(onReset).toHaveBeenCalledTimes(1);
  });
});
