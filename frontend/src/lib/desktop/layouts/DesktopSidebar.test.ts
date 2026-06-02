import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  createComponentTestFactory,
  fireEvent,
  screen,
  waitFor,
} from '../../../test/render-helpers';
import DesktopSidebar from './DesktopSidebar.svelte';

// Translation mock: return the key so aria-labels are stable and queryable.
vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

describe('DesktopSidebar - post-login redirect wiring (#3306)', () => {
  const sidebarTest = createComponentTestFactory(DesktopSidebar);
  const originalLocationDescriptor = Object.getOwnPropertyDescriptor(window, 'location');

  function setLocation(pathname: string, search: string) {
    Object.defineProperty(window, 'location', {
      configurable: true,
      writable: true,
      value: { href: '', pathname, search, reload: vi.fn() },
    });
  }

  beforeEach(() => {
    vi.clearAllMocks();
    // The LoginModal focus trap reads layout/styles; stub them for jsdom.
    Object.defineProperty(window, 'getComputedStyle', {
      value: vi.fn(() => ({
        getPropertyValue: vi.fn(() => ''),
        visibility: 'visible',
        display: 'block',
      })),
      writable: true,
    });
    Object.defineProperty(HTMLElement.prototype, 'focus', { value: vi.fn(), writable: true });
  });

  afterEach(() => {
    if (originalLocationDescriptor) {
      Object.defineProperty(window, 'location', originalLocationDescriptor);
    }
  });

  it('passes the full path + query string of the current view to LoginModal on login click', async () => {
    setLocation(
      '/ui/detections',
      '?queryType=species&species=Phoenicurus+phoenicurus&date=2026-06-02'
    );

    sidebarTest.render({
      securityEnabled: true,
      accessAllowed: false, // not logged in -> the login button is shown
      authConfig: { basicEnabled: true, enabledProviders: [] },
    });

    // Open the login modal via the login button.
    const loginButton = screen.getByRole('button', { name: 'auth.openLoginModal' });
    await fireEvent.click(loginButton);

    // LoginModal renders a hidden <input name="redirect"> bound to the redirect target.
    // It must carry the FULL current URL, query string included (the #3306 fix).
    await waitFor(() => {
      const redirectInput = screen.getByDisplayValue(
        '/ui/detections?queryType=species&species=Phoenicurus+phoenicurus&date=2026-06-02'
      ) as HTMLInputElement;
      expect(redirectInput.name).toBe('redirect');
    });
  });

  it('passes only the path when the current view has no query string', async () => {
    setLocation('/ui/dashboard', '');

    sidebarTest.render({
      securityEnabled: true,
      accessAllowed: false,
      authConfig: { basicEnabled: true, enabledProviders: [] },
    });

    const loginButton = screen.getByRole('button', { name: 'auth.openLoginModal' });
    await fireEvent.click(loginButton);

    await waitFor(() => {
      const redirectInput = screen.getByDisplayValue('/ui/dashboard') as HTMLInputElement;
      expect(redirectInput.name).toBe('redirect');
    });
  });
});
