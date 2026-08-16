import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/svelte';
import type { CatalogEntry } from '$lib/types/models';

// Page-level coverage for the model-gallery install-error split:
// a failed install must NOT blank the grid (installError is a separate banner from
// catalogError), the banner's Retry must re-run the install (not reload the
// catalog), and a network-shaped failure must surface the Download Source hint.
// isNetworkDownloadError is unit-tested and fail-safe, but those page behaviors are
// not, so they get an integration test here (AudioSettingsPage.test.ts is the
// precedent for mocking a page's api module and rendering the whole page).

// Keep isNetworkDownloadError real (it is the classifier under test); stub the I/O.
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

// The region selector fetches on mount and is irrelevant to this flow; stub it to a
// no-op component so it does not pull in its own network calls.
vi.mock('$lib/desktop/features/settings/components/ModelRegionSelector.svelte');

// The Settings tab (not exercised here) reads these stores; provide minimal shapes
// so the page's derived state initializes without touching a real settings load.
vi.mock('$lib/stores/settings', async () => {
  const { writable } = await vi.importActual<typeof import('svelte/store')>('svelte/store');
  const settingsStore = writable({
    isLoading: false,
    isSaving: false,
    error: null,
    originalData: { birdnet: { huggingFaceEndpoint: '' } },
    formData: { birdnet: { huggingFaceEndpoint: '' } },
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
    }),
    dynamicThresholdSettings: writable({ enabled: false }),
    realtimeSettings: writable({}),
    batSettings: writable({}),
    perchSettings: writable({}),
    birdNetV3Settings: writable({}),
  };
});

// The other onMount loaders (locales, range filter) go through the shared api util;
// resolve them to benign values so mount does not hit the network.
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

// A network-shaped download failure (matches isNetworkDownloadError's real regex).
const NETWORK_ERROR = 'HTTP request failed for https://huggingface.co/model: connection refused';

function birdEntry(overrides: Partial<CatalogEntry> = {}): CatalogEntry {
  return {
    id: 'test-bird',
    name: 'Test Bird Model',
    description: 'A test bird classifier',
    author: 'Tester',
    license: 'CC-BY-NC-4.0',
    commercialUse: false,
    category: 'bird',
    region: '',
    speciesCount: 100,
    version: '1.0',
    installed: false,
    compatible: true,
    totalSizeBytes: 1_000_000,
    hasGeomodel: false,
    ...overrides,
  };
}

async function gotoAvailableTab(): Promise<void> {
  // Top-level Models tab, then the Available gallery sub-tab (i18n mock echoes keys).
  await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
  await fireEvent.click(
    await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.available/ })
  );
}

const installButtonName = /analysis\.gallery\.install.*Test Bird Model/;

describe('AnalysisSettingsPage model gallery error handling', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    // jsdom does not implement <dialog> showModal/close; polyfill so the license
    // dialog open/close path does not throw (its content renders on licenseModel).
    HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
      this.open = true;
    };
    HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement) {
      this.open = false;
    };
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [birdEntry()] });
    vi.mocked(modelsApi.fetchInstalled).mockResolvedValue([]);
    vi.mocked(modelsApi.fetchModelRegions).mockRejectedValue(new Error('no regions in test'));
    vi.mocked(modelsApi.installModel).mockResolvedValue(undefined);
    // The SSE progress stream immediately reports a network-shaped failure.
    vi.mocked(modelsApi.subscribeInstallProgress).mockImplementation(
      (_id, _onProgress, _onComplete, onError) => {
        onError(NETWORK_ERROR);
        return () => {};
      }
    );
  });

  it('keeps the grid rendered on install failure, shows the network hint, and retries the install (not the catalog)', async () => {
    render(AnalysisSettingsPage);
    await gotoAvailableTab();

    // Accept the license and start the install.
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    );

    await waitFor(() =>
      expect(modelsApi.installModel).toHaveBeenCalledWith('test-bird', undefined)
    );

    // The failure banner appears with the network-shaped Download Source hint.
    await waitFor(() =>
      expect(screen.getByText(/analysis\.gallery\.errors\.actionFailed/)).toBeInTheDocument()
    );
    expect(screen.getByText(/analysis\.gallery\.errors\.downloadSourceHint/)).toBeInTheDocument();

    // The grid is NOT blanked: the available card's Install button is still present.
    expect(screen.getByRole('button', { name: installButtonName })).toBeInTheDocument();

    // Retry re-runs the install, and does not reload the catalog.
    const catalogCallsBeforeRetry = vi.mocked(modelsApi.fetchCatalog).mock.calls.length;
    await fireEvent.click(screen.getByRole('button', { name: /analysis\.gallery\.retry/ }));

    await waitFor(() => expect(modelsApi.installModel).toHaveBeenCalledTimes(2));
    expect(modelsApi.installModel).toHaveBeenLastCalledWith('test-bird', undefined);
    expect(vi.mocked(modelsApi.fetchCatalog).mock.calls.length).toBe(catalogCallsBeforeRetry);
  });
});
