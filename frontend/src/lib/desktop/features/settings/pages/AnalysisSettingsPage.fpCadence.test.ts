import { describe, it, expect, beforeAll, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, cleanup, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { t } from '$lib/i18n';
import type { AnalysisCadenceInfo } from '$lib/desktop/features/system/inference.types';

// Page-level coverage for the false positive filter cadence readout: the
// capacity notices, the per-model readout from the server counts, the estimate
// shown for unsaved or not yet applied settings, and the fallback when the
// server has published no plan.

const { cadenceState, stores } = vi.hoisted(() => {
  const cadenceState: { value: AnalysisCadenceInfo | null } = { value: null };
  const stores: Record<string, { set: (value: unknown) => void }> = {};
  return { cadenceState, stores };
});

vi.mock('$lib/stores/acousticModels.svelte', () => ({
  analysisCadence: () => cadenceState.value,
  invalidateAcousticModels: vi.fn().mockResolvedValue(undefined),
  watchAcousticModels: vi.fn(() => () => {}),
}));

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
    fetchModelRegions: vi.fn(),
    fetchRegionCoverageMap: vi.fn(),
  };
});

vi.mock('$lib/stores/models.svelte', () => ({
  invalidateModels: vi.fn(),
}));

vi.mock('$lib/desktop/features/settings/components/ModelRegionSelector.svelte');

vi.mock('$lib/stores/settings', async () => {
  const { writable } = await vi.importActual<typeof import('svelte/store')>('svelte/store');
  const settingsStore = writable<unknown>({});
  const birdnetSettings = writable<unknown>({});
  const realtimeSettings = writable<unknown>({});
  stores.settingsStore = settingsStore;
  stores.birdnetSettings = birdnetSettings;
  stores.realtimeSettings = realtimeSettings;
  return {
    settingsStore,
    settingsActions: {
      updateSection: vi.fn(),
      loadRangeFilterSpecies: vi.fn().mockResolvedValue({ count: 0, species: [] }),
    },
    hasUnsavedChanges: writable(false),
    birdnetSettings,
    dynamicThresholdSettings: writable({ enabled: false }),
    realtimeSettings,
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
      get: vi.fn().mockResolvedValue({ count: 0 }),
      post: vi.fn().mockResolvedValue({}),
      put: vi.fn().mockResolvedValue({}),
      delete: vi.fn().mockResolvedValue({}),
    },
    getCsrfToken: vi.fn().mockReturnValue('test-csrf-token'),
  };
});

import AnalysisSettingsPage from './AnalysisSettingsPage.svelte';
import { navigation } from '$lib/stores/navigation.svelte';
import { invalidateAcousticModels, watchAcousticModels } from '$lib/stores/acousticModels.svelte';

const FP = 'settings.main.sections.falsePositiveFilter';

function cadence(overrides: Partial<AnalysisCadenceInfo> = {}): AnalysisCadenceInfo {
  return {
    status: 'capped',
    filterLevel: 5,
    configuredOverlapSec: 2.8,
    effectiveOverlapSec: 1.8,
    minBaseStepMs: 1200,
    sourceCount: 2,
    modelCount: 3,
    unknownLatencyModels: [],
    models: [
      {
        id: 'Bat',
        name: 'Bat',
        clipMs: 3000,
        stepMs: 1500,
        confirmations: 2,
        windowsInReference: 4,
      },
      {
        id: 'BirdNET_V2.4',
        name: 'BirdNET v2.4',
        clipMs: 3000,
        stepMs: 1200,
        probeLatencyMs: 166,
        confirmations: 4,
        windowsInReference: 5,
      },
      {
        id: 'BirdNET_V3.0',
        name: 'BirdNET v3.0',
        clipMs: 5000,
        stepMs: 2000,
        probeLatencyMs: 874,
        confirmations: 3,
        windowsInReference: 3,
      },
    ],
    ...overrides,
  };
}

/** Sets the saved and the edited level and overlap. */
function setSettings(saved: { level: number; overlap: number }, form = saved): void {
  stores.settingsStore.set({
    isLoading: false,
    isSaving: false,
    error: null,
    originalData: {
      birdnet: { overlap: saved.overlap },
      realtime: { falsePositiveFilter: { level: saved.level } },
    },
    formData: {
      birdnet: { overlap: form.overlap },
      realtime: { falsePositiveFilter: { level: form.level } },
    },
  });
  stores.birdnetSettings.set({
    threshold: 0.7,
    overlap: form.overlap,
    rangeFilter: { threshold: 0.01 },
  });
  stores.realtimeSettings.set({ falsePositiveFilter: { level: form.level } });
}

/** Params of the t() calls made for a key, in call order. */
function paramsFor(key: string): Array<Record<string, unknown>> {
  return vi
    .mocked(t)
    .mock.calls.filter(([k]) => k === key)
    .map(([, params]) => params ?? {});
}

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement) {
    this.open = false;
  };
});

