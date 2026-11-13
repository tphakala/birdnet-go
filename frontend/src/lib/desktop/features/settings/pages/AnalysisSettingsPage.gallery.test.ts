import { describe, it, expect, beforeEach, afterEach, beforeAll, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/svelte';
import type { CatalogEntry } from '$lib/types/models';
import { catalogEntry as birdEntry, downloadProgress } from '../../../../../test/fixtures/catalog';

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
    settingsActions: {
      updateSection: vi.fn(),
      refreshKeepingEdits: vi.fn().mockResolvedValue(undefined),
    },
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
    bsgBatSettings: writable({}),
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
import { MODEL_OPERATION_IN_PROGRESS_KEY } from '$lib/utils/modelsApi';
import { settingsStore, settingsActions } from '$lib/stores/settings';
import { invalidateModels } from '$lib/stores/models.svelte';
import { toastActions } from '$lib/stores/toast';
import { t } from '$lib/i18n';
import { navigation } from '$lib/stores/navigation.svelte';
import { ApiError } from '$lib/utils/api';

beforeEach(() => {
  // Use jsdom's real Location so pushState updates the URL used by nested tabs.
  // The shared setup substitutes a static location object for navigation spies.
  Object.defineProperty(window, 'location', {
    value: document.location,
    writable: true,
    configurable: true,
  });
  navigation.redirect('/ui/settings/analysis');
});

// A network-shaped download failure (matches isNetworkDownloadError's real regex).
const NETWORK_ERROR = 'HTTP request failed for https://huggingface.co/model: connection refused';

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

  it('renders a nested deep link and reacts to query-only router navigation', async () => {
    navigation.redirect('/ui/settings/analysis?tab=models&modelTab=available');
    render(AnalysisSettingsPage);
    await screen.findByRole('button', { name: installButtonName });
    expect(screen.getByRole('tab', { name: /analysis\.gallery\.tabs\.available/ })).toHaveAttribute(
      'aria-selected',
      'true'
    );

    navigation.navigate('/ui/settings/analysis?tab=models');
    await waitFor(() =>
      expect(
        screen.getByRole('tab', { name: /analysis\.gallery\.tabs\.installed/ })
      ).toHaveAttribute('aria-selected', 'true')
    );
    navigation.navigate('/ui/settings/analysis?tab=models&modelTab=available');
    await screen.findByRole('button', { name: installButtonName });
    navigation.navigate('/ui/settings/analysis?tab=removed&modelTab=available');
    await waitFor(() =>
      expect(screen.getByRole('tab', { name: /analysis\.tabs\.settings/ })).toHaveAttribute(
        'aria-selected',
        'true'
      )
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

  it('shows the experimental notice in the license dialog of an experimental model', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ channel: 'experimental' })],
    });
    render(AnalysisSettingsPage);
    await gotoAvailableTab();

    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));

    expect(
      await screen.findByText('analysis.gallery.experimental.dialogNotice')
    ).toBeInTheDocument();
  });

  it('shows the preview notice with the build label in the license dialog of a preview model', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ channel: 'preview', buildLabel: 'b42', version: '9.9' })],
    });
    render(AnalysisSettingsPage);
    await gotoAvailableTab();

    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));

    expect(await screen.findByText('analysis.gallery.preview.dialogNotice')).toBeInTheDocument();
    expect(
      screen.queryByText('analysis.gallery.experimental.dialogNotice')
    ).not.toBeInTheDocument();
    expect(t).toHaveBeenCalledWith('analysis.gallery.preview.dialogNotice', { build: 'b42' });
  });

  it('names the model version in the preview license dialog notice when there is no build label', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ channel: 'preview', version: '9.9' })],
    });
    render(AnalysisSettingsPage);
    await gotoAvailableTab();

    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));

    expect(await screen.findByText('analysis.gallery.preview.dialogNotice')).toBeInTheDocument();
    expect(t).toHaveBeenCalledWith('analysis.gallery.preview.dialogNotice', { build: '9.9' });
  });

  it('shows no experimental notice in the license dialog of a stable model', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ channel: 'stable' })],
    });
    render(AnalysisSettingsPage);
    await gotoAvailableTab();

    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ });

    expect(
      screen.queryByText('analysis.gallery.experimental.dialogNotice')
    ).not.toBeInTheDocument();
  });

  async function startTestBirdInstall(): Promise<void> {
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    );
  }

  it('shows the in-progress reason and keeps Retry when an install is refused because another operation runs', async () => {
    const reason = 'A model install is already in progress (Geomodel). Try again when it finishes.';
    const refusal = new ApiError(reason, 409, new Response(null, { status: 409 }));
    refusal.errorKey = MODEL_OPERATION_IN_PROGRESS_KEY;
    vi.mocked(modelsApi.installModel).mockRejectedValue(refusal);

    await startTestBirdInstall();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(reason);
    expect(alert).not.toHaveTextContent('analysis.gallery.errors.details');
    expect(screen.getByRole('button', { name: /analysis\.gallery\.retry/ })).toBeInTheDocument();
    expect(modelsApi.subscribeInstallProgress).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByRole('button', { name: /analysis\.gallery\.retry/ }));
    await waitFor(() => expect(modelsApi.installModel).toHaveBeenCalledTimes(2));
  });

  it('keeps the details disclosure for an install 409 without the in-progress key', async () => {
    vi.mocked(modelsApi.installModel).mockRejectedValue(
      new ApiError('conflict', 409, new Response(null, { status: 409 }))
    );

    await startTestBirdInstall();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('analysis.gallery.errors.details');
  });

  it('shows the in-progress reason when a reinstall is refused', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ id: 'inst', name: 'Installed Model', installed: true })],
    });
    const reason = 'A model install is already in progress (Geomodel). Try again when it finishes.';
    const refusal = new ApiError(reason, 409, new Response(null, { status: 409 }));
    refusal.errorKey = MODEL_OPERATION_IN_PROGRESS_KEY;
    vi.mocked(modelsApi.reinstallModel).mockRejectedValue(refusal);

    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.reinstall.*Installed Model/ })
    );

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(reason);
    expect(alert).not.toHaveTextContent('analysis.gallery.errors.details');
    expect(screen.getByRole('button', { name: /analysis\.gallery\.retry/ })).toBeInTheDocument();
  });

  it.each(['install', 'reinstall'] as const)(
    'refreshes the settings store as soon as a %s completes, before the completion timer',
    async action => {
      vi.mocked(modelsApi.subscribeInstallProgress).mockImplementation(
        (_id, _onProgress, onComplete) => {
          onComplete();
          return () => {};
        }
      );
      vi.mocked(modelsApi.reinstallModel).mockResolvedValue(undefined);

      if (action === 'install') {
        await startTestBirdInstall();
      } else {
        vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
          catalog: [birdEntry({ id: 'inst', name: 'Installed Model', installed: true })],
        });
        render(AnalysisSettingsPage);
        await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
        await fireEvent.click(
          await screen.findByRole('button', {
            name: /analysis\.gallery\.reinstall.*Installed Model/,
          })
        );
      }

      await waitFor(() => expect(settingsActions.refreshKeepingEdits).toHaveBeenCalledTimes(1));
      // invalidateModels runs inside the 2 s completion timer, which unmount cancels.
      expect(invalidateModels).not.toHaveBeenCalled();
    }
  );
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

  it('shows a success toast after a model is removed even when the settings refresh fails', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ id: 'inst', name: 'Installed Model', installed: true })],
    });
    vi.mocked(modelsApi.uninstallModel).mockResolvedValue(undefined);
    vi.mocked(settingsActions.refreshKeepingEdits).mockRejectedValueOnce(new Error('offline'));

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
    expect(settingsActions.refreshKeepingEdits).toHaveBeenCalledTimes(1);
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

  it('shows the dependents reason instead of the retry hint when a remove is refused', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ id: 'geo', name: 'Geomodel', installed: true })],
    });
    const reason = 'Geomodel is needed by BirdNET v3.0. Remove those models first.';
    vi.mocked(modelsApi.uninstallModel).mockRejectedValue(
      new ApiError(reason, 409, new Response(null, { status: 409 }))
    );

    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.remove.*Geomodel/ })
    );
    await fireEvent.click(screen.getByRole('button', { name: 'analysis.gallery.remove' }));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(reason);
    expect(alert).not.toHaveTextContent('analysis.gallery.errors.removeRetryHint');
    expect(alert).not.toHaveTextContent('analysis.gallery.errors.details');
  });

  it('keeps the retry hint and details for any other remove failure', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ id: 'inst', name: 'Installed Model', installed: true })],
    });
    vi.mocked(modelsApi.uninstallModel).mockRejectedValue(
      new ApiError('disk error', 500, new Response(null, { status: 500 }))
    );

    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.remove.*Installed Model/ })
    );
    await fireEvent.click(screen.getByRole('button', { name: 'analysis.gallery.remove' }));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('analysis.gallery.errors.removeRetryHint');
    expect(alert).toHaveTextContent('analysis.gallery.errors.details');
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

