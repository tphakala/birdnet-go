import { describe, it, expect, beforeEach, afterEach, beforeAll, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import type { CatalogEntry, ModelRegionsResponse } from '$lib/types/models';
import { catalogEntry as birdEntry, downloadProgress } from '../../../../../test/fixtures/catalog';
import { deferred } from '../../../../../test/async-helpers';

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
    cancelInstall: vi.fn(),
    installModel: vi.fn(),
    reinstallModel: vi.fn(),
    uninstallModel: vi.fn(),
    subscribeInstallProgress: vi.fn(),
    fetchModelRegions: vi.fn(),
    fetchRegionCoverageMap: vi.fn(),
    fetchModelDocument: vi.fn(),
  };
});

vi.mock('$lib/stores/models.svelte', () => ({
  getAvailableModels: vi.fn(() => []),
  modelsLoading: vi.fn(() => false),
  refreshModels: vi.fn(),
  watchModels: vi.fn(() => () => {}),
}));

// The next steps wizard is global (App mounts it); only its launch is under test here
vi.mock('$lib/desktop/features/wizard/wizardState.svelte', () => ({
  wizardState: { isActive: false, launch: vi.fn(() => true) },
}));

// The region selector fetches on mount and is irrelevant to this flow; stub it to a
// no-op component so it does not pull in its own network calls.
vi.mock('$lib/desktop/features/settings/components/ModelRegionSelector.svelte');

// The live BirdNET form section, kept reachable so a test can set an unsaved value
const { BIRDNET_FORM_DEFAULTS, settingsStores } = vi.hoisted(() => {
  const settingsStores: { birdnetSettings?: { set: (value: Record<string, unknown>) => void } } =
    {};
  return {
    BIRDNET_FORM_DEFAULTS: { huggingFaceEndpoint: '', threshold: 0.03, sensitivity: 1, overlap: 0 },
    settingsStores,
  };
});

