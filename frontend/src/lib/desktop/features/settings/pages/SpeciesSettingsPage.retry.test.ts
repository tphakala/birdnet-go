import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { get } from 'svelte/store';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import {
  settingsStore,
  type GlobalSettingsState,
  type SettingsFormData,
} from '$lib/stores/settings';
import { api } from '$lib/utils/api';
import SpeciesSettingsPage from './SpeciesSettingsPage.svelte';

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: {
    get: vi.fn().mockResolvedValue({}),
    post: vi.fn().mockResolvedValue({}),
    put: vi.fn().mockResolvedValue({}),
    delete: vi.fn().mockResolvedValue({}),
  },
}));

const RETRY_KEY = 'settings.species.activeSpecies.retry';
const LOAD_ERROR_KEY = 'settings.species.activeSpecies.errors.loadFailed';
const RANGE_TEST_PATH = '/api/v2/range/species/test';

describe('SpeciesSettingsPage active species load error', () => {
  let originalStoreState: GlobalSettingsState;

  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn();
    originalStoreState = get(settingsStore);
    // Partial on purpose: the page reads only these fields here
    const formData = {
      birdnet: { latitude: 60.1, longitude: 24.9, locationConfigured: true },
      realtime: { species: { include: [], exclude: [], config: {} } },
      taxonomySynonyms: {},
    } as unknown as SettingsFormData;
    settingsStore.update(state => ({
      ...state,
      formData,
      originalData: JSON.parse(JSON.stringify(formData)),
      dataLoaded: true,
      isSaving: false,
      error: null,
    }));
    vi.mocked(api.get).mockResolvedValue({});
    vi.mocked(api.post).mockReset();
  });

  afterEach(() => {
    settingsStore.set(originalStoreState);
  });

  it('shows the error with a Retry button that loads the species again', async () => {
    vi.mocked(api.post).mockRejectedValueOnce(new Error('offline'));
    vi.mocked(api.post).mockResolvedValue({
      species: [],
      count: 0,
      threshold: 0.01,
      location: { latitude: 60.1, longitude: 24.9 },
    });
    render(SpeciesSettingsPage);

    expect(await screen.findByText(LOAD_ERROR_KEY)).toBeInTheDocument();
    const retry = screen.getByRole('button', { name: RETRY_KEY });
    const rangeCalls = () =>
      vi.mocked(api.post).mock.calls.filter(call => call[0] === RANGE_TEST_PATH).length;
    expect(rangeCalls()).toBe(1);

    await fireEvent.click(retry);

    await waitFor(() => expect(rangeCalls()).toBe(2));
    await waitFor(() => expect(screen.queryByText(LOAD_ERROR_KEY)).toBeNull());
  });
});
