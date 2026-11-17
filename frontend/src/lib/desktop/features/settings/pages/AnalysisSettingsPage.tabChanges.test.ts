import { describe, it, expect, beforeEach, beforeAll, vi } from 'vitest';
import { render, screen, cleanup, waitFor, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import userEvent from '@testing-library/user-event';
import type { CatalogEntry } from '$lib/types/models';
import type { BackendModel } from '$lib/stores/models.svelte';

// The Analysis page tabs show an unsaved-change dot when any card badge on the tab would.
// These tests pin which edits light which tab, using the same accessible description the
// Security page tests use (the dot itself is aria-hidden, its sr-only text describes the tab),
// and the detection thresholds card: the shared threshold, the custom threshold switch and the
// classifier rows built from the models list.

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

// The enabled models list (GET /api/v2/models) that drives the threshold rows. Tests set it
// before rendering.
const { modelsList } = vi.hoisted((): { modelsList: { current: BackendModel[] } } => ({
  modelsList: { current: [] },
}));
vi.mock('$lib/stores/models.svelte', () => ({
  getAvailableModels: vi.fn(() => modelsList.current),
  modelsLoading: vi.fn(() => false),
  refreshModels: vi.fn(),
  watchModels: vi.fn(() => () => {}),
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
    // No bat, Perch, BirdNET v3.0 or BirdNET+ section in the config unless a test seeds one,
    // as on a default install.
    batSettings: derived(mockStore, state => state.formData.bat),
    bsgBatSettings: derived(mockStore, state => state.formData.bsgbat),
    perchSettings: derived(mockStore, state => state.formData.perch),
    birdNetV3Settings: derived(mockStore, state => state.formData.birdnetv3),
    birdNetPlusSettings: derived(mockStore, state => state.formData.birdnetplus),
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
import { api } from '$lib/utils/api';
import { navigation } from '$lib/stores/navigation.svelte';
import { settingsActions } from '$lib/stores/settings';
import { modelsLoading } from '$lib/stores/models.svelte';
import { t } from '$lib/i18n';

const HAS_CHANGES = 'settings.tabs.hasChanges';
const BAT_CATALOG_ID = 'bat-test';
const BSG_BAT_CATALOG_ID = 'bsg-bat';
const EDITED_THRESHOLD = 0.7;
const EDITED_TRIGGER = 0.9;
const RANGE_STATUS_URL = '/api/v2/range/status';

/** Enabled models as GET /api/v2/models lists them. */
const MODEL = {
  birdnet: { id: 'birdnet', registryId: 'BirdNET_V2.4', name: 'BirdNET v2.4', category: 'bird' },
  bsg: { id: 'bsg', registryId: 'BSG', name: 'BSG Finland', category: 'bird' },
  perch: { id: 'perch_v2', registryId: 'Perch_V2', name: 'Google Perch v2', category: 'wildlife' },
  birdnetv3: {
    id: 'birdnet_v3.0',
    registryId: 'BirdNET_V3.0',
    name: 'BirdNET v3.0',
    category: 'wildlife',
  },
  birdnetplus: {
    id: 'birdnet_plus_v3.0',
    registryId: 'BirdNET_Plus_V3.0',
    name: 'BirdNET+ V3.0',
    category: 'wildlife',
  },
  bat: { id: 'bat', registryId: 'Bat', name: 'Bat Classifier', category: 'bat' },
  bsgbat: { id: 'bsg_bat', registryId: 'BSG_BAT', name: 'BSG-BAT', category: 'bat' },
} satisfies Record<string, BackendModel>;

/** Tabs that are always present, in page order. The Bats tab exists only with a bat model. */
const ALWAYS_TABS = ['detection', 'filters', 'inference', 'models'] as const;

/** A fresh copy of the saved config every test starts from. */
function savedConfig() {
  return { birdnet: { ...BASE_BIRDNET }, realtime: { ...BASE_REALTIME } };
}

/** Edit birdnet keys in the form copy only, as a control does. */
function editBirdnet(patch: Record<string, unknown>) {
  editSection('birdnet', patch);
}

/** Edit keys of a top-level config section in the form copy only. */
function editSection(section: string, patch: Record<string, unknown>) {
  mockStore.update(state => ({
    ...state,
    formData: { ...state.formData, [section]: { ...state.formData[section], ...patch } },
  }));
}

/** Make updateSection write to the form copy, as the real store does. */
function applyUpdatesToForm() {
  vi.mocked(settingsActions.updateSection).mockImplementation((section, patch) =>
    editSection(section, patch)
  );
}

/** Put a top-level config section into both the saved copy and the form copy. */
function seedSection(section: string, value: Record<string, unknown>) {
  mockStore.update(state => ({
    ...state,
    originalData: { ...state.originalData, [section]: { ...value } },
    formData: { ...state.formData, [section]: { ...value } },
  }));
}

function batCatalogEntry(id: string = BAT_CATALOG_ID): CatalogEntry {
  return {
    id,
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

function tab(id: string): HTMLElement {
  return screen.getByRole('tab', { name: new RegExp(`analysis\\.tabs\\.${id}`) });
}

/** Which of the always-present tabs show the change dot. */
function dottedTabs(): string[] {
  return ALWAYS_TABS.filter(id => descriptionOf(tab(id)) === HAS_CHANGES);
}

/** Params of the t() calls made for a key, in call order. */
function paramsFor(key: string): Array<Record<string, unknown>> {
  return vi
    .mocked(t)
    .mock.calls.filter(([k]) => k === key)
    .map(([, params]) => params ?? {});
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

/** Reset the store, the mocks and the URL; open the page on the given tab. */
function resetPage(tabId = 'detection') {
  cleanup();
  vi.clearAllMocks();
  // clearAllMocks keeps implementations, so drop the form-writing one applyUpdatesToForm installs.
  vi.mocked(settingsActions.updateSection).mockReset();
  modelsList.current = [];
  mockStore.update(state => ({ ...state, originalData: savedConfig(), formData: savedConfig() }));
  vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [] });
  vi.mocked(modelsApi.fetchModelRegions).mockRejectedValue(new Error('no regions in test'));
  vi.mocked(api.get).mockResolvedValue({});
  // Use jsdom's real Location so the router reads the query string.
  Object.defineProperty(window, 'location', {
    value: document.location,
    writable: true,
    configurable: true,
  });
  navigation.redirect(`/ui/settings/analysis?tab=${tabId}`);
}

describe('AnalysisSettingsPage tab change dots', () => {
  beforeEach(() => resetPage());

  // Each edit lights exactly the tab its card renders on. A change pair left in the wrong tab's
  // aggregate after a card moves would light the wrong tab.
  it.each([
    { name: 'shared threshold', edit: () => editBirdnet({ threshold: 0.5 }), tab: 'detection' },
    {
      name: 'dynamic threshold',
      edit: () =>
        editSection('realtime', {
          dynamicThreshold: { ...BASE_REALTIME.dynamicThreshold, trigger: EDITED_TRIGGER },
        }),
      tab: 'detection',
    },
    { name: 'species language', edit: () => editBirdnet({ locale: 'fi' }), tab: 'detection' },
    {
      name: 'false positive filter',
      edit: () => editSection('realtime', { falsePositiveFilter: { level: 3 } }),
      tab: 'filters',
    },
    {
      name: 'range filter',
      edit: () => editBirdnet({ rangeFilter: { threshold: EDITED_THRESHOLD } }),
      tab: 'filters',
    },
    { name: 'inference threads', edit: () => editBirdnet({ threads: 2 }), tab: 'inference' },
    {
      name: 'inference backend',
      edit: () => editBirdnet({ backend: 'openvino' }),
      tab: 'inference',
    },
    {
      name: 'download source',
      edit: () => editBirdnet({ huggingFaceEndpoint: 'https://mirror.example.org' }),
      tab: 'models',
    },
    // Main owns the Location tab, so a location edit lights none of these.
    { name: 'location', edit: () => editBirdnet({ latitude: 61.5, longitude: 25.1 }), tab: null },
  ])('a $name edit lights only its tab', async ({ edit, tab: expected }) => {
    render(AnalysisSettingsPage);
    await tick();
    expect(dottedTabs()).toEqual([]);

    edit();
    await tick();

    expect(dottedTabs()).toEqual(expected ? [expected] : []);
  });

  it('clears the dot when an edit is set back to the saved value', async () => {
    render(AnalysisSettingsPage);
    await tick();
    editBirdnet({ threshold: 0.5 });
    await tick();
    expect(dottedTabs()).toEqual(['detection']);

    editBirdnet({ threshold: BASE_BIRDNET.threshold });
    await tick();
    expect(dottedTabs()).toEqual([]);
  });

  it('treats an "auto" backend and an unset one as the same value', async () => {
    render(AnalysisSettingsPage);
    await tick();
    editBirdnet({ backend: 'auto', openVinoDevice: 'auto' });
    await tick();
    expect(dottedTabs()).toEqual([]);
  });

  describe('classifier threshold rows', () => {
    const rowCases = [
      {
        name: 'Perch',
        model: MODEL.perch,
        section: 'perch',
        saved: { overrideThreshold: false, threshold: 0.5 },
        edit: { overrideThreshold: true, threshold: EDITED_THRESHOLD },
      },
      {
        name: 'BirdNET v3.0',
        model: MODEL.birdnetv3,
        section: 'birdnetv3',
        saved: { overrideThreshold: false, threshold: 0.5 },
        edit: { overrideThreshold: true },
      },
      {
        name: 'BirdNET+ V3.0',
        model: MODEL.birdnetplus,
        section: 'birdnetplus',
        saved: { overrideThreshold: true, threshold: 0.5 },
        edit: { threshold: EDITED_THRESHOLD },
      },
      {
        name: 'bat',
        model: MODEL.bat,
        section: 'bat',
        saved: { enabled: true, threshold: 0.5, nighttimeOnly: true },
        edit: { threshold: EDITED_THRESHOLD },
      },
      {
        name: 'BSG-BAT',
        model: MODEL.bsgbat,
        section: 'bsgbat',
        saved: { threshold: 0.5 },
        edit: { threshold: EDITED_THRESHOLD },
      },
    ];

    it.each(rowCases)(
      'a $name threshold edit lights the Detection tab while the classifier is enabled',
      async ({ model, section, saved, edit }) => {
        modelsList.current = [model];
        seedSection(section, saved);
        render(AnalysisSettingsPage);
        await screen.findByTestId(`threshold-row-${model.id}`);
        expect(dottedTabs()).toEqual([]);

        editSection(section, edit);
        await tick();

        expect(dottedTabs()).toEqual(['detection']);
      }
    );

    it.each(rowCases)(
      'a $name threshold edit lights no tab while the classifier is not enabled',
      async ({ model, section, saved, edit }) => {
        seedSection(section, saved);
        render(AnalysisSettingsPage);
        await tick();
        expect(screen.queryByTestId(`threshold-row-${model.id}`)).toBeNull();

        editSection(section, edit);
        await tick();

        expect(dottedTabs()).toEqual([]);
      }
    );
  });

  describe('Bats tab', () => {
    it('is absent while no bat model is installed', async () => {
      render(AnalysisSettingsPage);
      await waitFor(() => expect(modelsApi.fetchCatalog).toHaveBeenCalled());
      await tick();
      expect(screen.queryByRole('tab', { name: /analysis\.tabs\.bats/ })).toBeNull();
    });

    it('lights for a bat card edit and leaves the other tabs alone', async () => {
      vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [batCatalogEntry()] });
      seedSection('bat', { enabled: true, threshold: 0.5, nighttimeOnly: true });
      render(AnalysisSettingsPage);
      const batsTab = await screen.findByRole('tab', { name: /analysis\.tabs\.bats/ });
      expect(descriptionOf(batsTab)).toBe('');

      editSection('bat', { nighttimeOnly: false });
      await tick();

      expect(descriptionOf(batsTab)).toBe(HAS_CHANGES);
      expect(dottedTabs()).toEqual([]);
    });

    it('shows no dot for BSG-BAT while no bsgbat section is saved', async () => {
      // A server always sends the bat section; the bsgbat section is the one left out.
      modelsList.current = [MODEL.bsgbat];
      seedSection('bat', { enabled: true, threshold: 0.5, nighttimeOnly: true });
      vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
        catalog: [batCatalogEntry(BSG_BAT_CATALOG_ID)],
      });
      render(AnalysisSettingsPage);
      const batsTab = await screen.findByRole('tab', { name: /analysis\.tabs\.bats/ });
      await tick();

      expect(descriptionOf(batsTab)).toBe('');
      expect(dottedTabs()).toEqual([]);
    });
  });
});

describe('AnalysisSettingsPage detection thresholds', () => {
  beforeEach(() => resetPage());

  const SHARED_LABEL = 'analysis.thresholds.shared.label';

  it('shows the shared threshold while BirdNET v2.4 is not enabled', async () => {
    modelsList.current = [MODEL.birdnetplus];
    render(AnalysisSettingsPage);
    await screen.findByTestId('threshold-row-shared');

    expect(screen.queryByTestId(`threshold-row-${MODEL.birdnet.id}`)).toBeNull();
    expect(paramsFor('analysis.thresholds.shared.usedBy').at(-1)).toEqual({
      models: MODEL.birdnetplus.name,
    });
  });

  it('lists only the classifiers that use the shared threshold', async () => {
    modelsList.current = [MODEL.birdnet, MODEL.perch, MODEL.bsg, MODEL.bat];
    seedSection('perch', { overrideThreshold: true, threshold: 0.6 });
    render(AnalysisSettingsPage);
    await screen.findByTestId('threshold-row-shared');

    expect(paramsFor('analysis.thresholds.shared.usedBy').at(-1)).toEqual({
      models: `${MODEL.birdnet.name}, ${MODEL.bsg.name}`,
    });
  });

  it('shows a loading help on the shared row while the models list loads', async () => {
    const original = vi.mocked(modelsLoading).getMockImplementation();
    vi.mocked(modelsLoading).mockReturnValue(true);
    try {
      render(AnalysisSettingsPage);
      const row = await screen.findByTestId('threshold-row-shared');

      expect(within(row).getByText('common.loading')).toBeInTheDocument();
      expect(within(row).queryByText('analysis.thresholds.shared.unused')).toBeNull();
    } finally {
      vi.mocked(modelsLoading).mockReset();
      if (original) vi.mocked(modelsLoading).mockImplementation(original);
    }
  });

  it('says so when no enabled classifier uses the shared threshold', async () => {
    modelsList.current = [MODEL.bat];
    render(AnalysisSettingsPage);
    await screen.findByTestId('threshold-row-shared');

    expect(screen.getByText('analysis.thresholds.shared.unused')).toBeInTheDocument();
  });

  it('gives no row to a classifier without a known threshold', async () => {
    modelsList.current = [
      MODEL.birdnet,
      { id: 'mystery', registryId: 'Mystery_V1', name: 'Mystery', category: 'bird' },
    ];
    render(AnalysisSettingsPage);
    await screen.findByTestId(`threshold-row-${MODEL.birdnet.id}`);

    expect(screen.queryByTestId('threshold-row-mystery')).toBeNull();
  });

  it('saves a shared threshold edit to the birdnet section, rounded to the step', async () => {
    render(AnalysisSettingsPage);
    const input = await screen.findByRole('spinbutton', { name: SHARED_LABEL });

    const user = userEvent.setup();
    await user.clear(input);
    await user.type(input, '0.456');
    await user.tab();

    expect(settingsActions.updateSection).toHaveBeenLastCalledWith('birdnet', { threshold: 0.46 });
  });

  it('shows the shared value, disabled, on a row that uses it', async () => {
    modelsList.current = [MODEL.perch];
    seedSection('perch', { overrideThreshold: false, threshold: 0.6 });
    render(AnalysisSettingsPage);
    const input = await screen.findByRole('spinbutton', { name: MODEL.perch.name });

    expect(input).toBeDisabled();
    expect(input).toHaveValue(BASE_BIRDNET.threshold);
  });

  it('switches a classifier to its custom threshold, keeping a saved value in range', async () => {
    modelsList.current = [MODEL.perch];
    seedSection('perch', { overrideThreshold: false, threshold: 0.6 });
    render(AnalysisSettingsPage);
    const toggle = await screen.findByRole('switch', { name: new RegExp(MODEL.perch.name) });

    await userEvent.setup().click(toggle);

    expect(settingsActions.updateSection).toHaveBeenLastCalledWith('perch', {
      overrideThreshold: true,
    });
  });

  it('starts a custom threshold from the shared value when the saved one is out of range', async () => {
    modelsList.current = [MODEL.birdnetv3];
    // A saved 0 is accepted by the backend but below the row's minimum.
    seedSection('birdnetv3', { overrideThreshold: false, threshold: 0 });
    render(AnalysisSettingsPage);
    const toggle = await screen.findByRole('switch', { name: new RegExp(MODEL.birdnetv3.name) });

    await userEvent.setup().click(toggle);

    expect(settingsActions.updateSection).toHaveBeenLastCalledWith('birdnetv3', {
      overrideThreshold: true,
      threshold: BASE_BIRDNET.threshold,
    });
  });

  it('returns to the saved values when a seeded custom threshold is switched off again', async () => {
    modelsList.current = [MODEL.birdnetv3];
    seedSection('birdnetv3', { overrideThreshold: false, threshold: 0 });
    applyUpdatesToForm();
    render(AnalysisSettingsPage);
    const toggle = await screen.findByRole('switch', { name: new RegExp(MODEL.birdnetv3.name) });
    const user = userEvent.setup();

    await user.click(toggle);
    await tick();
    expect(dottedTabs()).toEqual(['detection']);
    await user.click(toggle);
    await tick();

    expect(dottedTabs()).toEqual([]);
    expect(settingsActions.updateSection).toHaveBeenLastCalledWith('birdnetv3', {
      overrideThreshold: false,
      threshold: 0,
    });
  });

  it('keeps a value typed into a seeded custom threshold when the switch goes off', async () => {
    modelsList.current = [MODEL.birdnetv3];
    seedSection('birdnetv3', { overrideThreshold: false, threshold: 0 });
    applyUpdatesToForm();
    render(AnalysisSettingsPage);
    const toggle = await screen.findByRole('switch', { name: new RegExp(MODEL.birdnetv3.name) });
    const user = userEvent.setup();

    await user.click(toggle);
    const input = screen.getByRole('spinbutton', { name: MODEL.birdnetv3.name });
    await user.clear(input);
    await user.type(input, '0.8');
    await user.tab();
    await user.click(toggle);

    expect(settingsActions.updateSection).toHaveBeenLastCalledWith('birdnetv3', {
      overrideThreshold: false,
    });
  });

  it('removes a seeded threshold again when none was saved', async () => {
    modelsList.current = [MODEL.birdnetv3];
    seedSection('birdnetv3', { overrideThreshold: false });
    applyUpdatesToForm();
    render(AnalysisSettingsPage);
    const toggle = await screen.findByRole('switch', { name: new RegExp(MODEL.birdnetv3.name) });
    const user = userEvent.setup();

    await user.click(toggle);
    await user.click(toggle);
    await tick();

    expect(dottedTabs()).toEqual([]);
    expect(settingsActions.updateSection).toHaveBeenLastCalledWith('birdnetv3', {
      overrideThreshold: false,
      threshold: undefined,
    });
  });

  it('leaves a saved custom threshold alone when the switch goes off after a save', async () => {
    modelsList.current = [MODEL.birdnetv3];
    seedSection('birdnetv3', { overrideThreshold: false, threshold: 0 });
    applyUpdatesToForm();
    render(AnalysisSettingsPage);
    const toggle = await screen.findByRole('switch', { name: new RegExp(MODEL.birdnetv3.name) });
    const user = userEvent.setup();

    await user.click(toggle);
    // Saving makes the seeded value the saved one.
    seedSection('birdnetv3', { overrideThreshold: true, threshold: BASE_BIRDNET.threshold });
    await tick();
    await user.click(toggle);

    expect(settingsActions.updateSection).toHaveBeenLastCalledWith('birdnetv3', {
      overrideThreshold: false,
    });
  });

  it('saves a typed classifier threshold rounded to the step, to its own section only', async () => {
    modelsList.current = [MODEL.bsgbat];
    seedSection('bsgbat', { threshold: 0.5 });
    render(AnalysisSettingsPage);
    const input = await screen.findByRole('spinbutton', { name: MODEL.bsgbat.name });

    const user = userEvent.setup();
    await user.clear(input);
    await user.type(input, '0.736');
    await user.tab();

    expect(settingsActions.updateSection).toHaveBeenLastCalledWith('bsgbat', { threshold: 0.74 });
    expect(
      vi.mocked(settingsActions.updateSection).mock.calls.map(([section]) => section)
    ).not.toContain('bat');
  });
});

describe('AnalysisSettingsPage inference runtime', () => {
  const INFERENCE_URL = '/api/v2/system/inference';
  const BACKEND_LABEL = 'analysis.inference.backend.label';
  const DEVICE_LABEL = 'analysis.inference.device.label';

  /** A runtime status as GET /api/v2/system/inference reports it. */
  function runtimeStatus({ onnx = true, openvino = true, devices = ['CPU'] } = {}) {
    return {
      backends: {
        tflite: { available: true },
        onnx: { available: onnx },
        openvino: { supported: openvino, active: false, devices },
      },
    };
  }

  /** Answer the runtime request with the given handler and every other GET with {}. */
  function mockRuntime(handler: () => Promise<unknown>) {
    vi.mocked(api.get).mockImplementation(async (url: string) =>
      url === INFERENCE_URL ? handler() : {}
    );
  }

  /** Labels of the options a select offers, read from its open list. */
  async function optionLabels(select: HTMLElement): Promise<string[]> {
    await userEvent.setup().click(select);
    return screen.getAllByRole('option').map(option => option.textContent.trim());
  }

  beforeEach(() => resetPage('inference'));

  it('says why the runtime selects are disabled while the runtime list loads', async () => {
    mockRuntime(() => new Promise(() => {}));
    render(AnalysisSettingsPage);
    const backend = await screen.findByRole('combobox', { name: BACKEND_LABEL });

    expect(backend).toBeDisabled();
    const status = screen.getByText('analysis.inference.backendsLoading');
    expect(backend.getAttribute('aria-describedby')?.split(' ')).toContain(status.id);
  });

  it('retries a failed runtime lookup from the warning and enables the selects', async () => {
    // Every lookup fails until the test lets them through, so only Retry can load the list.
    let failing = true;
    mockRuntime(async () => {
      if (failing) throw new Error('runtime lookup failed');
      return runtimeStatus();
    });
    render(AnalysisSettingsPage);
    const warning = await screen.findByText('analysis.inference.backendsUnavailable');
    const backend = screen.getByRole('combobox', { name: BACKEND_LABEL });
    expect(backend).toBeDisabled();
    const alert = warning.closest('[id]');
    expect(alert).not.toBeNull();
    expect(backend.getAttribute('aria-describedby')?.split(' ')).toContain(alert?.id);

    failing = false;
    await userEvent.setup().click(screen.getByRole('button', { name: 'common.retry' }));

    await waitFor(() => expect(backend).toBeEnabled());
    expect(warning).not.toBeInTheDocument();
    await waitFor(() => expect(backend).toHaveFocus());
  });

  it('offers only Auto when the host reports neither ONNX Runtime nor OpenVINO', async () => {
    mockRuntime(async () => runtimeStatus({ onnx: false, openvino: false }));
    render(AnalysisSettingsPage);
    const backend = await screen.findByRole('combobox', { name: BACKEND_LABEL });
    await waitFor(() => expect(backend).toBeEnabled());

    expect(await optionLabels(backend)).toEqual(['analysis.inference.backend.options.auto']);
    expect(screen.queryByRole('combobox', { name: DEVICE_LABEL })).toBeNull();
  });

  it('keeps a saved backend the host lacks and hides the device for ONNX Runtime', async () => {
    seedSection('birdnet', { ...BASE_BIRDNET, backend: 'onnx', openVinoDevice: 'gpu' });
    mockRuntime(async () => runtimeStatus({ onnx: false }));
    render(AnalysisSettingsPage);
    const backend = await screen.findByRole('combobox', { name: BACKEND_LABEL });
    await waitFor(() => expect(backend).toBeEnabled());

    expect(await optionLabels(backend)).toContain('analysis.inference.backend.options.onnx');
    expect(screen.queryByRole('combobox', { name: DEVICE_LABEL })).toBeNull();
  });

  it('restores the unset saved backend when Auto is picked again', async () => {
    mockRuntime(async () => runtimeStatus());
    applyUpdatesToForm();
    render(AnalysisSettingsPage);
    const backend = await screen.findByRole('combobox', { name: BACKEND_LABEL });
    await waitFor(() => expect(backend).toBeEnabled());
    const user = userEvent.setup();

    await user.click(backend);
    await user.click(
      screen.getByRole('option', { name: 'analysis.inference.backend.options.onnx' })
    );
    await user.click(backend);
    await user.click(
      screen.getByRole('option', { name: 'analysis.inference.backend.options.auto' })
    );

    // The saved config has no backend key, so the form must not keep a raw 'auto' that a
    // whole-form comparison would count as an unsaved change.
    expect(settingsActions.updateSection).toHaveBeenLastCalledWith('birdnet', {
      backend: undefined,
    });
  });

  it('restores the unset saved device when Auto is picked again', async () => {
    mockRuntime(async () => runtimeStatus());
    applyUpdatesToForm();
    render(AnalysisSettingsPage);
    const device = await screen.findByRole('combobox', { name: DEVICE_LABEL });
    await waitFor(() => expect(device).toBeEnabled());
    const user = userEvent.setup();

    await user.click(device);
    await user.click(screen.getByRole('option', { name: 'analysis.inference.device.options.cpu' }));
    await user.click(device);
    await user.click(
      screen.getByRole('option', { name: 'analysis.inference.device.options.auto' })
    );

    expect(settingsActions.updateSection).toHaveBeenLastCalledWith('birdnet', {
      openVinoDevice: undefined,
    });
  });

  it('keeps an explicit saved auto backend when Auto is picked again', async () => {
    seedSection('birdnet', { ...BASE_BIRDNET, backend: 'auto' });
    mockRuntime(async () => runtimeStatus());
    applyUpdatesToForm();
    render(AnalysisSettingsPage);
    const backend = await screen.findByRole('combobox', { name: BACKEND_LABEL });
    await waitFor(() => expect(backend).toBeEnabled());
    const user = userEvent.setup();

    await user.click(backend);
    await user.click(
      screen.getByRole('option', { name: 'analysis.inference.backend.options.onnx' })
    );
    await user.click(backend);
    await user.click(
      screen.getByRole('option', { name: 'analysis.inference.backend.options.auto' })
    );

    expect(settingsActions.updateSection).toHaveBeenLastCalledWith('birdnet', { backend: 'auto' });
  });

  it('offers GPU only when the host has one or it is saved', async () => {
    mockRuntime(async () => runtimeStatus({ devices: ['CPU'] }));
    render(AnalysisSettingsPage);
    const device = await screen.findByRole('combobox', { name: DEVICE_LABEL });
    await waitFor(() => expect(device).toBeEnabled());
    expect(await optionLabels(device)).not.toContain('analysis.inference.device.options.gpu');

    resetPage('inference');
    seedSection('birdnet', { ...BASE_BIRDNET, openVinoDevice: 'gpu' });
    mockRuntime(async () => runtimeStatus({ devices: ['CPU'] }));
    render(AnalysisSettingsPage);
    const savedGpu = await screen.findByRole('combobox', { name: DEVICE_LABEL });
    await waitFor(() => expect(savedGpu).toBeEnabled());
    expect(await optionLabels(savedGpu)).toContain('analysis.inference.device.options.gpu');

    resetPage('inference');
    mockRuntime(async () => runtimeStatus({ devices: ['CPU', 'GPU.0'] }));
    render(AnalysisSettingsPage);
    const hostGpu = await screen.findByRole('combobox', { name: DEVICE_LABEL });
    await waitFor(() => expect(hostGpu).toBeEnabled());
    expect(await optionLabels(hostGpu)).toContain('analysis.inference.device.options.gpu');
  });
});

describe('AnalysisSettingsPage range filter scope', () => {
  beforeEach(() => resetPage('filters'));

  it('names only the classifiers the active range backend filters', async () => {
    modelsList.current = [MODEL.birdnet, MODEL.perch, MODEL.bat];
    vi.mocked(api.get).mockImplementation(async (url: string) =>
      url === RANGE_STATUS_URL
        ? {
            geomodel: null,
            classifiers: [
              { id: 'BirdNET_V2.4', name: MODEL.birdnet.name, coveredByBackend: true },
              { id: 'Perch_V2', name: MODEL.perch.name, coveredByBackend: false },
            ],
          }
        : {}
    );
    render(AnalysisSettingsPage);

    await waitFor(() =>
      expect(paramsFor('analysis.scope.rangeFilter.appliesTo').at(-1)).toEqual({
        models: MODEL.birdnet.name,
      })
    );
    expect(paramsFor('analysis.scope.rangeFilter.notApplied').at(-1)).toEqual({
      models: `${MODEL.perch.name}, ${MODEL.bat.name}`,
    });
  });
});

describe('AnalysisSettingsPage range filter overview', () => {
  beforeEach(() => resetPage('filters'));

  it('lists only the classifiers the active range backend covers', async () => {
    modelsList.current = [MODEL.birdnet, MODEL.perch];
    const classifier = (id: string, name: string, coveredByBackend: boolean) => ({
      id,
      name,
      coveredByBackend,
      totalSpecies: 100,
      withRangeData: 90,
      withoutRangeData: 10,
    });
    vi.mocked(api.get).mockImplementation(async (url: string) =>
      url === RANGE_STATUS_URL
        ? {
            geomodel: { version: '1', totalSpecies: 100, autoSelected: true },
            classifiers: [
              classifier('BirdNET_V2.4', MODEL.birdnet.name, true),
              classifier('Perch_V2', MODEL.perch.name, false),
            ],
          }
        : {}
    );
    render(AnalysisSettingsPage);

    const table = await screen.findByRole('table');
    expect(within(table).getByText(MODEL.birdnet.name)).toBeInTheDocument();
    expect(within(table).queryByText(MODEL.perch.name)).toBeNull();
  });

  it('lists a classifier whose coverage flag is missing and hides the table when none is covered', async () => {
    modelsList.current = [MODEL.birdnet, MODEL.perch];
    const status = (classifiers: Array<Record<string, unknown>>) => ({
      geomodel: { version: '1', totalSpecies: 100, autoSelected: true },
      classifiers,
    });
    const row = { totalSpecies: 100, withRangeData: 90, withoutRangeData: 10 };
    vi.mocked(api.get).mockImplementation(async (url: string) =>
      url === RANGE_STATUS_URL
        ? status([
            { id: 'BirdNET_V2.4', name: MODEL.birdnet.name, ...row },
            { id: 'Perch_V2', name: MODEL.perch.name, coveredByBackend: false, ...row },
          ])
        : {}
    );
    render(AnalysisSettingsPage);
    const table = await screen.findByRole('table');
    expect(within(table).getByText(MODEL.birdnet.name)).toBeInTheDocument();

    cleanup();
    vi.mocked(api.get).mockImplementation(async (url: string) =>
      url === RANGE_STATUS_URL
        ? status([{ id: 'Perch_V2', name: MODEL.perch.name, coveredByBackend: false, ...row }])
        : {}
    );
    render(AnalysisSettingsPage);
    await screen.findByText('analysis.rangeFilter.status.title');
    expect(screen.queryByRole('table')).toBeNull();
  });
});

describe('AnalysisSettingsPage dynamic threshold', () => {
  beforeEach(() => resetPage());

  const MIN_ABOVE_TRIGGER = 'analysis.dynamicThreshold.minAboveTrigger';

  function seedDynamic(patch: Record<string, unknown>) {
    editSection('realtime', {
      dynamicThreshold: { ...BASE_REALTIME.dynamicThreshold, enabled: true, ...patch },
    });
  }

  it('flags a minimum above the trigger on the minimum field', async () => {
    seedDynamic({ trigger: 0.5, min: 0.9 });
    render(AnalysisSettingsPage);

    const input = await screen.findByRole('spinbutton', {
      name: 'settings.main.sections.dynamicThreshold.minimum.label',
    });
    const message = await screen.findByText(MIN_ABOVE_TRIGGER);
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input.getAttribute('aria-describedby')?.split(' ')).toContain(message.id);
    expect(screen.getAllByText(MIN_ABOVE_TRIGGER)).toHaveLength(1);
    expect(
      screen.getByRole('spinbutton', {
        name: 'settings.main.sections.dynamicThreshold.trigger.label',
      })
    ).not.toHaveAttribute('aria-invalid');
  });

  it('shows no error while the minimum does not exceed the trigger', async () => {
    seedDynamic({ trigger: 0.5, min: 0.5 });
    render(AnalysisSettingsPage);
    await screen.findByRole('spinbutton', {
      name: 'settings.main.sections.dynamicThreshold.minimum.label',
    });

    expect(screen.queryByText(MIN_ABOVE_TRIGGER)).toBeNull();
  });
});