// The page reads these stores; provide minimal shapes so its derived state
// initializes without touching a real settings load.
vi.mock('$lib/stores/settings', async () => {
  const { writable } = await vi.importActual<typeof import('svelte/store')>('svelte/store');
  const settingsStore = writable({
    isLoading: false,
    isSaving: false,
    error: null,
    dataLoaded: true,
    originalData: { birdnet: { huggingFaceEndpoint: '' } },
    formData: { birdnet: { huggingFaceEndpoint: '' } },
  });
  return {
    // The page reports a blocking dynamic threshold error here; these tests do not read it.
    setValidationIssues: vi.fn(),
    settingsStore,
    settingsActions: {
      updateSection: vi.fn(),
      refreshKeepingEdits: vi.fn().mockResolvedValue(undefined),
    },
    hasUnsavedChanges: writable(false),
    birdnetSettings: (settingsStores.birdnetSettings =
      writable<Record<string, unknown>>(BIRDNET_FORM_DEFAULTS)),
    dynamicThresholdSettings: writable({ enabled: false }),
    realtimeSettings: writable({}),
    batSettings: writable({}),
    bsgBatSettings: writable({}),
    perchSettings: writable({}),
    birdNetV3Settings: writable({}),
    birdNetPlusSettings: writable({}),
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
import {
  MODEL_AGREEMENT_REQUIRED_KEY,
  MODEL_CANCEL_UNAVAILABLE_KEY,
  MODEL_OPERATION_IN_PROGRESS_KEY,
} from '$lib/utils/modelsApi';
import { settingsStore, settingsActions } from '$lib/stores/settings';
import { getAvailableModels, refreshModels } from '$lib/stores/models.svelte';
import { toastActions } from '$lib/stores/toast';
import { t } from '$lib/i18n';
import { navigation } from '$lib/stores/navigation.svelte';
import { ApiError } from '$lib/utils/api';
import { wizardState } from '$lib/desktop/features/wizard/wizardState.svelte';

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

// A document that needs an agreement not yet recorded.
const POLICY_SHA = 'a'.repeat(64);
const policyDocument = {
  repo: 'org/model',
  kind: 'acceptable_use',
  requiresAgreement: true,
  agreed: false,
  sha256: POLICY_SHA,
};

// The 409 a backend that enforces agreements answers for an outstanding document.
function agreementRefusal(): ApiError {
  const refusal = new ApiError('needs agreement', 409, new Response(null, { status: 409 }));
  refusal.errorKey = MODEL_AGREEMENT_REQUIRED_KEY;
  refusal.errorParams = {
    name: 'Test Bird Model',
    documents: [{ repo: 'org/model', kind: 'acceptable_use', sha256: POLICY_SHA }],
  };
  return refusal;
}

// A request that fails for a reason other than an agreement (a 5xx, a dropped
// connection) after the user agreed.
function serverError(): ApiError {
  return new ApiError('server error', 500, new Response(null, { status: 500 }));
}

async function agreeToPolicy(): Promise<void> {
  await fireEvent.click(
    await screen.findByRole('checkbox', { name: /analysis\.gallery\.agreement\.agree/ })
  );
  await fireEvent.click(
    screen.getByRole('button', { name: /analysis\.gallery\.agreement\.agreeAndContinue/ })
  );
}

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
      expect(screen.getByRole('tab', { name: /analysis\.tabs\.detection/ })).toHaveAttribute(
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

  it('shows the superseded notice with the successor name in the license dialog', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ supersededBy: { id: 'newer', name: 'Newer Model' } })],
    });
    render(AnalysisSettingsPage);
    await gotoAvailableTab();

    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));

    expect(await screen.findByText('analysis.gallery.superseded.dialogNotice')).toBeInTheDocument();
    expect(t).toHaveBeenCalledWith('analysis.gallery.superseded.dialogNotice', {
      name: 'Newer Model',
    });
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
    expect(screen.queryByText('analysis.gallery.superseded.dialogNotice')).not.toBeInTheDocument();
  });

  async function startTestBirdInstall(): Promise<void> {
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    );
  }

  it('shows no error banner or mirror hint when a download is cancelled', async () => {
    let cancelled: (() => void) | undefined;
    vi.mocked(modelsApi.subscribeInstallProgress).mockImplementation(
      (_id, onProgress, _onComplete, _onError, onCancelled) => {
        onProgress(downloadProgress());
        cancelled = onCancelled;
        return () => {};
      }
    );
    vi.mocked(modelsApi.cancelInstall).mockResolvedValue(undefined);
    await startTestBirdInstall();

    await fireEvent.click(await screen.findByRole('button', { name: /^Cancel Test Bird Model/ }));
    expect(modelsApi.cancelInstall).toHaveBeenCalledWith('test-bird');
    expect(
      await screen.findByRole('button', { name: /analysis\.gallery\.cancelling Test Bird Model/ })
    ).toHaveAttribute('aria-disabled', 'true');

    cancelled?.();

    expect(await screen.findByRole('button', { name: installButtonName })).toBeInTheDocument();
    expect(screen.queryByText(/analysis\.gallery\.errors\.actionFailed/)).toBeNull();
    expect(screen.queryByText(/analysis\.gallery\.errors\.downloadSourceHint/)).toBeNull();
    expect(screen.queryByRole('button', { name: /^Cancel / })).toBeNull();
  });

  it('keeps the download running and clears Cancelling when the cancel is refused', async () => {
    vi.mocked(modelsApi.subscribeInstallProgress).mockImplementation((_id, onProgress) => {
      onProgress(downloadProgress());
      return () => {};
    });
    const refusal = new ApiError('too late', 409, new Response(null, { status: 409 }));
    refusal.errorKey = MODEL_CANCEL_UNAVAILABLE_KEY;
    vi.mocked(modelsApi.cancelInstall).mockRejectedValue(refusal);
    await startTestBirdInstall();

    await fireEvent.click(await screen.findByRole('button', { name: /^Cancel Test Bird Model/ }));

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /^Cancel Test Bird Model/ })).not.toHaveAttribute(
        'aria-disabled'
      )
    );
    expect(screen.queryByText(/analysis\.gallery\.errors\.actionFailed/)).toBeNull();
  });

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
      // refreshModels runs inside the 2 s completion timer, which unmount cancels.
      expect(refreshModels).not.toHaveBeenCalled();
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

  it('stops the apply-all queue when a swap is cancelled, marking the item neither applied nor failed', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [
        v24Installed(),
        v24Installed({ id: 'second-model', name: 'Second Model', permanent: false }),
      ],
    });
    vi.mocked(modelsApi.installModel).mockResolvedValue(undefined);
    vi.mocked(modelsApi.cancelInstall).mockResolvedValue(undefined);
    let cancelled: (() => void) | undefined;
    vi.mocked(modelsApi.subscribeInstallProgress).mockImplementation(
      (id, onProgress, _onComplete, _onError, onCancelled) => {
        onProgress(downloadProgress({ catalogId: id }));
        cancelled = onCancelled;
        return () => {};
      }
    );
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.optimize\.review/ })
    );
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.optimize\.applyAll/ })
    );
    await waitFor(() => expect(modelsApi.installModel).toHaveBeenCalledTimes(1));

    await fireEvent.click(await screen.findByRole('button', { name: /^Cancel BirdNET v2\.4/ }));
    await waitFor(() => expect(modelsApi.cancelInstall).toHaveBeenCalledWith('birdnet-v2.4'));
    cancelled?.();

    await waitFor(() => expect(screen.queryByRole('button', { name: /^Cancel / })).toBeNull());
    // Give the completion effect and the queue a chance to start the next swap
    await tick();
    expect(modelsApi.installModel).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('analysis.gallery.optimize.applied')).toBeNull();
    expect(screen.queryByText(/^analysis\.gallery\.retry$/)).toBeNull();
  });

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

  it('shows no optimize banner for an installed model superseded by a visible successor', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [v24Installed({ supersededBy: { id: 'newer', name: 'Newer Model' } })],
    });
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.installed/ });
    expect(screen.queryByText('analysis.gallery.optimize.bannerTitle')).toBeNull();
    expect(screen.queryByRole('button', { name: /analysis\.gallery\.optimize\.swap/ })).toBeNull();
  });

  describe('update of an installed older release', () => {
    // An installed 3.0 build whose 3.0.1 successor is the same global build; the host
    // recommendation is a different (regional) variant, so Optimize would offer it
    // if the entry were not on the update path.
    function updatable(): CatalogEntry {
      const variant = (id: string, installed: boolean, recommended: boolean) => ({
        id,
        precision: 'fp32',
        default: false,
        installed,
        speciesCount: 10,
        sizeBytes: 1,
        compatible: true,
        recommended,
      });
      return birdEntry({
        installed: true,
        installedVariantId: 'fp32',
        recommendedVariantId: 'fp32-3.0.1@nordic',
        update: { variantId: 'fp32-3.0.1', fromVersion: '3.0', toVersion: '3.0.1' },
        variants: [
          variant('fp32', true, false),
          variant('fp32-3.0.1', false, false),
          variant('fp32-3.0.1@nordic', false, true),
        ],
      });
    }

    beforeEach(() => {
      vi.mocked(modelsApi.installModel).mockResolvedValue(undefined);
      vi.mocked(modelsApi.subscribeInstallProgress).mockImplementation(() => () => {});
    });

    it('shows no optimize banner or Optimize button, only the update note and action', async () => {
      vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [updatable()] });
      render(AnalysisSettingsPage);
      await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));

      expect(
        await screen.findByRole('button', { name: /analysis\.gallery\.update\.action/ })
      ).toBeInTheDocument();
      expect(screen.queryByText('analysis.gallery.optimize.bannerTitle')).toBeNull();
      expect(
        screen.queryByRole('button', { name: /analysis\.gallery\.optimize\.swap/ })
      ).toBeNull();
    });

    it('opens the dialog with the successor build selected, not the host recommendation', async () => {
      vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [updatable()] });
      render(AnalysisSettingsPage);
      await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));

      await fireEvent.click(
        await screen.findByRole('button', { name: /analysis\.gallery\.update\.action/ })
      );
      await fireEvent.click(
        await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
      );

      await waitFor(() =>
        expect(modelsApi.installModel).toHaveBeenCalledWith('test-bird', 'fp32-3.0.1')
      );
    });
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
    expect(within(previewCard).queryByRole('note')).toBeNull();
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

