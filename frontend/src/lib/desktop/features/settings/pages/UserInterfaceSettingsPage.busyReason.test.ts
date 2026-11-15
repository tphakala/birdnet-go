import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { screen, cleanup } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { renderTyped } from '../../../../../test/render-helpers';
import { settingsStore } from '$lib/stores/settings';
import UserInterfaceSettingsPage from './UserInterfaceSettingsPage.svelte';

// Tests run without translations, so the text shown is the key.
const LOADING_REASON = 'settings.loading';
const SAVING_REASON = 'settings.actions.saving';
const BLUE_SCHEME_NAME = 'settings.appearance.schemeBlue';

/** Put the real settings store in a loading or saving state before the page renders. */
function seedBusyState(flags: { isLoading: boolean; isSaving: boolean }) {
  settingsStore.update(store => ({ ...store, ...flags, dataLoaded: true }));
}

describe('UserInterfaceSettingsPage color scheme picker', () => {
  const initial = get(settingsStore);

  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({ providers: [] }) })
    );
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    settingsStore.set(initial);
  });

  it('is not blocked while Settings is idle', () => {
    seedBusyState({ isLoading: false, isSaving: false });
    renderTyped(UserInterfaceSettingsPage);

    const blue = screen.getByRole('radio', { name: BLUE_SCHEME_NAME });
    expect(blue).not.toHaveAttribute('aria-disabled');
    expect(blue).not.toHaveAccessibleDescription();
  });

  it('blocks the picker with the saving reason while Settings saves', () => {
    seedBusyState({ isLoading: false, isSaving: true });
    renderTyped(UserInterfaceSettingsPage);

    const blue = screen.getByRole('radio', { name: BLUE_SCHEME_NAME });
    expect(blue).toHaveAttribute('aria-disabled', 'true');
    expect(blue).toHaveAccessibleDescription(SAVING_REASON);
  });

  it('blocks the picker with the loading reason while Settings loads', () => {
    seedBusyState({ isLoading: true, isSaving: false });
    renderTyped(UserInterfaceSettingsPage);

    const blue = screen.getByRole('radio', { name: BLUE_SCHEME_NAME });
    expect(blue).toHaveAttribute('aria-disabled', 'true');
    expect(blue).toHaveAccessibleDescription(LOADING_REASON);
  });

  it('gives the loading reason when Settings is both loading and saving', () => {
    seedBusyState({ isLoading: true, isSaving: true });
    renderTyped(UserInterfaceSettingsPage);

    const blue = screen.getByRole('radio', { name: BLUE_SCHEME_NAME });
    expect(blue).toHaveAttribute('aria-disabled', 'true');
    expect(blue).toHaveAccessibleDescription(LOADING_REASON);
  });
});
