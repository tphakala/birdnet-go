/**
 * Location map controller
 *
 * Owns one MapLibre map used to pick a station location: map options, the
 * draggable pin, click and drag placement, wheel handling, pinch zoom, double
 * tap protection and teardown. `LocationMap.svelte` creates one controller for
 * the inline map and one for the expanded view.
 *
 * The controller reports a coordinate only for a user action (click, drag end).
 * `showLocation` moves the map and pin without reporting anything.
 */

import type { Map as MapLibreMap, MapMouseEvent, Marker } from 'maplibre-gl';
import {
  MAP_CONFIG,
  createMapStyle,
  getInitialZoom,
} from '$lib/desktop/features/settings/utils/mapConfig';
import { roundCoordinate } from '$lib/utils/geolocation';

/** Delay before a typed coordinate moves the map. */
export const COORDINATE_SYNC_DEBOUNCE_MS = 500;
/** Animation time when typed coordinates move the map. */
export const COORDINATE_SYNC_DURATION_MS = 300;
/** Animation time of the zoom buttons and modifier-wheel zoom. */
export const ZOOM_STEP_DURATION_MS = 300;
/**
 * A second click inside this window is a double click or double tap. Pin
 * placement waits this long when double-tap protection is on.
 */
export const DOUBLE_TAP_WINDOW_MS = 300;
/** Zoom of the world overview the `world` start view uses when the coordinates are 0,0. */
export const WORLD_OVERVIEW_ZOOM = 1;
/** Stacking order of the expanded map dialog, above the settings page chrome. */
export const Z_INDEX_LOCATION_MAP_DIALOG = 9999;

const LONGITUDE_MIN = -180;
const LONGITUDE_MAX = 180;
const LONGITUDE_PERIOD = LONGITUDE_MAX - LONGITUDE_MIN;

/**
 * Bring a longitude from a repeated copy of the world map back into
 * -180..180, the way MapLibre's `LngLat.wrap` does (-180 becomes 180).
 */
function wrapLongitude(longitude: number): number {
  const wrapped =
    ((((longitude - LONGITUDE_MIN) % LONGITUDE_PERIOD) + LONGITUDE_PERIOD) % LONGITUDE_PERIOD) +
    LONGITUDE_MIN;
  return wrapped === LONGITUDE_MIN ? LONGITUDE_MAX : wrapped;
}

/** The loaded MapLibre module. */
export type MapLibreModule = typeof import('maplibre-gl');

/**
 * Which view the inline map is created in when the coordinates are not set
 * (0,0): `region` is the settings map (zoom 5), `world` shows the whole world
 * (zoom 1). With set coordinates both start at the default zoom. The expanded
 * map starts at the inline map's current zoom instead.
 */
export type LocationMapStartView = 'region' | 'world';

/** Zoom to open a map at for the given coordinates. */
export function initialZoom(
  latitude: number,
  longitude: number,
  startView: LocationMapStartView
): number {
  if (startView === 'region') {
    return getInitialZoom(latitude, longitude);
  }
  return latitude !== 0 || longitude !== 0 ? MAP_CONFIG.DEFAULT_ZOOM : WORLD_OVERVIEW_ZOOM;
}

/** Options for `createLocationMapController`. */
export interface LocationMapControllerOptions {
  maplibre: MapLibreModule;
  container: HTMLElement;
  latitude: number;
  longitude: number;
  /** Create the pin at construction. */
  showMarker: boolean;
  zoom: number;
  /**
   * `modifier` zooms only with Ctrl or Cmd held (the page keeps scrolling
   * otherwise); `always` leaves every wheel event to MapLibre's scroll zoom.
   */
  wheel: 'modifier' | 'always';
  /** Enable two-finger pinch zoom (rotation stays off). */
  pinchZoom: boolean;
  /**
   * Delay placement by `DOUBLE_TAP_WINDOW_MS` and drop it when a double click,
   * double tap or user zoom gesture follows.
   */
  doubleTapZoomKeepsPin: boolean;
  /** Recentre the map on the placed pin at the current zoom. */
  recenterOnPick: boolean;
  /** Called with the rounded coordinates after a click or drag end. */
  onPick: (latitude: number, longitude: number) => void;
}

/** Options for `LocationMapController.showLocation`. */
export interface ShowLocationOptions {
  /** Create the pin when there is none yet. */
  createMarker: boolean;
  /** Target zoom; defaults to the current zoom. */
  zoom?: number;
  duration: number;
}

/** Handle on one location map. */
export interface LocationMapController {
  readonly map: MapLibreMap;
  hasMarker(): boolean;
  /** Move the map and pin. Never reports a pick. */
  showLocation(latitude: number, longitude: number, options: ShowLocationOptions): void;
  /** Place a click that is still waiting out the double-tap window, now. */
  flushPendingPick(): void;
  zoomIn(): void;
  zoomOut(): void;
  getZoom(): number;
  /** Drop a pending pick, remove listeners and the map. Safe to call twice. */
  destroy(): void;
}

interface PendingPick {
  latitude: number;
  longitude: number;
  timer: ReturnType<typeof setTimeout>;
}

