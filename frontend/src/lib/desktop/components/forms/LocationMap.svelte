<!--
  LocationMap Component

  Purpose: Pick a station location on an OpenStreetMap map. Shared by the
  settings page and the setup wizard.

  Features:
  - Click the map or drag the pin to set the location (3 decimals)
  - Typed or detected coordinates move the map after a short delay, keeping the zoom
  - Zoom buttons, Ctrl/Cmd + wheel zoom and keyboard navigation
  - Optional place search (the `placeSearch` prop) above the map and in the expanded
    dialog: choosing a place sets the location like a map click and zooms in
  - Expand button opening a full screen map in a dialog that is portalled into
    the surrounding dialog (or the page body) and handles Escape and Tab itself
  - MapLibre is loaded on first use, with a loading overlay and an error placeholder

  Only a user action (click, drag end, choosing a searched place) calls
  `onLocationChange`. Mounting, changing the coordinate props and expanding the
  map never do.

  Props: see the Props interface; the defaults reproduce the settings page map.
  - latitude, longitude: current coordinates
  - locationSet: whether the coordinates are a real location (pin shown)
  - onLocationChange: called with the rounded coordinates after a user action
  - title: heading of the expanded dialog
  - ready: the map is built only once this is true
  - mapClass: height classes of the map
  - controls: zoom and expand buttons `below` the map or `overlay`ed in its corner
  - pinchZoom: allow two-finger pinch zoom (rotation stays off)
  - doubleTapZoomKeepsPin: placing the pin waits for a possible double click or
    double tap, which then zooms without moving the pin
  - placeSearch: show a place search box (PlaceSearch) above the map
  - startView: the start zoom is chosen when the inline map is created and depends on whether the coordinates are set (not 0,0): 11 when set, else 5 for `region` or 1 for `world`; the expanded map starts at the inline map's current zoom

  @component
-->
<script lang="ts" module>
  import type { MapLibreModule } from './locationMapController';

  // MapLibre is loaded once and reused by later mounts (switching tabs and
  // back should not show the loading overlay again).
  let loadedMapLibre: MapLibreModule | null = null;
</script>

