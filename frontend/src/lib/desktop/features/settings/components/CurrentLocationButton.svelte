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
  import { formatNumber } from '$lib/utils/formatters';
  import {
    getBrowserLocationSupport,
    requestBrowserLocation,
    type BrowserLocationResult,
  } from '$lib/utils/geolocation';
  import SettingsButton from './SettingsButton.svelte';

  interface Props {
    latitude: number;
    longitude: number;
    coordinateIntentVersion?: number;
    onLocation: (_latitude: number, _longitude: number) => void;
    disabled?: boolean;
    /** Hide the label row and the idle help text; the button and accuracy readout stay. */
    compact?: boolean;
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

  let {
    latitude,
    longitude,
    coordinateIntentVersion = 0,
    onLocation,
    disabled = false,
    compact = false,
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

  function showUnexpectedFailure(error?: unknown) {
    if (error !== undefined) {
      logger.error('Browser geolocation failed unexpectedly', error);
    }
    toastActions.error(t('settings.main.sections.rangeFilter.stationLocation.geolocationFailed'));
  }

  function handleResult(result: BrowserLocationResult, requestId: number) {
    if (!isCurrentRequest(requestId)) return;

    switch (result.status) {
      case 'success':
        handleSuccess(result, requestId);
        return;
      case 'denied':
        finishRequest(requestId);
        logger.warn('Browser geolocation permission denied', result.error);
        toastActions.warning(
          t('settings.main.sections.rangeFilter.stationLocation.geolocationDenied')
        );
        return;
      case 'unavailable':
        finishRequest(requestId);
        logger.warn('Browser geolocation position unavailable', result.error);
        toastActions.error(
          t('settings.main.sections.rangeFilter.stationLocation.geolocationUnavailable')
        );
        return;
      case 'timeout':
        finishRequest(requestId);
        logger.warn('Browser geolocation request timed out', result.error);
        toastActions.error(
          t('settings.main.sections.rangeFilter.stationLocation.geolocationTimedOut')
        );
        return;
      default:
        // invalid and failed coordinates, a synchronous API throw, and any
        // other error code.
        finishRequest(requestId);
        showUnexpectedFailure(result.error);
    }
  }

  function handleSuccess(
    result: Extract<BrowserLocationResult, { status: 'success' }>,
    requestId: number
  ) {
    finishRequest(requestId);

    try {
      onLocation(result.latitude, result.longitude);
      detectedPosition = {
        latitude: result.latitude,
        longitude: result.longitude,
        accuracy: result.accuracyMeters,
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
    const support = getBrowserLocationSupport();
    if (support === 'insecure') {
      toastActions.warning(
        t('settings.main.sections.rangeFilter.stationLocation.geolocationRequiresHttps')
      );
      return;
    }
    if (support === 'unsupported') {
      toastActions.error(
        t('settings.main.sections.rangeFilter.stationLocation.geolocationUnsupported')
      );
      return;
    }

    const requestId = ++nextRequestId;
    pendingRequest = { id: requestId, latitude, longitude, coordinateIntentVersion };
    locating = true;
    detectedPosition = null;

    requestBrowserLocation(result => handleResult(result, requestId));
  }

  onDestroy(() => {
    active = false;
    pendingRequest = null;
  });
</script>

<div class="form-control min-w-0">
  {#if !compact}
    <div class="label">
      <span class="label-text">
        {t('settings.main.sections.rangeFilter.stationLocation.automaticLocation')}
      </span>
    </div>
  {/if}

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
      <span class="help-text" role="status" aria-atomic="true">
        {t('settings.main.sections.rangeFilter.stationLocation.accuracy', {
          accuracy: formatNumber(displayedAccuracy),
        })}
      </span>
    {:else if !compact}
      <span class="help-text">
        {t('settings.main.sections.rangeFilter.stationLocation.locationHelp')}
      </span>
    {/if}
  </div>
</div>
