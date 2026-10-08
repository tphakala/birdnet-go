import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import type { SettingsFormData } from '$lib/stores/settings';

// Mock API to prevent network calls during mount
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn().mockResolvedValue({ en: 'English', hu: 'Magyar' }),
    post: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    data?: unknown;
    constructor(message: string, status: number, data?: unknown) {
      super(message);
      this.status = status;
      this.data = data;
    }
  },
}));

// Mock LocationPickerMap (relies on maplibre-gl) to keep this test focused
vi.mock('../components/LocationPickerMap.svelte');

// Mock LanguageSelector - we manipulate the UI locale directly via getLocale() mock
vi.mock('$lib/desktop/components/ui/LanguageSelector.svelte');

// Controllable getLocale mock shared across tests
let currentLocale = 'en';

vi.mock('$lib/i18n', async () => {
  return {
    t: vi.fn((key: string) => key),
    getLocale: vi.fn(() => currentLocale),
    setLocale: vi.fn((locale: string) => {
      currentLocale = locale;
    }),
    isValidLocale: vi.fn(() => true),
  };
});

// Mock the settings module with a controllable store and a spied saveSection
vi.mock('$lib/stores/settings', async () => {
  const { createSettingsMock } = await import('./stepTestUtils');
  return createSettingsMock({
    birdnet: { latitude: 40, longitude: -74, locale: 'en' },
    realtime: {
      dashboard: {
        thumbnails: {
          summary: true,
          recent: true,
          imageProvider: 'wikimedia',
          fallbackPolicy: 'all',
        },
        summaryLimit: 100,
        locale: 'en',
      },
    },
  });
});

import LocationLanguageStep from './LocationLanguageStep.svelte';
import LanguageSelector from '$lib/desktop/components/ui/LanguageSelector.svelte';
import { settingsActions, settingsStore } from '$lib/stores/settings';
import { setLocale } from '$lib/i18n';
import { flushAsync, renderStep } from './stepTestUtils';

