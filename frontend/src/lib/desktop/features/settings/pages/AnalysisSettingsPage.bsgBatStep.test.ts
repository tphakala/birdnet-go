import { describe, it, expect, beforeEach, beforeAll, vi } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { tick } from 'svelte';
import userEvent from '@testing-library/user-event';
import { t } from '$lib/i18n';
import type { CatalogEntry } from '$lib/types/models';

// The BSG-BAT analysis step field on the Analysis page: where it shows, what it displays for a
// saved value of 0, how it saves and tracks changes, when it asks for a fresh cadence readout,
// and the confirmation counts the bat filter shows for the step each bat model runs at.

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

vi.mock('$lib/stores/acousticModels.svelte', () => ({
  analysisCadence: () => null,
  invalidateAcousticModels: vi.fn().mockResolvedValue(undefined),
  watchAcousticModels: vi.fn(() => () => {}),
}));

vi.mock('$lib/desktop/features/settings/components/ModelRegionSelector.svelte');

// The mocked store keeps its config as loose sections so a test can leave the bsgbat section out.
type SectionMap = Record<string, Record<string, unknown>>;
interface MockSettingsState {
  isLoading: boolean;
  isSaving: boolean;
  error: null;
  originalData: SectionMap;
  formData: SectionMap;
}

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
    batSettings: derived(mockStore, state => state.formData.bat),
    bsgBatSettings: derived(mockStore, state => state.formData.bsgbat),
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
import { settingsActions } from '$lib/stores/settings';
import { invalidateAcousticModels } from '$lib/stores/acousticModels.svelte';
import { navigation } from '$lib/stores/navigation.svelte';

const STEP_LABEL = 'analysis.detection.bsgBatStep.label';
const BAT = 'analysis.detection.batFalsePositiveFilter';
const STEP = 'analysis.detection.bsgBatStep';
const BAT_TITLE = 'analysis.bat.title';
const HAS_CHANGES = 'settings.tabs.hasChanges';
const BATTY_CATALOG_ID = 'bat-test';
const BSG_BAT_CATALOG_ID = 'bsg-bat';
const MODERATE = 2;
const STRICT = 4;

function savedConfig() {
  return {
    birdnet: { ...BASE_BIRDNET },
    realtime: { ...BASE_REALTIME },
    bat: { enabled: true, threshold: 0.5, nighttimeOnly: true, falsePositiveFilter: { level: 0 } },
  };
}

/** Put a section into both the saved copy and the form copy. */
function seedSection(section: string, value: Record<string, unknown>) {
  mockStore.update(state => ({
    ...state,
    originalData: { ...state.originalData, [section]: { ...value } },
    formData: { ...state.formData, [section]: { ...value } },
  }));
}

/** Edit keys of a section in the form copy only, as a control does. */
function editSection(section: string, patch: Record<string, unknown>) {
  mockStore.update(state => ({
    ...state,
    formData: { ...state.formData, [section]: { ...state.formData[section], ...patch } },
  }));
}

/** Change keys of a section in the saved copy only, as a save does. */
function saveSection(section: string, patch: Record<string, unknown>) {
  mockStore.update(state => ({
    ...state,
    originalData: {
      ...state.originalData,
      [section]: { ...state.originalData[section], ...patch },
    },
  }));
}

function batCatalogEntry(id: string): CatalogEntry {
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

async function renderWithCatalog(...ids: string[]) {
  vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
    catalog: ids.map(id => batCatalogEntry(id)),
  });
  render(AnalysisSettingsPage);
  await screen.findByText(BAT_TITLE);
}

function descriptionOf(tab: HTMLElement): string {
  const id = tab.getAttribute('aria-describedby');
  return id ? (document.getElementById(id)?.textContent ?? '') : '';
}

function settingsTab(): HTMLElement {
  return screen.getByRole('tab', { name: /analysis\.tabs\.settings/ });
}

/** Params of the t() calls made for a key, in call order. */
function paramsFor(key: string): Array<Record<string, unknown>> {
  return vi
    .mocked(t)
    .mock.calls.filter(([k]) => k === key)
    .map(([, params]) => params ?? {});
}

function stepInput(): HTMLInputElement {
  return screen.getByLabelText<HTMLInputElement>(STEP_LABEL);
}

