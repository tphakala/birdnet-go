import { describe, it, expect, beforeAll, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor, within, cleanup } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';

// Page-level coverage for the range filter species dialog: it must take focus
// when View Species opens it, even though always-mounted Modal dialogs earlier
// in the document also carry a title, and it must be named by its own heading.

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
      loadRangeFilterSpecies: vi.fn().mockResolvedValue({
        count: 2,
        species: [
          { scientificName: 'Parus major', commonName: 'Great Tit' },
          { scientificName: 'Erithacus rubecula', commonName: 'European Robin' },
        ],
      }),
    },
    hasUnsavedChanges: writable(false),
    birdnetSettings: writable({
      huggingFaceEndpoint: '',
      threshold: 0.03,
      sensitivity: 1,
      overlap: 0,
      latitude: 60.1,
      longitude: 24.9,
      locationConfigured: true,
      rangeFilter: { threshold: 0.01 },
    }),
    dynamicThresholdSettings: writable({ enabled: false }),
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
      get: vi.fn().mockResolvedValue({ count: 2 }),
      post: vi.fn().mockResolvedValue({ count: 2, species: [] }),
      put: vi.fn().mockResolvedValue({}),
      delete: vi.fn().mockResolvedValue({}),
    },
    getCsrfToken: vi.fn().mockReturnValue('test-csrf-token'),
  };
});

import AnalysisSettingsPage from './AnalysisSettingsPage.svelte';
import { navigation } from '$lib/stores/navigation.svelte';

const DIALOG_TITLE = 'settings.main.sections.rangeFilter.modal.title';
const VIEW_SPECIES = /settings\.main\.sections\.rangeFilter\.speciesCount\.viewSpecies/;

// jsdom does not implement <dialog> showModal/close; the page's other dialogs call them
beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement) {
    this.open = false;
  };
});

describe('AnalysisSettingsPage range filter dialog', () => {
  let decoy: HTMLElement;

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: document.location,
      writable: true,
      configurable: true,
    });
    navigation.redirect('/ui/settings/analysis');
    // Stands in for an always-mounted Modal (the header's ConfirmModal) that comes
    // earlier in the document and carries the same shared title id
    decoy = document.createElement('div');
    decoy.setAttribute('role', 'dialog');
    decoy.setAttribute('aria-modal', 'true');
    decoy.setAttribute('aria-labelledby', 'modal-title');
    decoy.className = 'invisible';
    document.body.prepend(decoy);
  });

  afterEach(() => {
    cleanup();
    decoy.remove();
  });

  // The dialog that holds the page's own heading, whatever else carries its name
  const rangeDialog = (): HTMLElement => {
    const dialog = screen
      .getByRole('heading', { name: DIALOG_TITLE })
      .closest<HTMLElement>('[role="dialog"]');
    if (!dialog) throw new Error('The range filter heading is not inside a dialog');
    return dialog;
  };

  async function openDialog(user: ReturnType<typeof userEvent.setup>) {
    render(AnalysisSettingsPage);
    const viewSpecies = await screen.findByRole('button', { name: VIEW_SPECIES });
    await waitFor(() => expect(viewSpecies).toBeEnabled());
    await user.click(viewSpecies);
    return viewSpecies;
  }

  it('View Species moves focus into the range filter dialog', async () => {
    const user = userEvent.setup();
    await openDialog(user);

    await screen.findByRole('heading', { name: DIALOG_TITLE });
    await waitFor(() => expect(rangeDialog().contains(document.activeElement)).toBe(true));
    expect(within(rangeDialog()).getAllByRole('button')[0]).toHaveFocus();
  });

  it('Escape closes the range filter dialog and returns focus to View Species', async () => {
    const user = userEvent.setup();
    const viewSpecies = await openDialog(user);
    await screen.findByRole('heading', { name: DIALOG_TITLE });
    await waitFor(() => expect(rangeDialog().contains(document.activeElement)).toBe(true));

    await user.keyboard('{Escape}');

    await waitFor(() => expect(screen.queryByRole('heading', { name: DIALOG_TITLE })).toBeNull());
    expect(viewSpecies).toHaveFocus();
  });

  it('does not move focus on close when View Species was removed while the dialog was open', async () => {
    const user = userEvent.setup();
    const viewSpecies = await openDialog(user);
    await screen.findByRole('heading', { name: DIALOG_TITLE });
    await waitFor(() => expect(rangeDialog().contains(document.activeElement)).toBe(true));
    const focusSpy = vi.spyOn(viewSpecies, 'focus');
    viewSpecies.remove();

    await user.keyboard('{Escape}');

    await waitFor(() => expect(screen.queryByRole('heading', { name: DIALOG_TITLE })).toBeNull());
    expect(focusSpy).not.toHaveBeenCalled();
  });

  it('the range filter dialog is named by its own title', async () => {
    const user = userEvent.setup();
    await openDialog(user);

    const dialog = await screen.findByRole('dialog', { name: DIALOG_TITLE });
    expect(dialog).toHaveAttribute('aria-labelledby');
    expect(dialog.getAttribute('aria-labelledby')).not.toBe('modal-title');
  });
});
