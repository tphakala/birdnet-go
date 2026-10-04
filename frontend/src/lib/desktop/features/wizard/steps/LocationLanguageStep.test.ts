import { describe, it, expect, beforeEach, vi } from 'vitest';
import { fireEvent } from '@testing-library/svelte';
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

// Mock the settings module with a controllable store; updateSection merges into it
vi.mock('$lib/stores/settings', async () => {
  const { createSettingsMock } = await import('./stepTestUtils');
  return createSettingsMock(
    {
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
    },
    { applyUpdates: true }
  );
});

import LocationLanguageStep from './LocationLanguageStep.svelte';
import { settingsActions, settingsStore } from '$lib/stores/settings';
import { setLocale } from '$lib/i18n';
import { flushAsync, renderStep } from './stepTestUtils';

// The leave handler contract shared by every step is in stepContract.test.ts
describe('LocationLanguageStep - UI locale persistence in the leave handler', () => {
  beforeEach(() => {
    vi.clearAllMocks();
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

  it('persists UI locale to realtime.dashboard when only the UI locale changed (dirty=false)', async () => {
    const { leave } = renderStep(LocationLanguageStep);
    await flushAsync();

    // Simulate LanguageSelector invoking setLocale
    setLocale('hu');

    await leave();

    // realtime update must be dispatched even though no other field is dirty
    const realtimeCall = (
      settingsActions.updateSection as unknown as ReturnType<typeof vi.fn>
    ).mock.calls.find(([section]) => section === 'realtime');
    expect(realtimeCall).toBeDefined();
    const [, payload] = realtimeCall as [string, { dashboard: Record<string, unknown> }];
    expect(payload.dashboard.locale).toBe('hu');
    // Existing dashboard fields must be preserved (shallow merge)
    expect(payload.dashboard.summaryLimit).toBe(100);
    expect(payload.dashboard.thumbnails).toBeDefined();

    // Birdnet section should NOT have been updated because dirty is false
    const birdnetCall = (
      settingsActions.updateSection as unknown as ReturnType<typeof vi.fn>
    ).mock.calls.find(([section]) => section === 'birdnet');
    expect(birdnetCall).toBeUndefined();

    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(1);
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

    // The leave handler must still fire the realtime update so the backend
    // is healed to match the runtime choice.
    const realtimeCall = (
      settingsActions.updateSection as unknown as ReturnType<typeof vi.fn>
    ).mock.calls.find(([section]) => section === 'realtime');
    expect(realtimeCall).toBeDefined();
    const [, payload] = realtimeCall as [string, { dashboard: Record<string, unknown> }];
    expect(payload.dashboard.locale).toBe('hu');
    // Dashboard fields from the store snapshot must be preserved (shallow merge)
    expect(payload.dashboard.summaryLimit).toBe(100);
    expect(payload.dashboard.thumbnails).toBeDefined();

    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(1);
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
    const realtimeCall = (
      settingsActions.updateSection as unknown as ReturnType<typeof vi.fn>
    ).mock.calls.find(([section]) => section === 'realtime');
    expect(realtimeCall).toBeDefined();
    const [, payload] = realtimeCall as [string, { dashboard: Record<string, unknown> }];
    expect(payload.dashboard.locale).toBe('hu');
    expect(payload.dashboard.summaryLimit).toBe(100);

    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(1);
  });

  it('saves without toasts', async () => {
    currentLocale = 'hu';
    const { leave } = renderStep(LocationLanguageStep);
    await flushAsync();

    await leave();

    expect(settingsActions.saveSettings).toHaveBeenCalledWith({ notify: false });
  });

  it('a second leave call after a successful save does nothing', async () => {
    currentLocale = 'hu';
    const { leave } = renderStep(LocationLanguageStep);
    await flushAsync();

    await leave();
    await leave();

    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(1);
  });

  it('dispatches both realtime and birdnet updates with a single save when both change', async () => {
    const { leave, container } = renderStep(LocationLanguageStep);
    await flushAsync();

    // Change the latitude via the underlying NumberField input (triggers dirty=true)
    const numberInputs = container.querySelectorAll('input[type="number"]');
    expect(numberInputs.length).toBeGreaterThanOrEqual(1);
    const latitudeInput = numberInputs[0] as HTMLInputElement;
    await fireEvent.input(latitudeInput, { target: { value: '41.5' } });
    await fireEvent.change(latitudeInput, { target: { value: '41.5' } });

    // Change the UI locale via the mocked i18n store
    setLocale('hu');

    await leave();

    const updateCalls = (settingsActions.updateSection as unknown as ReturnType<typeof vi.fn>).mock
      .calls;
    const realtimeCall = updateCalls.find(([section]) => section === 'realtime');
    const birdnetCall = updateCalls.find(([section]) => section === 'birdnet');

    expect(realtimeCall).toBeDefined();
    expect(birdnetCall).toBeDefined();
    const [, realtimePayload] = realtimeCall as [string, { dashboard: { locale: string } }];
    expect(realtimePayload.dashboard.locale).toBe('hu');
    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(1);
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
});
