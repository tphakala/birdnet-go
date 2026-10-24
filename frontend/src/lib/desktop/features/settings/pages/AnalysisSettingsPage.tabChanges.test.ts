import { describe, it, expect, beforeEach, beforeAll, vi } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import type { CatalogEntry, InstalledModel } from '$lib/types/models';

// The Analysis page tabs show an unsaved-change dot when any card badge on the tab would.
// These tests pin which edits light which tab, using the same accessible description the
// Security page tests use (the dot itself is aria-hidden, its sr-only text describes the tab).

vi.mock('$lib/utils/modelsApi', async () => {
  const actual =
    await vi.importActual<typeof import('$lib/utils/modelsApi')>('$lib/utils/modelsApi');
  return {
    ...actual,
    fetchCatalog: vi.fn(),
    fetchInstalled: vi.fn(),
    installModel: vi.fn(),
    reinstallModel: vi.fn(),
    uninstallModel: vi.fn(),
    subscribeInstallProgress: vi.fn(),
    fetchModelRegions: vi.fn(),
    fetchRegionCoverageMap: vi.fn(),
  };
});

vi.mock('$lib/stores/models.svelte', () => ({
  invalidateModels: vi.fn(),
}));

vi.mock('$lib/desktop/features/settings/components/ModelRegionSelector.svelte');

// The mocked store keeps its config as loose sections, so these tests can seed sections the real
// form type does not list in every case.
type SectionMap = Record<string, Record<string, unknown>>;
interface MockSettingsState {
  isLoading: boolean;
  isSaving: boolean;
  error: null;
  originalData: SectionMap;
  formData: SectionMap;
}

// The saved config every test starts from: a birdnet section with a range filter and a realtime
// section with a dynamic threshold, so those cards have something to compare. The store is built
// here, typed, so the tests and the module mock share one instance without a cast.
const { BASE_BIRDNET, BASE_REALTIME, mockStore } = await vi.hoisted(async () => {
  const { writable } = await import('svelte/store');
  const BASE_BIRDNET = {
    huggingFaceEndpoint: '',
    threshold: 0.03,
    locale: 'en-uk',
    latitude: 60.1,
    longitude: 24.9,
    sensitivity: 1,
    overlap: 0,
    rangeFilter: { threshold: 0.01 },
  };
  const BASE_REALTIME = {
    dynamicThreshold: { enabled: false, debug: false, trigger: 0.8, min: 0.3, validHours: 24 },
  };
  const mockStore = writable<MockSettingsState>({
    isLoading: false,
    isSaving: false,
    error: null,
    originalData: { birdnet: { ...BASE_BIRDNET }, realtime: { ...BASE_REALTIME } },
    formData: { birdnet: { ...BASE_BIRDNET }, realtime: { ...BASE_REALTIME } },
  });
  return { BASE_BIRDNET, BASE_REALTIME, mockStore };
});

