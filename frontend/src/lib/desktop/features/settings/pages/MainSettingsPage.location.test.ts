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

function setStore(
  birdnet: Partial<SettingsFormData['birdnet']>,
  state: { isLoading?: boolean } = {}
) {
  const formData = {
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

  settingsStore.set({
    formData,
    // The baseline is a structural copy, as after a settings load.
    originalData: JSON.parse(JSON.stringify(formData)) as SettingsFormData,
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

  it('a map pick updates the coordinates and marks the location configured', async () => {
    setStore({ latitude: 0, longitude: 0, locationConfigured: false });
    await openLocationTab();

    latestMapProps().onLocationChange(60.123, 24.456);

    expect(get(settingsStore).formData.birdnet).toMatchObject({
      latitude: 60.123,
      longitude: 24.456,
      locationConfigured: true,
    });
  });

  it('does not mark the map ready while settings load', async () => {
    setStore({}, { isLoading: true });
    await openLocationTab();
    expect(latestMapProps().ready).toBe(false);

    setStore({}, { isLoading: false });
    await vi.waitFor(() => expect(latestMapProps().ready).toBe(true));
  });

  it('does not change the stored location by only showing the map', async () => {
    setStore({ latitude: 60.123, longitude: 24.456, locationConfigured: true });
    await openLocationTab();

    const state = get(settingsStore);
    expect(state.formData.birdnet).toMatchObject({
      latitude: state.originalData.birdnet.latitude,
      longitude: state.originalData.birdnet.longitude,
      locationConfigured: state.originalData.birdnet.locationConfigured,
    });
  });
});