// The leave handler contract shared by every step is in stepContract.test.ts
describe('LocationLanguageStep - UI locale persistence in the leave handler', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(settingsActions.saveSection).mockResolvedValue(undefined);
    currentLocale = 'en';

    // Reset store to pristine state before every test
    settingsStore.set({
      isLoading: false,
      isSaving: false,
      error: null,
      dataLoaded: true,
      activeSection: 'main',
      originalData: {
        birdnet: {
          latitude: 40,
          longitude: -74,
          locale: 'en',
        },
        realtime: {
          dashboard: {
            thumbnails: {
              summary: true,
              recent: true,
              imageProvider: 'wikimedia',
              fallbackPolicy: 'all',
            },
            summaryLimit: 100,
            locale: 'en',
          },
        },
      } as unknown as SettingsFormData,
      formData: {
        birdnet: {
          latitude: 40,
          longitude: -74,
          locale: 'en',
        },
        realtime: {
          dashboard: {
            thumbnails: {
              summary: true,
              recent: true,
              imageProvider: 'wikimedia',
              fallbackPolicy: 'all',
            },
            summaryLimit: 100,
            locale: 'en',
          },
        },
      } as unknown as SettingsFormData,
    });
  });

  /** The saveSection calls so far as [section, payload] pairs. */
  const sectionCalls = () => vi.mocked(settingsActions.saveSection).mock.calls;

  /** Types a new latitude into the first number input (marks the step dirty). */
  async function editLatitude(container: HTMLElement, value: string) {
    const latitudeInput = container.querySelector('input[type="number"]');
    if (!(latitudeInput instanceof HTMLInputElement)) throw new Error('latitude input not found');
    await fireEvent.input(latitudeInput, { target: { value } });
    await fireEvent.change(latitudeInput, { target: { value } });
  }

  it('persists UI locale to realtime.dashboard when only the UI locale changed (dirty=false)', async () => {
    const { leave } = renderStep(LocationLanguageStep);
    await flushAsync();

    // Simulate LanguageSelector invoking setLocale
    setLocale('hu');

    await leave();

    // Only the dashboard locale is sent; birdnet is not saved because dirty is false
    expect(sectionCalls()).toEqual([['dashboard', { locale: 'hu' }]]);
    expect(settingsActions.saveSettings).not.toHaveBeenCalled();
    expect(settingsActions.updateSection).not.toHaveBeenCalled();
  });

  it('heals drift-on-entry: saves backend-merged locale when runtime differs from persisted at mount, even without interaction', async () => {
    // Simulate runtime/localStorage drift: the i18n runtime already holds "hu"
    // (e.g. a prior wizard run set localStorage but failed to persist to
    // config.yaml). The backend still shows "en" (the default).
    currentLocale = 'hu';

    const { leave } = renderStep(LocationLanguageStep);
    await flushAsync();

    // User does NOT interact with the wizard - no setLocale, no field edits.
    await leave();

    // The leave handler must still save the dashboard locale so the backend
    // is healed to match the runtime choice.
    expect(sectionCalls()).toEqual([['dashboard', { locale: 'hu' }]]);
  });

  it('heals drift-on-entry when backend locale is undefined (fresh-install case): runtime locale gets persisted without interaction', async () => {
    // Fresh install scenario: backend Dashboard.Locale is the Go zero
    // value "" which the API's `omitempty` tag serializes as undefined.
    // The browser is in Hungarian, so the runtime locale is "hu".
    currentLocale = 'hu';
    settingsStore.update(state => {
      const realtime = state.formData.realtime as Record<string, unknown>;
      const dashboard = realtime.dashboard as Record<string, unknown>;
      delete dashboard.locale;
      return state;
    });

    const { leave } = renderStep(LocationLanguageStep);
    await flushAsync();

    // User does not interact.
    await leave();

    // The leave handler must still persist the runtime locale so the backend is
    // initialized to match what the user is seeing.
    expect(sectionCalls()).toEqual([['dashboard', { locale: 'hu' }]]);
  });

  it('a second leave call after a successful save does nothing', async () => {
    currentLocale = 'hu';
    const { leave } = renderStep(LocationLanguageStep);
    await flushAsync();

    await leave();
    await leave();

    expect(settingsActions.saveSection).toHaveBeenCalledTimes(1);
  });

  it('sends birdnet then dashboard when both change', async () => {
    const { leave, container } = renderStep(LocationLanguageStep);
    await flushAsync();
    await editLatitude(container, '41.5');

    // Change the UI locale via the mocked i18n store
    setLocale('hu');

    await leave();

    expect(sectionCalls()).toEqual([
      ['birdnet', { latitude: 41.5, longitude: -74, locale: 'en', locationConfigured: true }],
      ['dashboard', { locale: 'hu' }],
    ]);
  });

  it('the birdnet payload carries locationConfigured true for non-zero coordinates', async () => {
    const { leave, container } = renderStep(LocationLanguageStep);
    await flushAsync();
    await editLatitude(container, '41.5');

    await leave();

    expect(sectionCalls()).toEqual([
      ['birdnet', { latitude: 41.5, longitude: -74, locale: 'en', locationConfigured: true }],
    ]);
  });

  it('the birdnet payload omits locationConfigured for zero coordinates', async () => {
    settingsStore.update(state => {
      const birdnet = state.formData.birdnet as unknown as Record<string, unknown>;
      birdnet.latitude = 0;
      birdnet.longitude = 0;
      return state;
    });
    const { leave, container } = renderStep(LocationLanguageStep);
    await flushAsync();
    await editLatitude(container, '0');

    await leave();

    expect(sectionCalls()).toEqual([['birdnet', { latitude: 0, longitude: 0, locale: 'en' }]]);
  });

  it('a failed dashboard save after a successful birdnet save retries only dashboard', async () => {
    vi.mocked(settingsActions.saveSection)
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error('dashboard failed'));
    const { leave, container } = renderStep(LocationLanguageStep);
    await flushAsync();
    await editLatitude(container, '41.5');
    setLocale('hu');

    await expect(leave()).rejects.toThrow('dashboard failed');
    vi.mocked(settingsActions.saveSection).mockClear();
    await leave();

    expect(sectionCalls()).toEqual([['dashboard', { locale: 'hu' }]]);
  });

  it('restores the UI language when the step is left without saving', async () => {
    const { unmount } = renderStep(LocationLanguageStep);
    await flushAsync();

    setLocale('hu');
    unmount();

    expect(currentLocale).toBe('en');
  });

  it('keeps the UI language after it was saved', async () => {
    const { leave, unmount } = renderStep(LocationLanguageStep);
    await flushAsync();

    setLocale('hu');
    await leave();
    unmount();

    expect(currentLocale).toBe('hu');
  });

  it('keeps the UI language when the step unmounts while its save then succeeds', async () => {
    let resolveSave: () => void = () => {};
    vi.mocked(settingsActions.saveSection).mockImplementationOnce(
      () =>
        new Promise<void>(resolve => {
          resolveSave = resolve;
        })
    );
    const { leave, unmount } = renderStep(LocationLanguageStep);
    await flushAsync();

    setLocale('hu');
    const pending = leave();
    await flushAsync();
    unmount();
    expect(currentLocale).toBe('hu');
    resolveSave();
    await pending;
    await flushAsync();

    expect(currentLocale).toBe('hu');
  });

  it('sends no dashboard save once the step unmounted during the birdnet save, and restores the UI language', async () => {
    let resolveSave: () => void = () => {};
    vi.mocked(settingsActions.saveSection).mockImplementationOnce(
      () =>
        new Promise<void>(resolve => {
          resolveSave = resolve;
        })
    );
    const { leave, unmount, container } = renderStep(LocationLanguageStep);
    await flushAsync();
    await editLatitude(container, '41.5');
    setLocale('hu');

    const pending = leave();
    await flushAsync();
    unmount();
    resolveSave();
    await pending;
    await flushAsync();

    expect(sectionCalls().map(([section]) => section)).toEqual(['birdnet']);
    expect(currentLocale).toBe('en');
  });

  it('restores the UI language when the step unmounts while its save then fails', async () => {
    let rejectSave: (err: Error) => void = () => {};
    vi.mocked(settingsActions.saveSection).mockImplementationOnce(
      () =>
        new Promise<void>((_, reject) => {
          rejectSave = reject;
        })
    );
    const { leave, unmount } = renderStep(LocationLanguageStep);
    await flushAsync();

    setLocale('hu');
    const pending = leave();
    await flushAsync();
    unmount();
    rejectSave(new Error('save failed'));
    await expect(pending).rejects.toThrow('save failed');
    await flushAsync();

    expect(currentLocale).toBe('en');
  });
});

