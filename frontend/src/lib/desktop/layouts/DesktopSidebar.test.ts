import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  createComponentTestFactory,
  fireEvent,
  screen,
  waitFor,
} from '../../../test/render-helpers';
import DesktopSidebar from './DesktopSidebar.svelte';
import { sidebar } from '$lib/stores/sidebar';
import { analyticsControls } from '$lib/desktop/features/analytics/registry/analyticsControls.svelte';

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

describe('DesktopSidebar - flat task-grouped sections (#1273/#1274/#1275)', () => {
  const sidebarTest = createComponentTestFactory(DesktopSidebar);

  // Resolve a button by its (mocked-key) text; throw with a clear message if missing.
  const getBtn = (text: string): HTMLButtonElement => {
    const el = screen.getByText(text).closest('button');
    if (!el) throw new Error(`Button with text "${text}" not found in sidebar`);
    return el;
  };

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
    // The sidebar collapse state is a persisted singleton store; force expanded so
    // tests start from a known baseline and the collapsed test opts in explicitly.
    sidebar.expand();
    // analyticsControls is a module-level singleton shared across the suite; reset
    // its filters to defaults so item URLs stay query-less unless a test opts in.
    analyticsControls.applyParams({ range: 'month', start: '', end: '', species: [], source: '' });
  });

  afterEach(() => {
    sidebar.expand();
    analyticsControls.applyParams({ range: 'month', start: '', end: '', species: [], source: '' });
  });

  it('renders Dashboard, Live Audio, then the four section headers in order, each wired to its group via aria-labelledby', () => {
    const { container } = sidebarTest.render({ currentRoute: '/ui/dashboard' });

    // Top-level flat items.
    expect(screen.getByText('navigation.dashboard')).toBeTruthy();
    expect(screen.getByText('navigation.liveAudio')).toBeTruthy();

    // Four section headers in the spec'd order.
    const headerLabels = [
      'navigation.sections.explore',
      'navigation.sections.patterns',
      'navigation.sections.environment',
      'navigation.sections.dataQuality',
    ];
    headerLabels.forEach(label => expect(screen.getByText(label)).toBeTruthy());

    // Each section is a role="group" labelled by its header id, in document order.
    const groups = Array.from(
      container.querySelectorAll<HTMLElement>('[aria-labelledby^="nav-section-"]')
    );
    expect(groups.map(g => g.getAttribute('aria-labelledby'))).toEqual([
      'nav-section-explore',
      'nav-section-patterns',
      'nav-section-environment',
      'nav-section-dataQuality',
    ]);

    // Each group's aria-labelledby resolves to a header element carrying that id.
    groups.forEach(g => {
      const id = g.getAttribute('aria-labelledby');
      expect(id).toBeTruthy();
      expect(container.querySelector(`#${id}`)).toBeTruthy();
    });
  });

  it('renders every analytics item plus Search and navigates each to its route', async () => {
    const onNavigate = vi.fn();
    sidebarTest.render({ currentRoute: '/ui/dashboard', onNavigate });

    const expectations: Array<[string, string]> = [
      ['analytics.hub.tabs.summary', '/analytics/summary'],
      ['analytics.species.title', '/analytics/species'],
      ['navigation.search', '/search'],
      ['analytics.hub.tabs.patterns', '/analytics/activity'],
      ['analytics.hub.tabs.trends', '/analytics/trends'],
      ['analytics.hub.tabs.nocturnal', '/analytics/nocturnal'],
      ['analytics.hub.tabs.biodiversity', '/analytics/biodiversity'],
      ['analytics.hub.tabs.weather', '/analytics/weather'],
      ['analytics.hub.tabs.soundscape', '/analytics/soundscape'],
      ['analytics.hub.tabs.quality', '/analytics/review'],
    ];

    for (const [text] of expectations) {
      expect(screen.getByText(text)).toBeTruthy();
    }

    for (const [text, url] of expectations) {
      onNavigate.mockClear();
      await fireEvent.click(getBtn(text));
      expect(onNavigate).toHaveBeenCalledWith(url);
    }
  });

  it('exposes aria-current="page" on the active item', () => {
    sidebarTest.render({ currentRoute: '/ui/analytics/trends' });

    const trendsBtn = getBtn('analytics.hub.tabs.trends');
    expect(trendsBtn.getAttribute('aria-current')).toBe('page');

    // Siblings are not marked current.
    expect(getBtn('analytics.hub.tabs.summary').getAttribute('aria-current')).toBeNull();
  });

  it('collapsed mode: headers are sr-only, items keep an accessible name, and no analytics flyout is used', () => {
    sidebar.collapse();
    const { container } = sidebarTest.render({ currentRoute: '/ui/dashboard' });

    // The section headers are present in the DOM (so aria-labelledby stays valid) but sr-only.
    const exploreHeader = container.querySelector('#nav-section-explore');
    expect(exploreHeader).toBeTruthy();
    expect(exploreHeader?.className).toContain('sr-only');

    // Collapsed items render icon-only (no visible label text) but still expose an aria-label.
    const searchBtn = screen.getByRole('button', { name: 'navigation.search' });
    expect(searchBtn).toBeTruthy();
    const summaryBtn = screen.getByRole('button', { name: 'analytics.hub.tabs.summary' });
    expect(summaryBtn).toBeTruthy();

    // No analytics flyout/collapsible: the analytics submenu trigger aria-label must be absent.
    expect(screen.queryByLabelText('navigation.analyticsSubmenu')).toBeNull();
  });

  it('shows the coming-soon chip for Weather and Soundscape (expanded) but not Nocturnal', () => {
    sidebarTest.render({ currentRoute: '/ui/dashboard' });

    const badge = 'analytics.comingSoon.badge';
    const weatherBtn = getBtn('analytics.hub.tabs.weather');
    const soundscapeBtn = getBtn('analytics.hub.tabs.soundscape');
    const nocturnalBtn = getBtn('analytics.hub.tabs.nocturnal');

    expect(weatherBtn.textContent).toContain(badge);
    expect(soundscapeBtn.textContent).toContain(badge);
    expect(nocturnalBtn.textContent).not.toContain(badge);
  });

  it('does not render About as a top-level item; it lives under Help and activates the Help section on /ui/about', async () => {
    sidebarTest.render({ currentRoute: '/ui/about' });

    // About is now a Help subitem. With the Help section auto-expanded on /ui/about,
    // the About entry is rendered and marked current.
    await waitFor(() => {
      expect(screen.getByText('navigation.about')).toBeTruthy();
    });

    const aboutBtn = getBtn('navigation.about');
    expect(aboutBtn.getAttribute('aria-current')).toBe('page');

    // It sits inside the Help collapsible, not at the top level next to Dashboard.
    expect(aboutBtn.closest('[aria-labelledby^="nav-section-"]')).toBeNull();
  });

  it('deep-link (#1275 part A): analytics item URLs carry the active query while Search/Dashboard stay query-less', async () => {
    const onNavigate = vi.fn();
    // Set a non-default filter so queryString is non-empty.
    analyticsControls.applyParams({ range: 'year' });
    expect(analyticsControls.queryString).toBe('range=year');

    sidebarTest.render({ currentRoute: '/ui/dashboard', onNavigate });

    // Analytics item carries the query.
    await fireEvent.click(getBtn('analytics.hub.tabs.trends'));
    expect(onNavigate).toHaveBeenCalledWith('/analytics/trends?range=year');

    // Search does not.
    onNavigate.mockClear();
    await fireEvent.click(getBtn('navigation.search'));
    expect(onNavigate).toHaveBeenCalledWith('/search');

    // Dashboard does not.
    onNavigate.mockClear();
    await fireEvent.click(getBtn('navigation.dashboard'));
    expect(onNavigate).toHaveBeenCalledWith('/');
  });
});
