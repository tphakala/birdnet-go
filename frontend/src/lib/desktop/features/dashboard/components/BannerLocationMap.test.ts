import { describe, it, expect, afterEach, vi } from 'vitest';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import BannerLocationMap from './BannerLocationMap.svelte';

const factory = createComponentTestFactory(BannerLocationMap);

const EXPAND_NAME = 'dashboard.banner.expandMap';
const CLOSE_NAME = 'dashboard.banner.closeExpandedMap';
const DIALOG_NAME = 'dashboard.banner.expandedMapLabel';
const SAMPLE_LATITUDE = 60.17;
const SAMPLE_LONGITUDE = 24.94;

function renderMap() {
  factory.render({ props: { latitude: SAMPLE_LATITUDE, longitude: SAMPLE_LONGITUDE } });
}

describe('BannerLocationMap', () => {
  afterEach(() => {
    cleanup();
  });

  it('returns focus to the expand button when it was pressed without taking focus', async () => {
    renderMap();
    const expand = screen.getByRole('button', { name: EXPAND_NAME });
    // Safari and iPadOS do not focus a clicked button
    await fireEvent.pointerDown(expand);
    await fireEvent.click(expand);
    const close = await screen.findByRole('button', { name: CLOSE_NAME });
    await waitFor(() => expect(close).toHaveFocus());

    await fireEvent.keyDown(window, { key: 'Escape' });

    await waitFor(() => expect(screen.queryByRole('dialog', { name: DIALOG_NAME })).toBeNull());
    expect(expand).toHaveFocus();
  });

  it('does not move focus on close when the expand button was removed while the map was open', async () => {
    renderMap();
    const expand = screen.getByRole('button', { name: EXPAND_NAME });
    expand.focus();
    await fireEvent.click(expand);
    await waitFor(() => expect(screen.getByRole('button', { name: CLOSE_NAME })).toHaveFocus());
    const focusSpy = vi.spyOn(expand, 'focus');
    expand.remove();

    await fireEvent.keyDown(window, { key: 'Escape' });

    await waitFor(() => expect(screen.queryByRole('dialog', { name: DIALOG_NAME })).toBeNull());
    expect(focusSpy).not.toHaveBeenCalled();
  });
});
