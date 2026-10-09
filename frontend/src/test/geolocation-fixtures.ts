/**
 * Browser geolocation fixtures shared by the geolocation helper, CurrentLocationButton
 * and the pages that use it.
 */

/** Overrides window.isSecureContext; undo with clearGeolocationGlobals(). */
export function setSecureContext(value: boolean): void {
  Object.defineProperty(window, 'isSecureContext', { configurable: true, value });
}

/** Overrides navigator.geolocation; undo with clearGeolocationGlobals(). */
export function setGeolocation(value: Geolocation | undefined): void {
  Object.defineProperty(navigator, 'geolocation', { configurable: true, value });
}

/** Removes the overrides set by setSecureContext and setGeolocation. */
export function clearGeolocationGlobals(): void {
  Reflect.deleteProperty(navigator, 'geolocation');
  Reflect.deleteProperty(window, 'isSecureContext');
}

/** A position as the browser reports it. */
export function createPosition(
  latitude: number,
  longitude: number,
  accuracy = 10
): GeolocationPosition {
  return {
    coords: {
      latitude,
      longitude,
      accuracy,
      altitude: null,
      altitudeAccuracy: null,
      heading: null,
      speed: null,
      toJSON: () => ({}),
    },
    timestamp: Date.now(),
    toJSON: () => ({}),
  };
}

/** A position error with the given code (1 denied, 2 unavailable, 3 timeout). */
export function createPositionError(code: number): GeolocationPositionError {
  return {
    code,
    message: 'Test geolocation failure',
    PERMISSION_DENIED: 1,
    POSITION_UNAVAILABLE: 2,
    TIMEOUT: 3,
  };
}