/** Type a value into the step field and leave it, as a user does. */
async function typeStep(value: string) {
  const user = userEvent.setup();
  await user.clear(stepInput());
  await user.type(stepInput(), value);
  await user.tab();
}

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement) {
    this.open = false;
  };
});

describe('AnalysisSettingsPage BSG-BAT analysis step', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    vi.mocked(t).mockClear();
    mockStore.update(state => ({
      ...state,
      originalData: savedConfig(),
      formData: savedConfig(),
    }));
    // Apply an update to the form copy, as the real store does, so the tests can compare the
    // form with the saved copy the way hasUnsavedChanges does.
    vi.mocked(settingsActions.updateSection).mockImplementation((section, data) => {
      editSection(section, { ...data });
    });
    vi.mocked(modelsApi.fetchInstalled).mockResolvedValue([]);
    vi.mocked(modelsApi.fetchModelRegions).mockRejectedValue(new Error('no regions in test'));
    Object.defineProperty(window, 'location', {
      value: document.location,
      writable: true,
      configurable: true,
    });
    navigation.redirect('/ui/settings/analysis');
  });

  describe('visibility and shown value', () => {
    it('is not shown when only a BattyBirdNET model is installed', async () => {
      await renderWithCatalog(BATTY_CATALOG_ID);

      expect(screen.queryByLabelText(STEP_LABEL)).toBeNull();
    });

    it('is shown with the bounds of the backend range when BSG-BAT is installed', async () => {
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 300 });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);

      expect(stepInput()).toHaveAttribute('min', '100');
      expect(stepInput()).toHaveAttribute('max', '512');
      expect(stepInput()).toHaveAttribute('step', '1');
      expect(stepInput()).toHaveValue(300);
    });

    it.each([
      { name: 'a saved 0', section: { threshold: 0.5, stepFrames: 0 } },
      { name: 'no saved step', section: { threshold: 0.5 } },
    ])('shows the default of 250 frames for $name', async ({ section }) => {
      seedSection('bsgbat', section);
      await renderWithCatalog(BSG_BAT_CATALOG_ID);

      expect(stepInput()).toHaveValue(250);
    });

    it.each([
      { name: 'a stored 0', stored: 0 },
      { name: 'a removed value', stored: undefined },
      { name: 'a value below the range', stored: 99 },
      { name: 'a value above the range', stored: 513 },
      { name: 'a fractional value', stored: 300.5 },
    ])('falls back to 250 frames when the form value becomes $name', async ({ stored }) => {
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 300 });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);
      expect(stepInput()).toHaveValue(300);

      editSection('bsgbat', { stepFrames: stored });

      await waitFor(() => expect(stepInput()).toHaveValue(250));
    });

    it('shows the step in seconds and the confirmation count of the bat filter level', async () => {
      seedSection('bat', {
        enabled: true,
        threshold: 0.5,
        nighttimeOnly: true,
        falsePositiveFilter: { level: MODERATE },
      });
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 250 });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);

      expect(paramsFor(`${STEP}.readoutWithFilter`).at(-1)).toMatchObject({
        seconds: '0.50',
        count: '4',
      });

      editSection('bsgbat', { stepFrames: 512 });
      await tick();
      expect(paramsFor(`${STEP}.readoutWithFilter`).at(-1)).toMatchObject({
        seconds: '1.02',
        count: '2',
      });
    });

    it('shows only the step in seconds while the bat filter is off', async () => {
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 375 });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);

      expect(paramsFor(`${STEP}.readout`).at(-1)).toMatchObject({ seconds: '0.75' });
      expect(paramsFor(`${STEP}.readoutWithFilter`)).toEqual([]);
    });
  });

  describe('editing', () => {
    it('saves an edit to the bsgbat section only', async () => {
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 250 });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);

      await typeStep('375');

      expect(settingsActions.updateSection).toHaveBeenLastCalledWith('bsgbat', {
        stepFrames: 375,
      });
      expect(
        vi.mocked(settingsActions.updateSection).mock.calls.map(([section]) => section)
      ).not.toContain('bat');
    });

    it.each([
      { typed: '50', saved: 100 },
      { typed: '600', saved: 512 },
    ])('clamps $typed frames to $saved', async ({ typed, saved }) => {
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 250 });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);

      await typeStep(typed);

      expect(settingsActions.updateSection).toHaveBeenLastCalledWith('bsgbat', {
        stepFrames: saved,
      });
    });

    it('rounds a fractional entry to a whole frame', async () => {
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 250 });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);

      await typeStep('300.6');

      expect(settingsActions.updateSection).toHaveBeenLastCalledWith('bsgbat', {
        stepFrames: 301,
      });
    });
  });

  describe('change tracking', () => {
    /** The global unsaved-changes test: the raw form copy against the raw saved copy. */
    function formDiffersFromSaved(): boolean {
      const state = get(mockStore);
      return JSON.stringify(state.formData) !== JSON.stringify(state.originalData);
    }

    it.each([
      { name: 'the default saved as 0', saved: 0 },
      { name: 'an explicitly saved 250', saved: 250 },
    ])('leaves nothing unsaved when $name is edited and set back', async ({ saved }) => {
      seedSection('bsgbat', { threshold: 0.5, stepFrames: saved });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);
      expect(formDiffersFromSaved()).toBe(false);

      await typeStep('300');
      expect(formDiffersFromSaved()).toBe(true);
      expect(descriptionOf(settingsTab())).toBe(HAS_CHANGES);

      await typeStep('250');
      expect(formDiffersFromSaved()).toBe(false);
      expect(descriptionOf(settingsTab())).toBe('');
    });

    it('lights the Settings tab for a step edit and clears it when set back', async () => {
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 250 });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);
      expect(descriptionOf(settingsTab())).toBe('');

      editSection('bsgbat', { stepFrames: 400 });
      await tick();
      expect(descriptionOf(settingsTab())).toBe(HAS_CHANGES);

      editSection('bsgbat', { stepFrames: 250 });
      await tick();
      expect(descriptionOf(settingsTab())).toBe('');
    });

    it('treats a saved 0 and the default of 250 as the same step', async () => {
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 0 });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);

      editSection('bsgbat', { stepFrames: 250 });
      await tick();

      expect(descriptionOf(settingsTab())).toBe('');
    });
  });

  describe('cadence readout refresh', () => {
    it('refetches the readout when a saved step changes', async () => {
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 250 });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);
      expect(invalidateAcousticModels).not.toHaveBeenCalled();

      saveSection('bsgbat', { stepFrames: 400 });
      await waitFor(() => expect(invalidateAcousticModels).toHaveBeenCalledTimes(1));
    });

    it('does not refetch when the saved value only changes spelling of the default', async () => {
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 0 });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);

      saveSection('bsgbat', { stepFrames: 250 });
      await tick();

      expect(invalidateAcousticModels).not.toHaveBeenCalled();
    });
  });

  describe('bat filter confirmation counts', () => {
    function seedBatLevel(level: number) {
      seedSection('bat', {
        enabled: true,
        threshold: 0.5,
        nighttimeOnly: true,
        falsePositiveFilter: { level },
      });
    }

    it('states the BSG-BAT count, not the BattyBirdNET one, when only BSG-BAT is installed', async () => {
      seedBatLevel(MODERATE);
      await renderWithCatalog(BSG_BAT_CATALOG_ID);

      expect(paramsFor(`${BAT}.detectionCount`).at(-1)).toMatchObject({ count: '4' });
    });

    it('follows the configured step', async () => {
      seedBatLevel(STRICT);
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 512 });
      await renderWithCatalog(BSG_BAT_CATALOG_ID);

      expect(paramsFor(`${BAT}.detectionCount`).at(-1)).toMatchObject({ count: '4' });

      editSection('bsgbat', { stepFrames: 100 });
      await tick();
      expect(paramsFor(`${BAT}.detectionCount`).at(-1)).toMatchObject({ count: '18' });
    });

    it('states the BattyBirdNET count at its fixed 1.5 s step when only BattyBirdNET is installed', async () => {
      seedBatLevel(STRICT);
      seedSection('bsgbat', { threshold: 0.5, stepFrames: 100 });
      await renderWithCatalog(BATTY_CATALOG_ID);

      expect(paramsFor(`${BAT}.detectionCount`).at(-1)).toMatchObject({ count: '3' });
    });

    it('states both counts when both bat models are installed', async () => {
      seedBatLevel(MODERATE);
      await renderWithCatalog(BATTY_CATALOG_ID, BSG_BAT_CATALOG_ID);

      expect(paramsFor(`${BAT}.detectionCountByModel`).at(-1)).toMatchObject({
        batty: '2',
        bsgBat: '4',
      });
    });
  });
});
