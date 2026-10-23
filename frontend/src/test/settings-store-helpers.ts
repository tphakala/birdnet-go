import { settingsStore } from '$lib/stores/settings';
import { settingsAPI } from '$lib/utils/settingsApi.js';
import { get } from 'svelte/store';
import { vi } from 'vitest';

/**
 * Makes the mocked settings API serve what the store last confirmed, so a save's
 * rebase GET returns the store's own baseline. Tests that exercise a changing
 * server install their own load implementation. The caller mocks settingsApi.
 */
export function serveConfirmedSettings() {
  vi.mocked(settingsAPI.load).mockImplementation(async () =>
    JSON.parse(JSON.stringify(get(settingsStore).originalData))
  );
}