describe('AnalysisSettingsPage model gallery variant preselection', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    vi.mocked(modelsApi.fetchInstalled).mockResolvedValue([]);
    vi.mocked(modelsApi.installModel).mockResolvedValue(undefined);
    vi.mocked(modelsApi.subscribeInstallProgress).mockImplementation(() => () => {});
  });

  afterEach(() => {
    for (const mock of [
      modelsApi.installModel,
      modelsApi.subscribeInstallProgress,
      modelsApi.fetchModelRegions,
      modelsApi.fetchCatalog,
    ]) {
      vi.mocked(mock).mockReset();
    }
    settingsStores.birdnetSettings?.set(BIRDNET_FORM_DEFAULTS);
  });

  const slice = (id: string, region: string) => ({
    id,
    region,
    speciesCount: 10,
    default: false,
    installed: false,
    sizeBytes: 1,
    compatible: true,
    recommended: false,
  });
  const regionOption = (slug: string) => ({
    slug,
    name: slug,
    group: 'europe',
    groupDisplay: 'Europe',
    tier: 1,
    countries: { core: [], partial: [] },
  });

  // No recommended or installed id: the blocked global default comes first, then a
  // compatible africa slice, then a compatible nordic slice. Opens the install dialog
  // once the regions have loaded and accepts the license.
  function regionsResolvingTo(resolvedSlug: string): ModelRegionsResponse {
    return {
      modelRegion: 'auto',
      locationConfigured: true,
      resolved: { slug: resolvedSlug, source: 'auto', ambiguous: false },
      regions: [regionOption('africa'), regionOption('nordic')],
      families: [],
    };
  }

  // Renders the page and opens the install dialog of the regional entry, after the
  // regions response has been applied when waitForRegions is set.
  async function openInstallDialog(waitForRegions = false): Promise<void> {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [
        birdEntry({
          variants: [
            { ...slice('fp32', ''), default: true, compatible: false },
            slice('fp32@africa', 'africa'),
            slice('fp32@nordic', 'nordic'),
          ],
        }),
      ],
    });
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    if (waitForRegions) {
      await waitFor(() => expect(modelsApi.fetchModelRegions).toHaveBeenCalled());
      await vi.mocked(modelsApi.fetchModelRegions).mock.results[0]?.value;
      await tick();
    }
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
  }

  async function acceptLicense(): Promise<void> {
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    );
  }

  // Opens the install dialog once the regions have loaded and accepts the license.
  async function installWithRegions(resolvedSlug: string): Promise<void> {
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue(regionsResolvingTo(resolvedSlug));
    await openInstallDialog(true);
    await acceptLicense();
  }

  // Holds the regions response until release is called, so the dialog can open first.
  function holdRegions(resolvedSlug: string): () => Promise<void> {
    let release: (value: ModelRegionsResponse) => void = () => {};
    vi.mocked(modelsApi.fetchModelRegions).mockReturnValue(
      new Promise(resolve => {
        release = resolve;
      })
    );
    return async () => {
      release(regionsResolvingTo(resolvedSlug));
      await tick();
      await tick();
    };
  }

  it('preselects the region slice once the regions load after the dialog opened', async () => {
    const releaseRegions = holdRegions('nordic');
    await openInstallDialog();

    await releaseRegions();
    await acceptLicense();

    await waitFor(() =>
      expect(modelsApi.installModel).toHaveBeenCalledWith('test-bird', 'fp32@nordic')
    );
  });

  it('keeps a variant the user picked when the regions load afterwards', async () => {
    const releaseRegions = holdRegions('nordic');
    await openInstallDialog();
    // No region is active yet, so the other regions' slices sit behind the disclosures
    for (let i = 0; i < 2 && !document.getElementById('license-variant-fp32@africa'); i++) {
      await fireEvent.click(
        screen.getByRole('button', { name: /analysis\.gallery\.variants\.show/ })
      );
    }
    const africa = document.getElementById('license-variant-fp32@africa');
    if (!(africa instanceof HTMLInputElement)) throw new Error('the africa slice is not listed');
    await fireEvent.click(africa);

    await releaseRegions();
    await acceptLicense();

    await waitFor(() =>
      expect(modelsApi.installModel).toHaveBeenCalledWith('test-bird', 'fp32@africa')
    );
  });

  it("installs the auto-resolved region's slice when nothing is recommended and the global build is blocked", async () => {
    await installWithRegions('nordic');

    await waitFor(() =>
      expect(modelsApi.installModel).toHaveBeenCalledWith('test-bird', 'fp32@nordic')
    );
  });

  it('keeps the blocked global build preselected in Global mode, not the auto-resolved slice', async () => {
    settingsStores.birdnetSettings?.set({ ...BIRDNET_FORM_DEFAULTS, modelRegion: 'global' });

    await installWithRegions('nordic');

    // The blocked notice shows and the accepted license sends no install request
    expect(await screen.findAllByText('analysis.gallery.variants.incompatible')).not.toHaveLength(
      0
    );
    expect(modelsApi.installModel).not.toHaveBeenCalled();
  });

  it('installs the slice of the unsaved region picked in the form, not the auto-resolved one', async () => {
    settingsStores.birdnetSettings?.set({ ...BIRDNET_FORM_DEFAULTS, modelRegion: 'nordic' });

    await installWithRegions('africa');

    await waitFor(() =>
      expect(modelsApi.installModel).toHaveBeenCalledWith('test-bird', 'fp32@nordic')
    );
  });
});

