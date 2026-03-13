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

  onMount(() => {
    import('maplibre-gl').then(maplibre => {
      map = new maplibre.Map({
        container: mapContainer,
        style: createMapStyle(),
        center: [longitude, latitude],
        zoom: MAP_CONFIG.DEFAULT_ZOOM,
        interactive: false,
        attributionControl: false,
      });

      new maplibre.Marker().setLngLat([longitude, latitude]).addTo(map);
    });

    return () => map?.remove();
  });
</script>

<div bind:this={mapContainer} class="h-40 w-full overflow-hidden rounded-lg {className}"></div>