/** Create a map in `container` and wire the placement behaviour. */
export function createLocationMapController(
  options: LocationMapControllerOptions
): LocationMapController {
  const {
    maplibre,
    container,
    latitude,
    longitude,
    wheel,
    pinchZoom,
    doubleTapZoomKeepsPin,
    recenterOnPick,
    onPick,
  } = options;

  const map = new maplibre.Map({
    container,
    style: createMapStyle(),
    center: [longitude, latitude],
    zoom: options.zoom,
    scrollZoom: MAP_CONFIG.SCROLL_ZOOM,
    keyboard: MAP_CONFIG.KEYBOARD_NAV,
    fadeDuration: MAP_CONFIG.FADE_DURATION,
    pitchWithRotate: MAP_CONFIG.PITCH_WITH_ROTATE,
    touchZoomRotate: pinchZoom ? true : MAP_CONFIG.TOUCH_ZOOM_ROTATE,
  });

  let destroyed = false;
  let marker: Marker | null = null;
  let pendingPick: PendingPick | null = null;

  if (pinchZoom) {
    map.touchZoomRotate.disableRotation();
  }

  map.on('load', () => {
    map.resize();
  });

  function zoomBy(deltaY: number) {
    if (deltaY > 0) {
      map.zoomOut({ duration: ZOOM_STEP_DURATION_MS });
    } else {
      map.zoomIn({ duration: ZOOM_STEP_DURATION_MS });
    }
  }

  // `always` hands every wheel event to MapLibre's own scroll zoom; adding the
  // custom step as well would zoom twice per tick. `modifier` keeps scroll zoom
  // off and zooms in steps only while Ctrl or Cmd is held.
  const handleWheel = (event: WheelEvent) => {
    if (!(event.ctrlKey || event.metaKey)) return;
    event.preventDefault();
    zoomBy(event.deltaY);
  };
  if (wheel === 'always') {
    map.scrollZoom.enable();
  } else {
    container.addEventListener('wheel', handleWheel, false);
  }

  function removeMarker() {
    marker?.remove();
    marker = null;
  }

  function placeMarker(lat: number, lng: number) {
    if (marker) {
      marker.setLngLat([lng, lat]);
      return;
    }
    const created = new maplibre.Marker({ draggable: true }).setLngLat([lng, lat]).addTo(map);
    created.on('dragend', () => {
      const position = created.getLngLat();
      pick(position.lat, position.lng);
    });
    marker = created;
  }

  function pick(lat: number, lng: number) {
    if (destroyed) return;
    const roundedLatitude = roundCoordinate(lat);
    let roundedLongitude = roundCoordinate(wrapLongitude(lng));
    // Rounding can land just east of the antimeridian on -180 again.
    if (roundedLongitude === LONGITUDE_MIN) roundedLongitude = LONGITUDE_MAX;

    placeMarker(roundedLatitude, roundedLongitude);
    if (recenterOnPick) {
      map.easeTo({
        center: [roundedLongitude, roundedLatitude],
        zoom: map.getZoom(),
        duration: MAP_CONFIG.ANIMATION_DURATION,
      });
    }
    onPick(roundedLatitude, roundedLongitude);
  }

  function cancelPendingPick() {
    if (!pendingPick) return;
    clearTimeout(pendingPick.timer);
    pendingPick = null;
  }

  function flushPendingPick() {
    const pending = pendingPick;
    if (!pending) return;
    cancelPendingPick();
    pick(pending.latitude, pending.longitude);
  }

  if (options.showMarker) {
    placeMarker(latitude, longitude);
  }

  map.on('click', (event: MapMouseEvent) => {
    const { lat, lng } = event.lngLat;
    if (!doubleTapZoomKeepsPin) {
      pick(lat, lng);
      return;
    }
    if (pendingPick) {
      // Second click inside the window: a double click or double tap.
      cancelPendingPick();
      return;
    }
    pendingPick = {
      latitude: lat,
      longitude: lng,
      timer: setTimeout(flushPendingPick, DOUBLE_TAP_WINDOW_MS),
    };
  });

  if (doubleTapZoomKeepsPin) {
    // A zoom with an originalEvent comes from a user gesture (double click,
    // tap zoom, pinch, keyboard); the buttons zoom programmatically.
    map.on('zoomstart', (event: { originalEvent?: unknown }) => {
      if (event.originalEvent) {
        cancelPendingPick();
      }
    });
  }

  return {
    map,
    hasMarker: () => marker !== null,
    showLocation(lat, lng, { createMarker, zoom, duration }) {
      if (destroyed) return;
      map.easeTo({ center: [lng, lat], zoom: zoom ?? map.getZoom(), duration });
      if (createMarker) {
        placeMarker(lat, lng);
      } else {
        removeMarker();
      }
    },
    flushPendingPick,
    zoomIn: () => map.zoomIn({ duration: ZOOM_STEP_DURATION_MS }),
    zoomOut: () => map.zoomOut({ duration: ZOOM_STEP_DURATION_MS }),
    getZoom: () => map.getZoom(),
    destroy() {
      if (destroyed) return;
      cancelPendingPick();
      destroyed = true;
      container.removeEventListener('wheel', handleWheel, false);
      removeMarker();
      map.remove();
    },
  };
}
