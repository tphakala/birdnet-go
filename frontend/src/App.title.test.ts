import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

// Only the document title is under test: the layout, the wizard and the pages App imports
// statically are replaced by stubs. Lazily loaded pages are never rendered, because the stubbed
// layout renders no children.
vi.mock('./lib/desktop/layouts/RootLayout.svelte', () => ({ default: () => {} }));
vi.mock('./lib/desktop/features/dashboard/pages/DashboardPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/wizard/WizardDialog.svelte', () => ({ default: () => {} }));
vi.mock('./lib/desktop/views/About.svelte', () => ({ default: () => {} }));
vi.mock('./lib/desktop/views/Help.svelte', () => ({ default: () => {} }));
vi.mock('./lib/desktop/views/Settings.svelte', () => ({ default: () => {} }));
vi.mock('./lib/desktop/views/Notifications.svelte', () => ({ default: () => {} }));

vi.mock('./lib/utils/logger', async importOriginal => {
  const original = await importOriginal<typeof import('./lib/utils/logger')>();
  const quiet = { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() };
  return { ...original, loggers: { ...original.loggers, auth: quiet } };
});

// The state the mocked initApp leaves behind; a test changes it before rendering.
const { state: startup, defaultSecurity: DEFAULT_SECURITY } = vi.hoisted(() => {
  const defaultSecurity = { enabled: false, accessAllowed: true, privateMode: false };
  const state: {
    security: { enabled: boolean; accessAllowed: boolean; privateMode: boolean };
    error: string | null;
  } = {
    security: { ...defaultSecurity },
    error: null,
  };
  return { state, defaultSecurity };
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
const NOTIFICATIONS_PATH = '/ui/notifications';
const SETTINGS_BASE = '/ui/settings';
const EMPTY_TRANSLATION = '';
const SRC_DIR = import.meta.dirname;
const SETTINGS_VIEW_PATH = join(SRC_DIR, 'lib/desktop/views/Settings.svelte');
const SETTINGS_SECTION_MAP_RE = /const sectionMap[^=]*=\s*\{([^}]*)\}/;
const OBJECT_KEY_RE = /^\s*(\w+)\s*:/gm;
const SETTINGS_NAME_PATTERN = /^\[settings\.sections\.\w+\] - \[navigation\.settings\] - /;

// The URL segments Settings.svelte maps to a section, so a section added there without a title
// in App.svelte fails the subpage test below.
function settingsSubpagesFromView(): string[] {
   
  const map = SETTINGS_SECTION_MAP_RE.exec(readFileSync(SETTINGS_VIEW_PATH, 'utf8'));
  return Array.from((map?.[1] ?? '').matchAll(OBJECT_KEY_RE), match => match[1]);
}
const SETTINGS_SUBPAGES = settingsSubpagesFromView();
const UNTRANSLATED_KEY = 'navigation.about';
const SETTINGS_KEY = 'navigation.settings';

// The test i18n mock returns the key for an unknown key, like the real t(); every other key
// gets a distinct, recognizable translation.
function translateKnown(key: string): string {
  return key === UNTRANSLATED_KEY ? key : `[${key}]`;
}

const originalT = vi.mocked(t).getMockImplementation();

describe('App document title', () => {
  beforeEach(() => {
    startup.security = { ...DEFAULT_SECURITY };
    startup.error = null;
    Object.assign(appState.security, DEFAULT_SECURITY);
    appState.initialized = false;
    appState.loading = false;
    appState.error = null;
    vi.mocked(t).mockImplementation(translateKnown);
    document.title = '';
  });

  afterEach(() => {
    window.location.search = '';
    vi.mocked(t).mockReset();
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

  it('titles the login screen without Settings when a settings page was requested', async () => {
    startup.security = { enabled: true, accessAllowed: false, privateMode: true };
    navigation.redirect(`${SETTINGS_BASE}/audio`);
    renderTyped(App);
    await waitFor(() => expect(document.title).toBe(`[auth.loginTitle] - ${APP_NAME}`));
  });

  it('titles the login screen without an error code when an error page was requested', async () => {
    startup.security = { enabled: true, accessAllowed: false, privateMode: true };
    window.location.search = `?error=${ERROR_CODE}`;
    navigation.redirect(UNKNOWN_PATH);
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

  it('gives every settings subpage a unique title that names Settings, apart from the other routes', async () => {
    navigation.redirect(NOTIFICATIONS_PATH);
    renderTyped(App);
    await waitFor(() => expect(document.title).toBe(`[navigation.notifications] - ${APP_NAME}`));
    expect(SETTINGS_SUBPAGES).toContain('main');
    const titles = [document.title];

    for (const subpage of SETTINGS_SUBPAGES) {
      const previous = document.title;
      navigation.navigate(`${SETTINGS_BASE}/${subpage}`);
      await waitFor(() => expect(document.title).not.toBe(previous));
      expect(document.title).toMatch(SETTINGS_NAME_PATTERN);
      titles.push(document.title);
    }

    expect(new Set(titles).size).toBe(titles.length);
  });

  it('keeps the section before Settings whatever the translations are', async () => {
    vi.mocked(t).mockImplementation(key => (key === SETTINGS_KEY ? 'Einstellungen' : `<${key}>`));
    navigation.redirect(`${SETTINGS_BASE}/audio`);
    renderTyped(App);
    await waitFor(() =>
      expect(document.title).toBe(`<settings.sections.audio> - Einstellungen - ${APP_NAME}`)
    );
  });

  it('shows plain Settings for a settings path without a known section', async () => {
    navigation.redirect(`${SETTINGS_BASE}/no-such-section`);
    renderTyped(App);
    await waitFor(() => expect(document.title).toBe(`[navigation.settings] - ${APP_NAME}`));
  });

  it('drops the Settings suffix when Settings has no translation', async () => {
    vi.mocked(t).mockImplementation(key => (key === SETTINGS_KEY ? key : `[${key}]`));
    navigation.redirect(`${SETTINGS_BASE}/audio`);
    renderTyped(App);
    await waitFor(() => expect(document.title).toBe(`[settings.sections.audio] - ${APP_NAME}`));
  });

  it.each([
    ['the key itself', UNTRANSLATED_KEY],
    ['an empty string', EMPTY_TRANSLATION],
  ])('shows only the app name when the page title translates to %s', async (_label, value) => {
    vi.mocked(t).mockImplementation(key => (key === UNTRANSLATED_KEY ? value : `[${key}]`));
    navigation.redirect(HELP_PATH);
    renderTyped(App);
    await waitFor(() => expect(document.title).toBe(`[navigation.helpAndSupport] - ${APP_NAME}`));

    navigation.navigate(ABOUT_PATH);
    await waitFor(() => expect(document.title).toBe(APP_NAME));
  });
});

const TITLE_IN_HEAD_RE = /<svelte:head\b[^>]*>(?:(?!<\/svelte:head>)[\s\S])*?<title(?=[\s/>])/;
const TITLE_ASSIGNMENT_RE = /\bdocument\.title\s*=(?!=)/;
const HTML_COMMENT_RE = /<!--[\s\S]*?-->/g;
const SCRIPT_COMMENT_RE = /\/\*[\s\S]*?\*\/|^\s*\/\/.*$/gm;
const APP_COMPONENT = 'App.svelte';
const TEST_FILE_RE = /\.(?:test|spec)\.[cm]?[jt]s$|(?:^|[\\/])test[\\/]/;
const SOURCE_FILE_RE = /\.(?:svelte|ts|js)$/;

function hasTitleInHead(source: string): boolean {
  return TITLE_IN_HEAD_RE.test(source.replace(HTML_COMMENT_RE, ''));
}

function assignsDocumentTitle(source: string): boolean {
  return TITLE_ASSIGNMENT_RE.test(
    source.replace(HTML_COMMENT_RE, '').replace(SCRIPT_COMMENT_RE, '')
  );
}

function sourceFiles(): string[] {
   
  return readdirSync(SRC_DIR, { recursive: true, encoding: 'utf8' }).filter(file =>
    SOURCE_FILE_RE.test(file)
  );
}

function offendersOf(files: string[], matches: (source: string) => boolean): string[] {
  return files.filter(file =>
    // eslint-disable-next-line security/detect-non-literal-fs-filename -- paths come from listing the source tree
    matches(readFileSync(join(SRC_DIR, file), 'utf8'))
  );
}

describe('document title writers', () => {
  it('has no <title> element inside a svelte:head outside App.svelte', () => {
    const components = sourceFiles().filter(
      file => file.endsWith('.svelte') && file !== APP_COMPONENT
    );
    expect(offendersOf(components, hasTitleInHead)).toEqual([]);
  });

  it('has no imperative document.title assignment outside App.svelte and tests', () => {
    const files = sourceFiles().filter(file => file !== APP_COMPONENT && !TEST_FILE_RE.test(file));
    expect(offendersOf(files, assignsDocumentTitle)).toEqual([]);
  });

  describe('scan rules', () => {
    it.each([
      '<svelte:head>\n  <title>x</title>\n</svelte:head>',
      '<svelte:head data-x="1" >\n<meta name="a" />\n<title >x</title></svelte:head>',
      '<svelte:head><title/></svelte:head>',
      '<!-- note --><svelte:head><title>x</title></svelte:head>',
    ])('flags a title in the head: %j', source => {
      expect(hasTitleInHead(source)).toBe(true);
    });

    it.each([
      '<!-- <svelte:head><title>x</title></svelte:head> -->',
      '<svelte:head><meta name="a" /></svelte:head><title>x</title>',
      '<svelte:head><titled /></svelte:head>',
    ])('ignores a title that is not in the head: %j', source => {
      expect(hasTitleInHead(source)).toBe(false);
    });

    it.each(['document.title = x;', 'document.title=x', "  document.title\n    = 'a'"])(
      'flags an assignment: %j',
      source => {
        expect(assignsDocumentTitle(source)).toBe(true);
      }
    );

    it.each([
      'const a = document.title;',
      'if (document.title === x) {}',
      '// document.title = x',
      '/* document.title = x */',
      '<!-- document.title = x -->',
    ])('ignores a read or a comment: %j', source => {
      expect(assignsDocumentTitle(source)).toBe(false);
    });
  });
});
