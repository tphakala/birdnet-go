import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, cleanup, fireEvent } from '@testing-library/svelte';
import { get } from 'svelte/store';

// End to end for the unsaved-changes guard: the real App, Settings shell, navigation store and
// registry. RootLayout keeps only the routed page; the settings pages and the dashboard are inert.
vi.mock('./lib/desktop/layouts/RootLayout.svelte', async () => ({
  default: (await import('./test/fixtures/MockRootLayout.svelte')).default,
}));
vi.mock('./lib/desktop/features/dashboard/pages/DashboardPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/wizard/WizardDialog.svelte', () => ({ default: () => {} }));
vi.mock('./lib/desktop/features/settings/pages/MainSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/settings/pages/AudioSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/settings/pages/FilterSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/settings/pages/IntegrationSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/settings/pages/SecuritySettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/settings/pages/SupportSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/settings/pages/SpeciesSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/settings/pages/NotificationsSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/settings/pages/UserInterfaceSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/settings/pages/AnalysisSettingsPage.svelte', () => ({
  default: () => {},
}));

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
import { navigation } from './lib/stores/navigation.svelte';
import { unsavedChanges } from './lib/stores/unsavedChanges.svelte';
import { settingsStore, settingsActions } from './lib/stores/settings';
import { settingsAPI } from './lib/utils/settingsApi.js';
import { SETTINGS_BASE_PATH } from './lib/utils/settingsAlias';
import { resetBasePath, setBasePath } from './lib/utils/urlHelpers';
import { serveConfirmedSettings } from './test/settings-store-helpers';

const DASHBOARD = ['', 'ui', 'dashboard'].join('/');
const SETTINGS_MAIN = [SETTINGS_BASE_PATH, 'main'].join('/');
const SETTINGS_AUDIO = [SETTINGS_BASE_PATH, 'audio'].join('/');
const DIALOG_TITLE = 'common.unsavedChanges.title';
const LOADS_AT_SETTINGS_ENTRY = 2; // App init plus the Settings shell mount

/** Resolves once every settings load the app started has settled. */
async function settingsLoadsSettled(expectedCalls: number) {
  await waitFor(() => {
    expect(vi.mocked(settingsAPI.load).mock.calls.length).toBeGreaterThanOrEqual(expectedCalls);
    expect(get(settingsStore).isLoading).toBe(false);
  });
}

function makeUnsavedEdit() {
  settingsStore.update(state => ({
    ...state,
    formData: { ...state.formData, main: { ...state.formData.main, name: 'edited' } },
  }));
}

function editIsKept(): boolean {
  return get(settingsStore).formData.main.name === 'edited';
}

