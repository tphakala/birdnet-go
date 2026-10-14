import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { tick } from 'svelte';
import SecuritySettingsPage from './SecuritySettingsPage.svelte';
import {
  settingsStore,
  settingsValidationErrors,
  settingsValidationReasons,
  type SecuritySettings,
  type SettingsFormData,
} from '$lib/stores/settings';
import { t } from '$lib/i18n';
import { settingsAPI } from '$lib/utils/settingsApi';
import { navigation } from '$lib/stores/navigation.svelte';

const getConnection = vi.mocked(settingsAPI.security.getConnection);

const TRUSTED_INVALID = 'security-trusted-proxies-invalid';
const TRUSTED_PENDING = 'security-trusted-proxies-pending';
const SUBNETS_INVALID = 'security-bypass-subnets-invalid';
const SUBNETS_PENDING = 'security-bypass-subnets-pending';

// The shared t() mock returns the bare key, which hides the tab and field a reason names.
// Show the parameters so the tests can check them.
const originalT = vi.mocked(t).getMockImplementation();

/** The reason text the page builds for a validation message and its tab and field keys. */
function reason(message: string, tab: string, field: string): string {
  return `${message} ${JSON.stringify({ tab, field })}`;
}

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
    vi.mocked(t).mockImplementation((key: string, params?: Record<string, unknown>) =>
      params ? `${key} ${JSON.stringify(params)}` : key
    );
    settingsValidationErrors.set([]);
    getConnection.mockClear();
    openTab('server');
  });

  afterEach(() => {
    vi.mocked(t).mockReset();
    if (originalT) vi.mocked(t).mockImplementation(originalT);
    settingsStore.set(initialState);
    settingsValidationErrors.set([]);
    settingsValidationReasons.set(new Map());
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
    expect(serverTab()).not.toHaveAttribute('aria-describedby');

    const field = screen.getByRole('textbox', { name: 'settings.security.reverseProxy.listLabel' });
    await fireEvent.input(field, { target: { value: '172.18.0.1' } });
    await fireEvent.keyDown(field, { key: 'Enter' });

    expect(formSecurity().trustedProxies).toEqual(['172.18.0.1']);
    expect(serverTab()).toHaveAccessibleDescription('settings.tabs.hasChanges');
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

  it('keeps typed but not added text across a tab switch and keeps Save blocked', async () => {
    seedSecurity({ trustedProxies: [] });
    render(SecuritySettingsPage);
    const field = () =>
      screen.getByRole<HTMLInputElement>('textbox', {
        name: 'settings.security.reverseProxy.listLabel',
      });
    await fireEvent.input(field(), { target: { value: '10.0.0.1' } });
    await tick();

    openTab('exceptions');
    await tick();
    expect(
      screen.queryByRole('textbox', { name: 'settings.security.reverseProxy.listLabel' })
    ).toBeNull();
    expect(get(settingsValidationErrors)).toContain(TRUSTED_PENDING);

    openTab('server');
    await tick();
    expect(field().value).toBe('10.0.0.1');
    expect(get(settingsValidationErrors)).toContain(TRUSTED_PENDING);
  });

  it('names the tab and field of each blocking entry for the save bar', async () => {
    seedSecurity({
      trustedProxies: ['not-an-address'],
      allowSubnetBypass: { enabled: true, subnet: '10.0.0.1' },
    });
    render(SecuritySettingsPage);
    await tick();

    const reasons = get(settingsValidationReasons);
    expect(reasons.get(TRUSTED_INVALID)).toBe(
      reason(
        'settings.security.validation.invalidEntry',
        'settings.security.serverConfiguration.title',
        'settings.security.reverseProxy.listLabel'
      )
    );
    expect(reasons.get(SUBNETS_INVALID)).toBe(
      reason(
        'settings.security.validation.invalidEntry',
        'settings.security.exceptions.title',
        'settings.security.allowedSubnetsLabel'
      )
    );
  });

  it('names the tab and field of each pending entry for the save bar', async () => {
    seedSecurity({ trustedProxies: [], allowSubnetBypass: { enabled: true, subnet: '' } });
    render(SecuritySettingsPage);
    const proxyField = screen.getByRole('textbox', {
      name: 'settings.security.reverseProxy.listLabel',
    });
    await fireEvent.input(proxyField, { target: { value: '10.0.0.1' } });
    await tick();

    openTab('exceptions');
    await tick();
    const subnetField = screen.getByRole('textbox', {
      name: 'settings.security.allowedSubnetsLabel',
    });
    await fireEvent.input(subnetField, { target: { value: '10.0.0.0/8' } });
    await tick();

    const reasons = get(settingsValidationReasons);
    expect(reasons.get(TRUSTED_PENDING)).toBe(
      reason(
        'settings.security.validation.pendingEntry',
        'settings.security.serverConfiguration.title',
        'settings.security.reverseProxy.listLabel'
      )
    );
    expect(reasons.get(SUBNETS_PENDING)).toBe(
      reason(
        'settings.security.validation.pendingEntry',
        'settings.security.exceptions.title',
        'settings.security.allowedSubnetsLabel'
      )
    );
  });

  it('blocks saving on typed but not added Allowed Subnets text only while the bypass is on', async () => {
    seedSecurity({ allowSubnetBypass: { enabled: true, subnet: '' } });
    openTab('exceptions');
    render(SecuritySettingsPage);
    const field = screen.getByRole('textbox', { name: 'settings.security.allowedSubnetsLabel' });
    await fireEvent.input(field, { target: { value: '10.0.0.0/8' } });
    await tick();
    expect(get(settingsValidationErrors)).toContain(SUBNETS_PENDING);

    editSecurity({ allowSubnetBypass: { enabled: false, subnet: '' } });
    await tick();
    expect(get(settingsValidationErrors)).not.toContain(SUBNETS_PENDING);

    editSecurity({ allowSubnetBypass: { enabled: true, subnet: '' } });
    await tick();
    expect(get(settingsValidationErrors)).toContain(SUBNETS_PENDING);
  });

  it('clears the drafts on Reset', async () => {
    seedSecurity({ trustedProxies: [] });
    render(SecuritySettingsPage);
    const field = screen.getByRole<HTMLInputElement>('textbox', {
      name: 'settings.security.reverseProxy.listLabel',
    });
    await fireEvent.input(field, { target: { value: '10.0.0.1' } });
    // Reset appears only with an unsaved change
    editSecurity({ trustedProxies: ['172.18.0.1'] });
    await tick();

    await fireEvent.click(screen.getByRole('button', { name: 'settings.actions.resetAriaLabel' }));
    await tick();

    expect(field.value).toBe('');
    expect(get(settingsValidationErrors)).not.toContain(TRUSTED_PENDING);
  });

  it('clears the add error on Reset', async () => {
    seedSecurity({ trustedProxies: [] });
    render(SecuritySettingsPage);
    const field = screen.getByRole<HTMLInputElement>('textbox', {
      name: 'settings.security.reverseProxy.listLabel',
    });
    // The valid token is added (which makes Reset visible); `abc` stays with an error
    await fireEvent.input(field, { target: { value: '10.0.0.1, abc' } });
    await fireEvent.keyDown(field, { key: 'Enter' });
    await tick();
    expect(field).toHaveAttribute('aria-invalid', 'true');
    expect(field.value).toBe('abc');
    expect(screen.getByText(/errors\.invalid/)).toBeInTheDocument();

    await fireEvent.click(screen.getByRole('button', { name: 'settings.actions.resetAriaLabel' }));
    await tick();

    expect(field.value).toBe('');
    expect(field).not.toHaveAttribute('aria-invalid', 'true');
    expect(screen.queryByText(/errors\.invalid/)).not.toBeInTheDocument();
  });

  it.each<{ name: string; label: string; tab: string | null; seed: () => void }>([
    {
      name: 'trusted proxies',
      label: 'settings.security.reverseProxy.listLabel',
      tab: null,
      seed: () => seedSecurity({ trustedProxies: ['10.0.0.1'] }),
    },
    {
      name: 'allowed subnets',
      label: 'settings.security.allowedSubnetsLabel',
      tab: 'exceptions',
      seed: () => seedSecurity({ allowSubnetBypass: { enabled: true, subnet: '10.0.0.1/32' } }),
    },
  ])('clears a duplicate-only add report on Reset: $name', async ({ label, tab, seed }) => {
    seed();
    if (tab) openTab(tab);
    render(SecuritySettingsPage);
    const field = screen.getByRole<HTMLInputElement>('textbox', { name: label });
    await fireEvent.input(field, {
      target: { value: label.includes('Subnets') ? '10.0.0.1/32' : '10.0.0.1' },
    });
    await fireEvent.keyDown(field, { key: 'Enter' });
    await tick();
    expect(screen.getByText(/errors\.duplicate/)).toBeInTheDocument();
    // Reset appears only with an unsaved change elsewhere
    editSecurity({ host: 'changed.example' });
    await tick();

    await fireEvent.click(screen.getByRole('button', { name: 'settings.actions.resetAriaLabel' }));
    await tick();

    expect(screen.queryByText(/errors\.duplicate/)).not.toBeInTheDocument();
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