// The store mock holds the saved copy and the form copy; the section stores are derived from the
// form copy, as in the real store.
vi.mock('$lib/stores/settings', async () => {
  const { writable, derived } =
    await vi.importActual<typeof import('svelte/store')>('svelte/store');
  return {
    settingsStore: mockStore,
    settingsActions: {
      updateSection: vi.fn(),
      refreshKeepingEdits: vi.fn().mockResolvedValue(undefined),
    },
    hasUnsavedChanges: writable(false),
    birdnetSettings: derived(mockStore, state => state.formData.birdnet),
    dynamicThresholdSettings: derived(mockStore, state => state.formData.realtime.dynamicThreshold),
    realtimeSettings: derived(mockStore, state => state.formData.realtime),
    // No bat, Perch or BirdNET v3.0 section in the config unless a test seeds one, as on a
    // default install.
    batSettings: derived(mockStore, state => state.formData.bat),
    perchSettings: derived(mockStore, state => state.formData.perch),
    birdNetV3Settings: derived(mockStore, state => state.formData.birdnetv3),
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
import * as modelsApi from '$lib/utils/modelsApi';
import { navigation } from '$lib/stores/navigation.svelte';

const HAS_CHANGES = 'settings.tabs.hasChanges';

const SETTINGS_SECTION_TITLE = {
  bat: 'analysis.bat.title',
  perch: 'analysis.perch.title',
  birdnetv3: 'analysis.birdnetv3.title',
} as const;

const PERCH_CATALOG_ID = 'perch-v2';
const BIRDNET_V3_CATALOG_ID = 'birdnet-v3.0';
const BAT_CATALOG_ID = 'bat-test';
const EDITED_THRESHOLD = 0.7;
const EDITED_TRIGGER = 0.9;

/** A fresh copy of the saved config every test starts from. */
function savedConfig() {
  return { birdnet: { ...BASE_BIRDNET }, realtime: { ...BASE_REALTIME } };
}

/** Edit birdnet keys in the form copy only, as a control does. */
function editBirdnet(patch: Record<string, unknown>) {
  mockStore.update(state => ({
    ...state,
    formData: { ...state.formData, birdnet: { ...state.formData.birdnet, ...patch } },
  }));
}

/** Edit keys of a top-level config section in the form copy only. */
function editSection(section: string, patch: Record<string, unknown>) {
  mockStore.update(state => ({
    ...state,
    formData: { ...state.formData, [section]: { ...state.formData[section], ...patch } },
  }));
}

/** Put a top-level config section into both the saved copy and the form copy. */
function seedSection(section: string, value: Record<string, unknown>) {
  mockStore.update(state => ({
    ...state,
    originalData: { ...state.originalData, [section]: { ...value } },
    formData: { ...state.formData, [section]: { ...value } },
  }));
}

function installedModel(catalogId: string): InstalledModel {
  return {
    catalogId,
    modelPath: `/models/${catalogId}.onnx`,
    labelsPath: `/models/${catalogId}.txt`,
    installedAt: '2026-01-01T00:00:00Z',
    version: '1.0',
  };
}

function batCatalogEntry(): CatalogEntry {
  return {
    id: BAT_CATALOG_ID,
    name: 'Test Bat Model',
    description: 'A test bat classifier',
    author: 'Tester',
    license: 'CC-BY-NC-4.0',
    commercialUse: false,
    category: 'bat',
    region: '',
    speciesCount: 10,
    version: '1.0',
    channel: 'stable',
    installed: true,
    compatible: true,
    totalSizeBytes: 1_000_000,
    hasGeomodel: false,
  };
}

/** The text of the description a tab points at, or an empty string when it has none. */
function descriptionOf(tab: HTMLElement): string {
  const id = tab.getAttribute('aria-describedby');
  return id ? (document.getElementById(id)?.textContent ?? '') : '';
}

function settingsTab(): HTMLElement {
  return screen.getByRole('tab', { name: /analysis\.tabs\.settings/ });
}

function modelsTab(): HTMLElement {
  return screen.getByRole('tab', { name: /analysis\.tabs\.models/ });
}

// jsdom does not implement <dialog> showModal/close, which the page's dialogs call on mount.
beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement) {
    this.open = false;
  };
});

