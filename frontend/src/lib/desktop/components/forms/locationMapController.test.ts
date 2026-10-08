import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  DOUBLE_TAP_WINDOW_MS,
  ZOOM_STEP_DURATION_MS,
  createLocationMapController,
  initialZoom,
  type LocationMapControllerOptions,
  type MapLibreModule,
} from './locationMapController';

type Handler = (event: unknown) => void;

interface FakeMarker {
  options: unknown;
  lngLat: [number, number];
  handlers: Map<string, Handler>;
  setLngLat: ReturnType<typeof vi.fn>;
  addTo: ReturnType<typeof vi.fn>;
  on: ReturnType<typeof vi.fn>;
  getLngLat: () => { lat: number; lng: number };
}

interface FakeMap {
  options: Record<string, unknown>;
  handlers: Map<string, Handler>;
  zoom: number;
  on: ReturnType<typeof vi.fn>;
  easeTo: ReturnType<typeof vi.fn>;
  zoomIn: ReturnType<typeof vi.fn>;
  zoomOut: ReturnType<typeof vi.fn>;
  getZoom: () => number;
  resize: ReturnType<typeof vi.fn>;
  remove: ReturnType<typeof vi.fn>;
  scrollZoom: { enable: ReturnType<typeof vi.fn> };
  touchZoomRotate: { disableRotation: ReturnType<typeof vi.fn> };
}

/** A plain fake of the parts of maplibre-gl the controller uses. */
function createFakeMapLibre() {
  const maps: FakeMap[] = [];
  const markers: FakeMarker[] = [];

  // `new` on a function that returns an object yields that object.
  const FakeMapClass = vi.fn(function (options: Record<string, unknown>) {
    {
      const handlers = new Map<string, Handler>();
      const fake: FakeMap = {
        options,
        handlers,
        zoom: typeof options.zoom === 'number' ? options.zoom : 0,
        on: vi.fn((name: string, handler: Handler) => {
          handlers.set(name, handler);
        }),
        easeTo: vi.fn(),
        zoomIn: vi.fn(),
        zoomOut: vi.fn(),
        getZoom: () => fake.zoom,
        resize: vi.fn(),
        remove: vi.fn(),
        scrollZoom: { enable: vi.fn() },
        touchZoomRotate: { disableRotation: vi.fn() },
      };
      maps.push(fake);
      return fake;
    }
  });

  const FakeMarkerClass = vi.fn(function (options: unknown) {
    {
      const handlers = new Map<string, Handler>();
      const fake: FakeMarker = {
        options,
        lngLat: [0, 0],
        handlers,
        setLngLat: vi.fn((lngLat: [number, number]) => {
          fake.lngLat = lngLat;
          return fake;
        }),
        addTo: vi.fn(() => fake),
        on: vi.fn((name: string, handler: Handler) => {
          handlers.set(name, handler);
          return fake;
        }),
        getLngLat: () => ({ lng: fake.lngLat[0], lat: fake.lngLat[1] }),
      };
      markers.push(fake);
      return fake;
    }
  });

  // The controller only needs these two members of the module.
  const maplibre = { Map: FakeMapClass, Marker: FakeMarkerClass } as unknown as MapLibreModule;
  return { maplibre, maps, markers };
}

function lastOf<T>(items: T[]): T {
  const item = items.at(-1);
  if (item === undefined) throw new Error('expected at least one item');
  return item;
}

function fire(map: FakeMap, name: string, event: unknown = {}) {
  const handler = map.handlers.get(name);
  if (!handler) throw new Error(`no ${name} handler registered`);
  handler(event);
}

function click(map: FakeMap, lat: number, lng: number) {
  fire(map, 'click', { lngLat: { lat, lng } });
}