describe('AnalysisSettingsPage model gallery cards', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    vi.mocked(modelsApi.fetchInstalled).mockResolvedValue([]);
    vi.mocked(modelsApi.fetchModelRegions).mockRejectedValue(new Error('no regions in test'));
  });

  afterEach(() => {
    // Some tests hold an action open with a never-settling mock, others stream progress: put
    // these shared mocks back to the bare ones the module mock starts with
    for (const mock of [
      modelsApi.uninstallModel,
      modelsApi.reinstallModel,
      modelsApi.installModel,
      modelsApi.subscribeInstallProgress,
    ]) {
      vi.mocked(mock).mockReset();
    }
  });

  // The same kinds of card (preview, incompatible) on each tab, plus a built-in card on
  // the Installed tab.
  function cardEntries(): CatalogEntry[] {
    const preview = { channel: 'preview', buildLabel: 'b42' } as const;
    return [
      birdEntry({ id: 'inst-builtin', name: 'Builtin', installed: true, permanent: true }),
      birdEntry({ id: 'inst-preview', name: 'Preview Installed', installed: true, ...preview }),
      birdEntry({ id: 'inst-bad', name: 'Bad Installed', installed: true, compatible: false }),
      birdEntry({ id: 'avail-preview', name: 'Preview Available', ...preview }),
      birdEntry({ id: 'avail-bad', name: 'Bad Available', compatible: false }),
    ];
  }

  function cardNamed(name: string): HTMLElement {
    return screen.getByRole('article', { name });
  }

  // A card's notice, found by its data-tone attribute
  function noticeOf(card: HTMLElement): HTMLElement {
    const found = card.querySelector('[data-tone]');
    if (!(found instanceof HTMLElement)) throw new Error('the card shows no notice');
    return found;
  }

  it('names the two tablists differently', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: cardEntries() });
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await screen.findByRole('article', { name: 'Builtin' });

    const names = screen.getAllByRole('tablist').map(list => list.getAttribute('aria-label'));
    expect(names).toEqual(['settings.tabs.navigation', 'analysis.gallery.tabs.navigationAria']);
  });

  it('renders both tabs through ModelCard, one article per entry', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: cardEntries() });
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await screen.findByRole('article', { name: 'Builtin' });

    const installed = ['Builtin', 'Preview Installed', 'Bad Installed'].map(cardNamed);
    expect(screen.getAllByRole('article')).toHaveLength(installed.length);
    // Built-in card: the footer carries the built-in pill instead of buttons
    expect(
      within(cardNamed('Builtin')).queryByRole('button', { name: /reinstall|remove/ })
    ).toBeNull();
    // Incompatible card explains itself in a status notice
    expect(noticeOf(cardNamed('Bad Installed'))).toHaveTextContent(
      'analysis.gallery.entryIncompatible'
    );
    expect(cardNamed('Preview Installed')).toHaveTextContent('analysis.gallery.preview.badge');

    await fireEvent.click(
      await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.available/ })
    );
    const previewCard = await screen.findByRole('article', { name: 'Preview Available' });
    const available = [previewCard, cardNamed('Bad Available')];
    expect(screen.getAllByRole('article')).toHaveLength(available.length);
    expect(within(previewCard).getByRole('note')).toHaveTextContent(
      'analysis.gallery.preview.cardNotice'
    );
    expect(previewCard).toHaveTextContent('analysis.gallery.preview.badge');
    expect(noticeOf(cardNamed('Bad Available'))).toHaveTextContent(
      'analysis.gallery.entryIncompatible'
    );
    expect(
      within(cardNamed('Bad Available')).getByRole('button', {
        name: /analysis\.gallery\.install Bad Available/,
      })
    ).toHaveAttribute('aria-disabled', 'true');
  });

  // A held action leaves its id set: the card it runs on shows the running label and the
  // other card pauses. Install is started from the Available tab through the license dialog.
  it.each([
    ['remove', 'analysis.gallery.remove', 'analysis.gallery.removing'],
    ['reinstall', 'analysis.gallery.reinstall', 'analysis.gallery.reinstalling'],
    ['install', 'analysis.gallery.install', 'analysis.gallery.installing'],
  ] as const)(
    'marks the card whose %s runs and pauses the other card',
    async (action, idleLabel, runningLabel) => {
      const installed = action !== 'install';
      vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
        catalog: [
          birdEntry({ id: 'first', name: 'First Model', installed }),
          birdEntry({ id: 'second', name: 'Second Model', installed }),
        ],
      });
      vi.mocked(modelsApi.uninstallModel).mockImplementation(() => new Promise<void>(() => {}));
      vi.mocked(modelsApi.reinstallModel).mockImplementation(() => new Promise<void>(() => {}));
      vi.mocked(modelsApi.installModel).mockImplementation(() => new Promise<void>(() => {}));

      render(AnalysisSettingsPage);
      await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
      if (!installed) {
        await fireEvent.click(
          await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.available/ })
        );
      }
      await fireEvent.click(
        await screen.findByRole('button', { name: `${idleLabel} First Model` })
      );
      if (action === 'remove') {
        // The dialog's own Remove carries no model name
        await fireEvent.click(screen.getByRole('button', { name: 'analysis.gallery.remove' }));
      } else if (action === 'install') {
        await fireEvent.click(
          await screen.findByRole('button', {
            name: /analysis\.gallery\.license\.acceptAndInstall/,
          })
        );
      }

      const running = await screen.findByRole('button', { name: `${runningLabel} First Model` });
      expect(running).toHaveAttribute('aria-disabled', 'true');
      const other = screen.getByRole('button', { name: `${idleLabel} Second Model` });
      expect(other).toHaveAttribute('aria-disabled', 'true');
      expect(screen.queryByRole('button', { name: `${runningLabel} Second Model` })).toBeNull();
    }
  );

  it('shows the download progress of a running install on its own card only', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [
        birdEntry({ id: 'first', name: 'First Model' }),
        birdEntry({ id: 'second', name: 'Second Model' }),
      ],
    });
    vi.mocked(modelsApi.installModel).mockResolvedValue(undefined);
    vi.mocked(modelsApi.subscribeInstallProgress).mockImplementation((_id, onProgress) => {
      onProgress(downloadProgress({ catalogId: 'first' }));
      return () => {};
    });

    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.available/ })
    );
    await fireEvent.click(
      await screen.findByRole('button', { name: 'analysis.gallery.install First Model' })
    );
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    );

    expect(await within(cardNamed('First Model')).findByRole('progressbar')).toHaveAttribute(
      'aria-valuenow',
      '50'
    );
    expect(within(cardNamed('Second Model')).queryByRole('progressbar')).toBeNull();
  });

  it('announces a finished install through the page live region, also after a tab switch', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ id: 'first', name: 'First Model' })],
    });
    vi.mocked(modelsApi.installModel).mockResolvedValue(undefined);
    vi.mocked(modelsApi.subscribeInstallProgress).mockImplementation((_id, onProgress) => {
      onProgress(downloadProgress({ catalogId: 'first', status: 'complete' }));
      return () => {};
    });

    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.available/ })
    );
    await fireEvent.click(
      await screen.findByRole('button', { name: 'analysis.gallery.install First Model' })
    );
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    );
    const announced = await screen.findByText('analysis.gallery.progress.complete', {
      selector: '[role="status"]',
    });

    // The running card unmounts with its tab; the announcement is not tied to it
    await fireEvent.click(
      await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.installed/ })
    );
    expect(announced).toBeInTheDocument();
    expect(announced).toHaveTextContent('analysis.gallery.progress.complete');
  });

  it('shows localized region names from the fetched regions on the Installed tab and in every Available section', async () => {
    const variant = {
      id: 'v-eu',
      region: 'eu',
      speciesCount: 10,
      default: true,
      installed: true,
      sizeBytes: 1,
      compatible: true,
      recommended: true,
    };
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue({
      modelRegion: 'auto',
      locationConfigured: false,
      resolved: { slug: '', source: 'global', ambiguous: false },
      regions: [
        {
          slug: 'eu',
          name: 'Europe',
          group: 'europe',
          groupDisplay: 'Europe',
          tier: 1,
          countries: { core: [], partial: [] },
        },
      ],
      families: [],
    });
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [
        birdEntry({
          id: 'inst-eu',
          name: 'Installed Regional',
          installed: true,
          variants: [variant],
          installedVariantId: 'v-eu',
        }),
        ...(['wildlife', 'bird', 'bat', 'geomodel'] as const).map(category =>
          birdEntry({
            id: `avail-${category}`,
            name: `Available ${category}`,
            category,
            variants: [{ ...variant, installed: false }],
            recommendedVariantId: 'v-eu',
          })
        ),
      ],
    });

    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await waitFor(() => expect(cardNamed('Installed Regional')).toHaveTextContent('Europe'));

    await fireEvent.click(
      await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.available/ })
    );
    await waitFor(() => expect(cardNamed('Available geomodel')).toHaveTextContent('Europe'));
    for (const category of ['wildlife', 'bird', 'bat', 'geomodel']) {
      expect(cardNamed(`Available ${category}`)).toHaveTextContent('Europe');
    }
  });
});
