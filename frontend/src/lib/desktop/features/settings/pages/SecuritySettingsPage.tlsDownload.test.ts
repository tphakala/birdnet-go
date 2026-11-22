import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import { get } from 'svelte/store';
import SecuritySettingsPage from './SecuritySettingsPage.svelte';
import { settingsStore, type SettingsFormData } from '$lib/stores/settings';
import { settingsAPI, type TLSCertificateInfo } from '$lib/utils/settingsApi';
import { navigation } from '$lib/stores/navigation.svelte';
import { resetBasePath, setBasePath } from '$lib/utils/urlHelpers';

const INSTALLED_CERTIFICATE: TLSCertificateInfo = {
  installed: true,
  mode: 'selfsigned',
  subject: 'CN=birdnet-go',
  issuer: 'CN=birdnet-go',
  notBefore: '2026-01-01T00:00:00Z',
  notAfter: '2031-01-01T00:00:00Z',
  daysUntilExpiry: 1500,
};

/** Seed a self-signed TLS mode in both the saved and the form data. */
function seedSelfSignedTls() {
  settingsStore.update(state => {
    const security = state.formData.security;
    if (!security) throw new Error('store has no security section');
    const formData: SettingsFormData = {
      ...state.formData,
      security: { ...security, tlsMode: 'selfsigned' },
    };
    return { ...state, formData, originalData: formData, dataLoaded: true };
  });
}

describe('SecuritySettingsPage TLS certificate download', () => {
  const initialState = get(settingsStore);
  // The shared settingsAPI mock has no tls section; stub the certificate load the page runs on mount.
  const originalTls = Reflect.get(settingsAPI, 'tls');

  beforeEach(() => {
    Object.assign(settingsAPI, {
      tls: { getCertificate: vi.fn().mockResolvedValue(INSTALLED_CERTIFICATE) },
    });
    navigation.redirect('/ui/settings/security?tab=server');
    seedSelfSignedTls();
  });

  afterEach(() => {
    Object.assign(settingsAPI, { tls: originalTls });
    settingsStore.set(initialState);
    navigation.redirect('/ui/dashboard');
    resetBasePath();
  });

  it('links the certificate download under the reverse proxy prefix', async () => {
    // BirdNET-Go served by a proxy at /birdnet, which does not rewrite response bodies.
    setBasePath('/birdnet');
    render(SecuritySettingsPage);

    const link = await screen.findByRole('link', { name: 'components.tls.downloadCertificate' });
    expect(link).toHaveAttribute('href', '/birdnet/api/v2/tls/certificate/download');
  });
});
