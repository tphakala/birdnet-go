import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, screen } from '@testing-library/svelte';
import { Map as MapLibreMap } from 'maplibre-gl';
import LocationMap from './LocationMap.svelte';
import { toastActions } from '$lib/stores/toast';
import { createComponentTestFactory } from '../../../../test/render-helpers';

// The stylesheet import is part of loading MapLibre, so a stylesheet that
// fails to load is a library that fails to load. Vitest keeps a loaded module
// in its registry, so this lives in its own file instead of in LocationMap.test.ts.
vi.mock('maplibre-gl/dist/maplibre-gl.css', () => {
  throw new Error('stylesheet failed to load');
});

const testFactory = createComponentTestFactory(LocationMap, {
  latitude: 60,
  longitude: 24,
  locationSet: true,
  onLocationChange: vi.fn(),
  title: 'Station location',
});

describe('LocationMap when MapLibre fails to load', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it('shows the placeholder and a toast', async () => {
    testFactory.render();

    expect(await screen.findByText('settings.main.errors.mapUnavailable')).toBeInTheDocument();
    expect(toastActions.error).toHaveBeenCalledWith('settings.main.errors.mapLibraryLoadFailed');
    expect(screen.queryByRole('application')).not.toBeInTheDocument();
    expect(MapLibreMap).not.toHaveBeenCalled();
  });

  it('stays silent when the library fails after the component is gone', async () => {
    const result = testFactory.render();
    result.unmount();
    await vi.dynamicImportSettled();

    expect(toastActions.error).not.toHaveBeenCalled();
    expect(MapLibreMap).not.toHaveBeenCalled();
  });
});