describe('AnalysisSettingsPage tab change dots', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    // The saved copy is never edited, so the form copy starts equal to it.
    mockStore.update(state => ({
      ...state,
      originalData: savedConfig(),
      formData: savedConfig(),
    }));
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [] });
    vi.mocked(modelsApi.fetchInstalled).mockResolvedValue([]);
    vi.mocked(modelsApi.fetchModelRegions).mockRejectedValue(new Error('no regions in test'));
    // Use jsdom's real Location so the router reads the query string.
    Object.defineProperty(window, 'location', {
      value: document.location,
      writable: true,
      configurable: true,
    });
    navigation.redirect('/ui/settings/analysis');
  });

  it.each([
    {
      name: 'a detection threshold edit lights only the Settings tab',
      patch: { threshold: 0.5 },
      settings: true,
      models: false,
    },
    {
      name: 'a download source edit lights only the Models tab',
      patch: { huggingFaceEndpoint: 'https://mirror.example.org' },
      settings: false,
      models: true,
    },
    {
      name: 'a location edit lights neither tab, since Main owns the Location tab',
      patch: { latitude: 61.5, longitude: 25.1 },
      settings: false,
      models: false,
    },
  ])('$name', async ({ patch, settings, models }) => {
    render(AnalysisSettingsPage);
    await tick();
    editBirdnet(patch);
    await tick();

    expect({
      settings: descriptionOf(settingsTab()),
      models: descriptionOf(modelsTab()),
    }).toEqual({ settings: settings ? HAS_CHANGES : '', models: models ? HAS_CHANGES : '' });
  });

  it('clears the dot when an edit is set back to the saved value', async () => {
    render(AnalysisSettingsPage);
    await tick();
    editBirdnet({ threshold: 0.5 });
    await tick();
    expect(descriptionOf(settingsTab())).toBe(HAS_CHANGES);

    editBirdnet({ threshold: 0.03 });
    await tick();
    expect(descriptionOf(settingsTab())).toBe('');
  });

  it('shows no dot when nothing is edited, ignoring the model cards hidden for lack of a model', async () => {
    // No bat, Perch or BirdNET v3.0 section in the saved config or the form, and none of those
    // models installed: their hidden cards must not light the Settings tab.
    render(AnalysisSettingsPage);
    await tick();
    expect(descriptionOf(settingsTab())).toBe('');
    expect(descriptionOf(modelsTab())).toBe('');
  });

  it.each([
    {
      name: 'a range filter edit',
      edit: () => editBirdnet({ rangeFilter: { threshold: EDITED_THRESHOLD } }),
    },
    {
      name: 'a dynamic threshold edit',
      edit: () =>
        editSection('realtime', {
          dynamicThreshold: { ...BASE_REALTIME.dynamicThreshold, trigger: EDITED_TRIGGER },
        }),
    },
  ])('$name lights only the Settings tab', async ({ edit }) => {
    render(AnalysisSettingsPage);
    await tick();
    expect(descriptionOf(settingsTab())).toBe('');

    edit();
    await tick();

    expect(descriptionOf(settingsTab())).toBe(HAS_CHANGES);
    expect(descriptionOf(modelsTab())).toBe('');
  });

  describe('model cards', () => {
    const modelCases = [
      {
        name: 'Perch',
        section: 'perch',
        saved: { overrideThreshold: false, threshold: 0.5 },
        edit: { overrideThreshold: true, threshold: EDITED_THRESHOLD },
        titleKey: SETTINGS_SECTION_TITLE.perch,
        install: () => {
          vi.mocked(modelsApi.fetchInstalled).mockResolvedValue([installedModel(PERCH_CATALOG_ID)]);
        },
      },
      {
        name: 'BirdNET v3.0',
        section: 'birdnetv3',
        saved: { overrideThreshold: false, threshold: 0.5 },
        edit: { overrideThreshold: true, threshold: EDITED_THRESHOLD },
        titleKey: SETTINGS_SECTION_TITLE.birdnetv3,
        install: () => {
          vi.mocked(modelsApi.fetchInstalled).mockResolvedValue([
            installedModel(BIRDNET_V3_CATALOG_ID),
          ]);
        },
      },
      {
        name: 'bat',
        section: 'bat',
        saved: { enabled: true, threshold: 0.5, nighttimeOnly: true },
        edit: { threshold: EDITED_THRESHOLD },
        titleKey: SETTINGS_SECTION_TITLE.bat,
        install: () => {
          vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [batCatalogEntry()] });
        },
      },
    ];

    it.each(modelCases)(
      'an edit of the $name card lights the Settings tab when the model is installed',
      async ({ section, saved, edit, titleKey, install }) => {
        install();
        seedSection(section, saved);
        render(AnalysisSettingsPage);
        await screen.findByText(titleKey);
        expect(descriptionOf(settingsTab())).toBe('');

        editSection(section, edit);
        await tick();

        expect(descriptionOf(settingsTab())).toBe(HAS_CHANGES);
        expect(descriptionOf(modelsTab())).toBe('');
      }
    );

    it.each(modelCases)(
      'an edit of the $name card does not light the Settings tab when the model is not installed',
      async ({ section, saved, edit, titleKey }) => {
        seedSection(section, saved);
        render(AnalysisSettingsPage);
        await waitFor(() => expect(modelsApi.fetchInstalled).toHaveBeenCalled());
        await tick();
        expect(screen.queryByText(titleKey)).toBeNull();

        editSection(section, edit);
        await tick();

        expect(descriptionOf(settingsTab())).toBe('');
      }
    );
  });
});
