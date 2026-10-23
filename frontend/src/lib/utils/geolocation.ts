/**
 * Browser geolocation and coordinate helpers shared by the settings page and
 * the location map.
 *
 * `requestBrowserLocation` is callback based on purpose: callers that react to
 * the result in the same task as the browser callback (the "Use browser
 * location" button and its tests) keep their timing.
 */

/** Number of decimals kept for stored station coordinates. */
export const COORDINATE_DECIMAL_PLACES = 3;

/** How long the browser may take to answer a location request. */
export const GEOLOCATION_TIMEOUT_MS = 10_000;

const EARTH_RADIUS_METERS = 6_371_000;
const DEGREES_TO_RADIANS = Math.PI / 180;
const MIN_LATITUDE = -90;
const MAX_LATITUDE = 90;
const MIN_LONGITUDE = -180;
const MAX_LONGITUDE = 180;

/** `GeolocationPositionError.code` values. */
const GEOLOCATION_ERROR_CODES = {
  permissionDenied: 1,
  positionUnavailable: 2,
  timeout: 3,
} as const;

/**
 * Round a coordinate to the precision stored in the configuration.
 *
 * Numerically identical to `parseFloat(value.toFixed(3))` for every number,
 * including `-0` and `NaN`.
 */
export function roundCoordinate(value: number): number {
  return Number(value.toFixed(COORDINATE_DECIMAL_PLACES));
}

/** The browser API, typed as optional because browsers omit it on insecure origins. */
function readGeolocation(): Geolocation | undefined {
  return navigator.geolocation;
}

/** Whether the browser can answer a location request on this page. */
export type BrowserLocationSupport = 'available' | 'insecure' | 'unsupported';

/**
 * Report whether browser geolocation can work here. Browsers may omit the API
 * entirely on insecure origins, so the secure-context problem is reported
 * first when it is known.
 */
export function getBrowserLocationSupport(): BrowserLocationSupport {
  if (typeof window !== 'undefined' && window.isSecureContext === false) {
    return 'insecure';
  }
  if (typeof navigator === 'undefined') {
    return 'unsupported';
  }
  return readGeolocation() ? 'available' : 'unsupported';
}

/** Outcome of `requestBrowserLocation`. */
export type BrowserLocationResult =
  | {
      status: 'success';
      /** Latitude rounded with `roundCoordinate`. */
      latitude: number;
      /** Longitude rounded with `roundCoordinate`. */
      longitude: number;
      /** Reported accuracy plus the rounding displacement, in meters; null when unknown. */
      accuracyMeters: number | null;
    }
  | {
      status:
        'insecure' | 'unsupported' | 'denied' | 'unavailable' | 'timeout' | 'invalid' | 'failed';
      error?: unknown;
    };

function distanceInMeters(
  startLatitude: number,
  startLongitude: number,
  endLatitude: number,
  endLongitude: number
): number {
  const latitudeDelta = (endLatitude - startLatitude) * DEGREES_TO_RADIANS;
  const longitudeDelta = (endLongitude - startLongitude) * DEGREES_TO_RADIANS;
  const startLatitudeRadians = startLatitude * DEGREES_TO_RADIANS;
  const endLatitudeRadians = endLatitude * DEGREES_TO_RADIANS;

  const haversine =
    Math.sin(latitudeDelta / 2) ** 2 +
    Math.cos(startLatitudeRadians) *
      Math.cos(endLatitudeRadians) *
      Math.sin(longitudeDelta / 2) ** 2;
  const centralAngle = 2 * Math.atan2(Math.sqrt(haversine), Math.sqrt(Math.max(0, 1 - haversine)));

  return EARTH_RADIUS_METERS * centralAngle;
}

function effectiveAccuracy(
  accuracy: number,
  detectedLatitude: number,
  detectedLongitude: number,
  roundedLatitude: number,
  roundedLongitude: number
): number | null {
  if (!Number.isFinite(accuracy)) return null;

  const roundingDisplacement = distanceInMeters(
    detectedLatitude,
    detectedLongitude,
    roundedLatitude,
    roundedLongitude
  );

  return Math.ceil(Math.max(0, accuracy) + roundingDisplacement);
}

function isInRange(latitude: number, longitude: number): boolean {
  return (
    Number.isFinite(latitude) &&
    Number.isFinite(longitude) &&
    latitude >= MIN_LATITUDE &&
    latitude <= MAX_LATITUDE &&
    longitude >= MIN_LONGITUDE &&
    longitude <= MAX_LONGITUDE
  );
}

function resultFromPosition(position: GeolocationPosition): BrowserLocationResult {
  const { latitude, longitude, accuracy } = position.coords;

  if (!isInRange(latitude, longitude)) {
    return { status: 'invalid', error: new Error('Browser returned invalid coordinates') };
  }

  const roundedLatitude = roundCoordinate(latitude);
  const roundedLongitude = roundCoordinate(longitude);
  return {
    status: 'success',
    latitude: roundedLatitude,
    longitude: roundedLongitude,
    accuracyMeters: effectiveAccuracy(
      accuracy,
      latitude,
      longitude,
      roundedLatitude,
      roundedLongitude
    ),
  };
}

function resultFromError(error: GeolocationPositionError): BrowserLocationResult {
  switch (error.code) {
    case GEOLOCATION_ERROR_CODES.permissionDenied:
      return { status: 'denied', error };
    case GEOLOCATION_ERROR_CODES.positionUnavailable:
      return { status: 'unavailable', error };
    case GEOLOCATION_ERROR_CODES.timeout:
      return { status: 'timeout', error };
    default:
      return { status: 'failed', error };
  }
}

/**
 * Ask the browser for one position and report the outcome through `onResult`,
 * exactly once. Insecure origins and missing APIs are reported synchronously
 * without calling the browser. The request itself cannot be cancelled, so a
 * caller that no longer wants the answer must ignore it.
 */
export function requestBrowserLocation(onResult: (result: BrowserLocationResult) => void): void {
  const support = getBrowserLocationSupport();
  if (support !== 'available') {
    onResult({ status: support });
    return;
  }

  const state = { reported: false };
  const report = (result: BrowserLocationResult) => {
    if (state.reported) return;
    state.reported = true;
    onResult(result);
  };

  try {
    navigator.geolocation.getCurrentPosition(
      position => report(resultFromPosition(position)),
      error => report(resultFromError(error)),
      {
        enableHighAccuracy: true,
        timeout: GEOLOCATION_TIMEOUT_MS,
        maximumAge: 0,
      }
    );
  } catch (error) {
    // A throw after the result was reported came from the caller's handler,
    // not the browser API; do not report a second result for it.
    if (state.reported) throw error;
    report({ status: 'failed', error });
  }
}
