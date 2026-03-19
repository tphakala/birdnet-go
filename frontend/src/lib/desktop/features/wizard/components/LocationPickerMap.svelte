<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import { createMapStyle, MAP_CONFIG } from '$lib/desktop/features/settings/utils/mapConfig';
  import { getLogger } from '$lib/utils/logger';

  const logger = getLogger('LocationPickerMap');

  interface Props {
    latitude: number;
    longitude: number;
    onLocationChange: (_lat: number, _lon: number) => void;
  }

  let { latitude, longitude, onLocationChange }: Props = $props();

  let mapContainer = $state<HTMLDivElement>();
  // Use $state.raw to avoid deep proxying of complex 3rd-party classes (WebGL context, etc.)
  let map = $state.raw<import('maplibre-gl').Map | undefined>(undefined);
  let marker = $state.raw<import('maplibre-gl').Marker | undefined>(undefined);
  let mapLoadFailed = $state(false);

  const PICKER_WORLD_ZOOM = 3;

  function getZoom(): number {
    return latitude !== 0 || longitude !== 0 ? MAP_CONFIG.DEFAULT_ZOOM : PICKER_WORLD_ZOOM;
  }

  onMount(() => {
    if (!mapContainer) return;

    let mounted = true;

    Promise.all([import('maplibre-gl'), import('maplibre-gl/dist/maplibre-gl.css')])
      .then(([maplibregl]) => {
        if (!mounted || !mapContainer) return;

        const mapInstance = new maplibregl.Map({
          container: mapContainer,
          style: createMapStyle(),
          center: [longitude, latitude],
          zoom: getZoom(),
          minZoom: MAP_CONFIG.MIN_ZOOM,
          maxZoom: MAP_CONFIG.MAX_ZOOM,
          pitchWithRotate: MAP_CONFIG.PITCH_WITH_ROTATE,
          touchZoomRotate: MAP_CONFIG.TOUCH_ZOOM_ROTATE,
          fadeDuration: MAP_CONFIG.FADE_DURATION,
        });

        const markerInstance = new maplibregl.Marker({ draggable: true })
          .setLngLat([longitude, latitude])
          .addTo(mapInstance);

        markerInstance.on('dragend', () => {
          const lngLat = markerInstance.getLngLat();
          onLocationChange(
            Math.round(lngLat.lat * 10000) / 10000,
            Math.round(lngLat.lng * 10000) / 10000
          );
        });

        mapInstance.on('click', (e: import('maplibre-gl').MapMouseEvent) => {
          const { lat, lng } = e.lngLat;
          const roundedLat = Math.round(lat * 10000) / 10000;
          const roundedLng = Math.round(lng * 10000) / 10000;
          markerInstance.setLngLat([roundedLng, roundedLat]);
          onLocationChange(roundedLat, roundedLng);
        });

        map = mapInstance;
        marker = markerInstance;
      })
      .catch(err => {
        logger.error('Failed to load MapLibre', err);
        mapLoadFailed = true;
      });

    return () => {
      mounted = false;
      map?.remove();
      map = undefined;
      marker = undefined;
    };
  });

  $effect(() => {
    if (!map || !marker) return;
    const currentPos = marker.getLngLat();
    if (
      Math.abs(currentPos.lat - latitude) > 0.0001 ||
      Math.abs(currentPos.lng - longitude) > 0.0001
    ) {
      marker.setLngLat([longitude, latitude]);
      map.flyTo({
        center: [longitude, latitude],
        zoom: getZoom(),
        duration: 500,
      });
    }
  });
</script>

{#if mapLoadFailed}
  <div
    role="alert"
    class="flex h-48 items-center justify-center rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] p-3 text-sm text-[var(--color-base-content)] opacity-60"
  >
    {t('wizard.steps.locationLanguage.locationError')}
  </div>
{:else}
  <div
    bind:this={mapContainer}
    class="h-48 w-full overflow-hidden rounded-lg border border-[var(--border-200)]"
  ></div>
{/if}
