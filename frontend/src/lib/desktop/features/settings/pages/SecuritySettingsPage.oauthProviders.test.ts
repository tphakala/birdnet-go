import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import { get } from 'svelte/store';
import SecuritySettingsPage from './SecuritySettingsPage.svelte';
import {
  settingsStore,
  settingsValidationErrors,
  settingsValidationReasons,
  type SecuritySettings,
  type SettingsFormData,
} from '$lib/stores/settings';
import { navigation } from '$lib/stores/navigation.svelte';

/** Seed the security section of both the saved and the form data. */
function seedSecurity(security: Partial<SecuritySettings>) {
  settingsStore.update(state => {
    const base = state.formData.security;
    if (!base) throw new Error('store has no security section');
    const formData: SettingsFormData = { ...state.formData, security: { ...base, ...security } };
    return { ...state, formData, originalData: formData, dataLoaded: true };
  });
}

describe('SecuritySettingsPage OAuth provider list', () => {
  const initialState = get(settingsStore);

  beforeEach(() => {
    settingsValidationErrors.set([]);
    navigation.redirect('/ui/settings/security?tab=oauth');
  });

  afterEach(() => {
    settingsStore.set(initialState);
    settingsValidationErrors.set([]);
    settingsValidationReasons.set(new Map());
  });

  it('renders an enabled and a disabled entry that share a provider ID', () => {
    seedSecurity({
      oauthProviders: [
        { provider: 'google', enabled: false, clientId: 'leftover', clientSecret: 'a' },
        { provider: 'google', enabled: true, clientId: 'live', clientSecret: 'b' },
      ],
    });

    render(SecuritySettingsPage);

    const toggles = screen.getAllByRole<HTMLInputElement>('checkbox', {
      name: 'settings.security.oauth.providers.enableToggle',
    });
    expect(toggles.map(toggle => toggle.checked)).toEqual([false, true]);
  });
});
