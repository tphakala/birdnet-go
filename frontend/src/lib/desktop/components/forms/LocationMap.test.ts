import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ComponentProps } from 'svelte';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { Map as MapLibreMap, Marker } from 'maplibre-gl';
import LocationMap from './LocationMap.svelte';
import {
  COORDINATE_SYNC_DEBOUNCE_MS,
  COORDINATE_SYNC_DURATION_MS,
  DOUBLE_TAP_WINDOW_MS,
  ZOOM_STEP_DURATION_MS,
} from './locationMapController';
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
const DONE = 'common.done';
// Placing a pin does not animate the map (MAP_CONFIG.ANIMATION_DURATION is 0).
const NO_ANIMATION_MS = 0;
// Time that is safely past a pending coordinate sync.
const PAST_SYNC_MS = COORDINATE_SYNC_DEBOUNCE_MS + 100;

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
    it('does not build the map until ready', async () => {
      const props = createProps({ ready: false });
      const result = testFactory.render(props);

      await vi.dynamicImportSettled();
      expect(mapCount()).toBe(0);

      await result.rerender({ ...props, ready: true });
      await vi.waitFor(() => expect(mapCount()).toBe(1));
    });

    it('builds the settings map with a pin and the region zoom by default', async () => {
      await mount({ latitude: 60, longitude: 24, locationSet: true });

      expect(mapOptionsAt(0)).toMatchObject({ center: [24, 60], zoom: 11, touchZoomRotate: false });
      expect(vi.mocked(Marker)).toHaveBeenCalledTimes(1);
    });

    it('shows no pin when the location is not set', async () => {
      await mount({ latitude: 0, longitude: 0, locationSet: false });

      expect(mapOptionsAt(0).zoom).toBe(5);
      expect(vi.mocked(Marker)).not.toHaveBeenCalled();
    });

    it('starts at the world overview when the coordinates are 0,0 and startView is world', async () => {
      await mount({ latitude: 0, longitude: 0, locationSet: false, startView: 'world' });

      expect(mapOptionsAt(0).zoom).toBe(1);
    });

    it('enables pinch zoom on both maps only when asked', async () => {
      const user = userEvent.setup();
      await mount({ pinchZoom: true });
      await openExpanded(user);

      for (const index of [0, 1]) {
        expect(mapOptionsAt(index).touchZoomRotate).toBe(true);
        expect(mapAt(index).touchZoomRotate.disableRotation).toHaveBeenCalled();
      }
    });

    it('keeps pinch zoom off on both maps by default', async () => {
      const user = userEvent.setup();
      await mount();
      await openExpanded(user);

      for (const index of [0, 1]) {
        expect(mapOptionsAt(index).touchZoomRotate).toBe(false);
      }
    });

    it('shows the placeholder and a toast when MapLibre fails to construct', async () => {
      vi.mocked(MapLibreMap).mockImplementationOnce(function () {
        throw new Error('WebGL unavailable');
      });

      testFactory.render(createProps());

      expect(await screen.findByText('settings.main.errors.mapUnavailable')).toBeInTheDocument();
      expect(toastActions.error).toHaveBeenCalledExactlyOnceWith(
        'settings.main.errors.mapLoadFailed'
      );
      expect(screen.queryByRole('application')).not.toBeInTheDocument();
      expect(mapCount()).toBe(1);
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
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS * 4);

      expect(props.onLocationChange).not.toHaveBeenCalled();
    });

    it('moves the map to typed coordinates after the debounce and keeps the zoom', async () => {
      const { props, rerender } = await mount();
      const map = mapAt(0);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: 61.5, longitude: 25.5 });
      await vi.advanceTimersByTimeAsync(COORDINATE_SYNC_DEBOUNCE_MS - 1);
      expect(map.easeTo).not.toHaveBeenCalled();

      await vi.advanceTimersByTimeAsync(1);
      expect(map.easeTo).toHaveBeenCalledExactlyOnceWith({
        center: [25.5, 61.5],
        zoom: 10,
        duration: COORDINATE_SYNC_DURATION_MS,
      });
    });

    it('moves only once for a burst of typing', async () => {
      const { props, rerender } = await mount();
      const map = mapAt(0);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: 61, longitude: 25 });
      await vi.advanceTimersByTimeAsync(COORDINATE_SYNC_DEBOUNCE_MS / 2);
      await rerender({ ...props, latitude: 62, longitude: 25 });
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);

      expect(map.easeTo).toHaveBeenCalledExactlyOnceWith({
        center: [25, 62],
        zoom: 10,
        duration: COORDINATE_SYNC_DURATION_MS,
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
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);
      expect(vi.mocked(Marker)).not.toHaveBeenCalled();

      await rerender({ ...props, latitude: 5, longitude: 5, locationSet: true });
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);
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
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);
      expect(map.easeTo).toHaveBeenLastCalledWith({
        center: [24, 60],
        zoom: 11,
        duration: COORDINATE_SYNC_DURATION_MS,
      });

      vi.mocked(map.getZoom).mockReturnValue(15);
      await rerender({ ...props, latitude: 61, longitude: 25, locationSet: true });
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);
      expect(map.easeTo).toHaveBeenLastCalledWith({
        center: [25, 61],
        zoom: 15,
        duration: COORDINATE_SYNC_DURATION_MS,
      });
    });

    it('world start view keeps a zoom below the location zoom when a pin already exists', async () => {
      const { props, rerender } = await mount({ startView: 'world', locationSet: true });
      const map = mapAt(0);
      vi.mocked(map.getZoom).mockReturnValue(5);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: 61, longitude: 25 });
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);

      expect(map.easeTo).toHaveBeenLastCalledWith({
        center: [25, 61],
        zoom: 5,
        duration: COORDINATE_SYNC_DURATION_MS,
      });
    });

    it('world start view keeps a zoom above the location zoom for the first location', async () => {
      const { props, rerender } = await mount({
        latitude: 0,
        longitude: 0,
        locationSet: false,
        startView: 'world',
      });
      const map = mapAt(0);
      vi.mocked(map.getZoom).mockReturnValue(14);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: 60, longitude: 24, locationSet: true });
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);

      expect(map.easeTo).toHaveBeenLastCalledWith({
        center: [24, 60],
        zoom: 14,
        duration: COORDINATE_SYNC_DURATION_MS,
      });
    });

    it('world start view keeps the zoom for coordinates that are not a set location', async () => {
      const { props, rerender } = await mount({
        latitude: 0,
        longitude: 0,
        locationSet: false,
        startView: 'world',
      });
      const map = mapAt(0);
      vi.mocked(map.getZoom).mockReturnValue(1);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: 5, longitude: 5, locationSet: false });
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);

      expect(map.easeTo).toHaveBeenLastCalledWith({
        center: [5, 5],
        zoom: 1,
        duration: COORDINATE_SYNC_DURATION_MS,
      });
      expect(vi.mocked(Marker)).not.toHaveBeenCalled();
    });

    it('keeps the zoom for the first location on a region map', async () => {
      const { props, rerender } = await mount({
        latitude: 0,
        longitude: 0,
        locationSet: false,
      });
      const map = mapAt(0);
      vi.mocked(map.getZoom).mockReturnValue(5);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: 60, longitude: 24, locationSet: true });
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);

      expect(map.easeTo).toHaveBeenLastCalledWith({
        center: [24, 60],
        zoom: 5,
        duration: COORDINATE_SYNC_DURATION_MS,
      });
    });

    it('leaves the inline map alone while the expanded map is open and catches up on close', async () => {
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      const { props, rerender } = await mount();
      const map = mapAt(0);
      const dialog = await openExpanded(user);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: 61, longitude: 25 });
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS * 2);
      expect(map.easeTo).not.toHaveBeenCalled();

      await user.click(within(dialog).getByRole('button', { name: DONE }));
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);
      expect(map.easeTo).toHaveBeenCalledWith({
        center: [25, 61],
        zoom: 10,
        duration: COORDINATE_SYNC_DURATION_MS,
      });
    });

    it('removes the pin when the location stops being set', async () => {
      const { props, rerender } = await mount({ locationSet: true });
      const pin = vi.mocked(Marker).mock.results.at(0);
      if (pin?.type !== 'return') throw new Error('the pin was not created');

      await rerender({ ...props, locationSet: false });
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);

      expect(pin.value.remove).toHaveBeenCalledTimes(1);
    });

    it.each([
      ['an infinite latitude', Number.POSITIVE_INFINITY, 25],
      ['an infinite longitude', 61, Number.NEGATIVE_INFINITY],
    ])('does not move the map for %s', async (_label, latitude, longitude) => {
      const { props, rerender } = await mount();
      const map = mapAt(0);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude, longitude });
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);

      expect(map.easeTo).not.toHaveBeenCalled();
    });

    it('does not move the map for NaN coordinates', async () => {
      const { props, rerender } = await mount();
      const map = mapAt(0);
      vi.mocked(map.easeTo).mockClear();

      await rerender({ ...props, latitude: Number.NaN, longitude: 25 });
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);

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

      expect(map.zoomIn).toHaveBeenCalledWith({ duration: ZOOM_STEP_DURATION_MS });
      expect(map.zoomOut).toHaveBeenCalledWith({ duration: ZOOM_STEP_DURATION_MS });
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

    it('zooms the inline map with the wheel only while Ctrl is held', async () => {
      await mount();
      const map = mapAt(0);
      const region = screen.getByRole('application', { name: MAP_LABEL });

      const plain = new WheelEvent('wheel', { deltaY: -100, cancelable: true, bubbles: true });
      region.dispatchEvent(plain);
      expect(plain.defaultPrevented).toBe(false);
      expect(map.zoomIn).not.toHaveBeenCalled();

      const withCtrl = new WheelEvent('wheel', {
        deltaY: -100,
        ctrlKey: true,
        cancelable: true,
        bubbles: true,
      });
      region.dispatchEvent(withCtrl);
      expect(withCtrl.defaultPrevented).toBe(true);
      expect(map.zoomIn).toHaveBeenCalledTimes(1);
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

    it('recentres the inline map on a click at its current zoom', async () => {
      await mount();
      const map = mapAt(0);

      handlerFor(map, 'click')({ lngLat: { lat: 60.5, lng: 24.5 } });

      expect(map.easeTo).toHaveBeenCalledWith({
        center: [24.5, 60.5],
        zoom: 10,
        duration: NO_ANIMATION_MS,
      });
    });

    it('waits out the double-tap window before reporting an inline click when asked', async () => {
      vi.useFakeTimers();
      const { props } = await mount({ doubleTapZoomKeepsPin: true });

      handlerFor(mapAt(0), 'click')({ lngLat: { lat: 60.5, lng: 24.5 } });
      expect(props.onLocationChange).not.toHaveBeenCalled();

      await vi.advanceTimersByTimeAsync(DOUBLE_TAP_WINDOW_MS);
      expect(props.onLocationChange).toHaveBeenCalledExactlyOnceWith(60.5, 24.5);
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

    it('starts the expanded map at the inline zoom even when that is zoom 0', async () => {
      const user = userEvent.setup();
      await mount();
      vi.mocked(mapAt(0).getZoom).mockReturnValue(0);

      await openExpanded(user);

      expect(mapOptionsAt(1).zoom).toBe(0);
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

      await user.click(within(dialog).getByRole('button', { name: DONE }));

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

    it('stays open when a press that started on the map ends on the backdrop', async () => {
      const user = userEvent.setup();
      await mount();
      const dialog = await openExpanded(user);
      const box = within(dialog).getByRole('document');

      // A pan from the map released over the backdrop: the browser sends the
      // click to the common ancestor, the backdrop.
      await fireEvent.pointerDown(box);
      await fireEvent.click(dialog);
      expect(screen.getByRole('dialog')).toBeInTheDocument();

      // A later click without a press on the backdrop must not reuse a flag.
      await fireEvent.click(dialog);
      expect(screen.getByRole('dialog')).toBeInTheDocument();

      await fireEvent.pointerDown(dialog);
      await fireEvent.click(dialog);
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

    it('does not recentre the expanded map on a click and leaves wheel zoom to MapLibre', async () => {
      const user = userEvent.setup();
      await mount();
      await openExpanded(user);
      const expandedMap = mapAt(1);

      handlerFor(expandedMap, 'click')({ lngLat: { lat: 60.5, lng: 24.5 } });
      expect(expandedMap.easeTo).not.toHaveBeenCalled();

      const region = screen.getByRole('application', { name: EXPANDED_MAP_LABEL });
      const wheel = new WheelEvent('wheel', { deltaY: -100, cancelable: true, bubbles: true });
      region.dispatchEvent(wheel);
      // MapLibre's own scroll zoom does the zooming; a second custom step would double it.
      expect(expandedMap.scrollZoom.enable).toHaveBeenCalledTimes(1);
      expect(wheel.defaultPrevented).toBe(false);
      expect(expandedMap.zoomIn).not.toHaveBeenCalled();
    });

    it('zoom buttons in the dialog zoom the expanded map, not the inline one', async () => {
      const user = userEvent.setup();
      await mount();
      const dialog = await openExpanded(user);

      await user.click(within(dialog).getByRole('button', { name: ZOOM_IN }));
      await user.click(within(dialog).getByRole('button', { name: ZOOM_OUT }));

      expect(mapAt(1).zoomIn).toHaveBeenCalledWith({ duration: ZOOM_STEP_DURATION_MS });
      expect(mapAt(1).zoomOut).toHaveBeenCalledWith({ duration: ZOOM_STEP_DURATION_MS });
      expect(mapAt(0).zoomIn).not.toHaveBeenCalled();
      expect(mapAt(0).zoomOut).not.toHaveBeenCalled();
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
        duration: NO_ANIMATION_MS,
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
      vi.useFakeTimers();
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      const { props } = await mount({ doubleTapZoomKeepsPin: true });
      const dialog = await openExpanded(user);

      handlerFor(mapAt(1), 'click')({ lngLat: { lat: 60.5, lng: 24.5 } });
      expect(props.onLocationChange).not.toHaveBeenCalled();

      await user.click(within(dialog).getByRole('button', { name: DONE }));
      expect(props.onLocationChange).toHaveBeenCalledExactlyOnceWith(60.5, 24.5);

      await vi.advanceTimersByTimeAsync(DOUBLE_TAP_WINDOW_MS * 2);
      expect(props.onLocationChange).toHaveBeenCalledTimes(1);
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
        const done = within(dialog).getByRole('button', { name: DONE });

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

    it('Shift+Tab from the dialog box wraps to the last element', async () => {
      const user = userEvent.setup();
      await mount();
      const dialog = await openExpanded(user);

      within(dialog).getByRole('document').focus();
      await user.tab({ shift: true });

      expect(within(dialog).getByRole('button', { name: DONE })).toHaveFocus();
    });

    it('Shift+Tab from the dialog root wraps to the last element', async () => {
      const user = userEvent.setup();
      await mount();
      const dialog = await openExpanded(user);

      dialog.focus();
      await user.tab({ shift: true });

      expect(within(dialog).getByRole('button', { name: DONE })).toHaveFocus();
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

describe('LocationMap place search', () => {
  const SEARCH_LABEL = 'components.locationMap.search.label';
  const originalFetch = globalThis.fetch;
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

  beforeEach(() => {
    vi.clearAllMocks();
    fetchMock = vi.fn<typeof fetch>(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            type: 'FeatureCollection',
            features: [
              {
                type: 'Feature',
                properties: { osm_type: 'R', osm_id: 34914, name: 'Helsinki' },
                geometry: { type: 'Point', coordinates: [24.9435408, 60.1666204] },
              },
            ],
          }),
          { status: 200 }
        )
      )
    );
    globalThis.fetch = fetchMock;
  });

  afterEach(() => {
    cleanup();
    globalThis.fetch = originalFetch;
  });

  /** Type a query into the search box in `scope` and pick the first result. */
  async function searchAndPick(scope: HTMLElement | undefined = undefined) {
    const root = scope ?? document.body;
    const box = within(root).getByRole('combobox', { name: SEARCH_LABEL });
    box.focus();
    await fireEvent.input(box, { target: { value: 'Helsinki' } });
    await fireEvent.keyDown(box, { key: 'Enter' });
    await fireEvent.click(await within(root).findByRole('option'));
  }

  it('shows no search unless placeSearch is set', async () => {
    await mount();

    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  });

  it('shows the search only once the map is ready', async () => {
    const props = createProps({ placeSearch: true, ready: false });
    const result = testFactory.render(props);

    expect(screen.queryByRole('combobox', { name: SEARCH_LABEL })).not.toBeInTheDocument();

    await result.rerender({ ...props, ready: true });
    expect(screen.getByRole('combobox', { name: SEARCH_LABEL })).toBeEnabled();
  });

  it('shows the search above the map when placeSearch is set', async () => {
    await mount({ placeSearch: true });

    const box = screen.getByRole('combobox', { name: SEARCH_LABEL });
    const map = screen.getByRole('application', { name: MAP_LABEL });
    expect(box.compareDocumentPosition(map) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(map).not.toContainElement(box);
  });

  it('a chosen place sets the pin, zooms in and reports rounded coordinates', async () => {
    const { props } = await mount({
      latitude: 0,
      longitude: 0,
      locationSet: false,
      placeSearch: true,
    });
    const map = mapAt(0);
    vi.mocked(map.easeTo).mockClear();

    await searchAndPick();

    expect(props.onLocationChange).toHaveBeenCalledExactlyOnceWith(60.167, 24.944);
    expect(map.easeTo).toHaveBeenCalledExactlyOnceWith({
      center: [24.944, 60.167],
      zoom: 11,
      duration: COORDINATE_SYNC_DURATION_MS,
    });
    expect(vi.mocked(Marker)).toHaveBeenCalledTimes(1);
  });

  it('keeps the chosen zoom when the coordinates come back as props', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      const { props, rerender } = await mount({ placeSearch: true });
      const map = mapAt(0);
      // The mock map never animates, so let it report the zoom it was last asked for.
      let zoom = 10;
      vi.mocked(map.getZoom).mockImplementation(() => zoom);
      vi.mocked(map.easeTo).mockImplementation(options => {
        zoom = options.zoom ?? zoom;
        return map;
      });
      vi.mocked(map.easeTo).mockClear();

      await searchAndPick();
      await rerender({ ...props, latitude: 60.167, longitude: 24.944 });
      await vi.advanceTimersByTimeAsync(PAST_SYNC_MS);

      // The sync that follows the parent update keeps the chosen zoom
      expect(map.easeTo).toHaveBeenLastCalledWith({
        center: [24.944, 60.167],
        zoom: 11,
        duration: COORDINATE_SYNC_DURATION_MS,
      });
      // The flight has to end before that sync reads the zoom
      expect(COORDINATE_SYNC_DURATION_MS).toBeLessThan(COORDINATE_SYNC_DEBOUNCE_MS);
    } finally {
      vi.useRealTimers();
    }
  });

  it('a place chosen in the expanded dialog moves both maps', async () => {
    const user = userEvent.setup();
    const { props } = await mount({ placeSearch: true });
    const dialog = await openExpanded(user);
    const [inlineMap, expandedMap] = [mapAt(0), mapAt(1)];
    vi.mocked(inlineMap.easeTo).mockClear();
    vi.mocked(expandedMap.easeTo).mockClear();

    await searchAndPick(dialog);

    expect(props.onLocationChange).toHaveBeenCalledExactlyOnceWith(60.167, 24.944);
    for (const map of [inlineMap, expandedMap]) {
      expect(map.easeTo).toHaveBeenCalledWith(
        expect.objectContaining({ center: [24.944, 60.167], zoom: 11 })
      );
    }
  });

  it('Escape in the expanded search closes its list first and the map after that', async () => {
    const user = userEvent.setup();
    await mount({ placeSearch: true });
    const dialog = await openExpanded(user);
    const box = within(dialog).getByRole('combobox', { name: SEARCH_LABEL });
    box.focus();
    await fireEvent.input(box, { target: { value: 'Helsinki' } });
    await fireEvent.keyDown(box, { key: 'Enter' });
    await within(dialog).findByRole('listbox');

    await fireEvent.keyDown(box, { key: 'Escape' });
    expect(within(dialog).queryByRole('listbox')).not.toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: TITLE })).toBeInTheDocument();

    await fireEvent.keyDown(box, { key: 'Escape' });
    expect(box).toHaveValue('');
    expect(screen.getByRole('dialog', { name: TITLE })).toBeInTheDocument();

    await fireEvent.keyDown(box, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: TITLE })).not.toBeInTheDocument();
  });

  it('still offers the search when the map library fails to build the map', async () => {
    vi.mocked(MapLibreMap).mockImplementationOnce(function () {
      throw new Error('WebGL unavailable');
    });
    const props = createProps({ placeSearch: true });
    testFactory.render(props);
    await screen.findByText('settings.main.errors.mapUnavailable');

    await searchAndPick();

    expect(props.onLocationChange).toHaveBeenCalledExactlyOnceWith(60.167, 24.944);
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
