import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/svelte';
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

// Mock LocationMap (relies on maplibre-gl) to keep this test focused; its
// props are read through latestMapProps()
vi.mock('$lib/desktop/components/forms/LocationMap.svelte');

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
import { toastActions } from '$lib/stores/toast';
import { flushAsync, renderStep } from './stepTestUtils';
import { deferred } from '../../../../../test/async-helpers';
import { latestMapProps } from '../../../../../test/location-map-helpers';
import {
  clearGeolocationGlobals,
  createPosition,
  setGeolocation,
  setSecureContext,
} from '../../../../../test/geolocation-fixtures';

interface StoredLocation {
  latitude?: number;
  longitude?: number;
  /** Defaults to true for non-zero coordinates, as the server stores them. */
  locationConfigured?: boolean;
}

/** Resets the settings store to a loaded state with the given stored location. */
function setStoredSettings({
  latitude = 40,
  longitude = -74,
  locationConfigured = latitude !== 0 || longitude !== 0,
}: StoredLocation = {}) {
  const data = () => ({
    birdnet: { latitude, longitude, locale: 'en', locationConfigured },
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
  settingsStore.set({
    isLoading: false,
    isSaving: false,
    error: null,
    dataLoaded: true,
    activeSection: 'main',
    // A deliberately partial fixture: only the sections the step reads
    originalData: data() as unknown as SettingsFormData,
    formData: data() as unknown as SettingsFormData,
  });
}

/** The saveSection calls so far as [section, payload] pairs. */
const sectionCalls = () => vi.mocked(settingsActions.saveSection).mock.calls;

/** Types a latitude and commits it (input then change), as a user leaving the field does. */
async function editLatitude(container: HTMLElement, value: string) {
  const latitudeInput = container.querySelector('input[type="number"]');
  if (!(latitudeInput instanceof HTMLInputElement)) throw new Error('latitude input not found');
  await fireEvent.input(latitudeInput, { target: { value } });
  await fireEvent.change(latitudeInput, { target: { value } });
}

// The leave handler contract shared by every step is in stepContract.test.ts
describe('LocationLanguageStep - UI locale persistence in the leave handler', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(settingsActions.saveSection).mockResolvedValue(undefined);
    currentLocale = 'en';

    setStoredSettings();
  });

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

  it('a typed 0,0 is a deliberate location and is saved as configured', async () => {
    setStoredSettings({ latitude: 0, longitude: 0 });
    const { leave, container } = renderStep(LocationLanguageStep);
    await flushAsync();
    await editLatitude(container, '0');

    await leave();

    expect(sectionCalls()).toEqual([
      ['birdnet', { latitude: 0, longitude: 0, locale: 'en', locationConfigured: true }],
    ]);
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

  it('shows the loading text with a spinner hidden from assistive technology while locales load', async () => {
    renderStep(LocationLanguageStep);

    const text = screen.getByText('wizard.steps.locationLanguage.localesLoading');
    const box = text.parentElement;
    if (!box) throw new Error('loading box not found');

    const spinner = box.querySelector('[aria-hidden="true"]');
    expect(spinner).not.toBeNull();
    expect(spinner?.querySelector('.animate-spin')).not.toBeNull();
    expect(within(box).queryByRole('status')).toBeNull();

    // Let the locales settle so the pending request does not outlive the test
    await waitFor(() => expect(document.getElementById('wizard-species-locale')).not.toBeNull());
  });

  it('describes the species language dropdown with its help text', async () => {
    renderStep(LocationLanguageStep);
    const trigger = await waitFor(() => {
      const el = document.getElementById('wizard-species-locale');
      if (!el) throw new Error('species dropdown not rendered');
      return el;
    });

    // The displayed value is the combobox value, so the description is the help text alone
    expect(trigger).toHaveTextContent('English');
    expect(trigger).toHaveAccessibleDescription(
      'wizard.steps.locationLanguage.speciesLanguageHelp'
    );
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

describe('LocationLanguageStep location', () => {
  const BROWSER_BUTTON = 'settings.main.sections.rangeFilter.stationLocation.useCurrentLocation';
  const INSECURE_HELP =
    'settings.main.sections.rangeFilter.stationLocation.geolocationInsecureHelp';
  const getCurrentPosition = vi.fn<Geolocation['getCurrentPosition']>();
  const geolocation: Geolocation = {
    getCurrentPosition,
    watchPosition: vi.fn<Geolocation['watchPosition']>(),
    clearWatch: vi.fn<Geolocation['clearWatch']>(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(settingsActions.saveSection).mockResolvedValue(undefined);
    currentLocale = 'en';
    getCurrentPosition.mockReset();
    setStoredSettings();
    setSecureContext(true);
    setGeolocation(geolocation);
  });

  afterEach(() => {
    clearGeolocationGlobals();
  });

  function coordinateInputs(container: HTMLElement) {
    const inputs = container.querySelectorAll<HTMLInputElement>('input[type="number"]');
    if (inputs.length !== 2) throw new Error('coordinate inputs not found');
    const [latitude, longitude] = inputs;
    return { latitude, longitude };
  }

  /** Clicks the browser location button and returns the browser's success callback. */
  async function startBrowserRequest(): Promise<PositionCallback> {
    let respond: PositionCallback | undefined;
    getCurrentPosition.mockImplementationOnce(success => {
      respond = success;
    });
    await fireEvent.click(screen.getByRole('button', { name: BROWSER_BUTTON }));
    if (!respond) throw new Error('the browser location request was not started');
    return respond;
  }

  async function renderLocationStep() {
    const result = renderStep(LocationLanguageStep);
    await flushAsync();
    return result;
  }

  it('renders the shared map with place search, overlay controls, pinch zoom, double-tap protection and the world start view', async () => {
    await renderLocationStep();

    const props = latestMapProps();
    expect(props).toMatchObject({
      latitude: 40,
      longitude: -74,
      locationSet: true,
      placeSearch: true,
      controls: 'overlay',
      pinchZoom: true,
      doubleTapZoomKeepsPin: true,
      startView: 'world',
      title: 'wizard.steps.locationLanguage.locationLabel',
    });
    expect(props.mapClass).toContain('h-[300px]');
  });

  it('shows the pin for a deliberately configured 0,0 location', async () => {
    setStoredSettings({ latitude: 0, longitude: 0, locationConfigured: true });
    await renderLocationStep();

    expect(latestMapProps()).toMatchObject({ latitude: 0, longitude: 0, locationSet: true });
  });

  it('marks the location unset when it was never configured', async () => {
    setStoredSettings({ latitude: 0, longitude: 0 });
    await renderLocationStep();

    expect(latestMapProps()).toMatchObject({ latitude: 0, longitude: 0, locationSet: false });
  });

  it('a deliberate 0,0 map pick shows the pin and is saved as configured', async () => {
    setStoredSettings({ latitude: 0, longitude: 0 });
    const { leave } = await renderLocationStep();

    latestMapProps().onLocationChange(0, 0);
    await flushAsync();
    expect(latestMapProps().locationSet).toBe(true);
    await leave();

    expect(sectionCalls()).toEqual([
      ['birdnet', { latitude: 0, longitude: 0, locale: 'en', locationConfigured: true }],
    ]);
  });

  it('a browser location on a never configured location shows the pin and is saved as configured', async () => {
    setStoredSettings({ latitude: 0, longitude: 0 });
    const { leave } = await renderLocationStep();

    const respond = await startBrowserRequest();
    respond(createPosition(60.12345, 24.98765));
    await flushAsync();
    expect(latestMapProps().locationSet).toBe(true);
    await leave();

    expect(sectionCalls()).toEqual([
      ['birdnet', { latitude: 60.123, longitude: 24.988, locale: 'en', locationConfigured: true }],
    ]);
  });

  it('leaves a never configured location unconfigured when only the species language changes', async () => {
    setStoredSettings({ latitude: 0, longitude: 0 });
    const { leave } = await renderLocationStep();

    const trigger = await waitFor(() => {
      const el = document.getElementById('wizard-species-locale');
      if (!el) throw new Error('species dropdown not rendered');
      return el;
    });
    await fireEvent.click(trigger);
    await fireEvent.click(await screen.findByRole('option', { name: /Magyar/ }));
    await leave();

    expect(sectionCalls()).toEqual([['birdnet', { latitude: 0, longitude: 0, locale: 'hu' }]]);
  });

  it('browser location fills the inputs rounded to three decimals', async () => {
    const { container } = await renderLocationStep();

    const respond = await startBrowserRequest();
    respond(createPosition(60.12345, 24.98765));
    await flushAsync();

    const inputs = coordinateInputs(container);
    await waitFor(() => expect(inputs.latitude).toHaveValue(60.123));
    expect(inputs.longitude).toHaveValue(24.988);
  });

  it('a browser location that arrives after the user typed is ignored', async () => {
    const { container, leave } = await renderLocationStep();
    const respond = await startBrowserRequest();

    // Typing alone (no change event yet) is newer intent: the late result is dropped
    const inputs = coordinateInputs(container);
    await fireEvent.input(inputs.latitude, { target: { value: '41' } });
    await flushAsync();
    respond(createPosition(52.1, 4.3));
    await flushAsync();

    expect(inputs.longitude).toHaveValue(-74);
    expect(latestMapProps()).toMatchObject({ latitude: 40, longitude: -74 });
    await leave();
    expect(sectionCalls()).toEqual([]);
  });

  it('a browser location that arrives while Next is saving is dropped, not reported as detected', async () => {
    const { leave } = await renderLocationStep();
    latestMapProps().onLocationChange(10, 20);
    await flushAsync();
    const respond = await startBrowserRequest();
    const save = deferred();
    vi.mocked(settingsActions.saveSection).mockImplementationOnce(() => save.promise);

    const leaving = leave();
    await flushAsync();
    respond(createPosition(52.1, 4.3));
    await flushAsync();
    save.resolve();
    await leaving;

    expect(toastActions.success).not.toHaveBeenCalled();
    expect(latestMapProps()).toMatchObject({ latitude: 10, longitude: 20 });
    expect(sectionCalls()).toEqual([
      ['birdnet', { latitude: 10, longitude: 20, locale: 'en', locationConfigured: true }],
    ]);
  });

  it('enables the browser location button again after a failed save, so it can be retried', async () => {
    const { leave } = await renderLocationStep();
    latestMapProps().onLocationChange(10, 20);
    await flushAsync();
    const save = deferred();
    vi.mocked(settingsActions.saveSection).mockImplementationOnce(() => save.promise);

    const leaving = leave();
    await flushAsync();
    expect(screen.getByRole('button', { name: BROWSER_BUTTON })).toBeDisabled();

    save.reject(new Error('save failed'));
    await expect(leaving).rejects.toThrow('save failed');
    await flushAsync();

    expect(screen.getByRole('button', { name: BROWSER_BUTTON })).toBeEnabled();
  });

  it('on an insecure origin the browser location button carries the HTTPS explanation', async () => {
    setSecureContext(false);
    await renderLocationStep();

    const button = screen.getByRole('button', { name: BROWSER_BUTTON });
    expect(button).toHaveAttribute('aria-disabled', 'true');
    expect(button).toHaveAccessibleDescription(INSECURE_HELP);
  });

  it('the browser location button is disabled while a position is requested', async () => {
    await renderLocationStep();

    await startBrowserRequest();

    expect(screen.getByRole('button', { name: /locating/ })).toBeDisabled();
  });

  type Step = (ctx: {
    container: HTMLElement;
    pending: { respond?: PositionCallback };
  }) => Promise<void>;
  const pick =
    (latitude: number, longitude: number): Step =>
    async () => {
      // A map click, pin drag or chosen place all report through onLocationChange
      latestMapProps().onLocationChange(latitude, longitude);
      await flushAsync();
    };
  const click: Step = async ({ pending }) => {
    pending.respond = await startBrowserRequest();
  };
  const result =
    (latitude: number, longitude: number): Step =>
    async ({ pending }) => {
      if (!pending.respond) throw new Error('no browser request is pending');
      pending.respond(createPosition(latitude, longitude));
      await flushAsync();
    };
  const type =
    (value: string): Step =>
    async ({ container }) => {
      await editLatitude(container, value);
      await flushAsync();
    };
  const saved = (latitude: number, longitude: number) => [
    ['birdnet', { latitude, longitude, locale: 'en', locationConfigured: true }],
  ];

  it.each<[string, Step[], unknown[]]>([
    ['no action', [], []],
    ['map pick', [pick(60.123, 24.456)], saved(60.123, 24.456)],
    ['map pick on the stored coordinates', [pick(40, -74)], saved(40, -74)],
    ['browser result', [click, result(52.12345, 4.98765)], saved(52.123, 4.988)],
    [
      'browser click, map pick, late result',
      [click, pick(10, 20), result(52.1, 4.3)],
      saved(10, 20),
    ],
    [
      'browser click, map pick on the same coordinates, late result',
      [click, pick(40, -74), result(52.1, 4.3)],
      saved(40, -74),
    ],
    [
      'browser click, place pick, late result',
      [click, pick(48.857, 2.352), result(52.1, 4.3)],
      saved(48.857, 2.352),
    ],
    [
      'browser click, typing, late result',
      [click, type('41.5'), result(52.1, 4.3)],
      saved(41.5, -74),
    ],
    ['map pick, browser click, result', [pick(10, 20), click, result(52.1, 4.3)], saved(52.1, 4.3)],
    ['browser result, then map pick', [click, result(52.1, 4.3), pick(10, 20)], saved(10, 20)],
  ])('%s saves what the user did last', async (_name, steps, expected) => {
    const { container, leave } = await renderLocationStep();
    const pending: { respond?: PositionCallback } = {};

    for (const step of steps) {
      await step({ container, pending });
    }
    await leave();

    expect(sectionCalls()).toEqual(expected);
  });
});
