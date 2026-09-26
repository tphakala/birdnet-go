/**
 * Tests for the source of the dashboard banner's location map.
 *
 * Guests never load the authenticated settings, so their birdnet store stays at
 * the empty defaults (locationConfigured: false). The banner must then fall back
 * to the station coordinates carried by the public app config, or the map never
 * renders for them (#4344). Authenticated users keep reading the live settings.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { renderTyped } from '../../../../../test/render-helpers';
import type { BannerConfig } from '$lib/stores/settings';

const { appState } = vi.hoisted(() => ({
  appState: { stationLocation: null as { latitude: number; longitude: number } | null },
}));

vi.mock('$lib/stores/appState.svelte', () => ({ appState }));

// BannerLocationMap relies on maplibre-gl; the mock records the props it receives.
vi.mock('./BannerLocationMap.svelte');

import { settingsStore } from '$lib/stores/settings';
import BannerCard from './BannerCard.svelte';
import BannerLocationMap from './BannerLocationMap.svelte';

const initialSettings = get(settingsStore);

const mapConfig: BannerConfig = {
  showImage: false,
  imagePath: '',
  title: 'Station',
  description: '',
  showLocationMap: true,
  showWeather: false,
};

/** Props of the most recent BannerLocationMap render, or undefined when it never rendered. */
function lastMapProps(): { latitude: number; longitude: number } | undefined {
  const calls = vi.mocked(BannerLocationMap).mock.calls;
  if (calls.length === 0) return undefined;
  const props = calls[calls.length - 1][1] as unknown as { latitude: number; longitude: number };
  return { latitude: props.latitude, longitude: props.longitude };
}

describe('BannerCard location map', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    appState.stationLocation = null;
    settingsStore.set(initialSettings);
  });

  afterEach(() => {
    cleanup();
    settingsStore.set(initialSettings);
  });

  it('renders the map for guests from the public station location', () => {
    appState.stationLocation = { latitude: 60.1699, longitude: 24.9384 };

    renderTyped(BannerCard, { props: { config: mapConfig } });

    expect(lastMapProps()).toEqual({ latitude: 60.1699, longitude: 24.9384 });
  });

  it('renders no map for guests when the public config has no station location', () => {
    renderTyped(BannerCard, { props: { config: mapConfig } });

    expect(lastMapProps()).toBeUndefined();
  });

  it('prefers the authenticated settings location over the public one', () => {
    appState.stationLocation = { latitude: 60.1699, longitude: 24.9384 };
    settingsStore.update(state => ({
      ...state,
      formData: {
        ...state.formData,
        birdnet: {
          ...state.formData.birdnet,
          latitude: 51.5072,
          longitude: -0.1276,
          locationConfigured: true,
        },
      },
    }));

    renderTyped(BannerCard, { props: { config: mapConfig } });

    expect(lastMapProps()).toEqual({ latitude: 51.5072, longitude: -0.1276 });
  });
});
