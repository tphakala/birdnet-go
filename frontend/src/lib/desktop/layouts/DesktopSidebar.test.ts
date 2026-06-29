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

describe('DesktopSidebar - analytics submenu', () => {
  const sidebarTest = createComponentTestFactory(DesktopSidebar);

  beforeEach(() => {
    vi.clearAllMocks();
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

  it('surfaces all six analytics views in the submenu when the analytics route is active', async () => {
    // Rendering with an analytics sub-route causes the $effect to set analyticsExpanded=true,
    // which makes CollapsibleNavSection render the item buttons in the DOM.
    sidebarTest.render({ currentRoute: '/ui/analytics/summary' });

    // Wait for the $effect to fire and the submenu items to appear.
    await waitFor(() => {
      // Each item renders as a button with text content equal to its i18n key
      // (the mock returns the key verbatim).
      expect(screen.getByText('analytics.hub.tabs.summary')).toBeTruthy();
    });

    expect(screen.getByText('analytics.species.title')).toBeTruthy();
    expect(screen.getByText('analytics.hub.tabs.patterns')).toBeTruthy();
    expect(screen.getByText('analytics.hub.tabs.trends')).toBeTruthy();
    expect(screen.getByText('analytics.hub.tabs.biodiversity')).toBeTruthy();
    expect(screen.getByText('analytics.hub.tabs.quality')).toBeTruthy();

    // Each item renders as a button (onclick calls the internal navigate handler).
    const summaryBtn = screen.getByText('analytics.hub.tabs.summary').closest('button');
    const speciesBtn = screen.getByText('analytics.species.title').closest('button');
    const activityBtn = screen.getByText('analytics.hub.tabs.patterns').closest('button');
    const trendsBtn = screen.getByText('analytics.hub.tabs.trends').closest('button');
    const biodiversityBtn = screen.getByText('analytics.hub.tabs.biodiversity').closest('button');
    const reviewBtn = screen.getByText('analytics.hub.tabs.quality').closest('button');

    expect(summaryBtn).toBeTruthy();
    expect(speciesBtn).toBeTruthy();
    expect(activityBtn).toBeTruthy();
    expect(trendsBtn).toBeTruthy();
    expect(biodiversityBtn).toBeTruthy();
    expect(reviewBtn).toBeTruthy();
  });

  it('each analytics submenu button routes to its correct URL via the onNavigate prop', async () => {
    // With onNavigate provided, navigationUrls uses the short form without the /ui/ prefix.
    const onNavigate = vi.fn();
    sidebarTest.render({ currentRoute: '/ui/analytics/summary', onNavigate });

    // Wait for the submenu to expand (the $effect fires on route match).
    await waitFor(() => {
      expect(screen.getByText('analytics.hub.tabs.summary')).toBeTruthy();
    });

    // Resolve each item's button; throw rather than use a non-null assertion so
    // the error message is clear if a button is missing.
    const getBtn = (text: string): HTMLButtonElement => {
      const el = screen.getByText(text).closest('button');
      if (!el) throw new Error(`Button with text "${text}" not found in sidebar`);
      return el;
    };

    // Click each item and assert the spy receives the correct route segment.
    await fireEvent.click(getBtn('analytics.hub.tabs.summary'));
    expect(onNavigate).toHaveBeenCalledWith('/analytics/summary');

    await fireEvent.click(getBtn('analytics.species.title'));
    expect(onNavigate).toHaveBeenCalledWith('/analytics/species');

    await fireEvent.click(getBtn('analytics.hub.tabs.patterns'));
    expect(onNavigate).toHaveBeenCalledWith('/analytics/activity');

    await fireEvent.click(getBtn('analytics.hub.tabs.trends'));
    expect(onNavigate).toHaveBeenCalledWith('/analytics/trends');

    await fireEvent.click(getBtn('analytics.hub.tabs.biodiversity'));
    expect(onNavigate).toHaveBeenCalledWith('/analytics/biodiversity');

    await fireEvent.click(getBtn('analytics.hub.tabs.quality'));
    expect(onNavigate).toHaveBeenCalledWith('/analytics/review');
  });
});
