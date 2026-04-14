import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
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

// Mock the settings module with a controllable store + tracked actions
vi.mock('$lib/stores/settings', async () => {
  const { writable } = await vi.importActual<typeof import('svelte/store')>('svelte/store');

  const initialFormData = {
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
  } as unknown as SettingsFormData;

  const settingsStore = writable({
    isLoading: false,
    isSaving: false,
    error: null,
    restartRequired: false,
    activeSection: 'main',
    originalData: JSON.parse(JSON.stringify(initialFormData)) as SettingsFormData,
    formData: JSON.parse(JSON.stringify(initialFormData)) as SettingsFormData,
  });

  const settingsActions = {
    updateSection: vi.fn((section: string, data: Record<string, unknown>) => {
      settingsStore.update(state => {
        const formData = state.formData as unknown as Record<string, Record<string, unknown>>;
        // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with controlled section keys
        const current = formData[section] ?? {};
        // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with controlled section keys
        formData[section] = { ...current, ...data };
        return state;
      });
    }),
    saveSettings: vi.fn().mockResolvedValue(undefined),
  };

  return {
    settingsStore,
    settingsActions,
  };
});

import LocationLanguageStep from './LocationLanguageStep.svelte';
import { settingsActions, settingsStore } from '$lib/stores/settings';
import { setLocale } from '$lib/i18n';

// Helper to flush pending microtasks (e.g. onMount continuations)
async function flushAsync() {
  await Promise.resolve();
  await Promise.resolve();
}

describe('LocationLanguageStep - UI locale persistence on unmount', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    currentLocale = 'en';

    // Reset store to pristine state before every test
    settingsStore.set({
      isLoading: false,
      isSaving: false,
      error: null,
      restartRequired: false,
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
    const { unmount } = render(LocationLanguageStep, { props: {} });
    await flushAsync();

    // Simulate LanguageSelector invoking setLocale
    setLocale('hu');

    unmount();
    await flushAsync();

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

  it('does NOT save when UI locale is unchanged and dirty is false', async () => {
    const { unmount } = render(LocationLanguageStep, { props: {} });
    await flushAsync();

    // No changes made whatsoever
    unmount();
    await flushAsync();

    const realtimeCall = (
      settingsActions.updateSection as unknown as ReturnType<typeof vi.fn>
    ).mock.calls.find(([section]) => section === 'realtime');
    expect(realtimeCall).toBeUndefined();
    expect(settingsActions.saveSettings).not.toHaveBeenCalled();
  });

  it('dispatches both realtime and birdnet updates with a single save when both change', async () => {
    const { unmount, container } = render(LocationLanguageStep, { props: {} });
    await flushAsync();

    // Change the latitude via the underlying NumberField input (triggers dirty=true)
    const numberInputs = container.querySelectorAll('input[type="number"]');
    expect(numberInputs.length).toBeGreaterThanOrEqual(1);
    const latitudeInput = numberInputs[0] as HTMLInputElement;
    await fireEvent.input(latitudeInput, { target: { value: '41.5' } });
    await fireEvent.change(latitudeInput, { target: { value: '41.5' } });

    // Change the UI locale via the mocked i18n store
    setLocale('hu');

    unmount();
    await flushAsync();

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
});
