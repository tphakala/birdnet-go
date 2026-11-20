import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { get } from 'svelte/store';
import SecuritySettingsPage from './SecuritySettingsPage.svelte';
import {
  settingsStore,
  settingsValidationErrors,
  settingsValidationReasons,
} from '$lib/stores/settings';
import { navigation } from '$lib/stores/navigation.svelte';

describe('SecuritySettingsPage terminal tab', () => {
  const initialState = get(settingsStore);
  const ENABLE_LABEL = 'settings.security.terminal.enableLabel';

  beforeEach(() => {
    settingsValidationErrors.set([]);
    settingsStore.update(state => ({ ...state, dataLoaded: true }));
    navigation.redirect('/ui/settings/security?tab=terminal');
  });

  afterEach(() => {
    settingsStore.set(initialState);
    settingsValidationErrors.set([]);
    settingsValidationReasons.set(new Map());
  });

  it('stays off and does not turn the terminal on when the confirmation is refused', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false);
    try {
      render(SecuritySettingsPage);

      const toggle = screen.getByRole<HTMLInputElement>('switch', { name: ENABLE_LABEL });
      expect(toggle.checked).toBe(false);

      await userEvent.click(toggle);

      expect(confirmSpy).toHaveBeenCalledWith('settings.security.terminal.confirmEnable');
      await waitFor(() => expect(toggle.checked).toBe(false));
      expect(get(settingsStore).formData.webServer?.enableTerminal ?? false).toBe(false);
    } finally {
      confirmSpy.mockRestore();
    }
  });

  it('stages the switch on in the form when the confirmation is accepted', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
    try {
      render(SecuritySettingsPage);

      const toggle = screen.getByRole<HTMLInputElement>('switch', { name: ENABLE_LABEL });
      await userEvent.click(toggle);

      expect(confirmSpy).toHaveBeenCalledWith('settings.security.terminal.confirmEnable');
      await waitFor(() => expect(toggle.checked).toBe(true));
      expect(get(settingsStore).formData.webServer?.enableTerminal).toBe(true);
    } finally {
      confirmSpy.mockRestore();
    }
  });
});