describe('AnalysisSettingsPage false positive filter cadence', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: document.location,
      writable: true,
      configurable: true,
    });
    navigation.redirect('/ui/settings/analysis');
    vi.mocked(t).mockClear();
    cadenceState.value = null;
  });

  afterEach(() => {
    cleanup();
  });

  it('shows the capped notice and the server counts for the bird models', () => {
    // Server steps that the client estimate would not produce (it gives 4 of 5
    // and 3 of 3 at the 1.2 s cap), so the readout must come from the server.
    const base = cadence();
    cadenceState.value = cadence({
      models: [
        ...base.models.filter(m => m.id === 'Bat'),
        {
          id: 'BirdNET_V2.4',
          name: 'BirdNET v2.4',
          clipMs: 3000,
          stepMs: 1500,
          confirmations: 3,
          windowsInReference: 4,
        },
        {
          id: 'BirdNET_V3.0',
          name: 'BirdNET v3.0',
          clipMs: 5000,
          stepMs: 2500,
          confirmations: 2,
          windowsInReference: 2.4,
        },
      ],
    });
    setSettings({ level: 5, overlap: 2.8 });
    render(AnalysisSettingsPage);

    expect(screen.getByTestId('fp-cadence-capped')).toBeInTheDocument();
    expect(paramsFor(`${FP}.capNotice`).at(-1)).toEqual({
      models: 3,
      sources: 2,
      configured: '2.8',
      effective: '1.8',
    });

    const readout = screen.getByTestId('fp-cadence-readout');
    expect(within(readout).getByText(`${FP}.readoutTitle`)).toBeInTheDocument();
    expect(within(readout).getAllByRole('listitem')).toHaveLength(2);
    const lines = paramsFor(`${FP}.readoutLine`);
    expect(lines.map(p => [p.model, p.count])).toEqual(
      expect.arrayContaining([
        ['BirdNET v2.4', 3],
        ['BirdNET v3.0', 2],
      ])
    );
    expect(lines.map(p => p.windows)).toEqual(expect.arrayContaining(['4', '2.4']));
    expect(lines.some(p => p.model === 'Bat')).toBe(false);
    expect(screen.queryByText(`${FP}.readoutPreview`)).not.toBeInTheDocument();
    expect(screen.queryByTestId('fp-cadence-unknown-latency')).not.toBeInTheDocument();
    expect(screen.getByText(`${FP}.cpuNote`)).toBeInTheDocument();
  });

  it('describes the level without a count while a plan is published', () => {
    cadenceState.value = cadence({ status: 'ok', effectiveOverlapSec: 2.8 });
    setSettings({ level: 5, overlap: 2.8 });
    render(AnalysisSettingsPage);

    expect(screen.getByText(`${FP}.levels.maximum`)).toBeInTheDocument();
    expect(screen.queryByTestId('fp-cadence-capped')).not.toBeInTheDocument();
    expect(screen.queryByTestId('fp-cadence-overloaded')).not.toBeInTheDocument();
  });

  it('shows the overloaded notice', () => {
    cadenceState.value = cadence({
      status: 'overloaded',
      effectiveOverlapSec: 0,
      minBaseStepMs: 0,
    });
    setSettings({ level: 5, overlap: 2.8 });
    render(AnalysisSettingsPage);

    expect(screen.getByTestId('fp-cadence-overloaded')).toBeInTheDocument();
    expect(paramsFor(`${FP}.overloadedNotice`).at(-1)).toEqual({ models: 3, sources: 2 });
    expect(screen.queryByTestId('fp-cadence-capped')).not.toBeInTheDocument();
  });

  it('names the models whose latency is unknown', () => {
    cadenceState.value = cadence({ unknownLatencyModels: ['BirdNET_V3.0'] });
    setSettings({ level: 5, overlap: 2.8 });
    render(AnalysisSettingsPage);

    expect(screen.getByTestId('fp-cadence-unknown-latency')).toBeInTheDocument();
    expect(paramsFor(`${FP}.unknownLatency`).at(-1)).toEqual({ models: 'BirdNET v3.0' });
  });

  it('falls back to the count from the configured overlap when no plan is published', () => {
    setSettings({ level: 5, overlap: 2.8 });
    render(AnalysisSettingsPage);

    expect(screen.queryByTestId('fp-cadence-readout')).not.toBeInTheDocument();
    expect(paramsFor(`${FP}.detectionCount`).at(-1)).toMatchObject({ count: '21' });
    expect(screen.getByText(`${FP}.cpuNote`)).toBeInTheDocument();
  });

  it('keeps the count in the description when the plan has no bird models', () => {
    cadenceState.value = cadence({
      models: cadence().models.filter(m => m.id === 'Bat'),
    });
    setSettings({ level: 5, overlap: 2.8 });
    render(AnalysisSettingsPage);

    expect(screen.queryByTestId('fp-cadence-readout')).not.toBeInTheDocument();
    expect(paramsFor(`${FP}.detectionCount`).at(-1)).toMatchObject({ count: '21' });
  });

  it('shows only the filter-off warning when the filter is off', () => {
    cadenceState.value = cadence({ status: 'filterOff', configuredOverlapSec: 1.5 });
    setSettings({ level: 0, overlap: 1.5 });
    render(AnalysisSettingsPage);

    expect(screen.getByText(`${FP}.warningOff`)).toBeInTheDocument();
    expect(screen.queryByTestId('fp-cadence-readout')).not.toBeInTheDocument();
    expect(screen.queryByText(`${FP}.cpuNote`)).not.toBeInTheDocument();
  });

  it('estimates the counts for an unsaved level change', () => {
    cadenceState.value = cadence();
    setSettings({ level: 5, overlap: 2.8 }, { level: 3, overlap: 2.4 });
    render(AnalysisSettingsPage);

    expect(screen.getByText(`${FP}.readoutPreviewTitle`)).toBeInTheDocument();
    expect(screen.getByText(`${FP}.readoutPreview`)).toBeInTheDocument();
    const lines = paramsFor(`${FP}.readoutLine`);
    // Level 3 at the device's 1.2 s step: ceil(5 * 0.5) = 3; v3.0 at 2.0 s: ceil(3 * 0.5) = 2.
    expect(lines.map(p => [p.model, p.count])).toEqual(
      expect.arrayContaining([
        ['BirdNET v2.4', 3],
        ['BirdNET v3.0', 2],
      ])
    );
  });

  it('marks the counts as estimates while saved settings are not applied yet', () => {
    // The stale server counts (overlap 2.4 s) differ from the estimate for the
    // saved 2.8 s capped at the 1.2 s step (4 of 5, 3 of 3).
    cadenceState.value = cadence({
      configuredOverlapSec: 2.4,
      effectiveOverlapSec: 2.4,
      models: [
        {
          id: 'BirdNET_V2.4',
          name: 'BirdNET v2.4',
          clipMs: 3000,
          stepMs: 600,
          confirmations: 7,
          windowsInReference: 10,
        },
        {
          id: 'BirdNET_V3.0',
          name: 'BirdNET v3.0',
          clipMs: 5000,
          stepMs: 1000,
          confirmations: 5,
          windowsInReference: 6,
        },
      ],
    });
    setSettings({ level: 5, overlap: 2.8 });
    render(AnalysisSettingsPage);

    expect(screen.getByText(`${FP}.readoutPreviewTitle`)).toBeInTheDocument();
    expect(screen.getByText(`${FP}.readoutPending`)).toBeInTheDocument();
    const lines = paramsFor(`${FP}.readoutLine`);
    expect(lines.map(p => [p.model, p.count])).toEqual(
      expect.arrayContaining([
        ['BirdNET v2.4', 4],
        ['BirdNET v3.0', 3],
      ])
    );
    expect(lines.some(p => p.count === 7 || p.count === 5)).toBe(false);
  });

  it('marks the counts as estimates while the snapshot predates a saved level', () => {
    // Level 4 was saved on the filter-on side, so no re-plan follows; the
    // snapshot still carries the level 5 counts until the next fetch.
    cadenceState.value = cadence();
    setSettings({ level: 4, overlap: 2.8 });
    render(AnalysisSettingsPage);

    expect(screen.getByText(`${FP}.readoutPending`)).toBeInTheDocument();
    const lines = paramsFor(`${FP}.readoutLine`);
    // Level 4 at the device's 1.2 s step: ceil(5 * 0.6) = 3; v3.0 at 2.0 s: ceil(3 * 0.6) = 2.
    expect(lines.map(p => [p.model, p.count])).toEqual(
      expect.arrayContaining([
        ['BirdNET v2.4', 3],
        ['BirdNET v3.0', 2],
      ])
    );
  });

  it('shows the server counts once the snapshot reflects the saved level', () => {
    cadenceState.value = cadence({ filterLevel: 4 });
    setSettings({ level: 4, overlap: 2.8 });
    render(AnalysisSettingsPage);

    expect(screen.getByText(`${FP}.readoutTitle`)).toBeInTheDocument();
    expect(screen.queryByText(`${FP}.readoutPending`)).not.toBeInTheDocument();
  });

  it('renders the capacity notices as notes, not alerts', () => {
    cadenceState.value = cadence();
    setSettings({ level: 5, overlap: 2.8 });
    render(AnalysisSettingsPage);

    expect(screen.getByTestId('fp-cadence-capped')).toHaveAttribute('role', 'note');
    expect(screen.getByText(`${FP}.capNoticeTitle`)).toBeInTheDocument();
  });

  it('watches the inference snapshot and refetches it after a saved level change', async () => {
    cadenceState.value = cadence();
    setSettings({ level: 5, overlap: 2.8 });
    render(AnalysisSettingsPage);
    await tick();

    expect(vi.mocked(watchAcousticModels)).toHaveBeenCalled();
    vi.mocked(invalidateAcousticModels).mockClear();

    setSettings({ level: 4, overlap: 2.8 });
    await tick();
    expect(vi.mocked(invalidateAcousticModels)).toHaveBeenCalledTimes(1);
  });

  it('never mentions the removed hardware note', () => {
    cadenceState.value = cadence();
    setSettings({ level: 5, overlap: 2.8 });
    render(AnalysisSettingsPage);

    expect(screen.queryByText(`${FP}.hardwareNote`)).not.toBeInTheDocument();
  });
});