<script lang="ts">
  import { onDestroy, tick, untrack } from 'svelte';
  import { Maximize2, MapPin, X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { createDebounce } from '$lib/utils/debounce';
  import { generateId } from '$lib/utils/uuid';
  import { roundCoordinate } from '$lib/utils/geolocation';
  import type { PlaceResult } from '$lib/utils/placeSearch';
  import { loggers } from '$lib/utils/logger';
  import { portal } from '$lib/utils/portal';
  import { toastActions } from '$lib/stores/toast';
  import { MAP_CONFIG } from '$lib/desktop/features/settings/utils/mapConfig';
  import PlaceSearch from './PlaceSearch.svelte';
  import {
    COORDINATE_SYNC_DEBOUNCE_MS,
    COORDINATE_SYNC_DURATION_MS,
    Z_INDEX_LOCATION_MAP_DIALOG,
    createLocationMapController,
    initialZoom,
    type LocationMapController,
    type LocationMapStartView,
  } from './locationMapController';

  interface Props {
    latitude: number;
    longitude: number;
    /** The coordinates are a real location, so the pin is shown. */
    locationSet: boolean;
    /** Called with the rounded coordinates after a click, a pin drag or a chosen place. */
    onLocationChange: (_latitude: number, _longitude: number) => void;
    /** Heading of the expanded map dialog. */
    title: string;
    /** The map is constructed only once this is true. */
    ready?: boolean;
    mapClass?: string;
    controls?: 'below' | 'overlay';
    pinchZoom?: boolean;
    doubleTapZoomKeepsPin?: boolean;
    startView?: LocationMapStartView;
    /** Show a place search above the map and in the expanded dialog. */
    placeSearch?: boolean;
    className?: string;
  }

  let {
    latitude,
    longitude,
    locationSet,
    onLocationChange,
    title,
    ready = true,
    mapClass = 'h-[350px]',
    controls = 'below',
    pinchZoom = false,
    doubleTapZoomKeepsPin = false,
    startView = 'region',
    placeSearch = false,
    className = '',
  }: Props = $props();

  const logger = loggers.ui;
  const titleId = generateId('location-map-title');
  const FOCUSABLE_SELECTOR =
    'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

  const roundButtonClass =
    'inline-flex items-center justify-center w-8 h-8 text-sm font-medium rounded-full hover:opacity-80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed';

  let mapElement: HTMLElement | undefined = $state();
  let expandButton: HTMLButtonElement | undefined = $state();
  let inline: LocationMapController | null = $state.raw(null);
  let expandedController: LocationMapController | null = $state.raw(null);
  let libraryLoading = $state(false);
  // Sticks after a hard failure (import error, MapLibre constructor throw) so
  // the init effect does not retry on every reactive cycle. A remount (for
  // example switching settings tabs away and back) starts fresh.
  let loadError = $state(false);
  let destroyed = false;

  let expanded = $state(false);
  let expandedMapElement: HTMLElement | undefined = $state();
  let dialogRoot: HTMLElement | undefined = $state();
  let dialogBox: HTMLElement | undefined = $state();
  let closeButton: HTMLButtonElement | undefined = $state();
  let portalTarget: HTMLElement = $state.raw(document.body);
  // A press that starts on the map and ends on the backdrop still fires click on
  // the backdrop (the common ancestor), so only a press that began there closes.
  let backdropPressStarted = false;

  async function initializeMap(container: HTMLElement) {
    // The effect waits for the bound container, but keep a local check so a
    // detached node never reaches MapLibre (its container lookup would throw).
    if (!container.isConnected) {
      logger.warn('LocationMap init called without a connected container');
      return;
    }

    try {
      if (!loadedMapLibre) {
        libraryLoading = true;
        try {
          const [maplibreModule] = await Promise.all([
            import('maplibre-gl'),
            import('maplibre-gl/dist/maplibre-gl.css'),
          ]);
          loadedMapLibre = maplibreModule;
        } catch (importError) {
          // Nobody is left to tell once the component is gone.
          if (destroyed) return;
          libraryLoading = false;
          logger.error('Failed to load MapLibre GL JS:', importError);
          toastActions.error(t('settings.main.errors.mapLibraryLoadFailed'));
          loadError = true;
          return;
        }
        if (destroyed) return;
        libraryLoading = false;

        // The user may have left while MapLibre was loading, which removes
        // the container.
        if (!container.isConnected) {
          logger.warn('LocationMap init aborted: container detached during import');
          return;
        }
      }

      inline = createLocationMapController({
        maplibre: loadedMapLibre,
        container,
        latitude,
        longitude,
        showMarker: locationSet,
        zoom: initialZoom(latitude, longitude, startView),
        wheel: 'modifier',
        pinchZoom,
        doubleTapZoomKeepsPin,
        recenterOnPick: true,
        onPick: (lat, lng) => handlePick('inline', lat, lng),
      });
    } catch (error) {
      if (destroyed) return;
      logger.error('Failed to initialize map:', error);
      toastActions.error(t('settings.main.errors.mapLoadFailed'));
      loadError = true;
    }
  }

  // Build the map once the container exists and the page says it is ready.
  $effect(() => {
    if (!ready || inline || loadError || libraryLoading) return;
    const container = mapElement;
    if (!container) return;
    untrack(() => void initializeMap(container));
  });

  // Typed or detected coordinates move the map after a short pause and keep
  // the zoom. Skipped while the expanded map is open (it is showing the
  // location); the effect re-runs when it closes.
  const syncDebounce = createDebounce(
    (controller: LocationMapController, lat: number, lng: number, set: boolean) => {
      if (!Number.isFinite(lat) || !Number.isFinite(lng)) return;

      let zoom: number | undefined;
      if (startView === 'world' && set && !controller.hasMarker()) {
        // The first location leaves the world view.
        zoom = Math.max(controller.getZoom(), MAP_CONFIG.DEFAULT_ZOOM);
      }
      controller.showLocation(lat, lng, {
        createMarker: set,
        duration: COORDINATE_SYNC_DURATION_MS,
        zoom,
      });
    },
    COORDINATE_SYNC_DEBOUNCE_MS
  );

  $effect(() => {
    const controller = inline;
    const lat = latitude;
    const lng = longitude;
    const set = locationSet;
    if (!controller || expanded) return;

    syncDebounce(controller, lat, lng, set);
    return () => syncDebounce.cancel();
  });

  // A pick on one map is reported to the parent, then moves the other map.
  function handlePick(source: 'inline' | 'expanded', lat: number, lng: number) {
    if (destroyed) return;
    onLocationChange(lat, lng);
    const other = source === 'inline' ? expandedController : inline;
    other?.showLocation(lat, lng, {
      createMarker: true,
      duration: MAP_CONFIG.ANIMATION_DURATION,
    });
  }

  // A searched place is reported like a click, then both maps fly there. The
  // flight must end before the coordinate sync above runs (it keeps whatever zoom
  // the map has by then), so it uses the sync's duration.
  function handlePlaceSelect(place: PlaceResult) {
    if (destroyed) return;
    const lat = roundCoordinate(place.latitude);
    const lng = roundCoordinate(place.longitude);
    onLocationChange(lat, lng);
    for (const controller of [inline, expandedController]) {
      controller?.showLocation(lat, lng, {
        createMarker: true,
        zoom: MAP_CONFIG.DEFAULT_ZOOM,
        duration: COORDINATE_SYNC_DURATION_MS,
      });
    }
  }

  // The expanded map exists while the dialog is open.
  $effect(() => {
    const container = expandedMapElement;
    if (!expanded || !container) return;

    let cancelled = false;
    let controller: LocationMapController | null = null;

    void tick().then(() => {
      const maplibre = loadedMapLibre;
      if (cancelled || !container.isConnected || !maplibre) return;
      untrack(() => {
        try {
          controller = createLocationMapController({
            maplibre,
            container,
            latitude,
            longitude,
            showMarker: locationSet,
            zoom: inline?.getZoom() ?? initialZoom(latitude, longitude, startView),
            wheel: 'always',
            pinchZoom,
            doubleTapZoomKeepsPin,
            recenterOnPick: false,
            onPick: (lat, lng) => handlePick('expanded', lat, lng),
          });
          expandedController = controller;
        } catch (error) {
          logger.error('Failed to initialize modal map:', error);
          toastActions.error(t('settings.main.errors.modalMapLoadFailed'));
          closeExpanded();
        }
      });
    });

    return () => {
      cancelled = true;
      controller?.destroy();
      if (expandedController === controller) {
        expandedController = null;
      }
    };
  });

  function openExpanded() {
    portalTarget = expandButton?.closest<HTMLElement>('[role="dialog"]') ?? document.body;
    expanded = true;
  }

  function closeExpanded() {
    // A click just before closing is still waiting out the double-tap window;
    // keep it.
    expandedController?.flushPendingPick();
    expanded = false;
    backdropPressStarted = false;
    expandButton?.focus();
  }

  // Native listener on the dialog root: Escape and Tab that the dialog handles
  // must not reach a surrounding modal's document-level handler.
  function handleDialogKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      closeExpanded();
      return;
    }
    if (event.key !== 'Tab' || !dialogRoot) return;

    event.stopPropagation();
    const focusable = Array.from(dialogRoot.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
    const first = focusable[0];
    const last = focusable.at(-1);
    if (!first || !last) {
      event.preventDefault();
      dialogBox?.focus();
      return;
    }

    const active = document.activeElement;
    const outside = active === null || active === dialogRoot || active === dialogBox;
    if (event.shiftKey && (active === first || outside)) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && (active === last || outside)) {
      event.preventDefault();
      first.focus();
    }
  }

  $effect(() => {
    const root = dialogRoot;
    if (!root) return;
    root.addEventListener('keydown', handleDialogKeydown);
    return () => root.removeEventListener('keydown', handleDialogKeydown);
  });

  // Move focus into the dialog when it opens. Wait a tick so the portal has
  // moved the dialog first; moving a focused element drops its focus.
  $effect(() => {
    const button = closeButton;
    if (!button) return;
    void tick().then(() => {
      // The dialog may have closed meanwhile (the expanded map failed to
      // build); focus then belongs to the expand button, not to a dying button.
      if (expanded) button.focus();
    });
  });

  onDestroy(() => {
    destroyed = true;
    syncDebounce.cancel();
    expandedController?.destroy();
    inline?.destroy();
  });
