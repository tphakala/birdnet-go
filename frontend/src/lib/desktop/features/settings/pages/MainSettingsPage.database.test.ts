import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { settingsStore } from '$lib/stores/settings';
import type { SettingsFormData } from '$lib/stores/settings';
import { navigation } from '$lib/stores/navigation.svelte';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';
import MainSettingsPage from './MainSettingsPage.svelte';

const DATABASE_TAB_URL = '/ui/settings/main?tab=database';
const STATS_MOVED_LINK_KEY = 'settings.main.sections.database.statsMoved.link';
const DATABASE_STATS_ENDPOINT = '/api/v2/system/database/stats';

function setStore() {
  // A deliberately partial fixture: the page fills in the output defaults the
  // Database tab reads, so only the node name is needed.
  const formData = { main: { name: 'TestNode' } } as unknown as SettingsFormData;
  settingsStore.set({
    formData,
    originalData: formData,
    isLoading: false,
    isSaving: false,
    activeSection: 'main',
    error: null,
    dataLoaded: true,
  });
}

describe('MainSettingsPage Database tab', () => {
  const originalLocation = window.location;

  beforeEach(() => {
    // Shared test setup stubs window.location; tab selection and the in-app
    // link need JSDOM's history-aware location.
    Object.defineProperty(window, 'location', { value: document.location, configurable: true });
    setStore();
    vi.mocked(globalThis.fetch).mockClear();
  });

  afterEach(() => {
    cleanup();
    Object.defineProperty(window, 'location', { value: originalLocation, configurable: true });
    resetBasePath();
  });

  it('links the note to System > Database through in-app navigation', async () => {
    setBasePath('/proxy');
    navigation.redirect(DATABASE_TAB_URL);
    const user = userEvent.setup();
    render(MainSettingsPage);

    const link = screen.getByRole('link', { name: STATS_MOVED_LINK_KEY });
    expect(link).toHaveAttribute('href', '/proxy/ui/system/database');

    await user.click(link);
    expect(navigation.currentPath).toBe('/ui/system/database');
  });

  it('does not request database statistics when the Database tab opens', () => {
    navigation.redirect(DATABASE_TAB_URL);
    render(MainSettingsPage);

    expect(document.getElementById('settings-tab-database')).toHaveAttribute(
      'aria-selected',
      'true'
    );
    const statsCalls = vi
      .mocked(globalThis.fetch)
      .mock.calls.filter(call => String(call[0]).includes(DATABASE_STATS_ENDPOINT));
    expect(statsCalls).toHaveLength(0);
  });
});
