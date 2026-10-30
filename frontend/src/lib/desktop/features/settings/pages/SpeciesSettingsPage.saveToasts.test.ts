import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { get } from 'svelte/store';
import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import {
  SettingsChangedElsewhereError,
  settingsStore,
  type GlobalSettingsState,
  type SettingsFormData,
} from '$lib/stores/settings';
import { toastActions } from '$lib/stores/toast';
import { settingsAPI } from '$lib/utils/settingsApi';
import { loggers } from '$lib/utils/logger';
import SpeciesSettingsPage from './SpeciesSettingsPage.svelte';

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: {
    get: vi.fn().mockResolvedValue({}),
    post: vi.fn().mockResolvedValue({}),
    put: vi.fn().mockResolvedValue({}),
    delete: vi.fn().mockResolvedValue({}),
  },
}));

const SPECIES = 'Turdus merula';
const HTTP_PRECONDITION_FAILED = 412;
// A 412 on every attempt makes the store give up with SettingsChangedElsewhereError after one resend
const SAVE_ATTEMPTS_BEFORE_CONFLICT = 2;

describe('SpeciesSettingsPage configuration save toasts', () => {
  const originalSave = vi.mocked(settingsAPI.save).getMockImplementation();
  let originalStoreState: GlobalSettingsState;
  let errorToast: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn();
    originalStoreState = get(settingsStore);
    errorToast = vi.spyOn(toastActions, 'error').mockReturnValue('toast-id');
    vi.mocked(loggers.settings.warn).mockClear();
    vi.mocked(loggers.settings.error).mockClear();
    // Partial on purpose: SettingsFormData is large and the page reads only these fields here
    const formData = {
      realtime: {
        species: {
          include: [],
          exclude: [],
          config: { [SPECIES]: { threshold: 0.7, interval: 15, actions: [] } },
        },
      },
      taxonomySynonyms: {},
    } as unknown as SettingsFormData;
    settingsStore.update(state => ({
      ...state,
      formData,
      originalData: JSON.parse(JSON.stringify(formData)),
      dataLoaded: true,
      isSaving: false,
      error: null,
      errorToasted: false,
    }));
  });

  afterEach(() => {
    vi.mocked(settingsAPI.save).mockReset();
    if (originalSave) vi.mocked(settingsAPI.save).mockImplementation(originalSave);
    settingsStore.set(originalStoreState);
    errorToast.mockRestore();
  });

  it.each([
    {
      name: 'a generic failure',
      failure: new Error('HTTP 500'),
      saveCalls: 1,
      toastMessage: 'notifications.content.settings.saveFailed',
      warnCalls: 0,
      errorCalls: 1,
    },
    {
      name: 'a changed-elsewhere conflict',
      failure: Object.assign(new Error('HTTP 412'), { status: HTTP_PRECONDITION_FAILED }),
      saveCalls: SAVE_ATTEMPTS_BEFORE_CONFLICT,
      toastMessage: new SettingsChangedElsewhereError().message,
      warnCalls: 1,
      errorCalls: 0,
    },
  ])(
    'reports $name with the store toast only and logs it once at the right level',
    async ({ failure, saveCalls, toastMessage, warnCalls, errorCalls }) => {
      vi.mocked(settingsAPI.save).mockRejectedValue(failure);
      const user = userEvent.setup();
      render(SpeciesSettingsPage);

      await user.click(
        await screen.findByRole('tab', { name: /settings\.species\.customConfiguration\.tabLabel/ })
      );
      await user.click(
        await screen.findByRole('button', {
          name: 'settings.species.customConfiguration.list.editTitle',
        })
      );
      await user.click(
        await screen.findByRole('button', { name: 'settings.species.customConfiguration.save' })
      );

      await waitFor(() => expect(settingsAPI.save).toHaveBeenCalledTimes(saveCalls));
      await waitFor(() => expect(errorToast).toHaveBeenCalled());
      expect(errorToast).toHaveBeenCalledTimes(1);
      expect(errorToast).toHaveBeenCalledWith(toastMessage);
      await waitFor(() => expect(loggers.settings.warn).toHaveBeenCalledTimes(warnCalls));
      expect(loggers.settings.error).toHaveBeenCalledTimes(errorCalls);
    }
  );
});