describe('App unsaved-changes guard', () => {
  const originalLocation = window.location;

  beforeEach(async () => {
    // Shared test setup stubs window.location; use JSDOM's history-aware location.
    Object.defineProperty(window, 'location', { value: document.location, configurable: true });
    setBasePath('');
    vi.mocked(settingsAPI.load).mockClear();
    serveConfirmedSettings();
    navigation.redirect(DASHBOARD);
    await navigation.navigate(SETTINGS_MAIN);
    render(App);
    await settingsLoadsSettled(LOADS_AT_SETTINGS_ENTRY);
  });

  afterEach(() => {
    cleanup();
    unsavedChanges.resetForTesting();
    navigation.redirect(DASHBOARD);
    resetBasePath();
    Object.defineProperty(window, 'location', { value: originalLocation, configurable: true });
  });

  it('asks before leaving Settings with edits, and Stay keeps the route and the edit', async () => {
    makeUnsavedEdit();
    const loadsBefore = vi.mocked(settingsAPI.load).mock.calls.length;

    void navigation.navigate(DASHBOARD);

    expect(await screen.findByText(DIALOG_TITLE)).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'common.unsavedChanges.stay' }));

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(navigation.currentPath).toBe(SETTINGS_MAIN);
    expect(editIsKept()).toBe(true);
    expect(vi.mocked(settingsAPI.load).mock.calls.length).toBe(loadsBefore);
  });

  it('does not ask when moving between settings sub-pages', async () => {
    makeUnsavedEdit();

    await navigation.navigate(SETTINGS_AUDIO);

    expect(screen.queryByRole('dialog')).toBeNull();
    expect(navigation.currentPath).toBe(SETTINGS_AUDIO);
    expect(editIsKept()).toBe(true);
  });

  it('does not ask while Settings is clean', async () => {
    await navigation.navigate(DASHBOARD);

    expect(screen.queryByRole('dialog')).toBeNull();
    expect(navigation.currentPath).toBe(DASHBOARD);
  });

  it('Leave goes to the dashboard and discards the edit, so coming back and leaving is silent', async () => {
    makeUnsavedEdit();

    void navigation.navigate(DASHBOARD);
    await fireEvent.click(
      await screen.findByRole('button', { name: 'common.unsavedChanges.leave' })
    );

    await waitFor(() => expect(navigation.currentPath).toBe(DASHBOARD));
    expect(get(settingsStore).formData).toEqual(get(settingsStore).originalData);

    const loadsBefore = vi.mocked(settingsAPI.load).mock.calls.length;
    await navigation.navigate(SETTINGS_MAIN);
    await settingsLoadsSettled(loadsBefore + 1);
    await navigation.navigate(DASHBOARD);

    expect(screen.queryByRole('dialog')).toBeNull();
    expect(navigation.currentPath).toBe(DASHBOARD);
  });

  it('holds browser Back while Settings has edits, out of sight of the route pages', async () => {
    makeUnsavedEdit();
    // A route page's own listener (dashboard, detections) is added after App's.
    const pageListener = vi.fn();
    window.addEventListener('popstate', pageListener);

    try {
      window.history.back();

      expect(await screen.findByText(DIALOG_TITLE)).toBeInTheDocument();
      expect(navigation.currentPath).toBe(SETTINGS_MAIN);
      expect(window.location.pathname).toBe(SETTINGS_MAIN);
      expect(pageListener).not.toHaveBeenCalled();
    } finally {
      window.removeEventListener('popstate', pageListener);
    }
  });

  it('goes back to the previous page on Leave after a held Back', async () => {
    makeUnsavedEdit();

    window.history.back();
    await fireEvent.click(
      await screen.findByRole('button', { name: 'common.unsavedChanges.leave' })
    );

    await waitFor(() => expect(navigation.currentPath).toBe(DASHBOARD));
    expect(window.location.pathname).toBe(DASHBOARD);
  });

  it('discards the edit once a save that was in flight at Leave fails', async () => {
    makeUnsavedEdit();
    settingsStore.update(state => ({ ...state, isSaving: true }));
    void navigation.navigate(DASHBOARD);
    await fireEvent.click(
      await screen.findByRole('button', { name: 'common.unsavedChanges.leave' })
    );
    await waitFor(() => expect(navigation.currentPath).toBe(DASHBOARD));
    expect(editIsKept()).toBe(true);

    // The save fails: the store clears isSaving and keeps the form as it was.
    settingsStore.update(state => ({ ...state, isSaving: false }));

    await waitFor(() =>
      expect(get(settingsStore).formData).toEqual(get(settingsStore).originalData)
    );
  });

  it('waits for queued saves before discarding, so a queued save never sends reset data', async () => {
    let queuedWorkDone: () => void = () => {};
    const whenIdle = vi.spyOn(settingsActions, 'whenIdle').mockReturnValue(
      new Promise<void>(resolve => {
        queuedWorkDone = resolve;
      })
    );
    makeUnsavedEdit();
    settingsStore.update(state => ({ ...state, isSaving: true }));
    void navigation.navigate(DASHBOARD);
    await fireEvent.click(
      await screen.findByRole('button', { name: 'common.unsavedChanges.leave' })
    );
    await waitFor(() => expect(navigation.currentPath).toBe(DASHBOARD));

    // The first save finished, but another one is still queued behind it.
    settingsStore.update(state => ({ ...state, isSaving: false }));
    await waitFor(() => expect(whenIdle).toHaveBeenCalled());
    expect(editIsKept()).toBe(true);

    queuedWorkDone();
    await waitFor(() =>
      expect(get(settingsStore).formData).toEqual(get(settingsStore).originalData)
    );
    whenIdle.mockRestore();
  });

  it('leaves the edits of a new Settings visit alone when an earlier save fails', async () => {
    makeUnsavedEdit();
    settingsStore.update(state => ({ ...state, isSaving: true }));
    void navigation.navigate(DASHBOARD);
    await fireEvent.click(
      await screen.findByRole('button', { name: 'common.unsavedChanges.leave' })
    );
    await waitFor(() => expect(navigation.currentPath).toBe(DASHBOARD));

    // Settings is opened again and its form edited while the first save is still out.
    const loadsBefore = vi.mocked(settingsAPI.load).mock.calls.length;
    await navigation.navigate(SETTINGS_MAIN);
    await settingsLoadsSettled(loadsBefore + 1);
    settingsStore.update(state => ({ ...state, isSaving: true }));
    makeUnsavedEdit();
    settingsStore.update(state => ({ ...state, isSaving: false }));

    expect(editIsKept()).toBe(true);
  });

  it('keeps the edit when Settings is left while a save is in flight', async () => {
    makeUnsavedEdit();
    settingsStore.update(state => ({ ...state, isSaving: true }));

    try {
      void navigation.navigate(DASHBOARD);
      await fireEvent.click(
        await screen.findByRole('button', { name: 'common.unsavedChanges.leave' })
      );
      await waitFor(() => expect(navigation.currentPath).toBe(DASHBOARD));

      // The save in flight would otherwise send the reset data.
      expect(editIsKept()).toBe(true);
    } finally {
      settingsStore.update(state => ({ ...state, isSaving: false }));
    }
  });
});
