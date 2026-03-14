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

  // Reactively update map center and marker when coordinates change
  $effect(() => {
    if (map && marker) {
      map.setCenter([longitude, latitude]);
      marker.setLngLat([longitude, latitude]);
    }
  });

  // Reactively update zoom when prop changes
  $effect(() => {
    if (map) {
      map.setZoom(zoom);
    }
  });

  // Reactively add/remove marker when showPin changes
  $effect(() => {
    if (!map || !maplibreModule) return;

    if (showPin && !marker) {
      marker = new maplibreModule.Marker().setLngLat([longitude, latitude]).addTo(map);
    } else if (!showPin && marker) {
      marker.remove();
      marker = undefined;
    }
  });
</script>

<div bind:this={mapContainer} class="h-40 w-full overflow-hidden rounded-lg {className}"></div>
