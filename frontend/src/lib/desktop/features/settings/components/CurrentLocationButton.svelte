<!--
  Current Location Button

  Requests a single position from the browser and reports it to the parent
  settings form. The parent remains responsible for persisting coordinates.
-->
<script lang="ts">
  import { MapPin } from '@lucide/svelte';
  import { onDestroy } from 'svelte';
  import { t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { loggers } from '$lib/utils/logger';
  import SettingsButton from './SettingsButton.svelte';

  interface Props {
    latitude: number;
    longitude: number;
    coordinateIntentVersion?: number;
    onLocation: (_latitude: number, _longitude: number) => void;
    disabled?: boolean;
  }

  interface DetectedPosition {
    latitude: number;
    longitude: number;
    accuracy: number | null;
    coordinateIntentVersion: number;
  }

  interface PendingRequest {
    id: number;
    latitude: number;
    longitude: number;
    coordinateIntentVersion: number;
  }

  interface BrowserGeolocationPosition {
    coords: {
      latitude: number;
      longitude: number;
      accuracy: number;
    };
  }

  interface BrowserGeolocationError {
    code: number;
    message: string;
  }

  const COORDINATE_DECIMAL_PLACES = 3;
  const GEOLOCATION_TIMEOUT_MS = 10_000;
  const EARTH_RADIUS_METERS = 6_371_000;
  const DEGREES_TO_RADIANS = Math.PI / 180;
  const GEOLOCATION_ERRORS = {
    permissionDenied: 1,
    positionUnavailable: 2,
    timeout: 3,
  } as const;

  let {
    latitude,
    longitude,
    coordinateIntentVersion = 0,
    onLocation,
    disabled = false,
  }: Props = $props();

  const logger = loggers.settings;
  let active = true;
  let nextRequestId = 0;
  let locating = $state(false);
  let pendingRequest = $state<PendingRequest | null>(null);
  let detectedPosition = $state<DetectedPosition | null>(null);
  let displayedAccuracy = $derived(
    detectedPosition &&
      detectedPosition.latitude === latitude &&
      detectedPosition.longitude === longitude &&
      detectedPosition.coordinateIntentVersion === coordinateIntentVersion
      ? detectedPosition.accuracy
      : null
  );

  // A manual/map/reset action or a save that starts after the request is newer
  // user intent. The browser request cannot be cancelled, so invalidate it
  // locally and ignore its eventual callbacks.
  $effect(() => {
    const request = pendingRequest;
    if (
      request &&
      (disabled ||
        latitude !== request.latitude ||
        longitude !== request.longitude ||
        coordinateIntentVersion !== request.coordinateIntentVersion)
    ) {
      pendingRequest = null;
      locating = false;
    }
  });

  function isCurrentRequest(requestId: number): boolean {
    return active && pendingRequest?.id === requestId;
  }

  function finishRequest(requestId: number): boolean {
    if (!isCurrentRequest(requestId)) return false;

    pendingRequest = null;
    locating = false;
    return true;
  }

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
    const centralAngle =
      2 * Math.atan2(Math.sqrt(haversine), Math.sqrt(Math.max(0, 1 - haversine)));

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

  function showUnexpectedFailure(error?: unknown) {
    if (error !== undefined) {
      logger.error('Browser geolocation failed unexpectedly', error);
    }
    toastActions.error(t('settings.main.sections.rangeFilter.stationLocation.geolocationFailed'));
  }

  function handleGeolocationError(error: BrowserGeolocationError, requestId: number) {
    if (!finishRequest(requestId)) return;

    switch (error.code) {
      case GEOLOCATION_ERRORS.permissionDenied:
        logger.warn('Browser geolocation permission denied', error);
        toastActions.warning(
          t('settings.main.sections.rangeFilter.stationLocation.geolocationDenied')
        );
        break;
      case GEOLOCATION_ERRORS.positionUnavailable:
        logger.warn('Browser geolocation position unavailable', error);
        toastActions.error(
          t('settings.main.sections.rangeFilter.stationLocation.geolocationUnavailable')
        );
        break;
      case GEOLOCATION_ERRORS.timeout:
        logger.warn('Browser geolocation request timed out', error);
        toastActions.error(
          t('settings.main.sections.rangeFilter.stationLocation.geolocationTimedOut')
        );
        break;
      default:
        showUnexpectedFailure(error);
    }
  }

  function handleGeolocationSuccess(position: BrowserGeolocationPosition, requestId: number) {
    if (!isCurrentRequest(requestId)) return;

    const { latitude: detectedLatitude, longitude: detectedLongitude, accuracy } = position.coords;

    if (
      !Number.isFinite(detectedLatitude) ||
      !Number.isFinite(detectedLongitude) ||
      detectedLatitude < -90 ||
      detectedLatitude > 90 ||
      detectedLongitude < -180 ||
      detectedLongitude > 180
    ) {
      finishRequest(requestId);
      showUnexpectedFailure(new Error('Browser returned invalid coordinates'));
      return;
    }

    const roundedLatitude = Number(detectedLatitude.toFixed(COORDINATE_DECIMAL_PLACES));
    const roundedLongitude = Number(detectedLongitude.toFixed(COORDINATE_DECIMAL_PLACES));
    const roundedAccuracy = effectiveAccuracy(
      accuracy,
      detectedLatitude,
      detectedLongitude,
      roundedLatitude,
      roundedLongitude
    );

    if (!finishRequest(requestId)) return;

    try {
      onLocation(roundedLatitude, roundedLongitude);
      detectedPosition = {
        latitude: roundedLatitude,
        longitude: roundedLongitude,
        accuracy: roundedAccuracy,
        coordinateIntentVersion,
      };
      toastActions.success(
        t('settings.main.sections.rangeFilter.stationLocation.locationDetected')
      );
    } catch (error) {
      showUnexpectedFailure(error);
    }
  }

  function useCurrentLocation() {
    // Browsers may omit the API entirely on insecure origins, so report the
    // secure-context problem first when it is known.
    if (typeof window !== 'undefined' && window.isSecureContext === false) {
      toastActions.warning(
        t('settings.main.sections.rangeFilter.stationLocation.geolocationRequiresHttps')
      );
      return;
    }

    if (typeof navigator === 'undefined' || !navigator.geolocation) {
      toastActions.error(
        t('settings.main.sections.rangeFilter.stationLocation.geolocationUnsupported')
      );
      return;
    }

    const requestId = ++nextRequestId;
    pendingRequest = { id: requestId, latitude, longitude, coordinateIntentVersion };
    locating = true;
    detectedPosition = null;

    try {
      navigator.geolocation.getCurrentPosition(
        position => handleGeolocationSuccess(position, requestId),
        error => handleGeolocationError(error, requestId),
        {
          enableHighAccuracy: true,
          timeout: GEOLOCATION_TIMEOUT_MS,
          maximumAge: 0,
        }
      );
    } catch (error) {
      if (finishRequest(requestId)) {
        showUnexpectedFailure(error);
      }
    }
  }

  onDestroy(() => {
    active = false;
    pendingRequest = null;
  });
</script>

<div class="form-control min-w-0">
  <div class="label">
    <span class="label-text">
      {t('settings.main.sections.rangeFilter.stationLocation.automaticLocation')}
    </span>
  </div>

  <div class="flex flex-wrap items-center gap-x-3 gap-y-1 xl:flex-col xl:items-start">
    <SettingsButton
      onclick={useCurrentLocation}
      {disabled}
      loading={locating}
      loadingText={t('settings.main.sections.rangeFilter.stationLocation.locating')}
    >
      <MapPin class="size-4" aria-hidden="true" />
      {t('settings.main.sections.rangeFilter.stationLocation.useCurrentLocation')}
    </SettingsButton>

    {#if displayedAccuracy !== null}
      <span class="help-text" role="status" aria-live="polite" aria-atomic="true">
        {t('settings.main.sections.rangeFilter.stationLocation.accuracy', {
          accuracy: displayedAccuracy,
        })}
      </span>
    {:else}
      <span class="help-text">
        {t('settings.main.sections.rangeFilter.stationLocation.locationHelp')}
      </span>
    {/if}
  </div>
</div>
