import { describe, it, expect, beforeAll, beforeEach, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';

// Page-level coverage for the rarity filter section: the enable checkbox and the
// band editor must write a complete rarityFilter object back through
// settingsActions.updateSection (a shallow merge, so a partial object would drop
// the other field). Mocks follow AnalysisSettingsPage.gallery.test.ts.

vi.mock('$lib/utils/modelsApi', async () => {
  const actual =
    await vi.importActual<typeof import('$lib/utils/modelsApi')>('$lib/utils/modelsApi');
  return {
    ...actual,
    fetchCatalog: vi.fn().mockResolvedValue({ catalog: [] }),
    fetchInstalled: vi.fn().mockResolvedValue([]),
    installModel: vi.fn(),
    reinstallModel: vi.fn(),
    uninstallModel: vi.fn(),
    subscribeInstallProgress: vi.fn(),
    fetchModelRegions: vi.fn().mockRejectedValue(new Error('no regions in test')),
    fetchRegionCoverageMap: vi.fn(),
  };
});

vi.mock('$lib/stores/models.svelte', () => ({
  invalidateModels: vi.fn(),
}));

vi.mock('$lib/desktop/features/settings/components/ModelRegionSelector.svelte');

const BANDS = [
  { maxOccurrence: 0.1, minDetections: 3 },
  { maxOccurrence: 0.9, minDetections: 2 },
];

vi.mock('$lib/stores/settings', async () => {
  const { writable } = await vi.importActual<typeof import('svelte/store')>('svelte/store');
  const rarityFilter = {
    enabled: true,
    bands: [
      { maxOccurrence: 0.1, minDetections: 3 },
      { maxOccurrence: 0.9, minDetections: 2 },
    ],
  };
  const settingsStore = writable({
    isLoading: false,
    isSaving: false,
    error: null,
    originalData: { birdnet: { huggingFaceEndpoint: '' }, realtime: { rarityFilter } },
    formData: { birdnet: { huggingFaceEndpoint: '' }, realtime: { rarityFilter } },
  });
  return {
    settingsStore,
    settingsActions: { updateSection: vi.fn() },
    hasUnsavedChanges: writable(false),
    birdnetSettings: writable({
      huggingFaceEndpoint: '',
      threshold: 0.03,
      sensitivity: 1,
      overlap: 0,
      locationConfigured: true,
    }),
    dynamicThresholdSettings: writable({ enabled: false }),
    rarityFilterSettings: writable(rarityFilter),
    DEFAULT_RARITY_BANDS: [],
    realtimeSettings: writable({}),
    batSettings: writable({}),
    perchSettings: writable({}),
    birdNetV3Settings: writable({}),
  };
});

vi.mock('$lib/utils/api', async () => {
  const actual = await vi.importActual<typeof import('$lib/utils/api')>('$lib/utils/api');
  return {
    ...actual,
    api: {
      get: vi.fn().mockResolvedValue({}),
      post: vi.fn().mockResolvedValue({}),
      put: vi.fn().mockResolvedValue({}),
      delete: vi.fn().mockResolvedValue({}),
    },
    getCsrfToken: vi.fn().mockReturnValue('test-csrf-token'),
  };
});

import AnalysisSettingsPage from './AnalysisSettingsPage.svelte';
import { settingsActions } from '$lib/stores/settings';
import { navigation } from '$lib/stores/navigation.svelte';

// jsdom does not implement <dialog> showModal/close, which the page's dialogs call.
beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement) {
    this.open = false;
  };
});

beforeEach(() => {
  vi.mocked(settingsActions.updateSection).mockClear();
  Object.defineProperty(window, 'location', {
    value: document.location,
    writable: true,
    configurable: true,
  });
  navigation.redirect('/ui/settings/analysis');
});

describe('AnalysisSettingsPage rarity filter', () => {
  it('keeps the bands when the filter is disabled', async () => {
    render(AnalysisSettingsPage);

    await fireEvent.click(
      await screen.findByLabelText('settings.main.sections.rarityFilter.enable.label')
    );

    expect(settingsActions.updateSection).toHaveBeenCalledWith('realtime', {
      rarityFilter: { enabled: false, bands: BANDS },
    });
  });

  it('writes an added band together with the enabled flag', async () => {
    render(AnalysisSettingsPage);

    await fireEvent.click(
      await screen.findByRole('button', { name: /components\.forms\.rarityBands\.addBand/ })
    );

    expect(settingsActions.updateSection).toHaveBeenCalledWith('realtime', {
      rarityFilter: {
        enabled: true,
        bands: [...BANDS, { maxOccurrence: 0.5, minDetections: 2 }],
      },
    });
  });
});
