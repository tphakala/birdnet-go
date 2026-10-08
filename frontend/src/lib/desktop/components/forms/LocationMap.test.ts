import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ComponentProps } from 'svelte';
import { cleanup, render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { Map as MapLibreMap, Marker } from 'maplibre-gl';
import LocationMap from './LocationMap.svelte';
import { toastActions } from '$lib/stores/toast';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import { createComponentTestFactory } from '../../../../test/render-helpers';

const testFactory = createComponentTestFactory(LocationMap);

const TITLE = 'Station location';
const ZOOM_IN = 'components.locationMap.zoomIn';
const ZOOM_OUT = 'components.locationMap.zoomOut';
const EXPAND = 'components.locationMap.expand';
const MAP_LABEL = 'components.locationMap.mapLabel';
const EXPANDED_MAP_LABEL = 'components.locationMap.expandedMapLabel';
const CLOSE = 'Close modal';

type Props = ComponentProps<typeof LocationMap>;

function createProps(overrides: Partial<Props> = {}): Props {
  return {
    latitude: 60,
    longitude: 24,
    locationSet: true,
    onLocationChange: vi.fn(),
    title: TITLE,
    ...overrides,
  };
}

function mapCount(): number {
  return vi.mocked(MapLibreMap).mock.results.length;
}

function mapAt(index: number): MapLibreMap {
  const result = vi.mocked(MapLibreMap).mock.results.at(index);
  if (result?.type !== 'return') throw new Error(`Map ${index} was not constructed`);
  return result.value;
}

function mapOptionsAt(index: number) {
  const options = vi.mocked(MapLibreMap).mock.calls.at(index)?.[0];
  if (!options) throw new Error(`Map ${index} was not constructed`);
  return options;
}

/** The handler a map registered with `map.on(name, handler)`. */
function handlerFor(map: MapLibreMap, name: string): (event: unknown) => void {
  const call = vi.mocked(map.on).mock.calls.find(([eventName]) => eventName === name);
  const handler: unknown = call?.[1];
  if (typeof handler !== 'function') throw new Error(`no ${name} handler registered`);
  return event => {
    Reflect.apply(handler, undefined, [event]);
  };
}

async function mount(overrides: Partial<Props> = {}) {
  const before = mapCount();
  const props = createProps(overrides);
  const result = testFactory.render(props);
  await vi.waitFor(() => expect(mapCount()).toBeGreaterThan(before));
  return { ...result, props };
}

async function openExpanded(user: ReturnType<typeof userEvent.setup>) {
  const before = mapCount();
  await user.click(screen.getByRole('button', { name: EXPAND }));
  await vi.waitFor(() => expect(mapCount()).toBeGreaterThan(before));
  return screen.getByRole('dialog', { name: TITLE });
}

describe('LocationMap', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  describe('building the map', () => {
    it('builds no map when the component is gone before the library loads', async () => {
      const result = testFactory.render(createProps());
      result.unmount();
      await new Promise(resolve => setTimeout(resolve, 50));

      expect(mapCount()).toBe(0);
      expect(toastActions.error).not.toHaveBeenCalled();
    });

    it('removes the map when unmounted', async () => {
      const result = await mount();
      const map = mapAt(0);

      result.unmount();

      expect(map.remove).toHaveBeenCalledTimes(1);
    });
  });

  describe('coordinates', () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });

    it('never reports a location on mount or when coordinates change from outside', async () => {
      const { props, rerender } = await mount();

      await rerender({ ...props, latitude: 61, longitude: 25 });
      await vi.advanceTimersByTimeAsync(2000);

      expect(props.onLocationChange).not.toHaveBeenCalled();
    });

    it('moves the map to typed coordinates after the debounce and keeps the zoom', async () => {
      const { props, rerender } = await mount();
      const map = mapAt(0);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: 61.5, longitude: 25.5 });
      await vi.advanceTimersByTimeAsync(499);
      expect(map.easeTo).not.toHaveBeenCalled();

      await vi.advanceTimersByTimeAsync(1);
      expect(map.easeTo).toHaveBeenCalledExactlyOnceWith({
        center: [25.5, 61.5],
        zoom: 10,
        duration: 300,
      });
    });

    it('moves only once for a burst of typing', async () => {
      const { props, rerender } = await mount();
      const map = mapAt(0);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: 61, longitude: 25 });
      await vi.advanceTimersByTimeAsync(300);
      await rerender({ ...props, latitude: 62, longitude: 25 });
      await vi.advanceTimersByTimeAsync(600);

      expect(map.easeTo).toHaveBeenCalledExactlyOnceWith({
        center: [25, 62],
        zoom: 10,
        duration: 300,
      });
    });

    it('creates the pin for typed coordinates only once the location is set', async () => {
      const { props, rerender } = await mount({
        latitude: 0,
        longitude: 0,
        locationSet: false,
      });
      expect(vi.mocked(Marker)).not.toHaveBeenCalled();

      await rerender({ ...props, latitude: 5, longitude: 5, locationSet: false });
      await vi.advanceTimersByTimeAsync(600);
      expect(vi.mocked(Marker)).not.toHaveBeenCalled();

      await rerender({ ...props, latitude: 5, longitude: 5, locationSet: true });
      await vi.advanceTimersByTimeAsync(600);
      expect(vi.mocked(Marker)).toHaveBeenCalledTimes(1);
    });

    it('world start view zooms in for the first location and keeps the zoom afterwards', async () => {
      const { props, rerender } = await mount({
        latitude: 0,
        longitude: 0,
        locationSet: false,
        startView: 'world',
      });
      const map = mapAt(0);
      vi.mocked(map.getZoom).mockReturnValue(1);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: 60, longitude: 24, locationSet: true });
      await vi.advanceTimersByTimeAsync(600);
      expect(map.easeTo).toHaveBeenLastCalledWith({ center: [24, 60], zoom: 11, duration: 300 });

      vi.mocked(map.getZoom).mockReturnValue(15);
      await rerender({ ...props, latitude: 61, longitude: 25, locationSet: true });
      await vi.advanceTimersByTimeAsync(600);
      expect(map.easeTo).toHaveBeenLastCalledWith({ center: [25, 61], zoom: 15, duration: 300 });
    });

    it('leaves the inline map alone while the expanded map is open and catches up on close', async () => {
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      const { props, rerender } = await mount();
      const map = mapAt(0);
      const dialog = await openExpanded(user);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: 61, longitude: 25 });
      await vi.advanceTimersByTimeAsync(1000);
      expect(map.easeTo).not.toHaveBeenCalled();

      await user.click(within(dialog).getByRole('button', { name: 'common.done' }));
      await vi.advanceTimersByTimeAsync(600);
      expect(map.easeTo).toHaveBeenCalledWith({ center: [25, 61], zoom: 10, duration: 300 });
    });

    it('does not move the map for NaN coordinates', async () => {
      const { props, rerender } = await mount();
      const map = mapAt(0);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: Number.NaN, longitude: 25 });
      await vi.advanceTimersByTimeAsync(600);

      expect(map.easeTo).not.toHaveBeenCalled();
    });
  });

  describe('controls', () => {
    it('zoom buttons zoom the map', async () => {
      const user = userEvent.setup();
      await mount();
      const map = mapAt(0);

      await user.click(screen.getByRole('button', { name: ZOOM_IN }));
      await user.click(screen.getByRole('button', { name: ZOOM_OUT }));

      expect(map.zoomIn).toHaveBeenCalledWith({ duration: 300 });
      expect(map.zoomOut).toHaveBeenCalledWith({ duration: 300 });
    });

    it('disables the buttons until the map exists', () => {
      testFactory.render(createProps({ ready: false }));

      expect(screen.getByRole('button', { name: ZOOM_IN })).toBeDisabled();
      expect(screen.getByRole('button', { name: EXPAND })).toBeDisabled();
    });

    it('shows the zoom help text', async () => {
      await mount();

      expect(screen.getByText('common.ui.mapZoomHelp')).toBeInTheDocument();
    });

    it('places the buttons inside the map frame, outside the map region, in overlay mode', async () => {
      await mount({ controls: 'overlay' });

      const mapRegion = screen.getByRole('application', { name: MAP_LABEL });
      const zoomIn = screen.getByRole('button', { name: ZOOM_IN });
      expect(mapRegion).not.toContainElement(zoomIn);
      expect(mapRegion.parentElement).toContainElement(zoomIn);
    });

    it('applies the map height class', async () => {
      await mount({ mapClass: 'h-[300px]' });

      expect(screen.getByRole('application', { name: MAP_LABEL })).toHaveClass('h-[300px]');
    });
  });

  describe('picking', () => {
    it('reports a click on the inline map rounded to three decimals', async () => {
      const { props } = await mount();

      handlerFor(mapAt(0), 'click')({ lngLat: { lat: 60.123456, lng: 24.987654 } });

      expect(props.onLocationChange).toHaveBeenCalledExactlyOnceWith(60.123, 24.988);
    });

    it('does not report anything after unmount', async () => {
      const { props, unmount } = await mount();
      const click = handlerFor(mapAt(0), 'click');

      unmount();
      click({ lngLat: { lat: 61, lng: 25 } });

      expect(props.onLocationChange).not.toHaveBeenCalled();
    });
  });

  describe('expanded map', () => {
    it('opens a dialog titled with the title prop', async () => {
      const user = userEvent.setup();
      await mount({ title: 'Where is the station' });

      await user.click(screen.getByRole('button', { name: EXPAND }));

      expect(screen.getByRole('dialog', { name: 'Where is the station' })).toBeInTheDocument();
      expect(screen.getByRole('application', { name: EXPANDED_MAP_LABEL })).toBeInTheDocument();
    });

    it('builds the expanded map at the inline zoom with a pin and unrestricted wheel zoom', async () => {
      const user = userEvent.setup();
      await mount();

      await openExpanded(user);

      expect(mapOptionsAt(1)).toMatchObject({ center: [24, 60], zoom: 10 });
      expect(mapAt(1).scrollZoom.enable).toHaveBeenCalled();
      expect(vi.mocked(Marker)).toHaveBeenCalledTimes(2);
    });

    it('shows the current coordinates in the dialog', async () => {
      const user = userEvent.setup();
      await mount({ latitude: 60.123, longitude: 24.456 });

      const dialog = await openExpanded(user);

      expect(within(dialog).getByText('60.123')).toBeInTheDocument();
      expect(within(dialog).getByText('24.456')).toBeInTheDocument();
    });

    it('closing the expanded map removes its map', async () => {
      const user = userEvent.setup();
      await mount();
      const dialog = await openExpanded(user);
      const expandedMap = mapAt(1);

      await user.click(within(dialog).getByRole('button', { name: 'common.done' }));

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(expandedMap.remove).toHaveBeenCalledTimes(1);
      expect(mapAt(0).remove).not.toHaveBeenCalled();
    });

    it('closes from the close button and the backdrop', async () => {
      const user = userEvent.setup();
      await mount();

      const dialog = await openExpanded(user);
      await user.click(within(dialog).getByRole('button', { name: CLOSE }));
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

      const reopened = await openExpanded(user);
      await user.click(reopened);
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('shows a toast and closes when the expanded map cannot be built', async () => {
      const user = userEvent.setup();
      await mount();
      vi.mocked(MapLibreMap).mockImplementationOnce(function () {
        throw new Error('WebGL unavailable');
      });

      await user.click(screen.getByRole('button', { name: EXPAND }));

      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
      expect(toastActions.error).toHaveBeenCalledWith('settings.main.errors.modalMapLoadFailed');
      expect(screen.getByRole('button', { name: EXPAND })).toHaveFocus();
    });

    it('a pick in the expanded map reports the location and updates the inline map', async () => {
      const user = userEvent.setup();
      const { props } = await mount();
      await openExpanded(user);
      const inlineMap = mapAt(0);
      vi.mocked(inlineMap.easeTo).mockClear();

      handlerFor(mapAt(1), 'click')({ lngLat: { lat: 60.1236, lng: 24.9876 } });

      expect(props.onLocationChange).toHaveBeenCalledExactlyOnceWith(60.124, 24.988);
      expect(inlineMap.easeTo).toHaveBeenCalledWith({
        center: [24.988, 60.124],
        zoom: 10,
        duration: 0,
      });
    });

    it('the first click in the expanded map shows the pin when none exists', async () => {
      const user = userEvent.setup();
      await mount({ latitude: 0, longitude: 0, locationSet: false });
      await openExpanded(user);
      expect(vi.mocked(Marker)).not.toHaveBeenCalled();

      handlerFor(mapAt(1), 'click')({ lngLat: { lat: 60, lng: 24 } });

      // One pin for the expanded map, one created for the inline map.
      expect(vi.mocked(Marker)).toHaveBeenCalledTimes(2);
    });

    it('a double-tap-protected pick made just before Done is kept', async () => {
      const user = userEvent.setup();
      const { props } = await mount({ doubleTapZoomKeepsPin: true });
      const dialog = await openExpanded(user);

      handlerFor(mapAt(1), 'click')({ lngLat: { lat: 60.5, lng: 24.5 } });
      expect(props.onLocationChange).not.toHaveBeenCalled();

      await user.click(within(dialog).getByRole('button', { name: 'common.done' }));

      expect(props.onLocationChange).toHaveBeenCalledExactlyOnceWith(60.5, 24.5);
    });

    it('moves focus into the dialog on open and back to the expand button on close', async () => {
      const user = userEvent.setup();
      await mount();
      const expandButton = screen.getByRole('button', { name: EXPAND });

      const dialog = await openExpanded(user);
      await waitFor(() =>
        expect(within(dialog).getByRole('button', { name: CLOSE })).toHaveFocus()
      );

      await user.keyboard('{Escape}');

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(expandButton).toHaveFocus();
    });

    it('Escape closes the expanded map and does not reach document listeners', async () => {
      const user = userEvent.setup();
      const documentKeydown = vi.fn();
      document.addEventListener('keydown', documentKeydown);
      try {
        await mount();
        const dialog = await openExpanded(user);
        await waitFor(() =>
          expect(within(dialog).getByRole('button', { name: CLOSE })).toHaveFocus()
        );

        await user.keyboard('{Escape}');

        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
        expect(documentKeydown).not.toHaveBeenCalled();
      } finally {
        document.removeEventListener('keydown', documentKeydown);
      }
    });

    it('Tab wraps inside the dialog and does not reach document listeners', async () => {
      const user = userEvent.setup();
      const documentKeydown = vi.fn<(event: KeyboardEvent) => void>();
      document.addEventListener('keydown', documentKeydown);
      try {
        await mount();
        const dialog = await openExpanded(user);
        const close = within(dialog).getByRole('button', { name: CLOSE });
        const done = within(dialog).getByRole('button', { name: 'common.done' });

        done.focus();
        await user.tab();
        expect(close).toHaveFocus();

        await user.tab({ shift: true });
        expect(done).toHaveFocus();
        // The Shift key press itself is not a Tab and is not stopped.
        expect(documentKeydown.mock.calls.filter(([event]) => event.key === 'Tab')).toEqual([]);
      } finally {
        document.removeEventListener('keydown', documentKeydown);
      }
    });

    it('clicking the dialog padding keeps focus inside the dialog', async () => {
      const user = userEvent.setup();
      await mount();
      const dialog = await openExpanded(user);
      const box = within(dialog).getByRole('document');

      await user.click(box);

      expect(dialog).toContainElement(
        document.activeElement instanceof HTMLElement ? document.activeElement : null
      );
      expect(document.activeElement).not.toBe(document.body);
    });

    it('is portalled into the body by default', async () => {
      const user = userEvent.setup();
      const result = await mount();

      const dialog = await openExpanded(user);

      expect(dialog.parentElement).toBe(document.body);
      expect(result.container).not.toContainElement(dialog);
    });

    it('is portalled into the surrounding dialog', async () => {
      const user = userEvent.setup();
      const outer = document.createElement('div');
      outer.setAttribute('role', 'dialog');
      outer.setAttribute('aria-label', 'Outer dialog');
      document.body.appendChild(outer);
      try {
        const before = mapCount();
        render(LocationMap, { target: outer, props: createProps() });
        await vi.waitFor(() => expect(mapCount()).toBeGreaterThan(before));

        const dialog = await openExpanded(user);

        expect(dialog.parentElement).toBe(outer);
      } finally {
        cleanup();
        outer.remove();
      }
    });

    it('removes both maps when unmounted with the dialog open', async () => {
      const user = userEvent.setup();
      const result = await mount();
      await openExpanded(user);
      const [inlineMap, expandedMap] = [mapAt(0), mapAt(1)];

      result.unmount();

      expect(inlineMap.remove).toHaveBeenCalledTimes(1);
      expect(expandedMap.remove).toHaveBeenCalledTimes(1);
    });
  });
});

describe('LocationMap Accessibility', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it('gives the map region and the buttons translated names', async () => {
    await mount();

    expect(screen.getByRole('application', { name: MAP_LABEL })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: ZOOM_IN })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: ZOOM_OUT })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: EXPAND })).toBeInTheDocument();
  });

  it('has no axe violations inline', async () => {
    const result = await mount();

    await expect(expectNoA11yViolations(result.container)).resolves.toBeUndefined();
  });

  it('has no axe violations with the expanded dialog open', async () => {
    const user = userEvent.setup();
    await mount();

    const dialog = await openExpanded(user);

    await expect(expectNoA11yViolations(dialog)).resolves.toBeUndefined();
    expect(within(dialog).getByRole('application', { name: EXPANDED_MAP_LABEL })).toBeVisible();
  });
});
