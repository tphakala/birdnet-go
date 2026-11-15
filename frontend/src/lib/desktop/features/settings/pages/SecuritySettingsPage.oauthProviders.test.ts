import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
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

  describe('post-logout redirect toggle', () => {
    const POST_LOGOUT_LABEL = 'settings.security.oauth.oidc.postLogoutRedirectLabel';
    const BASE_URL = 'https://birdnet.example.test';

    function seedProvider(provider: 'oidc' | 'google') {
      seedSecurity({
        baseUrl: BASE_URL,
        oauthProviders: [
          {
            provider,
            enabled: true,
            clientId: 'client',
            clientSecret: 'secret',
            userId: 'someone',
            ...(provider === 'oidc' && { issuerUrl: 'https://idp.example.test' }),
          },
        ],
      });
    }

    async function openEditForm() {
      render(SecuritySettingsPage);
      await userEvent.click(
        screen.getByRole('button', { name: 'settings.security.oauth.providers.editButton' })
      );
    }

    it('round-trips through edit and save for an OIDC provider', async () => {
      seedProvider('oidc');
      await openEditForm();

      const toggle = screen.getByRole<HTMLInputElement>('checkbox', { name: POST_LOGOUT_LABEL });
      expect(toggle.checked).toBe(false);
      await userEvent.click(toggle);
      await userEvent.click(
        screen.getByRole('button', { name: 'settings.security.oauth.form.saveButton' })
      );

      expect(get(settingsStore).formData.security?.oauthProviders?.[0]?.postLogoutRedirect).toBe(
        true
      );

      await userEvent.click(
        screen.getByRole('button', { name: 'settings.security.oauth.providers.editButton' })
      );
      expect(
        screen.getByRole<HTMLInputElement>('checkbox', { name: POST_LOGOUT_LABEL }).checked
      ).toBe(true);
    });

    it('is not offered for other providers', async () => {
      seedProvider('google');
      await openEditForm();

      expect(screen.queryByRole('checkbox', { name: POST_LOGOUT_LABEL })).toBeNull();
    });
  });
});