describe('locationMapController', () => {
  let fake: ReturnType<typeof createFakeMapLibre>;
  let container: HTMLElement;
  let onPick: ReturnType<typeof vi.fn<(lat: number, lng: number) => void>>;

  function create(overrides: Partial<LocationMapControllerOptions> = {}) {
    const controller = createLocationMapController({
      maplibre: fake.maplibre,
      container,
      latitude: 60,
      longitude: 24,
      showMarker: true,
      zoom: 11,
      wheel: 'modifier',
      pinchZoom: false,
      doubleTapZoomKeepsPin: false,
      recenterOnPick: true,
      onPick,
      ...overrides,
    });
    return { controller, map: lastOf(fake.maps) };
  }

  beforeEach(() => {
    fake = createFakeMapLibre();
    container = document.createElement('div');
    document.body.appendChild(container);
    onPick = vi.fn();
  });

  afterEach(() => {
    vi.useRealTimers();
    container.remove();
  });

  describe('placement', () => {
    it('click places the pin rounded to three decimals and reports it', () => {
      const { map } = create({ showMarker: false });

      click(map, 52.123456, 4.987654);

      expect(onPick).toHaveBeenCalledExactlyOnceWith(52.123, 4.988);
      expect(fake.markers).toHaveLength(1);
      expect(lastOf(fake.markers).lngLat).toEqual([4.988, 52.123]);
    });

    it('dragend reports the rounded marker position', () => {
      const { map } = create();
      expect(map.handlers.has('click')).toBe(true);
      const marker = lastOf(fake.markers);
      marker.lngLat = [5.43219, 51.98765];

      const handler = marker.handlers.get('dragend');
      handler?.({});

      expect(onPick).toHaveBeenCalledExactlyOnceWith(51.988, 5.432);
    });

    it('click recentres at the current zoom when recenterOnPick is set', () => {
      const { map } = create({ recenterOnPick: true });
      map.zoom = 14;

      click(map, 52.5, 4.5);

      expect(map.easeTo).toHaveBeenCalledWith({ center: [4.5, 52.5], zoom: 14, duration: 0 });
    });

    it('does not recentre when recenterOnPick is not set', () => {
      const { map } = create({ recenterOnPick: false });

      click(map, 52.5, 4.5);

      expect(map.easeTo).not.toHaveBeenCalled();
      expect(onPick).toHaveBeenCalledWith(52.5, 4.5);
    });

    it('moves the existing pin instead of creating another one', () => {
      const { map } = create({ showMarker: true });

      click(map, 1, 2);
      click(map, 3, 4);

      expect(fake.markers).toHaveLength(1);
      expect(lastOf(fake.markers).lngLat).toEqual([4, 3]);
    });
  });

  describe('showLocation', () => {
    it('creates no marker at start when showMarker is false', () => {
      const { controller } = create({ showMarker: false });

      expect(controller.hasMarker()).toBe(false);
      expect(fake.markers).toHaveLength(0);
    });

    it('creates the marker at start when showMarker is true', () => {
      const { controller } = create({ showMarker: true });

      expect(controller.hasMarker()).toBe(true);
      expect(lastOf(fake.markers).lngLat).toEqual([24, 60]);
    });

    it('never reports a pick', () => {
      const { controller } = create();

      controller.showLocation(61, 25, { createMarker: true, duration: 300 });

      expect(onPick).not.toHaveBeenCalled();
    });

    it('creates a marker only when asked', () => {
      const { controller } = create({ showMarker: false });

      controller.showLocation(61, 25, { createMarker: false, duration: 300 });
      expect(controller.hasMarker()).toBe(false);

      controller.showLocation(61, 25, { createMarker: true, duration: 300 });
      expect(controller.hasMarker()).toBe(true);
      expect(lastOf(fake.markers).lngLat).toEqual([25, 61]);
    });

    it('keeps the current zoom unless one is given', () => {
      const { controller, map } = create();
      map.zoom = 9;

      controller.showLocation(61, 25, { createMarker: true, duration: 300 });
      expect(map.easeTo).toHaveBeenLastCalledWith({ center: [25, 61], zoom: 9, duration: 300 });

      controller.showLocation(61, 25, { createMarker: true, zoom: 12, duration: 0 });
      expect(map.easeTo).toHaveBeenLastCalledWith({ center: [25, 61], zoom: 12, duration: 0 });
    });
  });

  describe('wheel', () => {
    it('modifier wheel zooms only with Ctrl or Cmd', () => {
      const { map } = create({ wheel: 'modifier' });

      const plain = new WheelEvent('wheel', { deltaY: -100, cancelable: true });
      container.dispatchEvent(plain);
      expect(plain.defaultPrevented).toBe(false);
      expect(map.zoomIn).not.toHaveBeenCalled();

      const ctrl = new WheelEvent('wheel', { deltaY: -100, ctrlKey: true, cancelable: true });
      container.dispatchEvent(ctrl);
      expect(ctrl.defaultPrevented).toBe(true);
      expect(map.zoomIn).toHaveBeenCalledWith({ duration: ZOOM_STEP_DURATION_MS });

      const meta = new WheelEvent('wheel', { deltaY: 100, metaKey: true, cancelable: true });
      container.dispatchEvent(meta);
      expect(map.zoomOut).toHaveBeenCalledWith({ duration: ZOOM_STEP_DURATION_MS });
      expect(map.scrollZoom.enable).not.toHaveBeenCalled();
    });

    it('always wheel enables scroll zoom and zooms without a modifier', () => {
      const { map } = create({ wheel: 'always' });

      const event = new WheelEvent('wheel', { deltaY: 100, cancelable: true });
      container.dispatchEvent(event);

      expect(map.scrollZoom.enable).toHaveBeenCalledTimes(1);
      expect(event.defaultPrevented).toBe(true);
      expect(map.zoomOut).toHaveBeenCalledWith({ duration: ZOOM_STEP_DURATION_MS });
    });
  });

  describe('pinch zoom', () => {
    it('stays off by default', () => {
      const { map } = create({ pinchZoom: false });

      expect(map.options.touchZoomRotate).toBe(false);
      expect(map.touchZoomRotate.disableRotation).not.toHaveBeenCalled();
    });

    it('on enables touch zoom and disables rotation', () => {
      const { map } = create({ pinchZoom: true });

      expect(map.options.touchZoomRotate).toBe(true);
      expect(map.touchZoomRotate.disableRotation).toHaveBeenCalledTimes(1);
    });
  });

  describe('map options', () => {
    it('keeps the settings map options and sets no zoom limits', () => {
      const { map } = create({ latitude: 60, longitude: 24, zoom: 7 });

      expect(map.options).toMatchObject({
        container,
        center: [24, 60],
        zoom: 7,
        scrollZoom: false,
        keyboard: true,
        fadeDuration: 0,
        pitchWithRotate: false,
      });
      expect(map.options).not.toHaveProperty('minZoom');
      expect(map.options).not.toHaveProperty('maxZoom');
    });

    it('resizes once the map has loaded', () => {
      const { map } = create();

      fire(map, 'load');

      expect(map.resize).toHaveBeenCalledTimes(1);
    });
  });

  describe('double-tap protection', () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });

    it('without protection a click picks at once', () => {
      const { map } = create({ doubleTapZoomKeepsPin: false });

      click(map, 52, 4);

      expect(onPick).toHaveBeenCalledTimes(1);
    });

    it('with protection a single click picks after the window', () => {
      const { map } = create({ doubleTapZoomKeepsPin: true });

      click(map, 52, 4);
      expect(onPick).not.toHaveBeenCalled();

      vi.advanceTimersByTime(DOUBLE_TAP_WINDOW_MS - 1);
      expect(onPick).not.toHaveBeenCalled();

      vi.advanceTimersByTime(1);
      expect(onPick).toHaveBeenCalledExactlyOnceWith(52, 4);
    });

    it('a second click inside the window places nothing', () => {
      const { map } = create({ doubleTapZoomKeepsPin: true });

      click(map, 52, 4);
      vi.advanceTimersByTime(DOUBLE_TAP_WINDOW_MS / 2);
      click(map, 52, 4);
      vi.advanceTimersByTime(DOUBLE_TAP_WINDOW_MS * 2);

      expect(onPick).not.toHaveBeenCalled();
    });

    it('a click after a cancelled double tap is a new single click', () => {
      const { map } = create({ doubleTapZoomKeepsPin: true });

      click(map, 52, 4);
      click(map, 52, 4);
      click(map, 53, 5);
      vi.advanceTimersByTime(DOUBLE_TAP_WINDOW_MS);

      expect(onPick).toHaveBeenCalledExactlyOnceWith(53, 5);
    });

    it('a user zoom gesture inside the window places nothing', () => {
      const { map } = create({ doubleTapZoomKeepsPin: true });

      click(map, 52, 4);
      fire(map, 'zoomstart', { originalEvent: new MouseEvent('dblclick') });
      vi.advanceTimersByTime(DOUBLE_TAP_WINDOW_MS * 2);

      expect(onPick).not.toHaveBeenCalled();
    });

    it('a programmatic zoom inside the window still places the pin', () => {
      const { map } = create({ doubleTapZoomKeepsPin: true });

      click(map, 52, 4);
      fire(map, 'zoomstart', {});
      vi.advanceTimersByTime(DOUBLE_TAP_WINDOW_MS);

      expect(onPick).toHaveBeenCalledExactlyOnceWith(52, 4);
    });

    it('flushPendingPick places a waiting click at once and only once', () => {
      const { controller, map } = create({ doubleTapZoomKeepsPin: true });

      click(map, 52, 4);
      controller.flushPendingPick();
      vi.advanceTimersByTime(DOUBLE_TAP_WINDOW_MS * 2);

      expect(onPick).toHaveBeenCalledExactlyOnceWith(52, 4);
    });

    it('flushPendingPick does nothing when no click is waiting', () => {
      const { controller } = create({ doubleTapZoomKeepsPin: true });

      controller.flushPendingPick();

      expect(onPick).not.toHaveBeenCalled();
    });
  });

  describe('destroy', () => {
    it('clears a pending pick, removes the wheel listener and the map', () => {
      vi.useFakeTimers();
      const { controller, map } = create({ doubleTapZoomKeepsPin: true, wheel: 'always' });

      click(map, 52, 4);
      controller.destroy();
      vi.advanceTimersByTime(DOUBLE_TAP_WINDOW_MS * 2);

      expect(onPick).not.toHaveBeenCalled();
      expect(map.remove).toHaveBeenCalledTimes(1);

      container.dispatchEvent(new WheelEvent('wheel', { deltaY: 100, cancelable: true }));
      expect(map.zoomOut).not.toHaveBeenCalled();
    });

    it('reports nothing for a click or drag after destroy', () => {
      const { controller, map } = create();
      const marker = lastOf(fake.markers);

      controller.destroy();
      click(map, 52, 4);
      marker.handlers.get('dragend')?.({});

      expect(onPick).not.toHaveBeenCalled();
    });

    it('is safe to call twice', () => {
      const { controller, map } = create();

      controller.destroy();
      controller.destroy();

      expect(map.remove).toHaveBeenCalledTimes(1);
    });
  });

  describe('initialZoom', () => {
    it('region: 5 at 0,0 and 11 when set', () => {
      expect(initialZoom(0, 0, 'region')).toBe(5);
      expect(initialZoom(60, 24, 'region')).toBe(11);
      expect(initialZoom(0, 24, 'region')).toBe(11);
    });

    it('world: 1 at 0,0 and 11 when set', () => {
      expect(initialZoom(0, 0, 'world')).toBe(1);
      expect(initialZoom(60, 24, 'world')).toBe(11);
      expect(initialZoom(0, 24, 'world')).toBe(11);
    });
  });
});