describe('AnalysisSettingsPage model gallery next steps after an install', () => {
  // Longer than the completion timer that refreshes the gallery after an install
  const PAST_COMPLETION_MS = 5000;

  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    // The clock is driven by the tests; it also follows real time so the
    // testing-library waits keep working
    vi.useFakeTimers({ shouldAdvanceTime: true, toFake: ['setTimeout', 'clearTimeout'] });
    vi.mocked(wizardState.launch).mockReturnValue(true);
    // The refreshed model list includes the installed model, as after a real install
    vi.mocked(getAvailableModels).mockReturnValue([
      { id: 'test_bird', registryId: 'Test_Bird', name: 'Test Bird Model', category: 'bird' },
    ] as ReturnType<typeof getAvailableModels>);
    vi.mocked(modelsApi.fetchInstalled).mockResolvedValue([]);
    vi.mocked(modelsApi.fetchModelRegions).mockRejectedValue(new Error('no regions in test'));
    vi.mocked(modelsApi.installModel).mockResolvedValue(undefined);
    vi.mocked(modelsApi.reinstallModel).mockResolvedValue(undefined);
    vi.mocked(modelsApi.subscribeInstallProgress).mockImplementation(
      (_id, _onProgress, onComplete) => {
        onComplete();
        return () => {};
      }
    );
  });

  afterEach(() => {
    vi.useRealTimers();
    setWizardActive(false);
  });

  // The mock's isActive is a plain value; a test sets it to stand for an open wizard
  function setWizardActive(active: boolean): void {
    Object.defineProperty(wizardState, 'isActive', { value: active, configurable: true });
  }

  async function installFromLicenseDialog(cardButton: RegExp): Promise<void> {
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('tab', { name: /analysis\.gallery\.tabs\.available/ })
    );
    await fireEvent.click(await screen.findByRole('button', { name: cardButton }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    );
  }

  // Runs the completion timer and lets the settings refresh after it settle
  async function runCompletionTimer(): Promise<void> {
    await vi.advanceTimersByTimeAsync(PAST_COMPLETION_MS);
    await tick();
  }

  const FRESH_MODEL = {
    catalogId: 'test-bird',
    name: 'Test Bird Model',
    category: 'bird',
    registryId: 'Test_Bird',
  };

  it('offers the next steps for the installed model after a fresh install', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ registryId: 'Test_Bird' })],
    });

    await installFromLicenseDialog(installButtonName);
    await runCompletionTimer();

    expect(wizardState.launch).toHaveBeenCalledWith('model-next-steps', { model: FRESH_MODEL });
    expect(settingsActions.refreshKeepingEdits).toHaveBeenCalled();
    expect(toastActions.success).not.toHaveBeenCalled();
  });

  describe('with a document agreement', () => {
    beforeEach(() => {
      vi.mocked(modelsApi.fetchModelDocument).mockResolvedValue({
        repo: 'org/model',
        kind: 'acceptable_use',
        requiresAgreement: true,
        sha256: POLICY_SHA,
        text: 'policy text',
      });
    });

    afterEach(() => {
      vi.mocked(modelsApi.installModel).mockReset();
    });

    function gatedEntry(): CatalogEntry {
      return birdEntry({ registryId: 'Test_Bird', documents: [policyDocument] });
    }

    async function openLicenseDialog(): Promise<void> {
      render(AnalysisSettingsPage);
      await gotoAvailableTab();
      await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    }

    it('offers the next steps after an install that went through the agreement dialog', async () => {
      vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [gatedEntry()] });
      vi.mocked(modelsApi.installModel).mockResolvedValue(undefined);
      await openLicenseDialog();
      await fireEvent.click(
        await screen.findByRole('button', { name: /analysis\.gallery\.license\.continue/ })
      );
      await agreeToPolicy();

      await waitFor(() =>
        expect(modelsApi.installModel).toHaveBeenCalledWith('test-bird', undefined, [POLICY_SHA])
      );
      await runCompletionTimer();

      expect(wizardState.launch).toHaveBeenCalledWith('model-next-steps', { model: FRESH_MODEL });
    });

    it('offers the next steps after a refused install is resumed with the agreement', async () => {
      const refusal = agreementRefusal();
      vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
        catalog: [birdEntry({ registryId: 'Test_Bird' })],
      });
      vi.mocked(modelsApi.installModel).mockRejectedValueOnce(refusal).mockResolvedValue(undefined);
      await openLicenseDialog();
      await fireEvent.click(
        await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
      );
      await agreeToPolicy();

      await waitFor(() =>
        expect(modelsApi.installModel).toHaveBeenLastCalledWith('test-bird', undefined, [
          POLICY_SHA,
        ])
      );
      await runCompletionTimer();

      expect(wizardState.launch).toHaveBeenCalledWith('model-next-steps', { model: FRESH_MODEL });
    });

    it('keeps the next steps and the agreement when a failed agreed install is retried', async () => {
      vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [gatedEntry()] });
      vi.mocked(modelsApi.installModel)
        .mockRejectedValueOnce(serverError())
        .mockResolvedValue(undefined);
      await openLicenseDialog();
      await fireEvent.click(
        await screen.findByRole('button', { name: /analysis\.gallery\.license\.continue/ })
      );
      await agreeToPolicy();

      await fireEvent.click(
        await screen.findByRole('button', { name: /analysis\.gallery\.retry/ })
      );
      await waitFor(() => expect(modelsApi.installModel).toHaveBeenCalledTimes(2));
      expect(modelsApi.installModel).toHaveBeenLastCalledWith('test-bird', undefined, [POLICY_SHA]);
      await runCompletionTimer();

      expect(wizardState.launch).toHaveBeenCalledWith('model-next-steps', { model: FRESH_MODEL });
    });
  });

  it('shows the success toast instead when no next step applies', async () => {
    vi.mocked(wizardState.launch).mockReturnValue(false);
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [birdEntry()] });

    await installFromLicenseDialog(installButtonName);
    await runCompletionTimer();

    expect(toastActions.success).toHaveBeenCalled();
    expect(t).toHaveBeenCalledWith('analysis.gallery.installSuccess', { name: 'Test Bird Model' });
  });

  it('does not offer the next steps after a reinstall', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ id: 'inst', name: 'Installed Model', installed: true })],
    });
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.reinstall.*Installed Model/ })
    );

    await waitFor(() => expect(modelsApi.reinstallModel).toHaveBeenCalledWith('inst'));
    await runCompletionTimer();
    // The completion timer ran: it reloads the catalog
    expect(vi.mocked(modelsApi.fetchCatalog).mock.calls.length).toBeGreaterThan(1);
    expect(wizardState.launch).not.toHaveBeenCalled();
  });

  it('does not offer the next steps after a variant swap of an installed model', async () => {
    const variant = {
      speciesCount: 100,
      sizeBytes: 1000,
      compatible: true,
      default: false,
    };
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [
        birdEntry({
          id: 'inst',
          name: 'Installed Model',
          registryId: 'Test_Bird',
          installed: true,
          installedVariantId: 'fp32',
          recommendedVariantId: 'int8',
          variants: [
            { ...variant, id: 'fp32', precision: 'fp32', installed: true, recommended: false },
            { ...variant, id: 'int8', precision: 'int8', installed: false, recommended: true },
          ],
        }),
      ],
    });
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('button', {
        name: /analysis\.gallery\.optimize\.swap.*Installed Model/,
      })
    );
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    );

    await waitFor(() => expect(modelsApi.installModel).toHaveBeenCalledWith('inst', 'int8'));
    await runCompletionTimer();
    expect(wizardState.launch).not.toHaveBeenCalled();
  });

  it('still offers the next steps when a failed fresh install succeeds on Retry', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ registryId: 'Test_Bird' })],
    });
    vi.mocked(modelsApi.subscribeInstallProgress).mockImplementationOnce(
      (_id, _onProgress, _onComplete, onError) => {
        onError('download failed');
        return () => {};
      }
    );

    await installFromLicenseDialog(installButtonName);
    await fireEvent.click(await screen.findByRole('button', { name: /analysis\.gallery\.retry/ }));
    await runCompletionTimer();

    expect(wizardState.launch).toHaveBeenCalledWith('model-next-steps', { model: FRESH_MODEL });
  });

  it('shows the success toast instead of a second wizard when one is already open', async () => {
    setWizardActive(true);
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ registryId: 'Test_Bird' })],
    });

    await installFromLicenseDialog(installButtonName);
    await runCompletionTimer();

    expect(wizardState.launch).not.toHaveBeenCalled();
    expect(t).toHaveBeenCalledWith('analysis.gallery.installSuccess', { name: 'Test Bird Model' });
  });

  it('waits for the settings refresh before it offers the next steps', async () => {
    const refresh = deferred();
    vi.mocked(settingsActions.refreshKeepingEdits).mockReturnValueOnce(refresh.promise);
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ registryId: 'Test_Bird' })],
    });

    await installFromLicenseDialog(installButtonName);
    await runCompletionTimer();
    expect(wizardState.launch).not.toHaveBeenCalled();

    refresh.resolve();
    await tick();
    await waitFor(() =>
      expect(wizardState.launch).toHaveBeenCalledWith('model-next-steps', { model: FRESH_MODEL })
    );
  });

  it('waits for the model list refresh before it offers the next steps', async () => {
    const models = deferred();
    vi.mocked(refreshModels).mockReturnValueOnce(models.promise);
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ registryId: 'Test_Bird' })],
    });

    await installFromLicenseDialog(installButtonName);
    await runCompletionTimer();
    expect(wizardState.launch).not.toHaveBeenCalled();

    models.resolve();
    await waitFor(() =>
      expect(wizardState.launch).toHaveBeenCalledWith('model-next-steps', { model: FRESH_MODEL })
    );
  });

  it('points to the audio settings when the refreshed model list lacks the new model', async () => {
    // A refresh that failed or was already running leaves the old list in place
    vi.mocked(getAvailableModels).mockReturnValue([]);
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ registryId: 'Test_Bird' })],
    });

    await installFromLicenseDialog(installButtonName);
    await runCompletionTimer();

    expect(wizardState.launch).not.toHaveBeenCalled();
    expect(t).toHaveBeenCalledWith('analysis.gallery.installedEnableOnSources', {
      name: 'Test Bird Model',
    });
  });

  it('offers nothing when the page was left before the settings refresh finished', async () => {
    const refresh = deferred();
    vi.mocked(settingsActions.refreshKeepingEdits).mockReturnValueOnce(refresh.promise);
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ registryId: 'Test_Bird' })],
    });

    await installFromLicenseDialog(installButtonName);
    await runCompletionTimer();
    cleanup();
    refresh.resolve();
    await tick();

    expect(wizardState.launch).not.toHaveBeenCalled();
    expect(toastActions.success).not.toHaveBeenCalled();
  });

  it('points to the audio settings instead of the next steps when the settings refresh fails', async () => {
    vi.mocked(settingsActions.refreshKeepingEdits).mockRejectedValueOnce(new Error('offline'));
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ registryId: 'Test_Bird' })],
    });

    await installFromLicenseDialog(installButtonName);
    await runCompletionTimer();

    expect(wizardState.launch).not.toHaveBeenCalled();
    expect(t).toHaveBeenCalledWith('analysis.gallery.installedEnableOnSources', {
      name: 'Test Bird Model',
    });
  });

  it('points to the audio settings instead of the next steps when the settings never loaded', async () => {
    settingsStore.update(state => ({ ...state, dataLoaded: false }));
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ registryId: 'Test_Bird' })],
    });

    try {
      await installFromLicenseDialog(installButtonName);
      await runCompletionTimer();

      expect(wizardState.launch).not.toHaveBeenCalled();
      expect(t).toHaveBeenCalledWith('analysis.gallery.installedEnableOnSources', {
        name: 'Test Bird Model',
      });
    } finally {
      settingsStore.update(state => ({ ...state, dataLoaded: true }));
    }
  });
});

