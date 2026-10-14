import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { tick } from 'svelte';
import SecuritySettingsPage from './SecuritySettingsPage.svelte';
import {
  settingsStore,
  settingsValidationErrors,
  type SecuritySettings,
  type SettingsFormData,
} from '$lib/stores/settings';
import { settingsAPI } from '$lib/utils/settingsApi';
import { navigation } from '$lib/stores/navigation.svelte';

const getConnection = vi.mocked(settingsAPI.security.getConnection);

const TRUSTED_INVALID = 'security-trusted-proxies-invalid';
const TRUSTED_PENDING = 'security-trusted-proxies-pending';
const SUBNETS_INVALID = 'security-bypass-subnets-invalid';

/** The store's security section with a patch applied; the store always holds one. */
function patched(
  base: SecuritySettings | undefined,
  patch: Partial<SecuritySettings>
): SecuritySettings {
  if (!base) throw new Error('store has no security section');
  return { ...base, ...patch };
}

/** The current form's security section. */
function formSecurity(): SecuritySettings {
  return patched(get(settingsStore).formData.security, {});
}

/** Seed the security section of both the saved and the form data. */
function seedSecurity(security: Partial<SecuritySettings>) {
  settingsStore.update(state => {
    const formData: SettingsFormData = {
      ...state.formData,
      security: patched(state.formData.security, security),
    };
    return { ...state, formData, originalData: formData, dataLoaded: true };
  });
}

/** Change only the form data, as an edit would. */
function editSecurity(security: Partial<SecuritySettings>) {
  settingsStore.update(state => ({
    ...state,
    formData: { ...state.formData, security: patched(state.formData.security, security) },
  }));
}

/** Change the saved data, as a successful save does. */
function saveSecurity(security: Partial<SecuritySettings>) {
  settingsStore.update(state => ({
    ...state,
    originalData: {
      ...state.originalData,
      security: patched(state.originalData.security, security),
    },
  }));
}

function openTab(tab: string) {
  navigation.redirect(`/ui/settings/security?tab=${tab}`);
}

function serverTab(): HTMLElement {
  return screen.getByRole('tab', { name: 'settings.security.serverConfiguration.title' });
}

function addressRows(listName: string): string[] {
  return within(screen.getByRole('list', { name: listName }))
    .getAllByRole('listitem')
    .map(row => row.querySelector('.font-mono')?.textContent ?? '');
}

