import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, waitFor } from '@testing-library/svelte';

// Only the routing is under test: RootLayout is replaced by a stub that records the props
// App passes it (title and the path handed to the sidebar).
interface StubLayoutProps {
  title: string;
  currentPath: string;
}
const layout: { props: StubLayoutProps | null } = vi.hoisted(() => ({ props: null }));
vi.mock('./lib/desktop/layouts/RootLayout.svelte', () => ({
  default: (_anchor: unknown, props: StubLayoutProps) => {
    layout.props = props;
  },
}));
vi.mock('./lib/desktop/features/dashboard/pages/DashboardPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/wizard/WizardDialog.svelte', () => ({ default: () => {} }));
vi.mock('./lib/desktop/views/Settings.svelte', () => ({ default: () => {} }));

// Partial mock: only the prefix helpers become controllable, the rest stays real.
vi.mock('./lib/utils/urlHelpers', async importOriginal => {
  const original = await importOriginal<typeof import('./lib/utils/urlHelpers')>();
  return {
    ...original,
    getAppBasePath: vi.fn(() => ''),
    buildAppUrl: vi.fn((path: string) => path),
  };
});

vi.mock('./lib/utils/logger', async importOriginal => {
  const original = await importOriginal<typeof import('./lib/utils/logger')>();
  const quiet = { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() };
  return { ...original, loggers: { ...original.loggers, auth: quiet } };
});

vi.mock('./lib/stores/appState.svelte', async importOriginal => {
  const original = await importOriginal<typeof import('./lib/stores/appState.svelte')>();
  return {
    ...original,
    initApp: vi.fn(async () => {
      Object.assign(original.appState.security, {
        enabled: false,
        accessAllowed: true,
        privateMode: false,
      });
      original.appState.initialized = true;
      original.appState.loading = false;
      return true;
    }),
  };
});

import App from './App.svelte';
import { getAppBasePath, buildAppUrl } from './lib/utils/urlHelpers';
import { navigation } from './lib/stores/navigation.svelte';
import { SETTINGS_BASE_PATH, SETTINGS_DEFAULT_PATH } from './lib/utils/settingsAlias';

const BARE = SETTINGS_BASE_PATH;
const MAIN = SETTINGS_DEFAULT_PATH;
const AUDIO = [BARE, 'audio'].join('/');
const GENERAL_TITLE_KEY = 'settings.sections.node';
const PROXY_PREFIX = '/proxy';
const QUERY_AND_HASH = '?tab=location#map';

// The i18n mock returns the key, so the title is the translation key.
async function titleFor(entry: string): Promise<string> {
  navigation.redirect(entry);
  // Entering the URL is the test's setup; only what App does afterwards counts.
  vi.mocked(window.history.pushState).mockClear();
  vi.mocked(window.history.replaceState).mockClear();
  render(App);
  await waitFor(() => expect(layout.props).not.toBeNull());
  return layout.props?.title ?? '';
}

function expectBareUrlUntouched(): void {
  expect(navigation.currentPath).toBe(BARE);
  expect(window.history.pushState).not.toHaveBeenCalled();
  expect(window.history.replaceState).not.toHaveBeenCalled();
}

describe('App bare settings path', () => {
  beforeEach(() => {
    layout.props = null;
    vi.spyOn(window.history, 'pushState');
    vi.spyOn(window.history, 'replaceState');
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.mocked(getAppBasePath).mockImplementation(() => '');
    vi.mocked(buildAppUrl).mockImplementation((path: string) => path);
    navigation.redirect('/ui/dashboard');
  });

  it.each([
    ['the main section', MAIN],
    ['the bare path', BARE],
    ['the bare path with a query and hash', `${BARE}${QUERY_AND_HASH}`],
    ['the bare path entered with a trailing slash', `${BARE}/`],
  ])('uses the General title for %s', async (_label, entry) => {
    expect(await titleFor(entry)).toBe(GENERAL_TITLE_KEY);
  });

  it('keeps the other section titles', async () => {
    expect(await titleFor(AUDIO)).toBe('settings.sections.audio');
  });

  it('does not rewrite the URL or touch history for the bare path', async () => {
    await titleFor(`${BARE}${QUERY_AND_HASH}`);
    expectBareUrlUntouched();
    expect(navigation.currentSearch).toBe('?tab=location');
  });

  it('keeps the proxy prefix and shows the General title when a prefix is configured', async () => {
    vi.mocked(getAppBasePath).mockImplementation(() => PROXY_PREFIX);
    vi.mocked(buildAppUrl).mockImplementation((path: string) => `${PROXY_PREFIX}${path}`);

    expect(await titleFor(`${PROXY_PREFIX}${BARE}/${QUERY_AND_HASH}`)).toBe(GENERAL_TITLE_KEY);
    expectBareUrlUntouched();
  });
});