describe('AnalysisSettingsPage model gallery document agreements', () => {
  // An installed model with a recommended variant other than the installed one, so it
  // carries an optimize offer.
  function swappable(id: string, name: string): CatalogEntry {
    const variant = (variantId: string, installed: boolean) => ({
      id: variantId,
      precision: 'fp32',
      default: installed,
      installed,
      speciesCount: 1,
      sizeBytes: 1,
      compatible: true,
      recommended: !installed,
    });
    return birdEntry({
      id,
      name,
      installed: true,
      installedVariantId: 'old',
      recommendedVariantId: 'new',
      variants: [variant('old', true), variant('new', false)],
    });
  }

  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    vi.mocked(modelsApi.fetchInstalled).mockResolvedValue([]);
    vi.mocked(modelsApi.fetchModelRegions).mockRejectedValue(new Error('no regions in test'));
    vi.mocked(modelsApi.fetchModelDocument).mockResolvedValue({
      repo: 'org/model',
      kind: 'acceptable_use',
      requiresAgreement: true,
      sha256: POLICY_SHA,
      text: 'policy text',
    });
    vi.mocked(modelsApi.subscribeInstallProgress).mockReturnValue(() => {});
    // An earlier test may have dismissed the optimize banner for the session.
    sessionStorage.clear();
  });

  afterEach(() => {
    vi.mocked(modelsApi.installModel).mockReset();
    vi.mocked(modelsApi.reinstallModel).mockReset();
  });

  it('asks for an outstanding agreement before the first install request', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ documents: [policyDocument] })],
    });
    vi.mocked(modelsApi.installModel).mockResolvedValue(undefined);
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.continue/ })
    );
    expect(modelsApi.installModel).not.toHaveBeenCalled();

    await agreeToPolicy();

    await waitFor(() =>
      expect(modelsApi.installModel).toHaveBeenCalledWith('test-bird', undefined, [POLICY_SHA])
    );
    expect(modelsApi.installModel).toHaveBeenCalledTimes(1);
  });

  it('repeats a refused install with the accepted documents, and a cancel shows no error', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [birdEntry()] });
    vi.mocked(modelsApi.installModel)
      .mockRejectedValueOnce(agreementRefusal())
      .mockRejectedValueOnce(agreementRefusal())
      .mockResolvedValue(undefined);
    render(AnalysisSettingsPage);
    await gotoAvailableTab();

    // First attempt: the user cancels the agreement.
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    );
    await screen.findByRole('checkbox', { name: /analysis\.gallery\.agreement\.agree/ });
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('checkbox')).not.toBeInTheDocument());
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();

    // Second attempt: the user agrees and the same install is sent again with it.
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    );
    await agreeToPolicy();

    await waitFor(() =>
      expect(modelsApi.installModel).toHaveBeenLastCalledWith('test-bird', undefined, [POLICY_SHA])
    );
    expect(modelsApi.installModel).toHaveBeenCalledTimes(3);
  });

  it('repeats a refused reinstall with the accepted documents', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ id: 'inst', name: 'Installed Model', installed: true })],
    });
    vi.mocked(modelsApi.reinstallModel)
      .mockRejectedValueOnce(agreementRefusal())
      .mockResolvedValue(undefined);
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.reinstall.*Installed Model/ })
    );

    await agreeToPolicy();

    await waitFor(() =>
      expect(modelsApi.reinstallModel).toHaveBeenLastCalledWith('inst', [POLICY_SHA])
    );
  });

  it('repeats a refused optimize apply with the accepted documents', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [swappable('one', 'Model One')],
    });
    vi.mocked(modelsApi.installModel)
      .mockRejectedValueOnce(agreementRefusal())
      .mockResolvedValue(undefined);
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.optimize\.review/ })
    );
    await fireEvent.click(
      await screen.findByRole('button', { name: /^analysis\.gallery\.optimize\.apply$/ })
    );

    await agreeToPolicy();

    await waitFor(() =>
      expect(modelsApi.installModel).toHaveBeenLastCalledWith('one', 'new', [POLICY_SHA])
    );
  });

  it('shows the refusal reason when the refusal names no document to agree to', async () => {
    const refusal = agreementRefusal();
    refusal.errorParams = { name: 'Test Bird Model', documents: [{ repo: 'org/model' }] };
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({ catalog: [birdEntry()] });
    vi.mocked(modelsApi.installModel).mockRejectedValue(refusal);
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    );

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('needs agreement');
    expect(alert).not.toHaveTextContent('analysis.gallery.errors.details');
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });

  it('shows the refusal reason when a reinstall refusal names no document to agree to', async () => {
    const refusal = agreementRefusal();
    refusal.errorParams = { name: 'Installed Model', documents: [] };
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ id: 'inst', name: 'Installed Model', installed: true })],
    });
    vi.mocked(modelsApi.reinstallModel).mockRejectedValue(refusal);
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.reinstall.*Installed Model/ })
    );

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('needs agreement');
    expect(alert).not.toHaveTextContent('analysis.gallery.errors.details');
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });

  it('installs an entry whose agreement is already recorded without asking again', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ documents: [{ ...policyDocument, agreed: true }] })],
    });
    vi.mocked(modelsApi.installModel).mockResolvedValue(undefined);
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    );

    await waitFor(() =>
      expect(modelsApi.installModel).toHaveBeenCalledWith('test-bird', undefined)
    );
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });

  it('does not ask again for documents the server accepted before the catalog reloads', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ documents: [policyDocument] })],
    });
    vi.mocked(modelsApi.installModel).mockResolvedValue(undefined);
    // The download fails after the server accepted the request and its agreements.
    vi.mocked(modelsApi.subscribeInstallProgress).mockImplementation(
      (_id, _onProgress, _onComplete, onError) => {
        onError('download failed');
        return () => {};
      }
    );
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.continue/ })
    );
    await agreeToPolicy();
    await screen.findByRole('alert');

    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));

    expect(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.acceptAndInstall/ })
    ).toBeInTheDocument();
  });

  it('lists the other documents in the license dialog as a link or a text to view', async () => {
    const licenseSha = 'c'.repeat(64);
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [
        birdEntry({
          documents: [
            {
              repo: 'org/model',
              kind: 'license',
              requiresAgreement: false,
              agreed: false,
              sha256: licenseSha,
            },
            {
              repo: 'org/other',
              kind: 'license',
              requiresAgreement: false,
              agreed: false,
              url: 'https://example.org/license',
            },
            {
              repo: 'org/third',
              kind: 'license',
              requiresAgreement: false,
              agreed: false,
              url: 'javascript:alert(1)',
            },
            {
              repo: 'org/fourth',
              kind: 'license',
              requiresAgreement: false,
              agreed: false,
              url: 'http://example.org/plain',
            },
          ],
        }),
      ],
    });
    vi.mocked(modelsApi.fetchModelDocument).mockResolvedValue({
      repo: 'org/model',
      kind: 'license',
      requiresAgreement: false,
      sha256: licenseSha,
      text: 'license text',
    });
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));

    const links = await screen.findAllByRole('link', {
      name: /analysis\.gallery\.documents\.kinds\.license/,
    });
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveAttribute('href', 'https://example.org/license');
    expect(links[0]).toHaveAttribute('target', '_blank');
    expect(links[0]).toHaveAttribute('rel', 'noopener noreferrer');

    const view = screen.getByRole('button', { name: /analysis\.gallery\.documents\.view/ });
    expect(view).toHaveAttribute('aria-expanded', 'false');
    await fireEvent.click(view);
    expect(await screen.findByText('license text')).toBeInTheDocument();
    expect(modelsApi.fetchModelDocument).toHaveBeenCalledWith(licenseSha);
    const hide = screen.getByRole('button', { name: /analysis\.gallery\.documents\.hide/ });
    expect(hide).toHaveAttribute('aria-expanded', 'true');
    await fireEvent.click(hide);
    expect(screen.queryByText('license text')).not.toBeInTheDocument();
  });

  it('keeps the agreed documents when a failed install is retried', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ documents: [policyDocument] })],
    });
    vi.mocked(modelsApi.installModel)
      .mockRejectedValueOnce(serverError())
      .mockResolvedValue(undefined);
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.continue/ })
    );
    await agreeToPolicy();

    await fireEvent.click(await screen.findByRole('button', { name: /analysis\.gallery\.retry/ }));

    await waitFor(() => expect(modelsApi.installModel).toHaveBeenCalledTimes(2));
    expect(modelsApi.installModel).toHaveBeenLastCalledWith('test-bird', undefined, [POLICY_SHA]);
  });

  it('does not resend agreements the server already recorded when a download is retried', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ documents: [policyDocument] })],
    });
    vi.mocked(modelsApi.installModel).mockResolvedValue(undefined);
    // The server accepted the request, and its agreements, before the download failed.
    vi.mocked(modelsApi.subscribeInstallProgress).mockImplementation(
      (_id, _onProgress, _onComplete, onError) => {
        onError('download failed');
        return () => {};
      }
    );
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.continue/ })
    );
    await agreeToPolicy();

    await fireEvent.click(await screen.findByRole('button', { name: /analysis\.gallery\.retry/ }));

    await waitFor(() => expect(modelsApi.installModel).toHaveBeenCalledTimes(2));
    expect(modelsApi.installModel).toHaveBeenLastCalledWith('test-bird', undefined);
  });

  it('drops agreed documents the server refused as invalid before a Retry', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ documents: [policyDocument] })],
    });
    vi.mocked(modelsApi.installModel)
      .mockRejectedValueOnce(
        new ApiError('not an agreement document', 400, new Response(null, { status: 400 }))
      )
      .mockResolvedValue(undefined);
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.continue/ })
    );
    await agreeToPolicy();

    await fireEvent.click(await screen.findByRole('button', { name: /analysis\.gallery\.retry/ }));

    await waitFor(() => expect(modelsApi.installModel).toHaveBeenCalledTimes(2));
    expect(modelsApi.installModel).toHaveBeenLastCalledWith('test-bird', undefined);
  });

  it('resumes a second refusal with every agreed document', async () => {
    const otherSha = 'b'.repeat(64);
    const secondRefusal = agreementRefusal();
    secondRefusal.errorParams = {
      name: 'Test Bird Model',
      documents: [{ repo: 'org/model', kind: 'license', sha256: otherSha }],
    };
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ documents: [policyDocument] })],
    });
    vi.mocked(modelsApi.installModel)
      .mockRejectedValueOnce(secondRefusal)
      .mockResolvedValue(undefined);
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.continue/ })
    );
    await agreeToPolicy();
    await waitFor(() => expect(modelsApi.installModel).toHaveBeenCalledTimes(1));
    await agreeToPolicy();

    await waitFor(() => expect(modelsApi.installModel).toHaveBeenCalledTimes(2));
    const resumed = vi.mocked(modelsApi.installModel).mock.lastCall;
    expect(resumed?.slice(0, 2)).toEqual(['test-bird', undefined]);
    // The server treats the accepted list as a set, so only its members matter.
    expect([...(resumed?.[2] ?? [])].sort()).toEqual([POLICY_SHA, otherSha].sort());
  });

  it('shows the refusal instead of asking again for documents already sent', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [birdEntry({ documents: [policyDocument] })],
    });
    // The server refuses the start even though it carried the policy agreement.
    vi.mocked(modelsApi.installModel).mockRejectedValue(agreementRefusal());
    render(AnalysisSettingsPage);
    await gotoAvailableTab();
    await fireEvent.click(await screen.findByRole('button', { name: installButtonName }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.license\.continue/ })
    );
    await agreeToPolicy();

    await waitFor(() => expect(modelsApi.installModel).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /analysis\.gallery\.retry/ })).toBeInTheDocument()
    );
    expect(
      screen.queryByRole('checkbox', { name: /analysis\.gallery\.agreement\.agree/ })
    ).not.toBeInTheDocument();
  });

  it('keeps the agreed documents when a failed reinstall is retried', async () => {
    // The catalog lists the entry's own documents, which the refusal names again.
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [
        birdEntry({
          id: 'inst',
          name: 'Installed Model',
          installed: true,
          documents: [policyDocument],
        }),
      ],
    });
    vi.mocked(modelsApi.reinstallModel)
      .mockRejectedValueOnce(agreementRefusal())
      .mockRejectedValueOnce(serverError())
      .mockResolvedValue(undefined);
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.reinstall.*Installed Model/ })
    );
    await agreeToPolicy();

    await fireEvent.click(await screen.findByRole('button', { name: /analysis\.gallery\.retry/ }));

    await waitFor(() => expect(modelsApi.reinstallModel).toHaveBeenCalledTimes(3));
    expect(modelsApi.reinstallModel).toHaveBeenLastCalledWith('inst', [POLICY_SHA]);
  });

  it('moves the apply-all queue on when an agreement is cancelled', async () => {
    vi.mocked(modelsApi.fetchCatalog).mockResolvedValue({
      catalog: [swappable('one', 'Model One'), swappable('two', 'Model Two')],
    });
    vi.mocked(modelsApi.installModel)
      .mockRejectedValueOnce(agreementRefusal())
      .mockResolvedValue(undefined);
    render(AnalysisSettingsPage);
    await fireEvent.click(await screen.findByRole('tab', { name: /analysis\.tabs\.models/ }));
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.optimize\.review/ })
    );
    await fireEvent.click(
      await screen.findByRole('button', { name: /analysis\.gallery\.optimize\.applyAll/ })
    );

    await screen.findByRole('checkbox', { name: /analysis\.gallery\.agreement\.agree/ });
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    await waitFor(() => expect(modelsApi.installModel).toHaveBeenLastCalledWith('two', 'new'));
    expect(screen.getByText('analysis.gallery.optimize.applyFailed')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});
