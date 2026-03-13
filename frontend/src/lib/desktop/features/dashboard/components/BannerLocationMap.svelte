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
    className?: string;
  }

  let { latitude, longitude, className = '' }: Props = $props();

  let mapContainer: HTMLDivElement;

  let map: import('maplibre-gl').Map | undefined;
  let marker: import('maplibre-gl').Marker | undefined;

  onMount(() => {
    let mounted = true;

    import('maplibre-gl').then(maplibre => {
      if (!mounted) return;

      map = new maplibre.Map({
        container: mapContainer,
        style: createMapStyle(),
        center: [longitude, latitude],
        zoom: MAP_CONFIG.DEFAULT_ZOOM,
        interactive: false,
        attributionControl: false,
      });

      marker = new maplibre.Marker().setLngLat([longitude, latitude]).addTo(map);
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
</script>

<div bind:this={mapContainer} class="h-40 w-full overflow-hidden rounded-lg {className}"></div>