describe('SecuritySettingsPage reverse proxy wiring', () => {
  const initialState = get(settingsStore);

  beforeEach(() => {
    settingsValidationErrors.set([]);
    getConnection.mockClear();
    openTab('server');
  });

  afterEach(() => {
    settingsStore.set(initialState);
    settingsValidationErrors.set([]);
  });

  it('shows the trusted proxies without the Cloudflare token and checks the preset', () => {
    seedSecurity({ trustedProxies: ['172.18.0.1', 'CLOUDFLARE', '10.0.0.0/8'] });
    render(SecuritySettingsPage);

    expect(addressRows('settings.security.reverseProxy.listLabel')).toEqual([
      '172.18.0.1',
      '10.0.0.0/8',
    ]);
    expect(
      screen.getByRole<HTMLInputElement>('checkbox', {
        name: 'settings.security.reverseProxy.cloudflareLabel',
      }).checked
    ).toBe(true);
  });

  it('marks the Server tab for a list change without the TLS restart note', async () => {
    seedSecurity({ trustedProxies: [] });
    render(SecuritySettingsPage);
    expect(within(serverTab()).queryByRole('status')).toBeNull();

    const field = screen.getByRole('textbox', { name: 'settings.security.reverseProxy.listLabel' });
    await fireEvent.input(field, { target: { value: '172.18.0.1' } });
    await fireEvent.keyDown(field, { key: 'Enter' });

    expect(formSecurity().trustedProxies).toEqual(['172.18.0.1']);
    expect(within(serverTab()).getByRole('status')).toBeInTheDocument();
    expect(screen.queryByText('settings.security.tls.restartRequired')).toBeNull();
  });

  it('checks the connection once after load and again after the saved list changes', async () => {
    seedSecurity({ trustedProxies: [] });
    render(SecuritySettingsPage);
    await waitFor(() => expect(getConnection).toHaveBeenCalledTimes(1));

    editSecurity({ trustedProxies: ['172.18.0.1'] });
    await tick();
    expect(getConnection).toHaveBeenCalledTimes(1);

    saveSecurity({ trustedProxies: ['172.18.0.1'] });
    await waitFor(() => expect(getConnection).toHaveBeenCalledTimes(2));
  });

  it('blocks saving while a trusted proxy row is invalid or text is pending', async () => {
    seedSecurity({ trustedProxies: ['not-an-address'] });
    render(SecuritySettingsPage);
    await tick();
    expect(get(settingsValidationErrors)).toContain(TRUSTED_INVALID);

    await fireEvent.click(screen.getByRole('button', { name: /not-an-address/ }));
    await tick();
    expect(get(settingsValidationErrors)).not.toContain(TRUSTED_INVALID);

    const field = screen.getByRole('textbox', { name: 'settings.security.reverseProxy.listLabel' });
    await fireEvent.input(field, { target: { value: '10.0.0.1' } });
    await tick();
    expect(get(settingsValidationErrors)).toContain(TRUSTED_PENDING);
  });

  it('ignores blank saved entries for display and validation', async () => {
    seedSecurity({ trustedProxies: ['', '172.18.0.1', '  '] });
    render(SecuritySettingsPage);
    await tick();

    expect(addressRows('settings.security.reverseProxy.listLabel')).toEqual(['172.18.0.1']);
    expect(get(settingsValidationErrors)).toEqual([]);
  });

  it('edits the bypass subnets as rows and writes a comma-separated string back', async () => {
    seedSecurity({
      allowSubnetBypass: { enabled: true, subnet: '10.0.0.0/8,192.168.10.0/24, ' },
    });
    openTab('exceptions');
    render(SecuritySettingsPage);

    expect(addressRows('settings.security.allowedSubnetsLabel')).toEqual([
      '10.0.0.0/8',
      '192.168.10.0/24',
    ]);

    const field = screen.getByRole('textbox', { name: 'settings.security.allowedSubnetsLabel' });
    await fireEvent.input(field, { target: { value: '172.16.0.0/12' } });
    await fireEvent.keyDown(field, { key: 'Enter' });
    expect(formSecurity().allowSubnetBypass.subnet).toBe(
      '10.0.0.0/8, 192.168.10.0/24, 172.16.0.0/12'
    );

    await fireEvent.click(screen.getByRole('button', { name: /172\.16\.0\.0\/12/ }));
    expect(formSecurity().allowSubnetBypass.subnet).toBe('10.0.0.0/8,192.168.10.0/24, ');
  });

  it('validates the bypass subnets only while the bypass is on', async () => {
    seedSecurity({ allowSubnetBypass: { enabled: false, subnet: '10.0.0.1' } });
    openTab('exceptions');
    render(SecuritySettingsPage);
    await tick();
    expect(get(settingsValidationErrors)).not.toContain(SUBNETS_INVALID);

    editSecurity({ allowSubnetBypass: { enabled: true, subnet: '10.0.0.1' } });
    await tick();
    expect(get(settingsValidationErrors)).toContain(SUBNETS_INVALID);
  });

  it('links the bypass note to the Reverse Proxy card, usable while the bypass is off', () => {
    seedSecurity({ allowSubnetBypass: { enabled: false, subnet: '' } });
    openTab('exceptions');
    render(SecuritySettingsPage);

    const link = screen.getByRole('link', { name: 'settings.security.bypassProxyLink' });
    expect(link.getAttribute('href')).toMatch(
      /\/ui\/settings\/security\?tab=server#reverse-proxy$/
    );
    expect(link.closest('fieldset')).toBeNull();
  });

  it('removes its validation errors when the page unmounts', async () => {
    seedSecurity({ trustedProxies: ['not-an-address'] });
    const { unmount } = render(SecuritySettingsPage);
    await tick();
    expect(get(settingsValidationErrors)).toContain(TRUSTED_INVALID);

    unmount();

    expect(get(settingsValidationErrors)).toEqual([]);
  });
});
