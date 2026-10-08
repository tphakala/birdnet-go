import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { get } from 'svelte/store';
import type { ComponentProps } from 'svelte';
import { settingsStore } from '$lib/stores/settings';
import type { SettingsFormData } from '$lib/stores/settings';

// The map itself is covered by LocationMap.test.ts; here only the props the
// page passes to it and the callback it provides are inspected.
vi.mock('$lib/desktop/components/forms/LocationMap.svelte');

import LocationMap from '$lib/desktop/components/forms/LocationMap.svelte';
import MainSettingsPage from './MainSettingsPage.svelte';

function latestMapProps(): ComponentProps<typeof LocationMap> {
  const call = vi.mocked(LocationMap).mock.calls.at(-1);
  const props = call?.[1];
  // The lint type checker types a mocked component's call as a one-element tuple,
  // so it cannot see that the props argument can be missing.
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
  if (!props) throw new Error('LocationMap was not rendered');
  return props;
}

type BirdnetOverrides = { [K in keyof SettingsFormData['birdnet']]?: unknown };

function createFormData(birdnet: BirdnetOverrides): SettingsFormData {
  // A deliberately partial fixture: only the sections the page reads for the
  // Location tab are filled in.
  return {
    main: { name: 'TestNode' },
    birdnet: {
      modelPath: '',
      labelPath: '',
      sensitivity: 1.0,
      threshold: 0.8,
      overlap: 0.0,
      locale: 'en',
      threads: 4,
      latitude: 40.7128,
      longitude: -74.006,
      locationConfigured: true,
      rangeFilter: {
        threshold: 0.03,
        passUnmappedSpecies: false,
        speciesCount: null,
        species: [],
      },
      ...birdnet,
    },
  } as unknown as SettingsFormData;
}

function setStore(birdnet: BirdnetOverrides, state: { isLoading?: boolean } = {}) {
  settingsStore.set({
    formData: createFormData(birdnet),
    // The baseline equals the form data, as right after a settings load.
    originalData: createFormData(birdnet),
    isLoading: state.isLoading ?? false,
    isSaving: false,
    activeSection: 'main',
    error: null,
    dataLoaded: true,
  });
}

async function openLocationTab() {
  render(MainSettingsPage);
  await fireEvent.click(screen.getByRole('tab', { name: /location/i }));
}

describe('MainSettingsPage location map', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it('passes the stored coordinates and locationConfigured to the map', async () => {
    setStore({ latitude: 60.123, longitude: 24.456, locationConfigured: true });

    await openLocationTab();

    expect(latestMapProps()).toMatchObject({
      latitude: 60.123,
      longitude: 24.456,
      locationSet: true,
      title: 'settings.main.sections.rangeFilter.stationLocation.label',
    });
  });

  it('passes locationSet false while the location is not configured', async () => {
    setStore({ latitude: 0, longitude: 0, locationConfigured: false });

    await openLocationTab();

    expect(latestMapProps().locationSet).toBe(false);
  });

  it('enables place search on the map', async () => {
    setStore({ latitude: 60.123, longitude: 24.456, locationConfigured: true });

    await openLocationTab();

    expect(latestMapProps().placeSearch).toBe(true);
  });

  it('a map pick updates the coordinates and marks the location configured', async () => {
    setStore({ latitude: 0, longitude: 0, locationConfigured: false });
    await openLocationTab();

    latestMapProps().onLocationChange(60.123, 24.456);

    expect(get(settingsStore).formData.birdnet).toMatchObject({
      latitude: 60.123,
      longitude: 24.456,
      locationConfigured: true,
    });
    await vi.waitFor(() =>
      expect(latestMapProps()).toMatchObject({
        latitude: 60.123,
        longitude: 24.456,
        locationSet: true,
      })
    );
  });

  it('does not mark the map ready while settings load', async () => {
    setStore({}, { isLoading: true });
    await openLocationTab();
    expect(latestMapProps().ready).toBe(false);

    setStore({}, { isLoading: false });
    await vi.waitFor(() => expect(latestMapProps().ready).toBe(true));
  });

  it('does not write the location when the Location tab opens', async () => {
    setStore({ latitude: 0, longitude: 0, locationConfigured: false });
    await openLocationTab();

    expect(get(settingsStore).formData.birdnet).toMatchObject({
      latitude: 0,
      longitude: 0,
      locationConfigured: false,
    });
  });

  describe('with a pending browser location request', () => {
    const geolocation = {
      getCurrentPosition: vi.fn<Geolocation['getCurrentPosition']>(),
      watchPosition: vi.fn<Geolocation['watchPosition']>(),
      clearWatch: vi.fn<Geolocation['clearWatch']>(),
    };

    beforeEach(() => {
      geolocation.getCurrentPosition.mockReset();
      Object.defineProperty(window, 'isSecureContext', { configurable: true, value: true });
      Object.defineProperty(navigator, 'geolocation', { configurable: true, value: geolocation });
    });

    afterEach(() => {
      Reflect.deleteProperty(navigator, 'geolocation');
      Reflect.deleteProperty(window, 'isSecureContext');
    });

    async function startBrowserRequest(): Promise<PositionCallback> {
      let respond: PositionCallback | undefined;
      geolocation.getCurrentPosition.mockImplementationOnce(success => {
        respond = success;
      });
      await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));
      if (!respond) throw new Error('the browser location request was not started');
      return respond;
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

    it('a map pick on the current coordinates still discards the late browser result', async () => {
      setStore({ latitude: 60.123, longitude: 24.456, locationConfigured: true });
      await openLocationTab();
      const respond = await startBrowserRequest();

      // Same values as stored: only the coordinate intent version tells the
      // button that the user acted on the map.
      latestMapProps().onLocationChange(60.123, 24.456);
      await vi.waitFor(() =>
        expect(screen.getByRole('button', { name: 'Use browser location' })).not.toBeDisabled()
      );
      respond(position(10.5, 20.5));

      expect(get(settingsStore).formData.birdnet).toMatchObject({
        latitude: 60.123,
        longitude: 24.456,
      });
    });

    it('a browser result applies its coordinates and marks the location configured', async () => {
      setStore({ latitude: 0, longitude: 0, locationConfigured: false });
      await openLocationTab();
      const respond = await startBrowserRequest();

      respond(position(10.5, 20.5));

      expect(get(settingsStore).formData.birdnet).toMatchObject({
        latitude: 10.5,
        longitude: 20.5,
        locationConfigured: true,
      });
      await vi.waitFor(() =>
        expect(latestMapProps()).toMatchObject({
          latitude: 10.5,
          longitude: 20.5,
          locationSet: true,
        })
      );
    });
  });
});
