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
    dataLoaded: false,
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
    resetAllSettings: vi.fn(),
  };

  return {
    settingsStore,
    settingsActions,
  };
});

import LocationLanguageStep from './LocationLanguageStep.svelte';
import { settingsActions, settingsStore } from '$lib/stores/settings';
import { setLocale } from '$lib/i18n';
import type { StepLeaveHandler } from '../types';

// Renders the step with a registerLeaveHandler spy and exposes the captured handler.
function renderStep() {
  let handler: StepLeaveHandler | undefined;
  const unregister = vi.fn();
  const registerLeaveHandler = vi.fn((h: StepLeaveHandler) => {
    handler = h;
    return unregister;
  });
  const result = render(LocationLanguageStep, { props: { registerLeaveHandler } });
  return {
    ...result,
    registerLeaveHandler,
    unregister,
    leave: () => {
      if (!handler) throw new Error('leave handler was not registered');
      return handler();
    },
  };
}

// Helper to flush pending microtasks (e.g. onMount continuations).
// Double Promise.resolve() processes both the immediate microtask and
// any follow-up microtasks queued during the first flush. Svelte's
// $effect cleanup and onMount initializers commonly chain a second
// microtask, so a single await can miss them. Revisit if new async
// layers are introduced and tests start flaking.
async function flushAsync() {
  await Promise.resolve();
  await Promise.resolve();
}

describe('LocationLanguageStep - UI locale persistence in the leave handler', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    currentLocale = 'en';

    // Reset store to pristine state before every test
    settingsStore.set({
      isLoading: false,
      isSaving: false,
      error: null,
      dataLoaded: false,
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
    const { leave } = renderStep();
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

    const { leave } = renderStep();
    await flushAsync();

    // User does NOT interact with the wizard — no setLocale, no field edits.
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

    const { leave } = renderStep();
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

  it('rejects from the leave handler when the save rejects', async () => {
    currentLocale = 'hu';
    const failure = new Error('save failed');
    vi.mocked(settingsActions.saveSettings).mockRejectedValueOnce(failure);

    const { leave } = renderStep();
    await flushAsync();

    await expect(leave()).rejects.toBe(failure);
    expect(settingsActions.resetAllSettings).toHaveBeenCalledTimes(1);
  });

  it('saves without toasts', async () => {
    currentLocale = 'hu';
    const { leave } = renderStep();
    await flushAsync();

    await leave();

    expect(settingsActions.saveSettings).toHaveBeenCalledWith({ notify: false });
  });

  it('registers a leave handler on mount and unregisters on destroy', async () => {
    const { registerLeaveHandler, unregister, unmount } = renderStep();
    await flushAsync();

    expect(registerLeaveHandler).toHaveBeenCalledTimes(1);
    expect(unregister).not.toHaveBeenCalled();

    unmount();

    expect(unregister).toHaveBeenCalledTimes(1);
  });

  it('unmounting never saves', async () => {
    currentLocale = 'hu';
    const { unmount } = renderStep();
    await flushAsync();

    unmount();
    await flushAsync();

    expect(settingsActions.updateSection).not.toHaveBeenCalled();
    expect(settingsActions.saveSettings).not.toHaveBeenCalled();
  });

  it('a second leave call after a successful save does nothing', async () => {
    currentLocale = 'hu';
    const { leave } = renderStep();
    await flushAsync();

    await leave();
    await leave();

    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(1);
  });

  it('does not save after the step unmounted when the handler runs late', async () => {
    currentLocale = 'hu';
    const failure = new Error('late failure');
    vi.mocked(settingsActions.saveSettings).mockRejectedValueOnce(failure);
    const { leave, unmount } = renderStep();
    await flushAsync();

    const pending = leave();
    unmount();

    await expect(pending).rejects.toBe(failure);
    expect(settingsActions.resetAllSettings).not.toHaveBeenCalled();
  });

  it('does NOT save when UI locale is unchanged and dirty is false', async () => {
    const { leave } = renderStep();
    await flushAsync();

    // No changes made whatsoever
    await leave();

    const realtimeCall = (
      settingsActions.updateSection as unknown as ReturnType<typeof vi.fn>
    ).mock.calls.find(([section]) => section === 'realtime');
    expect(realtimeCall).toBeUndefined();
    expect(settingsActions.saveSettings).not.toHaveBeenCalled();
  });

  it('dispatches both realtime and birdnet updates with a single save when both change', async () => {
    const { leave, container } = renderStep();
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
});
