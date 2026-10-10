import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup } from '@testing-library/svelte';
import { Map as MapLibreMap } from 'maplibre-gl';
import LocationMap from './LocationMap.svelte';
import { toastActions } from '$lib/stores/toast';
import { createComponentTestFactory } from '../../../../test/render-helpers';

const testFactory = createComponentTestFactory(LocationMap, {
  latitude: 60,
  longitude: 24,
  locationSet: true,
  onLocationChange: vi.fn(),
  title: 'Station location',
});

// LocationMap keeps MapLibre in a module-level cache once it has loaded, so the
// race between loading and unmounting can only be seen from a file where no
// earlier test has mounted the component. This file holds that one test.
describe('LocationMap unmounted while MapLibre loads', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it('builds no map and shows no toast', async () => {
    const result = testFactory.render();
    result.unmount();
    await vi.dynamicImportSettled();

    expect(MapLibreMap).not.toHaveBeenCalled();
    expect(toastActions.error).not.toHaveBeenCalled();
  });
});
