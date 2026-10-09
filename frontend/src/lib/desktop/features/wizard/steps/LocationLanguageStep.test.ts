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

import type { ComponentProps } from 'svelte';
import LocationLanguageStep from './LocationLanguageStep.svelte';
import LocationMap from '$lib/desktop/components/forms/LocationMap.svelte';
import LanguageSelector from '$lib/desktop/components/ui/LanguageSelector.svelte';
import { settingsActions, settingsStore } from '$lib/stores/settings';
import { setLocale } from '$lib/i18n';
import { toastActions } from '$lib/stores/toast';
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

/** Props of the last rendered LocationMap (the component is automocked). */
function latestMapProps(): ComponentProps<typeof LocationMap> {
  const call = vi.mocked(LocationMap).mock.calls.at(-1);
  const props = call?.[1];
  // The lint type checker types a mocked component's call as a one-element tuple,
  // so it cannot see that the props argument can be missing.
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
  if (!props) throw new Error('LocationMap was not rendered');
  return props;
}

describe('LocationLanguageStep location', () => {
  const BROWSER_BUTTON = 'settings.main.sections.rangeFilter.stationLocation.useCurrentLocation';
  const INSECURE_HELP =
    'settings.main.sections.rangeFilter.stationLocation.geolocationInsecureHelp';
  const getCurrentPosition = vi.fn<Geolocation['getCurrentPosition']>();

  function setStoredCoordinates(latitude: number, longitude: number) {
    const birdnet = { latitude, longitude, locale: 'en' };
    const realtime = { dashboard: { locale: 'en' } };
    settingsStore.set({
      isLoading: false,
      isSaving: false,
      error: null,
      dataLoaded: true,
      activeSection: 'main',
      // A deliberately partial fixture: only the sections the step reads
      originalData: { birdnet, realtime } as unknown as SettingsFormData,
      formData: { birdnet: { ...birdnet }, realtime } as unknown as SettingsFormData,
    });
  }

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(settingsActions.saveSection).mockResolvedValue(undefined);
    currentLocale = 'en';
    getCurrentPosition.mockReset();
    setStoredCoordinates(40, -74);
    vi.stubGlobal('navigator', { ...navigator, geolocation: { getCurrentPosition } });
    vi.stubGlobal('isSecureContext', true);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const sectionCalls = () => vi.mocked(settingsActions.saveSection).mock.calls;

  function coordinateInputs(container: HTMLElement) {
    const inputs = container.querySelectorAll<HTMLInputElement>('input[type="number"]');
    if (inputs.length !== 2) throw new Error('coordinate inputs not found');
    const [latitude, longitude] = inputs;
    return { latitude, longitude };
  }

  function position(latitude: number, longitude: number): GeolocationPosition {
    return {
      coords: {
        latitude,
        longitude,
        accuracy: 10,
        altitude: null,
        altitudeAccuracy: null,
        heading: null,
        speed: null,
        toJSON: () => ({}),
      },
      timestamp: 0,
      toJSON: () => ({}),
    };
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

  /** Types a latitude and commits it, as a user leaving the field does. */
  async function typeLatitude(container: HTMLElement, value: string) {
    const { latitude } = coordinateInputs(container);
    await fireEvent.input(latitude, { target: { value } });
    await fireEvent.change(latitude, { target: { value } });
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

  it('marks the location unset when the stored coordinates are 0,0', async () => {
    setStoredCoordinates(0, 0);
    await renderLocationStep();

    expect(latestMapProps()).toMatchObject({ latitude: 0, longitude: 0, locationSet: false });
  });

  it('a map pick is saved with locationConfigured on Next', async () => {
    const { leave } = await renderLocationStep();

    latestMapProps().onLocationChange(60.123, 24.456);
    await leave();

    expect(sectionCalls()).toEqual([
      ['birdnet', { latitude: 60.123, longitude: 24.456, locale: 'en', locationConfigured: true }],
    ]);
  });

  it('a map pick on the stored coordinates still saves', async () => {
    const { leave } = await renderLocationStep();

    latestMapProps().onLocationChange(40, -74);
    await leave();

    expect(sectionCalls()).toEqual([
      ['birdnet', { latitude: 40, longitude: -74, locale: 'en', locationConfigured: true }],
    ]);
  });

  it('browser location fills the coordinates rounded to three decimals and saves them', async () => {
    const { container, leave } = await renderLocationStep();

    const respond = await startBrowserRequest();
    respond(position(60.12345, 24.98765));
    await flushAsync();

    const inputs = coordinateInputs(container);
    await waitFor(() => expect(inputs.latitude).toHaveValue(60.123));
    expect(inputs.longitude).toHaveValue(24.988);
    await leave();
    expect(sectionCalls()).toEqual([
      ['birdnet', { latitude: 60.123, longitude: 24.988, locale: 'en', locationConfigured: true }],
    ]);
  });

  it('a browser location that arrives after the user typed is ignored', async () => {
    const { container, leave } = await renderLocationStep();
    const respond = await startBrowserRequest();

    // Typing alone (no change event yet) is newer intent: the late result is dropped
    const inputs = coordinateInputs(container);
    await fireEvent.input(inputs.latitude, { target: { value: '41' } });
    await flushAsync();
    respond(position(52.1, 4.3));
    await flushAsync();

    expect(inputs.longitude).toHaveValue(-74);
    expect(latestMapProps()).toMatchObject({ latitude: 40, longitude: -74 });
    await leave();
    expect(sectionCalls()).toEqual([]);
  });

  it('a browser location that arrives after a map pick on the same coordinates is ignored', async () => {
    const { leave } = await renderLocationStep();
    const respond = await startBrowserRequest();

    latestMapProps().onLocationChange(40, -74);
    await flushAsync();
    respond(position(52.1, 4.3));
    await flushAsync();

    expect(latestMapProps()).toMatchObject({ latitude: 40, longitude: -74 });
    await leave();
    expect(sectionCalls()).toEqual([
      ['birdnet', { latitude: 40, longitude: -74, locale: 'en', locationConfigured: true }],
    ]);
  });

  it('a browser location that arrives after a place pick is ignored', async () => {
    const { leave } = await renderLocationStep();
    const respond = await startBrowserRequest();

    // A chosen place reports through onLocationChange, like a map click
    latestMapProps().onLocationChange(48.857, 2.352);
    await flushAsync();
    respond(position(52.1, 4.3));
    await flushAsync();

    expect(latestMapProps()).toMatchObject({ latitude: 48.857, longitude: 2.352 });
    await leave();
    expect(sectionCalls()).toEqual([
      ['birdnet', { latitude: 48.857, longitude: 2.352, locale: 'en', locationConfigured: true }],
    ]);
  });

  it('a browser location that arrives while Next is saving is dropped, not reported as detected', async () => {
    const { leave } = await renderLocationStep();
    latestMapProps().onLocationChange(10, 20);
    await flushAsync();
    const respond = await startBrowserRequest();
    let finishSave: () => void = () => {};
    vi.mocked(settingsActions.saveSection).mockImplementationOnce(
      () =>
        new Promise<void>(resolve => {
          finishSave = resolve;
        })
    );

    const leaving = leave();
    await flushAsync();
    respond(position(52.1, 4.3));
    await flushAsync();
    finishSave();
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
    let failSave: (error: Error) => void = () => {};
    vi.mocked(settingsActions.saveSection).mockImplementationOnce(
      () =>
        new Promise<void>((_resolve, reject) => {
          failSave = reject;
        })
    );

    const leaving = leave();
    await flushAsync();
    expect(screen.getByRole('button', { name: BROWSER_BUTTON })).toBeDisabled();

    failSave(new Error('save failed'));
    await expect(leaving).rejects.toThrow('save failed');
    await flushAsync();

    expect(screen.getByRole('button', { name: BROWSER_BUTTON })).toBeEnabled();
  });

  it('on an insecure origin the step offers no enabled browser location and says why before any click', async () => {
    vi.stubGlobal('isSecureContext', false);
    await renderLocationStep();

    const button = screen.getByRole('button', { name: BROWSER_BUTTON });
    expect(button).toHaveAttribute('aria-disabled', 'true');
    expect(button).not.toBeDisabled();
    expect(button).toHaveAccessibleDescription(INSECURE_HELP);
    await fireEvent.click(button);
    expect(getCurrentPosition).not.toHaveBeenCalled();
    expect(toastActions.warning).not.toHaveBeenCalled();
    expect(latestMapProps().placeSearch).toBe(true);
  });

  it('the browser location button is disabled while a position is requested', async () => {
    await renderLocationStep();
    const button = screen.getByRole('button', { name: BROWSER_BUTTON });
    expect(button).toBeEnabled();

    await startBrowserRequest();

    expect(getCurrentPosition).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: /locating/ })).toBeDisabled();
  });

  type Step = (ctx: {
    container: HTMLElement;
    pending: { respond?: PositionCallback };
  }) => Promise<void>;
  const pick =
    (latitude: number, longitude: number): Step =>
    async () => {
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
      pending.respond(position(latitude, longitude));
      await flushAsync();
    };
  const type =
    (value: string): Step =>
    async ({ container }) => {
      await typeLatitude(container, value);
      await flushAsync();
    };

  it.each<[string, Step[], Array<[string, Record<string, unknown>]>]>([
    ['no action', [], []],
    [
      'map pick',
      [pick(10, 20)],
      [['birdnet', { latitude: 10, longitude: 20, locale: 'en', locationConfigured: true }]],
    ],
    [
      'browser result',
      [click, result(52.12345, 4.98765)],
      [['birdnet', { latitude: 52.123, longitude: 4.988, locale: 'en', locationConfigured: true }]],
    ],
    [
      'browser click, map pick, late result',
      [click, pick(10, 20), result(52.1, 4.3)],
      [['birdnet', { latitude: 10, longitude: 20, locale: 'en', locationConfigured: true }]],
    ],
    [
      'browser click, typing, late result',
      [click, type('41.5'), result(52.1, 4.3)],
      [['birdnet', { latitude: 41.5, longitude: -74, locale: 'en', locationConfigured: true }]],
    ],
    [
      'map pick, browser click, result',
      [pick(10, 20), click, result(52.1, 4.3)],
      [['birdnet', { latitude: 52.1, longitude: 4.3, locale: 'en', locationConfigured: true }]],
    ],
    [
      'browser result, then map pick',
      [click, result(52.1, 4.3), pick(10, 20)],
      [['birdnet', { latitude: 10, longitude: 20, locale: 'en', locationConfigured: true }]],
    ],
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
