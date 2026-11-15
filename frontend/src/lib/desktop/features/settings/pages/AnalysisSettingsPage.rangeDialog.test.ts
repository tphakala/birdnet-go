import { describe, it, expect, beforeAll, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor, within, cleanup, fireEvent } from '@testing-library/svelte';
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

// The birdnet settings the page reads, shared with the tests that change the station location
const birdnetStore = vi.hoisted(() => {
  const initial = {
    huggingFaceEndpoint: '',
    threshold: 0.03,
    sensitivity: 1,
    overlap: 0,
    latitude: 60.1,
    longitude: 24.9,
    locationConfigured: true,
    rangeFilter: { threshold: 0.01 },
  };
  let value = initial;
  const subscribers = new Set<(settings: typeof initial) => void>();
  return {
    initial,
    subscribe(run: (settings: typeof initial) => void) {
      subscribers.add(run);
      run(value);
      return () => subscribers.delete(run);
    },
    set(next: typeof initial) {
      value = next;
      subscribers.forEach(run => run(value));
    },
  };
});

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
    birdnetSettings: birdnetStore,
    dynamicThresholdSettings: writable({ enabled: false }),
    realtimeSettings: writable({}),
    batSettings: writable({}),
    bsgBatSettings: writable({}),
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
import { settingsActions, settingsStore } from '$lib/stores/settings';
import { get } from 'svelte/store';
import { api } from '$lib/utils/api';

const DIALOG_TITLE = 'settings.main.sections.rangeFilter.modal.title';
const REASON_LOADING_SPECIES = 'settings.main.sections.rangeFilter.modal.loadingSpecies';
const REASON_NO_SPECIES = 'settings.main.sections.rangeFilter.modal.noSpeciesFound';
const REASON_SETTINGS_LOADING = 'settings.loading';
const REASON_NO_LOCATION = 'settings.species.activeSpecies.locationNotConfigured.title';
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

  it('returns focus to View Species when it was pressed without taking focus', async () => {
    const user = userEvent.setup();
    render(AnalysisSettingsPage);
    const viewSpecies = await screen.findByRole('button', { name: VIEW_SPECIES });
    await waitFor(() => expect(viewSpecies).toBeEnabled());
    // Safari and iPadOS do not focus a clicked button
    await fireEvent.pointerDown(viewSpecies);
    await fireEvent.click(viewSpecies);
    await screen.findByRole('heading', { name: DIALOG_TITLE });
    await waitFor(() => expect(rangeDialog().contains(document.activeElement)).toBe(true));

    await user.keyboard('{Escape}');

    await waitFor(() => expect(screen.queryByRole('heading', { name: DIALOG_TITLE })).toBeNull());
    expect(viewSpecies).toHaveFocus();
  });

  it('keeps View Species focusable while the species load and returns focus to it on an early close', async () => {
    // A load that never settles keeps the dialog in its loading state
    vi.mocked(settingsActions.loadRangeFilterSpecies).mockImplementationOnce(
      () => new Promise(() => {})
    );
    const user = userEvent.setup();
    const viewSpecies = await openDialog(user);
    await screen.findByRole('heading', { name: DIALOG_TITLE });
    await waitFor(() => expect(rangeDialog().contains(document.activeElement)).toBe(true));

    // A natively disabled button would have dropped focus to body when the load began
    expect(viewSpecies).toHaveAttribute('aria-disabled', 'true');
    expect(viewSpecies).not.toBeDisabled();
    await user.keyboard('{Escape}');

    await waitFor(() => expect(screen.queryByRole('heading', { name: DIALOG_TITLE })).toBeNull());
    expect(viewSpecies).toHaveFocus();
    // A click while the species load must not reopen the dialog
    await user.click(viewSpecies);
    expect(screen.queryByRole('heading', { name: DIALOG_TITLE })).toBeNull();
  });

  describe('the View Species button says why it is blocked', () => {
    // The text the button points at through aria-describedby
    function reasonOf(button: HTMLElement): string | undefined {
      const id = button.getAttribute('aria-describedby');
      return id ? (document.getElementById(id)?.textContent.trim() ?? undefined) : undefined;
    }

    it('names the load while the species list loads', async () => {
      vi.mocked(settingsActions.loadRangeFilterSpecies).mockImplementationOnce(
        () => new Promise(() => {})
      );
      const user = userEvent.setup();
      const viewSpecies = await openDialog(user);

      expect(viewSpecies).toHaveAttribute('aria-disabled', 'true');
      expect(reasonOf(viewSpecies)).toBe(REASON_LOADING_SPECIES);
    });

    it('keeps the focus ring at full strength while the faded button is blocked', async () => {
      birdnetStore.set({ ...birdnetStore.initial, locationConfigured: false });
      try {
        render(AnalysisSettingsPage);
        const viewSpecies = await screen.findByRole('button', { name: VIEW_SPECIES });

        // The blocked button stays focusable; opacity on the element would fade its ring too.
        expect(viewSpecies).toHaveAttribute('aria-disabled', 'true');
        expect(viewSpecies).toHaveClass('focus-ring', 'aria-disabled:focus-visible:opacity-100');
      } finally {
        birdnetStore.set(birdnetStore.initial);
      }
    });

    it('names the missing location', async () => {
      birdnetStore.set({ ...birdnetStore.initial, locationConfigured: false });
      try {
        render(AnalysisSettingsPage);
        const viewSpecies = await screen.findByRole('button', { name: VIEW_SPECIES });

        expect(viewSpecies).toHaveAttribute('aria-disabled', 'true');
        expect(reasonOf(viewSpecies)).toBe(REASON_NO_LOCATION);
      } finally {
        birdnetStore.set(birdnetStore.initial);
      }
    });

    it('names the settings load before the location, which is unset until they arrive', async () => {
      const original = get(settingsStore);
      settingsStore.set({ ...original, isLoading: true });
      birdnetStore.set({ ...birdnetStore.initial, locationConfigured: false });
      try {
        render(AnalysisSettingsPage);
        const viewSpecies = await screen.findByRole('button', { name: VIEW_SPECIES });

        expect(viewSpecies).toHaveAttribute('aria-disabled', 'true');
        expect(reasonOf(viewSpecies)).toBe(REASON_SETTINGS_LOADING);
      } finally {
        settingsStore.set(original);
        birdnetStore.set(birdnetStore.initial);
      }
    });

    it('names an empty species list', async () => {
      vi.mocked(api.get).mockResolvedValueOnce({ count: 0 });
      vi.mocked(api.post).mockResolvedValueOnce({ count: 0, species: [] });
      render(AnalysisSettingsPage);
      const viewSpecies = await screen.findByRole('button', { name: VIEW_SPECIES });

      await waitFor(() => expect(reasonOf(viewSpecies)).toBe(REASON_NO_SPECIES));
      expect(viewSpecies).toHaveAttribute('aria-disabled', 'true');
    });

    it('has no reason once the button works', async () => {
      render(AnalysisSettingsPage);
      const viewSpecies = await screen.findByRole('button', { name: VIEW_SPECIES });

      await waitFor(() => expect(viewSpecies).not.toHaveAttribute('aria-disabled'));
      expect(viewSpecies).not.toHaveAttribute('aria-describedby');
    });
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
