import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { get } from 'svelte/store';
import { render, screen } from '@testing-library/svelte';
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

const BANNER_TITLE_KEY =
  'settings.species.activeSpecies.rangeFilterHealth.geomodelNotInstalled.title';
const MODELS_LINK_LABEL_KEY = 'settings.species.activeSpecies.rangeFilterHealth.openModels';

describe('SpeciesSettingsPage range filter banner without a location', () => {
  let originalStoreState: GlobalSettingsState;

  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn();
    originalStoreState = get(settingsStore);
    // Partial on purpose: the page reads only these fields here. Coordinates and
    // birdnet must be present in both formData and originalData or the page effect
    // returns before loading anything.
    const formData = {
      birdnet: { latitude: 0, longitude: 0, locationConfigured: false },
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
    vi.mocked(api.get).mockResolvedValue({
      participantsLoaded: true,
      active: false,
      fellBack: false,
      mappedSpecies: 0,
      locationConfigured: false,
      geomodel: null,
      geomodelIssue: 'not_installed',
      reloadFailed: false,
      classifiers: [{ name: 'Google Perch v2', coveredByBackend: false }],
    });
  });

  afterEach(() => {
    settingsStore.set(originalStoreState);
  });

  it('shows the missing geomodel banner with a Models link', async () => {
    render(SpeciesSettingsPage);

    expect(await screen.findByText(BANNER_TITLE_KEY)).toBeInTheDocument();
    const link = screen.getByRole('link', { name: MODELS_LINK_LABEL_KEY });
    expect(link.getAttribute('href')).toMatch(/\/ui\/settings\/analysis\?tab=models$/);
  });
});
