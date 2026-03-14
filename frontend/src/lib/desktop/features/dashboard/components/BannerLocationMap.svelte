<!--
  BannerLocationMap - Small read-only map showing station location.
  Uses MapLibre GL with OpenStreetMap tiles.
  Supports optional expand-to-fullscreen with interactive map.
  @component
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { Maximize2, X } from '@lucide/svelte';
  import { MAP_CONFIG, createMapStyle } from '$lib/desktop/features/settings/utils/mapConfig';

  interface Props {
    latitude: number;
    longitude: number;
    zoom?: number;
    showPin?: boolean;
    expandable?: boolean;
    className?: string;
  }

  let {
    latitude,
    longitude,
    zoom = MAP_CONFIG.DEFAULT_ZOOM,
    showPin = true,
    expandable = true,
    className = '',
  }: Props = $props();

  let mapContainer: HTMLDivElement;

  let map: import('maplibre-gl').Map | undefined;
  let marker: import('maplibre-gl').Marker | undefined;
  let maplibreModule: typeof import('maplibre-gl') | undefined;

  let expanded = $state(false);
  let expandedMapContainer: HTMLDivElement | undefined = $state();
  let expandedMap: import('maplibre-gl').Map | undefined;
  let expandedMarker: import('maplibre-gl').Marker | undefined;

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

  // Initialize expanded map when overlay opens.
  $effect(() => {
    if (!expanded || !expandedMapContainer || !maplibreModule) return;

    expandedMap = new maplibreModule.Map({
      container: expandedMapContainer,
      style: createMapStyle(),
      center: [longitude, latitude],
      zoom: zoom,
      interactive: true,
      attributionControl: {},
      scrollZoom: true,
    });

    if (showPin) {
      expandedMarker = new maplibreModule.Marker()
        .setLngLat([longitude, latitude])
        .addTo(expandedMap);
    }

    return () => {
      expandedMarker?.remove();
      expandedMarker = undefined;
      expandedMap?.remove();
      expandedMap = undefined;
    };
  });

  // Close on Escape key.
  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      expanded = false;
    }
  }
</script>

<svelte:window onkeydown={expanded ? handleKeydown : undefined} />

<div class="relative">
  <div bind:this={mapContainer} class="h-40 w-full overflow-hidden rounded-lg {className}"></div>

  {#if expandable}
    <button
      onclick={() => (expanded = true)}
      class="absolute right-2 top-2 rounded-md bg-[var(--color-base-100)]/70 p-1.5 text-[var(--color-base-content)]/70 backdrop-blur-sm transition-colors hover:bg-[var(--color-base-100)] hover:text-[var(--color-base-content)]"
      aria-label="Expand map"
    >
      <Maximize2 class="size-4" />
    </button>
  {/if}
</div>

{#if expanded}
  <!-- Fullscreen map overlay -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div
    class="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 backdrop-blur-sm"
    onclick={e => {
      if (e.target === e.currentTarget) expanded = false;
    }}
    role="dialog"
    aria-modal="true"
    aria-label="Expanded location map"
    tabindex="-1"
  >
    <div class="relative h-[80vh] w-[80vw] overflow-hidden rounded-2xl shadow-2xl">
      <div bind:this={expandedMapContainer} class="h-full w-full"></div>

      <button
        onclick={() => (expanded = false)}
        class="absolute right-3 top-3 rounded-full bg-[var(--color-base-100)]/80 p-2 text-[var(--color-base-content)] shadow-md backdrop-blur-sm transition-colors hover:bg-[var(--color-base-100)]"
        aria-label="Close expanded map"
      >
        <X class="size-5" />
      </button>
    </div>
  </div>
{/if}
