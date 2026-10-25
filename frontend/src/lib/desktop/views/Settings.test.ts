import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, fireEvent, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { settingsActions, settingsStore } from '$lib/stores/settings';
import { settingsAPI } from '$lib/utils/settingsApi.js';
import { toastActions } from '$lib/stores/toast';
import { renderTyped } from '../../../test/render-helpers';
import { serveConfirmedSettings } from '../../../test/settings-store-helpers';
import Settings from './Settings.svelte';

// The page sections are heavy and irrelevant here: only the error banner is under test.
vi.mock('$lib/desktop/features/settings/pages/MainSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('$lib/desktop/features/settings/pages/AudioSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('$lib/desktop/features/settings/pages/FilterSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('$lib/desktop/features/settings/pages/IntegrationSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('$lib/desktop/features/settings/pages/SecuritySettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('$lib/desktop/features/settings/pages/SupportSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('$lib/desktop/features/settings/pages/SpeciesSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('$lib/desktop/features/settings/pages/NotificationsSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('$lib/desktop/features/settings/pages/UserInterfaceSettingsPage.svelte', () => ({
  default: () => {},
}));
vi.mock('$lib/desktop/features/settings/pages/AnalysisSettingsPage.svelte', () => ({
  default: () => {},
}));

const initialState = get(settingsStore);
const DISMISS_LABEL = 'common.aria.dismissAlert';

/** Make the form differ from the saved data, so a save has something to send. */
function makeUnsavedChange() {
  settingsStore.update(state => ({
    ...state,
    formData: { ...state.formData, main: { ...state.formData.main, name: 'changed' } },
  }));
}

/** Fail the next save with a plain server error and run the save to its rejection. */
async function failSave(options?: { notify?: boolean }) {
  makeUnsavedChange();
  vi.mocked(settingsAPI.save).mockRejectedValueOnce(
    Object.assign(new Error('boom'), { status: 500 })
  );
  await settingsActions.saveSettings(options).catch(() => undefined);
}

/** Fail the next load and run it to its rejection. */
async function failLoad() {
  vi.mocked(settingsAPI.load).mockRejectedValueOnce(new Error('load down'));
  await settingsActions.loadSettings().catch(() => undefined);
}

type Step = 'saveFails' | 'saveFailsWithoutToast' | 'loadFails';

async function runStep(step: Step) {
  switch (step) {
    case 'saveFails':
      return failSave();
    case 'saveFailsWithoutToast':
      return failSave({ notify: false });
    case 'loadFails':
      return failLoad();
  }
}

describe('Settings error banner Accessibility', () => {
  const originalLoad = vi.mocked(settingsAPI.load).getMockImplementation();
  const originalSave = vi.mocked(settingsAPI.save).getMockImplementation();

  beforeEach(async () => {
    vi.mocked(toastActions.error).mockClear();
    serveConfirmedSettings();
    renderTyped(Settings);
    await waitFor(() => {
      if (!get(settingsStore).dataLoaded) throw new Error('settings not loaded yet');
    });
  });

  afterEach(() => {
    settingsStore.set(initialState);
    vi.mocked(settingsAPI.load).mockReset();
    if (originalLoad) vi.mocked(settingsAPI.load).mockImplementation(originalLoad);
    vi.mocked(settingsAPI.save).mockReset();
    if (originalSave) vi.mocked(settingsAPI.save).mockImplementation(originalSave);
  });

  it.each<{
    name: string;
    steps: { step: Step; text: string; toasts: number; announces: boolean }[];
  }>([
    {
      name: 'a toasted save failure, then a load failure',
      steps: [
        { step: 'saveFails', text: 'boom', toasts: 1, announces: false },
        { step: 'loadFails', text: 'load down', toasts: 1, announces: true },
      ],
    },
    {
      name: 'a save failure without a toast',
      steps: [{ step: 'saveFailsWithoutToast', text: 'boom', toasts: 0, announces: true }],
    },
  ])(
    'announces a failure from the banner only when no toast announced it: $name',
    async ({ steps }) => {
      for (const { step, text, toasts, announces } of steps) {
        await runStep(step);

        expect(await screen.findByText(text)).toBeInTheDocument();
        expect(toastActions.error).toHaveBeenCalledTimes(toasts);
        // The banner is the only possible alert here: the toast store is mocked.
        expect(screen.queryAllByRole('alert').map(el => el.textContent.trim())).toEqual(
          announces ? [text] : []
        );
      }
    }
  );

  it('dismisses the banner and shows it again on the next failure', async () => {
    await failSave();
    expect(await screen.findByText('boom')).toBeInTheDocument();

    await fireEvent.click(screen.getByRole('button', { name: DISMISS_LABEL }));

    await waitFor(() => expect(screen.queryByText('boom')).toBeNull());
    expect(get(settingsStore).error).toBeNull();

    await failSave();
    expect(await screen.findByText('boom')).toBeInTheDocument();
  });
});