describe('AnalysisSettingsPage species language', () => {
  beforeEach(() => resetPage());

  it('renders the shared, classifier and language row labels as blocks, so wrapped lines keep the text-sm line height', async () => {
    modelsList.current = [MODEL.perch];
    render(AnalysisSettingsPage);
    await screen.findByRole('combobox', { name: 'analysis.detection.locale.label' });

    const labels = [
      'analysis.thresholds.shared.label',
      'analysis.detection.locale.label',
      MODEL.perch.name,
    ].map(text => screen.getAllByText(text).find(el => el.tagName === 'SPAN'));
    for (const label of labels) expect(label).toHaveClass('block', 'text-sm');
  });

  it('is a setting row with the label and help beside the select', async () => {
    render(AnalysisSettingsPage);
    const select = await screen.findByRole('combobox', { name: 'analysis.detection.locale.label' });

    const row = select.closest('li');
    if (!row) throw new Error('the species language select is not inside a setting row');
    expect(within(row).getByText('analysis.detection.locale.helpText')).toBeVisible();
    expect(select.getAttribute('aria-describedby')).toBeTruthy();
  });
});

describe('AnalysisSettingsPage bat card', () => {
  const BSG_BAT_WARNING = 'analysis.detection.bsgBatThreshold.sampleRateWarning';
  const DISCARD_LABEL = 'analysis.detection.batDiscardUnlikely.label';
  const FILTER_FIELDS = {
    cvThreshold: 0.15,
    fftSize: 8192,
    hopSize: 4096,
    frequencySplitHz: 20000,
  };

  beforeEach(() => resetPage('bats'));

  async function renderWithCatalog(...ids: string[]) {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: ids.map(id => batCatalogEntry(id)),
    });
    render(AnalysisSettingsPage);
    await screen.findByText('analysis.bat.title');
  }

  function seedBat(ultrasonicFilter: Record<string, unknown>) {
    seedSection('bat', { enabled: true, threshold: 0.5, nighttimeOnly: true, ultrasonicFilter });
  }

  it('shows the BSG-BAT sample rate warning only while BSG-BAT is installed', async () => {
    await renderWithCatalog(BAT_CATALOG_ID);
    expect(screen.queryByText(BSG_BAT_WARNING)).toBeNull();

    cleanup();
    await renderWithCatalog(BSG_BAT_CATALOG_ID);
    expect(screen.getByText(BSG_BAT_WARNING)).toBeInTheDocument();
  });

  it('shows the discard unlikely switch only while post detection validation is on', async () => {
    seedBat({ enabled: false });
    await renderWithCatalog(BAT_CATALOG_ID);
    expect(screen.queryByText(DISCARD_LABEL)).toBeNull();

    cleanup();
    seedBat({ enabled: true });
    await renderWithCatalog(BAT_CATALOG_ID);
    expect(screen.getByText(DISCARD_LABEL)).toBeInTheDocument();
  });

  it('saves the discard unlikely switch without dropping the other filter fields', async () => {
    const seeded = { enabled: true, ...FILTER_FIELDS };
    seedBat(seeded);
    await renderWithCatalog(BAT_CATALOG_ID);

    await userEvent.setup().click(screen.getByRole('switch', { name: DISCARD_LABEL }));

    expect(settingsActions.updateSection).toHaveBeenLastCalledWith('bat', {
      ultrasonicFilter: { ...seeded, discardUnlikely: true },
    });
  });
});