</script>

{#snippet zoomControls(variant: 'inline' | 'expanded')}
  {@const controller = variant === 'inline' ? inline : expandedController}
  {@const disabled = !controller || (variant === 'inline' && libraryLoading)}
  {@const background =
    variant === 'inline' ? 'bg-[var(--color-base-300)]' : 'bg-[var(--color-base-200)]'}
  <button
    type="button"
    class={cn(roundButtonClass, background)}
    aria-label={t('components.locationMap.zoomIn')}
    {disabled}
    onclick={() => controller?.zoomIn()}
  >
    +
  </button>
  <button
    type="button"
    class={cn(roundButtonClass, background)}
    aria-label={t('components.locationMap.zoomOut')}
    {disabled}
    onclick={() => controller?.zoomOut()}
  >
    -
  </button>
  {#if variant === 'inline'}
    <button
      type="button"
      bind:this={expandButton}
      class={cn(roundButtonClass, background)}
      aria-label={t('components.locationMap.expand')}
      {disabled}
      onclick={openExpanded}
    >
      <Maximize2 class="size-4" aria-hidden="true" />
    </button>
  {/if}
{/snippet}

<div class={className}>
  {#if placeSearch}
    <PlaceSearch className="mb-3" disabled={!ready} onSelect={handlePlaceSelect} />
  {/if}
  {#if loadError}
    <div
      class={cn(
        'rounded-xl border border-[var(--border-200)] flex items-center justify-center bg-[var(--color-base-200)]',
        mapClass
      )}
    >
      <div class="text-center px-6">
        <MapPin
          class="size-8 mx-auto mb-2 text-[var(--color-base-content)]/40"
          aria-hidden="true"
        />
        <p class="text-sm text-[var(--color-base-content)]/60">
          {t('settings.main.errors.mapUnavailable')}
        </p>
      </div>
    </div>
  {:else}
    <div class="relative">
      <!-- The id is relied on by end-to-end tests; only one inline map is on a page. -->
      <div
        bind:this={mapElement}
        id="location-map"
        class={cn(
          'rounded-xl border border-[var(--border-200)] relative overflow-hidden',
          mapClass
        )}
        role="application"
        aria-label={t('components.locationMap.mapLabel')}
      >
        {#if libraryLoading}
          <div
            class="absolute inset-0 flex items-center justify-center bg-[var(--color-base-100)]/75 rounded-xl"
          >
            <div class="flex flex-col items-center gap-2">
              <span
                class="inline-block w-8 h-8 border-4 border-[var(--color-base-300)] border-t-[var(--color-primary)] rounded-full animate-spin"
                aria-hidden="true"
              ></span>
              <span class="text-sm text-[var(--color-base-content)]"
                >{t('common.ui.loadingMap')}</span
              >
            </div>
          </div>
        {/if}
      </div>
      {#if controls === 'overlay'}
        <div class="absolute top-2 right-2 z-10 flex flex-col gap-2">
          {@render zoomControls('inline')}
        </div>
      {/if}
    </div>
    {#if controls === 'below'}
      <div class="flex gap-2 mt-3">
        {@render zoomControls('inline')}
      </div>
    {/if}
    <p class="text-xs text-[var(--color-info)] mt-2">
      {t('common.ui.mapZoomHelp')}
    </p>
  {/if}
</div>

{#if expanded}
  <!-- Escape and Tab are handled by a native keydown listener on this element. -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div
    bind:this={dialogRoot}
    use:portal={portalTarget}
    class="fixed inset-0 bg-black/50 flex items-center justify-center backdrop-blur-sm"
    style:z-index={Z_INDEX_LOCATION_MAP_DIALOG}
    role="dialog"
    aria-modal="true"
    aria-labelledby={titleId}
    tabindex="-1"
    onpointerdown={e => (backdropPressStarted = e.button === 0 && e.target === e.currentTarget)}
    onclick={e => {
      const close = backdropPressStarted && e.target === e.currentTarget;
      backdropPressStarted = false;
      if (close) closeExpanded();
    }}
  >
    <div
      bind:this={dialogBox}
      class="bg-[var(--color-base-100)] rounded-2xl p-6 max-w-[95vw] max-h-[95vh] w-full h-full md:max-w-[90vw] md:max-h-[90vh] overflow-hidden flex flex-col shadow-2xl focus:outline-none"
      role="document"
      tabindex="-1"
    >
      <div class="flex justify-between items-center mb-4">
        <h3 id={titleId} class="text-xl font-semibold">
          {title}
        </h3>
        <button
          type="button"
          bind:this={closeButton}
          class="inline-flex items-center justify-center w-8 h-8 rounded-full bg-transparent hover:bg-black/5 dark:hover:bg-white/10 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]"
          aria-label={t('common.aria.closeModal')}
          onclick={closeExpanded}
        >
          <X class="size-5" aria-hidden="true" />
        </button>
      </div>

      {#if placeSearch}
        <PlaceSearch className="mb-4" onSelect={handlePlaceSelect} />
      {/if}

      <div class="mb-4 p-3 bg-[var(--color-base-200)]/50 rounded-lg">
        <div class="grid grid-cols-2 gap-4 text-sm">
          <div>
            <span class="text-[var(--color-base-content)] opacity-60"
              >{t('settings.main.sections.rangeFilter.latitude.label')}</span
            >
            <span class="font-medium ml-2">{latitude}</span>
          </div>
          <div>
            <span class="text-[var(--color-base-content)] opacity-60"
              >{t('settings.main.sections.rangeFilter.longitude.label')}</span
            >
            <span class="font-medium ml-2">{longitude}</span>
          </div>
        </div>
      </div>

      <div class="flex-1 min-h-0">
        <div
          bind:this={expandedMapElement}
          class="w-full h-full rounded-xl border border-[var(--border-200)] relative overflow-hidden"
          role="application"
          aria-label={t('components.locationMap.expandedMapLabel')}
        ></div>
      </div>

      <div
        class="flex justify-between items-center mt-4 pt-4 border-t border-[var(--color-base-200)]"
      >
        <div class="flex gap-2">
          {@render zoomControls('expanded')}
        </div>
        <p class="text-sm text-[var(--color-base-content)] opacity-60">
          {t('common.ui.mapSetLocationHelp')}
        </p>
        <button
          type="button"
          class="inline-flex items-center justify-center h-10 px-4 text-sm font-medium rounded-lg bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-2 transition-colors"
          onclick={closeExpanded}
        >
          {t('common.done')}
        </button>
      </div>
    </div>
  </div>
{/if}