describe('LocationLanguageStep Accessibility', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    currentLocale = 'en';
  });

  const speciesLabel = (container: HTMLElement) =>
    container.querySelector('label[for="wizard-species-locale"]');

  it('labels the species language dropdown only once it is rendered', async () => {
    const { container } = renderStep(LocationLanguageStep);

    // While the locales load there is no control for the label to point at
    expect(speciesLabel(container)).toBeNull();
    expect(document.getElementById('wizard-species-locale')).toBeNull();

    await waitFor(() => expect(document.getElementById('wizard-species-locale')).not.toBeNull());
    expect(speciesLabel(container)).not.toBeNull();
  });

  it('describes the species language dropdown with its help text', async () => {
    renderStep(LocationLanguageStep);
    const trigger = await waitFor(() => {
      const el = document.getElementById('wizard-species-locale');
      if (!el) throw new Error('species dropdown not rendered');
      return el;
    });

    expect(trigger).toHaveAccessibleDescription(
      'wizard.steps.locationLanguage.speciesLanguageHelp'
    );
  });

  describe('Use my location', () => {
    const getCurrentPosition = vi.fn();

    beforeEach(() => {
      getCurrentPosition.mockReset();
      vi.stubGlobal('navigator', { ...navigator, geolocation: { getCurrentPosition } });
      vi.stubGlobal('isSecureContext', true);
    });

    afterEach(() => {
      vi.unstubAllGlobals();
    });

    it('Use my location is disabled while a position is requested', async () => {
      renderStep(LocationLanguageStep);
      const button = await screen.findByRole('button', {
        name: 'wizard.steps.locationLanguage.useMyLocation',
      });
      expect(button).toBeEnabled();

      await fireEvent.click(button);

      expect(getCurrentPosition).toHaveBeenCalledTimes(1);
      expect(button).toBeDisabled();
    });
  });

  it('describes the UI language selector with its help text', () => {
    vi.mocked(LanguageSelector).mockClear();
    const { container } = renderStep(LocationLanguageStep);

    const help = container.querySelector('p[id^="wizard-ui-language-help"]');
    expect(help).toHaveTextContent('wizard.steps.locationLanguage.uiLanguageHelp');
    const props = vi.mocked(LanguageSelector).mock.calls[0]?.[1];
    expect(props).toMatchObject({ 'aria-describedby': help?.id });
  });
});
