<!--
  BannerLocationMap - Small read-only map showing station location.
  Uses MapLibre GL with OpenStreetMap tiles.
  @component
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { MAP_CONFIG, createMapStyle } from '$lib/desktop/features/settings/utils/mapConfig';

  interface Props {
    latitude: number;
    longitude: number;
    zoom?: number;
    showPin?: boolean;
    className?: string;
  }

  let {
    latitude,
    longitude,
    zoom = MAP_CONFIG.DEFAULT_ZOOM,
    showPin = true,
    className = '',
  }: Props = $props();

  let mapContainer: HTMLDivElement;

  let map: import('maplibre-gl').Map | undefined;
  let marker: import('maplibre-gl').Marker | undefined;
  let maplibreModule: typeof import('maplibre-gl') | undefined;

  onMount(() => {
    let mounted = true;

    import('maplibre-gl').then(maplibre => {
      if (!mounted) return;

      maplibreModule = maplibre;

      map = new maplibre.Map({
        container: mapContainer,
        style: createMapStyle(),
        center: [longitude, latitude],
        zoom: zoom,
        interactive: false,
        attributionControl: false,
      });

      if (showPin) {
        marker = new maplibre.Marker().setLngLat([longitude, latitude]).addTo(map);
      }
    });

    return () => {
      mounted = false;
      map?.remove();
    };
  });

  // Reactively update map center, zoom, and marker.
  // Read all reactive deps upfront to ensure proper subscription tracking —
  // conditional reads (e.g. inside `if (map)`) miss subscriptions on first run.
  $effect(() => {
    const lng = longitude;
    const lat = latitude;
    const z = zoom;
    const pin = showPin;

    if (!map) return;

    map.setCenter([lng, lat]);
    map.setZoom(z);

    if (pin && !marker && maplibreModule) {
      marker = new maplibreModule.Marker().setLngLat([lng, lat]).addTo(map);
    } else if (!pin && marker) {
      marker.remove();
      marker = undefined;
    } else if (marker) {
      marker.setLngLat([lng, lat]);
    }
  });
</script>

<div bind:this={mapContainer} class="h-40 w-full overflow-hidden rounded-lg {className}"></div>
