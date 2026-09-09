import { describe, it, expect, beforeEach, beforeAll, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/svelte';
import type { CatalogEntry } from '$lib/types/models';
import { CHANNEL_STABLE } from '$lib/utils/variantSelection';

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
import { settingsStore } from '$lib/stores/settings';
import { toastActions } from '$lib/stores/toast';
import { t } from '$lib/i18n';

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
    channel: CHANNEL_STABLE,
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

// jsdom does not implement <dialog> showModal/close; polyfill them once so the
// license and remove dialogs' open/close paths do not throw. The prototype patch
// is idempotent, so a single file-scope beforeAll covers both describe blocks.
beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement) {
    this.open = false;
  };
});

describe('AnalysisSettingsPage model gallery error handling', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
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

describe('AnalysisSettingsPage model gallery in-flight guard and region refetch', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    // Reset the saved region so each test starts from the 'auto' baseline
    // (undefined modelRegion resolves to 'auto' in the page).
    settingsStore.update(s => ({
      ...s,
      originalData: {
        ...s.originalData,
        birdnet: { ...s.originalData.birdnet, modelRegion: undefined },
      },
    }));
    vi.mocked(modelsApi.fetchInstalled).mockResolvedValue([]);
    vi.mocked(modelsApi.fetchModelRegions).mockRejectedValue(new Error('no regions in test'));
  });

  it('cross-disables another card while a reinstall is in flight, keeping the reason reachable', async () => {
    // One installed model (has a Reinstall control) and one available (has Install).
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ id: 'inst', name: 'Installed Model', installed: true }), birdEntry()],
    });
    // The reinstall never resolves, so reinstallingId stays set: an action is in flight.
    vi.mocked(modelsApi.reinstallModel).mockImplementation(() => new Promise<void>(() => {}));

    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));

    // Positive control: before any action starts, the Available card's Install is
    // enabled and carries no aria-disabled (so the assertions below prove a change).
    await fireEvent.click(
      await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.available/ })
    );
    const installBefore = await screen.findByRole('button', { name: installButtonName });
    expect(installBefore).toBeEnabled();
    expect(installBefore).not.toHaveAttribute('aria-disabled');

    // Back to Installed and start a reinstall that never resolves.
    await fireEvent.click(
      await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.installed/ })
    );
    await fireEvent.click(
      await screen.findByRole('button', {
        name: /analysis\.gallery\.reinstall.*Installed Model/,
      })
    );

    // On Available, Install is cross-disabled via aria-disabled (NOT native
    // disabled, so it stays tab-focusable), points at the shared status line, and
    // that live line explains why actions are paused.
    await fireEvent.click(
      await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.available/ })
    );
    const installBtn = await screen.findByRole('button', { name: installButtonName });
    await waitFor(() => expect(installBtn).toHaveAttribute('aria-disabled', 'true'));
    expect(installBtn).toBeEnabled();
    expect(installBtn).toHaveAttribute('aria-describedby', 'gallery-action-status');
    const status = document.getElementById('gallery-action-status');
    expect(status).not.toBeNull();
    expect(status?.textContent).toContain('analysis.gallery.actionInProgress');

    // Behavioral proof, not just the ARIA attributes: clicking the cross-disabled
    // Install must NOT open the license dialog. If the onclick guard
    // (e.preventDefault(); return) regressed to native-clickable, openLicenseDialog
    // would fire and the accept-and-install button would appear.
    await fireEvent.click(installBtn);
    expect(
      screen.queryByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    ).toBeNull();
  });

  it('re-fetches the catalog when the saved model region changes, but not on unrelated saves', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [birdEntry()] });
    render(AnalysisSettingsPage);

    // onMount loads the catalog once for the persisted region.
    await waitFor(() => expect(modelsApi.fetchCatalog).toHaveBeenCalledTimes(1));

    // A save that changes the region re-fetches, so the server recommendation flags
    // (computed from the persisted region) follow the selector.
    settingsStore.update(s => ({
      ...s,
      originalData: {
        ...s.originalData,
        birdnet: { ...s.originalData.birdnet, modelRegion: 'nordic' },
      },
    }));
    await waitFor(() => expect(modelsApi.fetchCatalog).toHaveBeenCalledTimes(2));

    // A save that changes an unrelated field must NOT re-fetch the catalog.
    settingsStore.update(s => ({
      ...s,
      originalData: {
        ...s.originalData,
        birdnet: { ...s.originalData.birdnet, huggingFaceEndpoint: 'https://example.com' },
      },
    }));
    // Let any pending effect flush, then assert the count did not move.
    await Promise.resolve();
    await Promise.resolve();
    expect(modelsApi.fetchCatalog).toHaveBeenCalledTimes(2);

    // Positive control: the region effect is still live after the unrelated save,
    // so a further region change must trigger a third fetch (the no-op above did
    // not tear the subscription down).
    settingsStore.update(s => ({
      ...s,
      originalData: {
        ...s.originalData,
        birdnet: { ...s.originalData.birdnet, modelRegion: 'iberia' },
      },
    }));
    await waitFor(() => expect(modelsApi.fetchCatalog).toHaveBeenCalledTimes(3));
  });

  it('shows a success toast after a model is removed', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ id: 'inst', name: 'Installed Model', installed: true })],
    });
    vi.mocked(modelsApi.uninstallModel).mockResolvedValue(undefined);

    render(AnalysisSettingsPage);
    // Installed is the default gallery sub-tab, so the card Remove is reachable directly.
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.remove.*Installed Model/ })
    );

    // Confirm in the dialog. Its Remove button carries no model-name suffix, so an
    // exact-string name matches only it, not the card's aria-labelled button.
    await fireEvent.click(screen.getByRole('button', { name: 'analysis.gallery.remove' }));

    await waitFor(() => expect(modelsApi.uninstallModel).toHaveBeenCalledWith('inst'));
    // The toast fires only after the post-remove catalog reload resolves, so wait
    // for it rather than asserting synchronously on the uninstall call.
    await waitFor(() =>
      expect(toastActions.success).toHaveBeenCalledWith(
        expect.stringContaining('analysis.gallery.removeSuccess')
      )
    );
    // The i18n mock echoes the key and strips params, so the toast string alone
    // cannot prove the model name is interpolated. Assert on the t() call itself
    // that the {name} param is passed through, closing that gap (#1566).
    expect(t).toHaveBeenCalledWith('analysis.gallery.removeSuccess', { name: 'Installed Model' });
  });
});

