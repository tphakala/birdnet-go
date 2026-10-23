import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';

// The login screen and the redirect for visitors who are already allowed are decided in App.
// The app layout and the dashboard are stubbed: only the routing and the login branch are
// under test.
vi.mock('./lib/desktop/layouts/RootLayout.svelte', () => ({ default: () => {} }));
vi.mock('./lib/desktop/features/dashboard/pages/DashboardPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/wizard/WizardDialog.svelte', () => ({ default: () => {} }));

// LoginModal logs through loggers.auth, which this test environment does not provide.
vi.mock('./lib/utils/logger', async importOriginal => {
  const original = await importOriginal<typeof import('./lib/utils/logger')>();
  const quiet = { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() };
  return { ...original, loggers: { ...original.loggers, auth: quiet } };
});

const security = vi.hoisted(() => ({
  value: {
    enabled: true,
    accessAllowed: false,
    privateMode: false,
    authConfig: { basicEnabled: true, enabledProviders: [] as string[] },
  },
}));

vi.mock('./lib/stores/appState.svelte', async importOriginal => {
  const original = await importOriginal<typeof import('./lib/stores/appState.svelte')>();
  return {
    ...original,
    initApp: vi.fn(async () => {
      Object.assign(original.appState.security, security.value);
      original.appState.initialized = true;
      original.appState.loading = false;
      return true;
    }),
  };
});

import App from './App.svelte';
import { navigation } from './lib/stores/navigation.svelte';

const LOGIN_DIALOG = 'dialog';

function openLoginRoute(search: string) {
  navigation.redirect(`/ui/login${search}`);
}

describe('App login route', () => {
  beforeEach(() => {
    security.value = {
      enabled: true,
      accessAllowed: false,
      privateMode: false,
      authConfig: { basicEnabled: true, enabledProviders: [] },
    };
  });

  afterEach(() => {
    navigation.redirect('/ui/dashboard');
  });

  it('shows the login form to a visitor who is not allowed in', async () => {
    openLoginRoute('?redirect=%2Fui%2Fsettings%2Fsecurity');
    render(App);

    expect(await screen.findByRole(LOGIN_DIALOG)).toBeInTheDocument();
    expect(navigation.currentPath).toBe('/ui/login');
  });

  it('sends a visitor who is already allowed to the requested page', async () => {
    security.value = { ...security.value, accessAllowed: true };
    openLoginRoute('?redirect=%2Fui%2Fsettings%2Faudio');
    render(App);

    await waitFor(() => expect(navigation.currentPath).toBe('/ui/settings/audio'));
    expect(screen.queryByRole(LOGIN_DIALOG)).toBeNull();
  });

  it('sends a visitor to the dashboard when security is off and the redirect is off-site', async () => {
    security.value = { ...security.value, enabled: false, accessAllowed: true };
    openLoginRoute('?redirect=https%3A%2F%2Fexample.com');
    render(App);

    await waitFor(() => expect(navigation.currentPath).toBe('/ui/dashboard'));
  });

  it('closing the login form goes to the dashboard', async () => {
    openLoginRoute('?redirect=%2Fui%2Fsettings%2Fsecurity');
    render(App);
    await screen.findByRole(LOGIN_DIALOG);

    await fireEvent.click(screen.getByRole('button', { name: /close login dialog/i }));
    await tick();

    await waitFor(() => expect(navigation.currentPath).toBe('/ui/dashboard'));
  });

  it('keeps the form open when closing it in private mode', async () => {
    security.value = { ...security.value, privateMode: true };
    openLoginRoute('?redirect=%2Fui%2Fsettings%2Fsecurity');
    render(App);
    await screen.findByRole(LOGIN_DIALOG);

    await fireEvent.click(screen.getByRole('button', { name: /close login dialog/i }));
    await tick();

    expect(screen.getByRole(LOGIN_DIALOG)).toBeInTheDocument();
    expect(navigation.currentPath).toBe('/ui/login');
  });

  it('posts the validated redirect target when logging in', async () => {
    const apiModule = await import('./lib/utils/api');
    const post = vi
      .spyOn(apiModule.api, 'post')
      .mockResolvedValue({ success: true, message: 'ok' });
    openLoginRoute('?redirect=%2Fui%2Fsettings%2Fsecurity%3Ftab%3Dserver');
    render(App);
    await screen.findByRole(LOGIN_DIALOG);

    await fireEvent.input(screen.getByLabelText('auth.password'), {
      target: { value: 'valid-password' },
    });
    await fireEvent.click(screen.getByRole('button', { name: /continue with password/i }));

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/api/v2/auth/login',
        expect.objectContaining({ redirectUrl: '/settings/security?tab=server' })
      )
    );
  });
});
