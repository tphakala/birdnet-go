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

/** Realtime sections a test seeds; each is a partial of the real section. */
export interface SeededRealtime {
  dashboard?: Record<string, unknown>;
  audio?: Record<string, unknown>;
}

/**
 * Marks settings as loaded (or not) and merges the given realtime sections into
 * both the saved settings and the form, as a load from the server would.
 */
export function seedSavedRealtime(sections: SeededRealtime, loaded = true) {
  settingsStore.update(store => {
    const base = store.originalData.realtime;
    const realtime = {
      ...base,
      dashboard: { ...base?.dashboard, ...sections.dashboard },
      ...(sections.audio ? { audio: { ...base?.audio, ...sections.audio } } : {}),
    };
    // Partial sections stand in for the full settings a load would bring
    return {
      ...store,
      dataLoaded: loaded,
      isLoading: false,
      originalData: { ...store.originalData, realtime },
      formData: { ...store.formData, realtime },
    } as unknown as typeof store;
  });
}