describe('AnalysisSettingsPage model gallery optimize + permanent card', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    settingsStore.update(s => ({
      ...s,
      originalData: {
        ...s.originalData,
        birdnet: { ...s.originalData.birdnet, modelRegion: undefined },
      },
    }));
    vi.mocked(modelsApi.fetchInstalled).mockResolvedValue([]);
    vi.mocked(modelsApi.fetchModelRegions).mockRejectedValue(new Error('no regions in test'));
  });

  // A permanent BirdNET v2.4 entry installed on its BuiltIn baseline, with a faster
  // DFT build recommended for this host (so it carries an optimize offer).
  function v24Installed(overrides: Partial<CatalogEntry> = {}): CatalogEntry {
    return birdEntry({
      id: 'birdnet-v2.4',
      name: 'BirdNET v2.4',
      installed: true,
      permanent: true,
      installedVariantId: 'builtin',
      recommendedVariantId: 'fp32-dfttrunc',
      variants: [
        {
          id: 'builtin',
          builtIn: true,
          default: true,
          installed: true,
          speciesCount: 6522,
          sizeBytes: 0,
          compatible: true,
          recommended: false,
        },
        {
          id: 'fp32-dfttrunc',
          precision: 'fp32',
          default: false,
          installed: false,
          speciesCount: 6522,
          sizeBytes: 54_000_000,
          compatible: true,
          recommended: true,
          reasons: [{ code: 'backend.recommended', args: { backend: 'onnxruntime-cpu' } }],
        },
      ],
      ...overrides,
    });
  }

  it('shows the optimize banner when an installed model has a better variant, and dismisses it', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [v24Installed()] });
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));

    // The banner is visible with the Review action.
    expect(await screen.findByText('analysis.gallery.optimize.bannerTitle')).toBeInTheDocument();
    const review = await screen.findByRole('button', {
      name: /analysis\.gallery\.optimize\.review/,
    });

    // Review opens the dialog.
    await fireEvent.click(review);
    expect(await screen.findByText('analysis.gallery.optimize.dialogTitle')).toBeInTheDocument();

    // Dismiss hides the banner for the session.
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.optimize\.dismiss/ })
    );
    await waitFor(() =>
      expect(screen.queryByText('analysis.gallery.optimize.bannerTitle')).toBeNull()
    );
  });

  it('shows no optimize banner when the installed variant is already the recommended one', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [v24Installed({ installedVariantId: 'fp32-dfttrunc' })],
    });
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    // Give the card grid time to render, then assert the banner never appears.
    await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.installed/ });
    expect(screen.queryByText('analysis.gallery.optimize.bannerTitle')).toBeNull();
  });

  it('renders the permanent card with a built-in badge, no Remove/Reinstall, and an Optimize action', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [v24Installed()] });
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));

    // The Optimize (swap) action is present on the permanent card.
    expect(
      await screen.findByRole('button', {
        name: /analysis\.gallery\.optimize\.swap.*BirdNET v2\.4/,
      })
    ).toBeInTheDocument();

    // The permanent model cannot be removed or reinstalled from its card.
    expect(
      screen.queryByRole('button', { name: /analysis\.gallery\.remove.*BirdNET v2\.4/ })
    ).toBeNull();
    expect(
      screen.queryByRole('button', { name: /analysis\.gallery\.reinstall.*BirdNET v2\.4/ })
    ).toBeNull();

    // The built-in label appears at least twice on the permanent card: the footer
    // built-in badge and the baseline hardware chip. (The review dialog, also in the
    // DOM, renders it again for the offer's from-variant, so assert a lower bound.)
    expect(screen.getAllByText('analysis.gallery.builtIn').length).toBeGreaterThanOrEqual(2);
  });
});
