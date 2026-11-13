import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

// Only the document title is under test: the layout and every page are replaced by stubs.
vi.mock('./lib/desktop/layouts/RootLayout.svelte', () => ({ default: () => {} }));
vi.mock('./lib/desktop/features/dashboard/pages/DashboardPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/wizard/WizardDialog.svelte', () => ({ default: () => {} }));
vi.mock('./lib/desktop/views/About.svelte', () => ({ default: () => {} }));
vi.mock('./lib/desktop/views/Help.svelte', () => ({ default: () => {} }));

vi.mock('./lib/utils/logger', async importOriginal => {
  const original = await importOriginal<typeof import('./lib/utils/logger')>();
  const quiet = { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() };
  return { ...original, loggers: { ...original.loggers, auth: quiet } };
});

// The state the mocked initApp leaves behind; a test changes it before rendering.
const startup = vi.hoisted(() => {
  const state: {
    security: { enabled: boolean; accessAllowed: boolean; privateMode: boolean };
    error: string | null;
  } = {
    security: { enabled: false, accessAllowed: true, privateMode: false },
    error: null,
  };
  return state;
});

vi.mock('./lib/stores/appState.svelte', async importOriginal => {
  const original = await importOriginal<typeof import('./lib/stores/appState.svelte')>();
  return {
    ...original,
    initApp: vi.fn(async () => {
      Object.assign(original.appState.security, startup.security);
      original.appState.error = startup.error;
      original.appState.initialized = true;
      original.appState.loading = false;
      return true;
    }),
  };
});

import App from './App.svelte';
import { renderTyped } from './test/render-helpers';
import { t } from './lib/i18n';
import { appState } from './lib/stores/appState.svelte';
import { navigation } from './lib/stores/navigation.svelte';

const APP_NAME = 'BirdNET-Go';
const ABOUT_PATH = '/ui/about';
const HELP_PATH = '/ui/help';
const DASHBOARD_PATH = '/ui/dashboard';
const UNKNOWN_PATH = '/ui/no-such-page';
const ERROR_CODE = '403';
const INIT_ERROR = 'init failed';
const UNTRANSLATED_KEY = 'navigation.about';

const SRC_LIB_DIR = join(import.meta.dirname, 'lib');

// The test i18n mock returns the key for an unknown key, like the real t(); every other key
// gets a distinct, recognizable translation.
function translateKnown(key: string): string {
  return key === UNTRANSLATED_KEY ? key : `[${key}]`;
}

const originalT = vi.mocked(t).getMockImplementation();

describe('App document title', () => {
  beforeEach(() => {
    startup.security = { enabled: false, accessAllowed: true, privateMode: false };
    startup.error = null;
    appState.initialized = false;
    appState.error = null;
    vi.mocked(t).mockImplementation(translateKnown);
    document.title = '';
  });

  afterEach(() => {
    window.location.search = '';
    if (originalT) vi.mocked(t).mockImplementation(originalT);
    navigation.redirect(DASHBOARD_PATH);
  });

  it('shows the page title followed by the app name', async () => {
    navigation.redirect(DASHBOARD_PATH);
    renderTyped(App);
    await waitFor(() => expect(document.title).toBe(`[navigation.dashboard] - ${APP_NAME}`));
  });

  it('follows client-side navigation', async () => {
    navigation.redirect(DASHBOARD_PATH);
    renderTyped(App);
    await waitFor(() => expect(document.title).toBe(`[navigation.dashboard] - ${APP_NAME}`));

    navigation.navigate(HELP_PATH);
    await waitFor(() => expect(document.title).toBe(`[navigation.helpAndSupport] - ${APP_NAME}`));

    navigation.navigate(DASHBOARD_PATH);
    await waitFor(() => expect(document.title).toBe(`[navigation.dashboard] - ${APP_NAME}`));
  });

  it('restores the page title after leaving an error page', async () => {
    navigation.redirect(UNKNOWN_PATH);
    renderTyped(App);
    await waitFor(() => expect(document.title).toBe(`[pageTitle.pageNotFound] - ${APP_NAME}`));

    navigation.navigate(DASHBOARD_PATH);
    await waitFor(() => expect(document.title).toBe(`[navigation.dashboard] - ${APP_NAME}`));
  });

  it('titles the login screen for a visitor who is not allowed in', async () => {
    startup.security = { enabled: true, accessAllowed: false, privateMode: true };
    navigation.redirect(DASHBOARD_PATH);
    renderTyped(App);
    await waitFor(() => expect(document.title).toBe(`[auth.loginTitle] - ${APP_NAME}`));
  });

  it('shows only the app name when the app failed to load', async () => {
    startup.error = INIT_ERROR;
    navigation.redirect(DASHBOARD_PATH);
    renderTyped(App);
    // The title is the app name while loading too, so assert only once init has failed.
    await waitFor(() => expect(appState.error).toBe(INIT_ERROR));
    await tick();
    expect(document.title).toBe(APP_NAME);
  });

  it('keeps the error code in the title of a generic error page', async () => {
    // The test environment replaces window.location with a plain object, which App reads.
    window.location.search = `?error=${ERROR_CODE}`;
    navigation.redirect(UNKNOWN_PATH);
    renderTyped(App);
    await waitFor(() =>
      expect(document.title).toBe(`${ERROR_CODE} - [common.error] - ${APP_NAME}`)
    );
  });

  it('shows only the app name when the page title has no translation', async () => {
    navigation.redirect(ABOUT_PATH);
    renderTyped(App);
    await waitFor(() => expect(document.title).toBe(APP_NAME));
  });
});

describe('document title writers', () => {
  it('has no <title> element inside a svelte:head outside App.svelte', () => {
    const files = readdirSync(SRC_LIB_DIR, { recursive: true, encoding: 'utf8' }).filter(file =>
      file.endsWith('.svelte')
    );
    const offenders = files.filter(file =>
      /<svelte:head>(?:(?!<\/svelte:head>)[\s\S])*?<title[\s>]/.test(
        // eslint-disable-next-line security/detect-non-literal-fs-filename -- paths come from listing the component directory
        readFileSync(join(SRC_LIB_DIR, file), 'utf8')
      )
    );
    expect(offenders).toEqual([]);
  });
});
