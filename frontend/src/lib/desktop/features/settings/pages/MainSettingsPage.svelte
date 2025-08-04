<!--
  Main Settings Page Component
  
  Purpose: Main configuration settings for BirdNET-Go including node settings,
  BirdNET parameters, database configuration, and user interface options.
  
  Features:
  - Node identification and settings
  - BirdNET analysis parameters (sensitivity, threshold, overlap)
  - Dynamic threshold configuration
  - Range filter with interactive map
  - Database type selection (SQLite/MySQL)
  - User interface preferences (language, thumbnails)
  
  Props: None - This is a page component that uses global settings stores
  
  Performance Optimizations:
  - Cached CSRF token with $derived to avoid repeated DOM queries
  - Reactive computed properties for change detection
  - Async API loading for non-critical data
  - Map lazy loading and conditional initialization
  - Removed page-level spinner to prevent flickering
  
  @component
-->
<script lang="ts">
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectField from '$lib/desktop/components/forms/SelectField.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import PasswordField from '$lib/desktop/components/forms/PasswordField.svelte';
  import {
    settingsStore,
    settingsActions,
    mainSettings,
    birdnetSettings,
    dashboardSettings,
    dynamicThresholdSettings,
    outputSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import { api, ApiError } from '$lib/utils/api';
  import { toastActions } from '$lib/stores/toast';
  import { alertIconsSvg, navigationIcons } from '$lib/utils/icons';
  import { t, getLocale } from '$lib/i18n';
  import { LOCALES } from '$lib/i18n/config';
  import { loggers } from '$lib/utils/logger';
  import {
    MAP_CONFIG,
    createMapStyle as createMapStyleFromConfig,
    getInitialZoom,
  } from '../utils/mapConfig';
  // MapLibre will be dynamically imported when needed to reduce bundle size

  const logger = loggers.settings;

  // PERFORMANCE OPTIMIZATION: Reactive settings with proper defaults
  // Using $derived ensures settings update when stores change without manual subscriptions
  let settings = $derived({
    main: $mainSettings || { name: '' },
    birdnet: $birdnetSettings || {
      sensitivity: 1.0,
      threshold: 0.8,
      overlap: 0.0,
      locale: 'en',
      threads: 0,
      latitude: 0,
      longitude: 0,
      modelPath: '',
      labelPath: '',
      rangeFilter: {
        threshold: 0.01,
      },
    },
    dynamicThreshold: $dynamicThresholdSettings || {
      enabled: false,
      debug: false,
      trigger: 0.8,
      min: 0.3,
      validHours: 24,
    },
    output: $outputSettings || {
      sqlite: {
        enabled: false,
        path: 'birdnet.db',
      },
      mysql: {
        enabled: false,
        username: '',
        password: '',
        database: '',
        host: 'localhost',
        port: '3306',
      },
    },
    dashboard: {
      ...($dashboardSettings || {
        thumbnails: {
          summary: true,
          recent: true,
          imageProvider: 'wikimedia',
          fallbackPolicy: 'all',
        },
        summaryLimit: 100,
      }),
      locale: $dashboardSettings?.locale || (getLocale() as string),
    },
  });

  let store = $derived($settingsStore);

  // Database type selection - determine which database is currently enabled
  let selectedDatabaseType = $state('sqlite');

  // Update selectedDatabaseType when settings change
  $effect(() => {
    if (settings.output.mysql.enabled) {
      selectedDatabaseType = 'mysql';
    } else if (settings.output.sqlite.enabled) {
      selectedDatabaseType = 'sqlite';
    } else {
      selectedDatabaseType = 'sqlite'; // Default to sqlite
    }
  });

  // PERFORMANCE OPTIMIZATION: Reactive change detection with $derived
  // Each section tracks its own changes independently for granular updates
  let mainSettingsHasChanges = $derived(
    hasSettingsChanged((store.originalData as any)?.main, (store.formData as any)?.main)
  );

  let birdnetSettingsHasChanges = $derived(
    hasSettingsChanged((store.originalData as any)?.birdnet, (store.formData as any)?.birdnet) ||
      hasSettingsChanged(
        (store.originalData as any)?.realtime?.dynamicThreshold,
        (store.formData as any)?.realtime?.dynamicThreshold
      )
  );

  let outputSettingsHasChanges = $derived(
    hasSettingsChanged((store.originalData as any)?.output, (store.formData as any)?.output)
  );

  let dashboardSettingsHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.realtime?.dashboard,
      (store.formData as any)?.realtime?.dashboard
    )
  );

  // API State Management
  interface ApiState<T> {
    loading: boolean;
    error: string | null;
    data: T;
  }

  // Locale options for BirdNET
  let birdnetLocales = $state<ApiState<Array<{ value: string; label: string }>>>({
    loading: true,
    error: null,
    data: [],
  });

  // PERFORMANCE OPTIMIZATION: Static UI locales computed once
  // These don't change during the session, so we compute them once
  const uiLocales = Object.entries(LOCALES).map(([code, info]) => ({
    value: code,
    label: `${info.flag} ${info.name}`,
  }));

  // Image provider options
  let providerOptions = $state<ApiState<Array<{ value: string; label: string }>>>({
    loading: true,
    error: null,
    data: [],
  });
  let multipleProvidersAvailable = $derived(providerOptions.data.length > 1);

  // Range filter state with proper structure
  let rangeFilterState = $state<{
    speciesCount: number | null;
    loading: boolean;
    testing: boolean;
    error: string | null;
    showModal: boolean;
    species: any[];
  }>({
    speciesCount: null,
    loading: false,
    testing: false,
    error: null,
    showModal: false,
    species: [],
  });

  // Focus management for modal accessibility
  let previouslyFocusedElement: HTMLElement | null = null;

  // Helper function to get all focusable elements within a container
  function getFocusableElements(container: HTMLElement): HTMLElement[] {
    const focusableSelectors = [
      'button:not([disabled])',
      'input:not([disabled])',
      'select:not([disabled])',
      'textarea:not([disabled])',
      'a[href]',
      '[tabindex]:not([tabindex="-1"])',
    ];

    const elements = container.querySelectorAll(focusableSelectors.join(', '));
    return Array.from(elements).filter(el => {
      const style = window.getComputedStyle(el as HTMLElement);
      return style.display !== 'none' && style.visibility !== 'hidden';
    }) as HTMLElement[];
  }

  // Focus trap handler for modal keyboard navigation
  function handleFocusTrap(event: KeyboardEvent, modal: HTMLElement) {
    if (event.key !== 'Tab') return;

    const focusableElements = getFocusableElements(modal);
    if (focusableElements.length === 0) return;

    const firstElement = focusableElements[0];
    const lastElement = focusableElements[focusableElements.length - 1];

    if (event.shiftKey) {
      // Shift + Tab - moving backwards
      if (document.activeElement === firstElement) {
        event.preventDefault();
        lastElement.focus();
      }
    } else {
      // Tab - moving forwards
      if (document.activeElement === lastElement) {
        event.preventDefault();
        firstElement.focus();
      }
    }
  }

  // Focus trapping effect for range filter modal
  $effect(() => {
    if (rangeFilterState.showModal) {
      // Store previously focused element
      previouslyFocusedElement = document.activeElement as HTMLElement;

      // Set focus to the modal after a microtask to ensure it's in the DOM
      setTimeout(() => {
        const modal = document.querySelector(
          '[role="dialog"][aria-labelledby="modal-title"]'
        ) as HTMLElement;
        if (modal) {
          // Focus the first focusable element or the modal itself
          const focusableElements = getFocusableElements(modal);
          if (focusableElements.length > 0) {
            focusableElements[0].focus();
          } else {
            modal.focus();
          }

          // Add focus trap event listener
          const trapHandler = (event: KeyboardEvent) => handleFocusTrap(event, modal);
          modal.addEventListener('keydown', trapHandler);

          // Cleanup function
          return () => {
            modal.removeEventListener('keydown', trapHandler);
          };
        }
      }, 0);
    } else if (previouslyFocusedElement) {
      // Restore focus to previously focused element
      previouslyFocusedElement.focus();
      previouslyFocusedElement = null;
    }
  });

  // Map state
  let mapElement: HTMLElement | undefined = $state();
  let modalMapElement: HTMLElement | undefined = $state();
  let maplibregl: typeof import('maplibre-gl') | null = null;
  let map: import('maplibre-gl').Map | null = $state(null);
  let modalMap: import('maplibre-gl').Map | null = $state(null);
  let marker: import('maplibre-gl').Marker | null = null;
  let modalMarker: import('maplibre-gl').Marker | null = null;
  let mapModalOpen = $state(false);
  let mapInitialized = $state(false);
  let mapLibraryLoading = $state(false);

  // PERFORMANCE OPTIMIZATION: Cache CSRF token with $derived
  let csrfToken = $derived(
    (document.querySelector('meta[name="csrf-token"]') as HTMLElement)?.getAttribute('content') ||
      ''
  );

  // PERFORMANCE OPTIMIZATION: Load API data concurrently
  // Use $effect instead of onMount for Svelte 5 pattern
  $effect(() => {
    loadInitialData();
  });

  // Initialize map when settings are actually loaded from server
  $effect(() => {
    logger.debug('Map initialization effect triggered:', {
      storeLoading: store.isLoading,
      mapElementExists: !!mapElement,
      mapInitialized,
      birdnetSettings: $birdnetSettings,
      derivedSettings: settings.birdnet,
      hasActualCoordinates:
        $birdnetSettings &&
        $birdnetSettings.latitude !== undefined &&
        $birdnetSettings.longitude !== undefined,
    });

    // Wait for actual coordinates to be loaded (not just fallbacks)
    const hasActualCoordinates =
      $birdnetSettings &&
      $birdnetSettings.latitude !== undefined &&
      $birdnetSettings.longitude !== undefined;

    if (!store.isLoading && mapElement && !mapInitialized && hasActualCoordinates) {
      logger.debug('Initializing map with actual coordinates:', {
        latitude: $birdnetSettings.latitude,
        longitude: $birdnetSettings.longitude,
        loadingComplete: !store.isLoading,
        hasActualSettings: !!$birdnetSettings,
      });
      initializeMap();
      mapInitialized = true;
    }
  });

  // Track if initial load is complete to prevent map jumps on page load
  let initialLoadComplete = $state(false);

  // Mark initial load as complete after map initialization
  $effect(() => {
    if (!store.isLoading && mapInitialized && !initialLoadComplete) {
      logger.debug('Map initial load complete, enabling coordinate reactive updates');
      initialLoadComplete = true;
    }
  });

  // Update map when coordinates change via input fields (after initial load)
  let coordinateUpdateTimer: ReturnType<typeof setTimeout> | undefined;

  $effect(() => {
    // Only update if:
    // 1. Initial load is complete (prevents jumps on page load)
    // 2. Map is initialized
    // 3. Modal is not open (don't interfere with modal interaction)
    if (initialLoadComplete && map && !mapModalOpen) {
      const lat = settings.birdnet.latitude;
      const lng = settings.birdnet.longitude;

      // Debounce updates to avoid too many map movements while typing
      clearTimeout(coordinateUpdateTimer);
      coordinateUpdateTimer = setTimeout(() => {
        if (lat !== undefined && lng !== undefined && !isNaN(lat) && !isNaN(lng)) {
          logger.debug('Updating map from coordinate inputs', { lat, lng });

          // Preserve current zoom level
          const currentZoom = map!.getZoom();
          map!.easeTo({
            center: [lng, lat],
            zoom: currentZoom,
            duration: 300, // Smooth transition
          });

          // Update or create marker
          if (marker) {
            marker.setLngLat([lng, lat]);
          } else if (maplibregl && (lat !== 0 || lng !== 0)) {
            // Create marker if it doesn't exist and coords are non-zero
            marker = new maplibregl.Marker({ draggable: true }).setLngLat([lng, lat]).addTo(map!);

            marker.on('dragend', () => {
              const lngLat = marker!.getLngLat();
              updateMarker(lngLat.lat, lngLat.lng);
            });
          }
        }
      }, 500); // 500ms debounce for typing
    }

    // Cleanup timer on effect re-run or unmount
    return () => {
      clearTimeout(coordinateUpdateTimer);
    };
  });

  // Cleanup map on component unmount
  $effect(() => {
    return () => {
      if (map) {
        map.remove();
        map = null;
        marker = null;
        mapInitialized = false;
      }
    };
  });

  // Manage modal map lifecycle with proper Svelte 5 pattern
  $effect(() => {
    let cleanup: (() => void) | undefined;

    if (mapModalOpen && modalMapElement && initialLoadComplete) {
      logger.debug('Opening map modal, initializing modal map');
      initializeModalMap().then(cleanupFn => {
        cleanup = cleanupFn;
      });
    }

    return () => {
      if (cleanup) {
        cleanup();
      }
      // Ensure reactive state is properly reset when effect cleanup runs
      if (!mapModalOpen && modalMap) {
        logger.debug('Cleaning up modal map reactive state');
        modalMap = null;
        modalMarker = null;
      }
    };
  });

  async function loadInitialData() {
    // Load all API data in parallel for better performance
    await Promise.all([loadBirdnetLocales(), loadImageProviders(), loadRangeFilterCount()]);
  }

  async function loadBirdnetLocales() {
    birdnetLocales.loading = true;
    birdnetLocales.error = null;

    try {
      const localesData = await api.get<Record<string, string>>('/api/v2/settings/locales');
      birdnetLocales.data = Object.entries(localesData || {}).map(([value, label]) => ({
        value,
        label: label as string,
      }));
    } catch (error) {
      if (error instanceof ApiError) {
        toastActions.warning(t('settings.main.errors.localesLoadFailed'));
      }
      birdnetLocales.error = t('settings.main.errors.localesLoadFailed');
      // Fallback to basic locales so form still works
      birdnetLocales.data = [{ value: 'en', label: 'English' }];
    } finally {
      birdnetLocales.loading = false;
    }
  }

  async function loadImageProviders() {
    providerOptions.loading = true;
    providerOptions.error = null;

    try {
      const providersData = await api.get<{
        providers?: Array<{ value: string; display: string }>;
      }>('/api/v2/settings/imageproviders');

      // Map v2 API response format to client format
      providerOptions.data = (providersData?.providers || []).map((provider: any) => ({
        value: provider.value,
        label: provider.display,
      }));
    } catch (error) {
      if (error instanceof ApiError) {
        toastActions.warning(t('settings.main.errors.providersLoadFailed'));
      }
      providerOptions.error = t('settings.main.errors.providersLoadFailed');
      // Fallback to basic provider so form still works
      providerOptions.data = [{ value: 'wikipedia', label: 'Wikipedia' }];
    } finally {
      providerOptions.loading = false;
    }
  }

  // Create map style configuration
  // Use imported map style configuration
  const createMapStyle = createMapStyleFromConfig;

  // Initialize MapLibre GL JS map
  async function initializeMap() {
    if (!mapElement || mapInitialized) return;

    try {
      // Dynamically import MapLibre GL JS and CSS when needed
      if (!maplibregl) {
        mapLibraryLoading = true;
        try {
          const [maplibreModule] = await Promise.all([
            import('maplibre-gl'),
            import('maplibre-gl/dist/maplibre-gl.css'),
          ]);
          maplibregl = maplibreModule;
          mapLibraryLoading = false;
        } catch (importError) {
          mapLibraryLoading = false;
          logger.error('Failed to load MapLibre GL JS:', importError, {
            component: 'MainSettingsPage',
            action: 'dynamic import',
          });

          // Provide user-friendly error message
          if (importError instanceof Error) {
            if (
              importError.message.includes('Network') ||
              importError.message.includes('Failed to fetch')
            ) {
              toastActions.error(
                'Failed to load map library. Please check your internet connection and try again.'
              );
            } else {
              toastActions.error(
                'Failed to load map library. Your browser may not support this feature.'
              );
            }
          } else {
            toastActions.error(
              'Failed to load map library. Please refresh the page and try again.'
            );
          }

          // Reset state and exit early
          mapInitialized = false;
          return;
        }
      }

      // Use actual coordinates from $birdnetSettings, not derived fallbacks
      const initialLat = $birdnetSettings?.latitude ?? 0;
      const initialLng = $birdnetSettings?.longitude ?? 0;
      const initialZoom = getInitialZoom(initialLat, initialLng);

      logger.debug('Map initialization values:', {
        initialLat,
        initialLng,
        initialZoom,
        actualBirdnetSettings: $birdnetSettings,
        derivedBirdnetSettings: settings.birdnet,
        mapElementSize: mapElement
          ? { width: mapElement.offsetWidth, height: mapElement.offsetHeight }
          : null,
      });

      map = new maplibregl.Map({
        container: mapElement,
        style: createMapStyle(),
        center: [initialLng, initialLat], // MapLibre uses [lng, lat] order
        zoom: initialZoom,
        scrollZoom: MAP_CONFIG.SCROLL_ZOOM,
        keyboard: MAP_CONFIG.KEYBOARD_NAV,
        // Disable animations during initialization
        fadeDuration: MAP_CONFIG.FADE_DURATION,
        pitchWithRotate: MAP_CONFIG.PITCH_WITH_ROTATE,
        touchZoomRotate: MAP_CONFIG.TOUCH_ZOOM_ROTATE,
      });

      // Force MapLibre to recalculate container size after initialization
      // This prevents coordinate calculation issues like 2x drag distance
      map.on('load', () => {
        if (map) {
          map.resize();
        }
      });

      // Enable zoom only when Ctrl/Cmd is pressed
      const handleWheel = (e: Event) => {
        const wheelEvent = e as Event & { deltaY: number; ctrlKey: boolean; metaKey: boolean };
        if ((wheelEvent.ctrlKey || wheelEvent.metaKey) && map) {
          wheelEvent.preventDefault();
          if (wheelEvent.deltaY > 0) {
            map.zoomOut({ duration: 300 }); // Smooth zoom
          } else {
            map.zoomIn({ duration: 300 }); // Smooth zoom
          }
        }
      };
      mapElement.addEventListener('wheel', handleWheel);

      // Add marker if coordinates exist (direct creation to avoid reactive loops)
      if ((initialLat !== 0 || initialLng !== 0) && maplibregl) {
        marker = new maplibregl.Marker({ draggable: true })
          .setLngLat([initialLng, initialLat])
          .addTo(map);

        marker.on('dragend', () => {
          const lngLat = marker!.getLngLat();
          updateMarker(lngLat.lat, lngLat.lng);
        });
      }

      // Handle map clicks
      if (map) {
        map.on('click', (e: import('maplibre-gl').MapMouseEvent) => {
          const lngLat = e.lngLat;
          updateMarker(lngLat.lat, lngLat.lng);
          // Preserve zoom level when centering on clicked location
          const currentZoom = map?.getZoom();
          map?.easeTo({
            center: [lngLat.lng, lngLat.lat],
            zoom: currentZoom, // Preserve current zoom level
            duration: MAP_CONFIG.ANIMATION_DURATION,
          });
        });
      }
    } catch (error) {
      logger.error('Failed to initialize map:', error, {
        component: 'MainSettingsPage',
        action: 'initializeMap',
        mapElement: !!mapElement,
        maplibregl: !!maplibregl,
      });

      // Provide specific error messages based on error type
      if (error instanceof Error) {
        if (error.message.includes('WebGL')) {
          toastActions.error(
            'Map requires WebGL support. Please ensure your browser supports WebGL and hardware acceleration is enabled.'
          );
        } else if (error.message.includes('container')) {
          toastActions.error('Map container not found. Please refresh the page and try again.');
        } else {
          toastActions.error(`Failed to load map: ${error.message}`);
        }
      } else {
        toastActions.error('Failed to load map. Please try refreshing the page.');
      }

      // Reset map initialization state so user can retry
      mapInitialized = false;
    }
  }

  // Update marker position and settings (called when coordinates change)
  function updateMarker(lat: number, lng: number) {
    if (!map) return;

    const roundedLat = parseFloat(lat.toFixed(3));
    const roundedLng = parseFloat(lng.toFixed(3));

    // Update settings
    settingsActions.updateSection('birdnet', {
      latitude: roundedLat,
      longitude: roundedLng,
    });

    // Update both maps
    updateMapView(roundedLat, roundedLng);

    // Test range filter with new coordinates
    debouncedTestRangeFilter();
  }

  // Update map view and marker position (visual only, no settings update)
  function updateMapView(lat: number, lng: number) {
    if (!map) return;

    // Update map center while preserving current zoom level
    const currentZoom = map.getZoom();
    map.easeTo({
      center: [lng, lat],
      zoom: currentZoom, // Preserve current zoom level
      duration: MAP_CONFIG.ANIMATION_DURATION,
    }); // MapLibre uses [lng, lat] order

    // Update or create marker
    if (marker) {
      marker.setLngLat([lng, lat]);
    } else if (maplibregl) {
      marker = new maplibregl.Marker({ draggable: true }).setLngLat([lng, lat]).addTo(map);

      marker.on('dragend', () => {
        const lngLat = marker!.getLngLat();
        updateMarker(lngLat.lat, lngLat.lng);
      });
    }

    // Sync modal map if it exists
    if (modalMap) {
      const modalCurrentZoom = modalMap.getZoom();
      modalMap.easeTo({
        center: [lng, lat],
        zoom: modalCurrentZoom, // Preserve current zoom level
        duration: MAP_CONFIG.ANIMATION_DURATION,
      });
      if (modalMarker) {
        modalMarker.setLngLat([lng, lat]);
      } else if (maplibregl) {
        modalMarker = new maplibregl.Marker({ draggable: true })
          .setLngLat([lng, lat])
          .addTo(modalMap);

        modalMarker.on('dragend', () => {
          const lngLat = modalMarker!.getLngLat();
          // Update settings only, don't trigger map view updates that could change zoom
          const roundedLat = parseFloat(lngLat.lat.toFixed(3));
          const roundedLng = parseFloat(lngLat.lng.toFixed(3));

          settingsActions.updateSection('birdnet', {
            latitude: roundedLat,
            longitude: roundedLng,
          });

          // Sync main map position without zoom changes
          if (map) {
            const mainCurrentZoom = map.getZoom();
            map.easeTo({
              center: [roundedLng, roundedLat],
              zoom: mainCurrentZoom, // Preserve current zoom level
              duration: MAP_CONFIG.ANIMATION_DURATION,
            });
            if (marker) {
              marker.setLngLat([roundedLng, roundedLat]);
            }
          }

          // Test range filter with new coordinates
          debouncedTestRangeFilter();
        });
      }
    }
  }

  // Initialize modal map when modal is opened
  // Note: This implementation includes full bidirectional sync:
  // - Modal map inherits current coordinates and zoom from main map when opened
  // - User interactions (click/drag) in modal update settings and sync back to main map
  // - Both maps stay synchronized through the shared settings state
  async function initializeModalMap() {
    if (!modalMapElement || modalMap || !maplibregl) return;

    // Define handleModalWheel outside try block so it's accessible in cleanup
    const handleModalWheel = (
      e: Event & {
        deltaY: number;
        ctrlKey: boolean;
        metaKey: boolean;
        preventDefault: () => void;
      }
    ) => {
      if (modalMap) {
        e.preventDefault();
        if (e.deltaY > 0) {
          modalMap.zoomOut({ duration: 300 }); // Smooth zoom
        } else {
          modalMap.zoomIn({ duration: 300 }); // Smooth zoom
        }
      }
    };

    try {
      const currentLat = $birdnetSettings?.latitude ?? 0;
      const currentLng = $birdnetSettings?.longitude ?? 0;
      const currentZoom = map?.getZoom() || getInitialZoom(currentLat, currentLng);

      logger.debug('Modal map initialization values:', {
        currentLat,
        currentLng,
        currentZoom,
        actualBirdnetSettings: $birdnetSettings,
      });

      modalMap = new maplibregl.Map({
        container: modalMapElement,
        style: createMapStyle(),
        center: [currentLng, currentLat],
        zoom: currentZoom,
        scrollZoom: MAP_CONFIG.SCROLL_ZOOM,
        keyboard: MAP_CONFIG.KEYBOARD_NAV,
        fadeDuration: MAP_CONFIG.FADE_DURATION,
        pitchWithRotate: MAP_CONFIG.PITCH_WITH_ROTATE,
        touchZoomRotate: MAP_CONFIG.TOUCH_ZOOM_ROTATE,
      });

      // Enable mouse wheel zooming in modal (no Ctrl requirement - already handled by handleModalWheel)
      if (modalMap) {
        modalMap.scrollZoom.enable();
      }
      modalMapElement.addEventListener('wheel', handleModalWheel);

      // Add marker if coordinates exist
      if (currentLat !== 0 || currentLng !== 0) {
        modalMarker = new maplibregl!.Marker({ draggable: true })
          .setLngLat([currentLng, currentLat])
          .addTo(modalMap);

        modalMarker.on('dragend', () => {
          const lngLat = modalMarker!.getLngLat();
          // Update settings only, don't trigger map view updates that could change zoom
          const roundedLat = parseFloat(lngLat.lat.toFixed(3));
          const roundedLng = parseFloat(lngLat.lng.toFixed(3));

          settingsActions.updateSection('birdnet', {
            latitude: roundedLat,
            longitude: roundedLng,
          });

          // Sync main map position without zoom changes
          if (map) {
            const mainCurrentZoom = map.getZoom();
            map.easeTo({
              center: [roundedLng, roundedLat],
              zoom: mainCurrentZoom, // Preserve current zoom level
              duration: MAP_CONFIG.ANIMATION_DURATION,
            });
            if (marker) {
              marker.setLngLat([roundedLng, roundedLat]);
            }
          }

          // Test range filter with new coordinates
          debouncedTestRangeFilter();
        });
      }

      // Handle modal map clicks
      if (modalMap) {
        modalMap.on('click', (e: import('maplibre-gl').MapMouseEvent) => {
          const lngLat = e.lngLat;
          // Update settings only, don't trigger map view updates that could change zoom
          const roundedLat = parseFloat(lngLat.lat.toFixed(3));
          const roundedLng = parseFloat(lngLat.lng.toFixed(3));

          settingsActions.updateSection('birdnet', {
            latitude: roundedLat,
            longitude: roundedLng,
          });

          // Update modal marker position
          if (modalMarker) {
            modalMarker.setLngLat([roundedLng, roundedLat]);
          }

          // Sync main map position without zoom changes
          if (map) {
            const mainCurrentZoom = map.getZoom();
            map.easeTo({
              center: [roundedLng, roundedLat],
              zoom: mainCurrentZoom, // Preserve current zoom level
              duration: MAP_CONFIG.ANIMATION_DURATION,
            });
            if (marker) {
              marker.setLngLat([roundedLng, roundedLat]);
            }
          }

          // Test range filter with new coordinates
          debouncedTestRangeFilter();
        });
      }
    } catch (error) {
      logger.error('Failed to initialize modal map:', error, {
        component: 'MainSettingsPage',
        action: 'initializeModalMap',
        modalMapElement: !!modalMapElement,
        maplibregl: !!maplibregl,
        modalMap: !!modalMap,
      });

      // Provide specific error messages based on error type
      if (error instanceof Error) {
        if (error.message.includes('WebGL')) {
          toastActions.error(
            'Modal map requires WebGL support. Please ensure your browser supports WebGL and hardware acceleration is enabled.'
          );
        } else if (error.message.includes('container')) {
          toastActions.error(
            'Modal map container not found. Please close and reopen the map modal.'
          );
        } else {
          toastActions.error(`Failed to load modal map: ${error.message}`);
        }
      } else {
        toastActions.error('Failed to load modal map. Please try closing and reopening the modal.');
      }

      // Close modal on error
      mapModalOpen = false;

      // Return empty cleanup function
      return () => {};
    }

    // Return cleanup function
    return () => {
      modalMapElement?.removeEventListener('wheel', handleModalWheel);
      if (modalMap) {
        modalMap.remove();
        // Don't set modalMap = null here, it's handled by the reactive state in the effect
      }
    };
  }

  // Map modal functions
  function openMapModal() {
    mapModalOpen = true;
  }

  function closeMapModal() {
    mapModalOpen = false;
    // Cleanup will happen in the effect
  }

  // Public method for programmatic map centering
  export function centerMapOnCoordinates(lat: number, lng: number) {
    // Validate inputs
    if (typeof lat !== 'number' || typeof lng !== 'number' || isNaN(lat) || isNaN(lng)) {
      logger.warn('Invalid coordinates provided to centerMapOnCoordinates', {
        lat,
        lng,
        component: 'MainSettingsPage',
      });
      return;
    }

    // Only update if map is initialized and not in modal view
    if (map && !mapModalOpen) {
      const currentZoom = map.getZoom();

      logger.debug('Centering map programmatically', {
        lat,
        lng,
        currentZoom,
        component: 'MainSettingsPage',
      });

      map.easeTo({
        center: [lng, lat],
        zoom: currentZoom, // Preserve current zoom level
        duration: MAP_CONFIG.ANIMATION_DURATION || 500, // Fallback to 500ms if config not set
      });

      // Update marker position if it exists
      if (marker) {
        marker.setLngLat([lng, lat]);
      }

      // Update settings with rounded coordinates
      const roundedLat = parseFloat(lat.toFixed(3));
      const roundedLng = parseFloat(lng.toFixed(3));

      settingsActions.updateSection('birdnet', {
        latitude: roundedLat,
        longitude: roundedLng,
      });
    } else if (!map) {
      logger.warn('Map not initialized, cannot center on coordinates', {
        component: 'MainSettingsPage',
      });
    } else if (mapModalOpen) {
      logger.debug('Map modal is open, skipping programmatic centering', {
        component: 'MainSettingsPage',
      });
    }
  }

  // Range filter functions
  let debounceTimer: any;
  let loadingDelayTimer: any;

  function debouncedTestRangeFilter() {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      testCurrentRangeFilter();
    }, 150); // Reduced from 500ms to 150ms for faster response
  }

  async function loadRangeFilterCount() {
    try {
      const response = await fetch('/api/v2/range/species/count', {
        headers: { 'X-CSRF-Token': csrfToken },
      });
      if (!response.ok) throw new Error('Failed to load range filter count');
      const data = await response.json();
      rangeFilterState.speciesCount = data.count;
    } catch (error) {
      logger.error('Failed to load range filter count:', error);
      rangeFilterState.error = t('settings.main.errors.rangeFilterCountFailed');
    }
  }

  async function testCurrentRangeFilter() {
    if (rangeFilterState.testing || !settings.birdnet.latitude || !settings.birdnet.longitude)
      return;

    // Clear any existing loading delay timer
    clearTimeout(loadingDelayTimer);

    // Only show loading state if the request takes longer than 100ms
    // This prevents flicker for fast responses
    loadingDelayTimer = setTimeout(() => {
      rangeFilterState.testing = true;
    }, 100);

    rangeFilterState.error = null;

    try {
      const data = await api.post<{ count: number; species?: any[] }>(
        '/api/v2/range/species/test',
        {
          latitude: settings.birdnet.latitude,
          longitude: settings.birdnet.longitude,
          threshold: settings.birdnet.rangeFilter.threshold,
        }
      );

      rangeFilterState.speciesCount = data.count;

      if (rangeFilterState.showModal) {
        rangeFilterState.species = data.species || [];
      }
    } catch (error) {
      logger.error('Failed to test range filter:', error);
      rangeFilterState.error = t('settings.main.errors.rangeFilterTestFailed');
      // Set count to null on error to show loading state next time
      rangeFilterState.speciesCount = null;
    } finally {
      // Clear the loading delay timer and testing state
      clearTimeout(loadingDelayTimer);
      rangeFilterState.testing = false;
    }
  }

  async function loadRangeFilterSpecies() {
    if (rangeFilterState.loading) return;

    rangeFilterState.loading = true;
    rangeFilterState.error = null;

    try {
      const params = new URLSearchParams({
        latitude: settings.birdnet.latitude.toString(),
        longitude: settings.birdnet.longitude.toString(),
        threshold: settings.birdnet.rangeFilter.threshold.toString(),
      });

      const data = await api.get<{ count: number; species: any[] }>(
        `/api/v2/range/species/list?${params}`
      );
      rangeFilterState.species = data.species || [];
      rangeFilterState.speciesCount = data.count;
    } catch (error) {
      logger.error('Failed to load species list:', error);
      rangeFilterState.error = t('settings.main.errors.rangeFilterLoadFailed');
    } finally {
      rangeFilterState.loading = false;
    }
  }

  // Watch for changes that affect range filter
  $effect(() => {
    // Track the specific values that should trigger a range filter update
    // These variables are used by Svelte's reactivity system to track changes
    const lat = settings.birdnet.latitude;
    const lng = settings.birdnet.longitude;

    // Only test if we have valid coordinates
    if (lat && lng) {
      debouncedTestRangeFilter();
    }
  });

  // Update handlers
  function updateMainName(name: string) {
    settingsActions.updateSection('main', { name });
  }

  function updateBirdnetSetting(key: string, value: any) {
    settingsActions.updateSection('birdnet', { [key]: value });
  }

  function updateDynamicThreshold(key: string, value: any) {
    settingsActions.updateSection('realtime', {
      dynamicThreshold: { ...settings.dynamicThreshold, [key]: value },
    });
  }

  function updateSQLiteSettings(updates: Partial<{ enabled: boolean; path: string }>) {
    settingsActions.updateSection('output', {
      ...settings.output,
      sqlite: { ...settings.output.sqlite, ...updates },
    });
  }

  function updateMySQLSettings(
    updates: Partial<{
      enabled: boolean;
      username: string;
      password: string;
      database: string;
      host: string;
      port: string;
    }>
  ) {
    settingsActions.updateSection('output', {
      ...settings.output,
      mysql: { ...settings.output.mysql, ...updates },
    });
  }

  function updateDatabaseType(type: 'sqlite' | 'mysql') {
    settingsActions.updateSection('output', {
      ...settings.output,
      sqlite: { ...settings.output.sqlite, enabled: type === 'sqlite' },
      mysql: { ...settings.output.mysql, enabled: type === 'mysql' },
    });
  }

  function updateDashboardSetting(key: string, value: any) {
    settingsActions.updateSection('realtime', {
      dashboard: { ...settings.dashboard, [key]: value },
    });
  }

  function updateThumbnailSetting(key: string, value: any) {
    settingsActions.updateSection('realtime', {
      dashboard: {
        ...settings.dashboard,
        thumbnails: { ...settings.dashboard.thumbnails, [key]: value },
      },
    });
  }

  function updateUILocale(locale: string) {
    settingsActions.updateSection('realtime', {
      dashboard: { ...settings.dashboard, locale },
    });
  }
</script>

<main class="space-y-4 settings-page-content" aria-label="Main settings configuration">
  <!-- Main Settings Section -->
  <SettingsSection
    title={t('settings.main.sections.main.title')}
    description={t('settings.main.sections.main.description')}
    defaultOpen={true}
    hasChanges={mainSettingsHasChanges}
  >
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6">
      <TextInput
        id="node-name"
        value={settings.main.name}
        label={t('settings.main.fields.nodeName.label')}
        placeholder={t('settings.main.fields.nodeName.placeholder')}
        helpText={t('settings.main.fields.nodeName.helpText')}
        disabled={store.isLoading || store.isSaving}
        onchange={updateMainName}
      />
    </div>
  </SettingsSection>

  <!-- BirdNET Settings Section -->
  <SettingsSection
    title={t('settings.main.sections.birdnet.title')}
    description={t('settings.main.sections.birdnet.description')}
    defaultOpen={true}
    hasChanges={birdnetSettingsHasChanges}
  >
    <div class="space-y-6">
      <!-- Basic BirdNET Settings -->
      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6">
        <NumberField
          label={t('settings.main.fields.sensitivity.label')}
          value={settings.birdnet.sensitivity}
          onUpdate={value => updateBirdnetSetting('sensitivity', value)}
          min={0.5}
          max={1.5}
          step={0.1}
          helpText={t('settings.main.fields.sensitivity.helpText')}
          disabled={store.isLoading || store.isSaving}
        />

        <NumberField
          label={t('settings.main.fields.threshold.label')}
          value={settings.birdnet.threshold}
          onUpdate={value => updateBirdnetSetting('threshold', value)}
          min={0.01}
          max={0.99}
          step={0.01}
          helpText={t('settings.main.fields.threshold.helpText')}
          disabled={store.isLoading || store.isSaving}
        />

        <NumberField
          label={t('settings.main.fields.overlap.label')}
          value={settings.birdnet.overlap}
          onUpdate={value => updateBirdnetSetting('overlap', value)}
          min={0.0}
          max={2.9}
          step={0.1}
          helpText={t('settings.main.fields.overlap.helpText')}
          disabled={store.isLoading || store.isSaving}
        />

        <SelectField
          id="locale"
          value={settings.birdnet.locale}
          label={t('settings.main.fields.locale.label')}
          options={birdnetLocales.data}
          helpText={t('settings.main.fields.locale.helpText')}
          disabled={store.isLoading || store.isSaving || birdnetLocales.loading}
          onchange={value => updateBirdnetSetting('locale', value)}
        />

        <NumberField
          label={t('settings.main.fields.tensorflowThreads.label')}
          value={settings.birdnet.threads}
          onUpdate={value => updateBirdnetSetting('threads', value)}
          min={0}
          max={32}
          step={1}
          helpText={t('settings.main.fields.tensorflowThreads.helpText')}
          disabled={store.isLoading || store.isSaving}
        />
      </div>

      <!-- Custom BirdNET Classifier -->
      <div>
        <h4 class="text-lg font-medium mt-6 pb-2">
          {t('settings.main.sections.customClassifier.title')}
        </h4>
        <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6">
          <TextInput
            id="model-path"
            value={settings.birdnet.modelPath}
            label={t('settings.main.sections.customClassifier.modelPath.label')}
            placeholder={t('settings.main.sections.customClassifier.modelPath.placeholder')}
            helpText={t('settings.main.sections.customClassifier.modelPath.helpText')}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateBirdnetSetting('modelPath', value)}
          />

          <TextInput
            id="label-path"
            value={settings.birdnet.labelPath}
            label={t('settings.main.sections.customClassifier.labelPath.label')}
            placeholder={t('settings.main.sections.customClassifier.labelPath.placeholder')}
            helpText={t('settings.main.sections.customClassifier.labelPath.helpText')}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateBirdnetSetting('labelPath', value)}
          />
        </div>
      </div>

      <!-- Dynamic Threshold -->
      <div>
        <h4 class="text-lg font-medium mt-6 pb-2">
          {t('settings.main.sections.dynamicThreshold.title')}
        </h4>
        <Checkbox
          checked={settings.dynamicThreshold.enabled}
          label={t('settings.main.sections.dynamicThreshold.enable.label')}
          helpText={t('settings.main.sections.dynamicThreshold.enable.helpText')}
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateDynamicThreshold('enabled', value)}
        />

        {#if settings.dynamicThreshold.enabled}
          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6 mt-4">
            <NumberField
              label={t('settings.main.sections.dynamicThreshold.trigger.label')}
              value={settings.dynamicThreshold.trigger}
              onUpdate={value => updateDynamicThreshold('trigger', value)}
              min={0.0}
              max={1.0}
              step={0.01}
              helpText={t('settings.main.sections.dynamicThreshold.trigger.helpText')}
              disabled={store.isLoading || store.isSaving}
            />

            <NumberField
              label={t('settings.main.sections.dynamicThreshold.minimum.label')}
              value={settings.dynamicThreshold.min}
              onUpdate={value => updateDynamicThreshold('min', value)}
              min={0.0}
              max={0.99}
              step={0.01}
              helpText={t('settings.main.sections.dynamicThreshold.minimum.helpText')}
              disabled={store.isLoading || store.isSaving}
            />

            <NumberField
              label={t('settings.main.sections.dynamicThreshold.expireTime.label')}
              value={settings.dynamicThreshold.validHours}
              onUpdate={value => updateDynamicThreshold('validHours', value)}
              min={0}
              max={1000}
              step={1}
              helpText={t('settings.main.sections.dynamicThreshold.expireTime.helpText')}
              disabled={store.isLoading || store.isSaving}
            />
          </div>
        {/if}
      </div>

      <!-- Range Filter -->
      <div>
        <h4 class="text-lg font-medium mt-6 pb-2">
          {t('settings.main.sections.rangeFilter.title')}
        </h4>

        <!-- Coordinates row - 2 columns -->
        <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6 mb-6">
          <NumberField
            label={t('settings.main.sections.rangeFilter.latitude.label')}
            value={settings.birdnet.latitude}
            onUpdate={value => updateBirdnetSetting('latitude', value)}
            min={-90.0}
            max={90.0}
            step={0.001}
            helpText={t('settings.main.sections.rangeFilter.latitude.helpText')}
            disabled={store.isLoading || store.isSaving}
          />

          <NumberField
            label={t('settings.main.sections.rangeFilter.longitude.label')}
            value={settings.birdnet.longitude}
            onUpdate={value => updateBirdnetSetting('longitude', value)}
            min={-180.0}
            max={180.0}
            step={0.001}
            helpText={t('settings.main.sections.rangeFilter.longitude.helpText')}
            disabled={store.isLoading || store.isSaving}
          />
        </div>

        <!-- Map container - full width -->
        <div class="mb-6">
          <label class="label justify-start" for="location-map">
            <span class="label-text"
              >{t('settings.main.sections.rangeFilter.stationLocation.label')}</span
            >
          </label>
          <div class="form-control">
            <div
              bind:this={mapElement}
              id="location-map"
              class="h-[300px] rounded-lg border border-base-300 relative"
              role="application"
              aria-label="Map for selecting station location"
              aria-describedby="map-help-text"
            >
              <!-- Map will be initialized here -->
              {#if mapLibraryLoading}
                <div
                  class="absolute inset-0 flex items-center justify-center bg-base-100 bg-opacity-75 rounded-lg"
                >
                  <div class="flex flex-col items-center gap-2">
                    <span class="loading loading-spinner loading-lg" aria-hidden="true"></span>
                    <span class="text-sm text-base-content" role="status" aria-live="assertive"
                      >Loading map...</span
                    >
                  </div>
                </div>
              {/if}
            </div>
            <div class="flex gap-2 mt-2">
              <button
                type="button"
                class="btn btn-sm btn-circle"
                aria-label="Zoom in"
                aria-disabled={!map || mapLibraryLoading}
                disabled={!map || mapLibraryLoading}
                onclick={() => map?.zoomIn({ duration: 300 })}
              >
                +
              </button>
              <button
                type="button"
                class="btn btn-sm btn-circle"
                aria-label="Zoom out"
                aria-disabled={!map || mapLibraryLoading}
                disabled={!map || mapLibraryLoading}
                onclick={() => map?.zoomOut({ duration: 300 })}
              >
                -
              </button>
              <button
                type="button"
                class="btn btn-sm btn-circle"
                aria-label="Expand map to full screen"
                aria-disabled={!map || mapLibraryLoading}
                disabled={!map || mapLibraryLoading}
                onclick={openMapModal}
              >
                {@html navigationIcons.expand}
              </button>
            </div>

            <!-- Screen reader announcements for coordinate changes -->
            <div class="sr-only" aria-live="polite" aria-atomic="true" role="status">
              {#if settings.birdnet.latitude && settings.birdnet.longitude}
                Current location: Latitude {settings.birdnet.latitude.toFixed(3)}, Longitude {settings.birdnet.longitude.toFixed(
                  3
                )}
              {/if}
            </div>
            <div class="label">
              <span id="map-help-text" class="label-text-alt"
                >{t('settings.main.sections.rangeFilter.stationLocation.helpText')}</span
              >
            </div>
            <div class="label">
              <span class="label-text-alt text-info"
                >💡 Hold Ctrl (or Cmd on Mac) + scroll to zoom the map. Use arrow keys to pan, +/-
                keys to zoom.</span
              >
            </div>
          </div>
        </div>

        <!-- Threshold and Species row - 2 columns -->
        <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
          <NumberField
            label={t('settings.main.sections.rangeFilter.threshold.label')}
            value={settings.birdnet.rangeFilter.threshold}
            onUpdate={value =>
              settingsActions.updateSection('birdnet', {
                rangeFilter: { ...settings.birdnet.rangeFilter, threshold: value },
              })}
            min={0.0}
            max={0.99}
            step={0.01}
            helpText={t('settings.main.sections.rangeFilter.threshold.helpText')}
            disabled={store.isLoading || store.isSaving}
          />

          <!-- Range Filter Species Count Display -->
          <div class="form-control">
            <div class="label justify-start">
              <span class="label-text"
                >{t('settings.main.sections.rangeFilter.speciesCount.label')}</span
              >
            </div>
            <div class="flex items-center space-x-2">
              <div class="flex items-center space-x-2">
                <div
                  class="text-lg font-bold text-primary"
                  class:opacity-60={rangeFilterState.testing}
                >
                  {rangeFilterState.speciesCount !== null
                    ? rangeFilterState.speciesCount
                    : t('settings.main.sections.rangeFilter.speciesCount.loading')}
                </div>
                {#if rangeFilterState.testing}
                  <span
                    class="loading loading-spinner loading-xs text-primary opacity-60"
                    aria-label={t('common.loading')}
                  ></span>
                {/if}
              </div>
              <button
                type="button"
                class="btn btn-sm btn-outline"
                disabled={!rangeFilterState.speciesCount || rangeFilterState.loading}
                onclick={() => {
                  rangeFilterState.showModal = true;
                  loadRangeFilterSpecies();
                }}
              >
                {#if rangeFilterState.loading}
                  <span class="loading loading-spinner loading-xs mr-1"></span>
                  {t('settings.main.sections.rangeFilter.speciesCount.loading')}
                {:else}
                  {t('settings.main.sections.rangeFilter.speciesCount.viewSpecies')}
                {/if}
              </button>
            </div>
            <div class="label">
              <span class="label-text-alt"
                >{t('settings.main.sections.rangeFilter.speciesCount.helpText')}</span
              >
            </div>

            {#if rangeFilterState.error}
              <div class="alert alert-error mt-2" role="alert">
                {@html alertIconsSvg.error}
                <span>{rangeFilterState.error}</span>
                <button
                  type="button"
                  class="btn btn-sm btn-ghost ml-auto"
                  aria-label="Dismiss error"
                  onclick={() => (rangeFilterState.error = null)}
                >
                  {@html navigationIcons.close}
                </button>
              </div>
            {/if}
          </div>
        </div>
      </div>
    </div>
  </SettingsSection>

  <!-- Database Output Settings Section -->
  <SettingsSection
    title={t('settings.main.sections.database.title')}
    description={t('settings.main.sections.database.description')}
    defaultOpen={true}
    hasChanges={outputSettingsHasChanges}
  >
    <div class="space-y-6">
      <!-- Database Type Selection -->
      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6">
        <SelectField
          id="database-type"
          bind:value={selectedDatabaseType}
          label={t('settings.main.sections.database.type.label')}
          options={[
            { value: 'sqlite', label: t('settings.main.sections.database.type.options.sqlite') },
            { value: 'mysql', label: t('settings.main.sections.database.type.options.mysql') },
          ]}
          helpText={t('settings.main.sections.database.type.helpText')}
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateDatabaseType(value as 'sqlite' | 'mysql')}
        />
      </div>

      <!-- SQLite Settings -->
      {#if selectedDatabaseType === 'sqlite'}
        <div class="space-y-4">
          <SettingsNote>
            <span>{t('settings.main.sections.database.sqlite.note')}</span>
          </SettingsNote>

          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6">
            <TextInput
              id="sqlite-path"
              value={settings.output.sqlite.path}
              label={t('settings.main.sections.database.sqlite.path.label')}
              placeholder={t('settings.main.sections.database.sqlite.path.placeholder')}
              helpText={t('settings.main.sections.database.sqlite.path.helpText')}
              disabled={store.isLoading || store.isSaving}
              onchange={path => updateSQLiteSettings({ path })}
            />
          </div>
        </div>
      {/if}

      <!-- MySQL Settings -->
      {#if selectedDatabaseType === 'mysql'}
        <div class="space-y-4">
          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <TextInput
              id="mysql-host"
              value={settings.output.mysql.host}
              label={t('settings.main.sections.database.mysql.host.label')}
              placeholder={t('settings.main.sections.database.mysql.host.placeholder')}
              helpText={t('settings.main.sections.database.mysql.host.helpText')}
              disabled={store.isLoading || store.isSaving}
              onchange={host => updateMySQLSettings({ host })}
            />

            <TextInput
              id="mysql-port"
              value={settings.output.mysql.port}
              label={t('settings.main.sections.database.mysql.port.label')}
              placeholder="3306"
              helpText={t('settings.main.sections.database.mysql.port.helpText')}
              disabled={store.isLoading || store.isSaving}
              onchange={port => updateMySQLSettings({ port })}
            />

            <TextInput
              id="mysql-username"
              value={settings.output.mysql.username}
              label={t('settings.main.sections.database.mysql.username.label')}
              placeholder={t('settings.main.sections.database.mysql.username.placeholder')}
              helpText={t('settings.main.sections.database.mysql.username.helpText')}
              disabled={store.isLoading || store.isSaving}
              onchange={username => updateMySQLSettings({ username })}
            />

            <PasswordField
              id="mysql-password"
              value={settings.output.mysql.password}
              label={t('settings.main.sections.database.mysql.password.label')}
              placeholder={t('settings.main.sections.database.mysql.password.placeholder')}
              helpText={t('settings.main.sections.database.mysql.password.helpText')}
              disabled={store.isLoading || store.isSaving}
              onUpdate={password => updateMySQLSettings({ password })}
            />

            <TextInput
              id="mysql-database"
              value={settings.output.mysql.database}
              label={t('settings.main.sections.database.mysql.database.label')}
              placeholder={t('settings.main.sections.database.mysql.database.placeholder')}
              helpText={t('settings.main.sections.database.mysql.database.helpText')}
              disabled={store.isLoading || store.isSaving}
              onchange={database => updateMySQLSettings({ database })}
            />
          </div>
        </div>
      {/if}
    </div>
  </SettingsSection>

  <!-- User Interface Settings Section -->
  <SettingsSection
    title={t('settings.main.sections.userInterface.title')}
    description={t('settings.main.sections.userInterface.description')}
    defaultOpen={true}
    hasChanges={dashboardSettingsHasChanges}
  >
    <div class="space-y-6">
      <div>
        <h4 class="text-lg font-medium pb-2">
          {t('settings.main.sections.userInterface.language.title')}
        </h4>
        <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6">
          <SelectField
            id="ui-locale"
            value={settings.dashboard.locale}
            label={t('settings.main.sections.userInterface.language.locale.label')}
            options={uiLocales}
            helpText={t('settings.main.sections.userInterface.language.locale.helpText')}
            disabled={store.isLoading || store.isSaving}
            onchange={updateUILocale}
          />
        </div>
      </div>

      <div>
        <h4 class="text-lg font-medium pb-2 mt-6">
          {t('settings.main.sections.userInterface.dashboard.title')}
        </h4>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
          <NumberField
            label={t('settings.main.sections.userInterface.dashboard.summaryLimit.label')}
            value={settings.dashboard.summaryLimit}
            onUpdate={value => updateDashboardSetting('summaryLimit', value)}
            min={10}
            max={1000}
            helpText={t('settings.main.sections.userInterface.dashboard.summaryLimit.helpText')}
            disabled={store.isLoading || store.isSaving}
          />
        </div>

        <div class="mt-4">
          <Checkbox
            checked={settings.dashboard.thumbnails.summary}
            label={t('settings.main.sections.userInterface.dashboard.thumbnails.summary.label')}
            helpText={t(
              'settings.main.sections.userInterface.dashboard.thumbnails.summary.helpText'
            )}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateThumbnailSetting('summary', value)}
          />

          <Checkbox
            checked={settings.dashboard.thumbnails.recent}
            label={t('settings.main.sections.userInterface.dashboard.thumbnails.recent.label')}
            helpText={t(
              'settings.main.sections.userInterface.dashboard.thumbnails.recent.helpText'
            )}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateThumbnailSetting('recent', value)}
          />

          <div class:opacity-50={!multipleProvidersAvailable}>
            <SelectField
              id="image-provider"
              value={settings.dashboard.thumbnails.imageProvider}
              label={t(
                'settings.main.sections.userInterface.dashboard.thumbnails.imageProvider.label'
              )}
              options={providerOptions.data}
              helpText={t(
                'settings.main.sections.userInterface.dashboard.thumbnails.imageProvider.helpText'
              )}
              disabled={store.isLoading ||
                store.isSaving ||
                !multipleProvidersAvailable ||
                providerOptions.loading}
              onchange={value => updateThumbnailSetting('imageProvider', value)}
            />
          </div>

          {#if multipleProvidersAvailable}
            <SelectField
              id="fallback-policy"
              value={settings.dashboard.thumbnails.fallbackPolicy}
              label={t(
                'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.label'
              )}
              options={[
                {
                  value: 'all',
                  label: t(
                    'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.options.all'
                  ),
                },
                {
                  value: 'none',
                  label: t(
                    'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.options.none'
                  ),
                },
              ]}
              helpText={t(
                'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.helpText'
              )}
              disabled={store.isLoading || store.isSaving}
              onchange={value => updateThumbnailSetting('fallbackPolicy', value)}
            />
          {/if}
        </div>
      </div>
    </div>
  </SettingsSection>
</main>

<!-- Map Modal -->
{#if mapModalOpen}
  <div
    class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center"
    style:z-index="9999"
    role="dialog"
    aria-modal="true"
    aria-labelledby="map-modal-title"
    tabindex="-1"
    onclick={e => e.target === e.currentTarget && closeMapModal()}
    onkeydown={e => e.key === 'Escape' && closeMapModal()}
  >
    <div
      class="bg-base-100 rounded-lg p-4 max-w-[95vw] max-h-[95vh] w-full h-full md:max-w-[90vw] md:max-h-[90vh] overflow-hidden flex flex-col"
      role="document"
    >
      <div class="flex justify-between items-center mb-4">
        <h3 id="map-modal-title" class="text-lg font-bold">
          {t('settings.main.sections.rangeFilter.stationLocation.label')}
        </h3>
        <button
          type="button"
          class="btn btn-sm btn-circle btn-ghost"
          aria-label="Close modal"
          onclick={closeMapModal}
        >
          {@html navigationIcons.close}
        </button>
      </div>

      <div class="mb-3 text-sm text-base-content/70">
        <div class="grid grid-cols-2 gap-4">
          <div>
            <span class="font-medium">{t('settings.main.sections.rangeFilter.latitude.label')}</span
            >
            <span> {settings.birdnet.latitude}</span>
          </div>
          <div>
            <span class="font-medium"
              >{t('settings.main.sections.rangeFilter.longitude.label')}</span
            >
            <span> {settings.birdnet.longitude}</span>
          </div>
        </div>
      </div>

      <div class="flex-1 min-h-0">
        <div
          bind:this={modalMapElement}
          class="w-full h-full rounded-lg border border-base-300 relative"
          role="application"
          aria-label="Full screen map for selecting station location"
        >
          <!-- Map will be initialized here when modal opens -->
          {#if mapLibraryLoading}
            <div
              class="absolute inset-0 flex items-center justify-center bg-base-100 bg-opacity-75 rounded-lg"
            >
              <div class="flex flex-col items-center gap-2">
                <span class="loading loading-spinner loading-lg" aria-hidden="true"></span>
                <span class="text-sm text-base-content" role="status" aria-live="assertive"
                  >Loading map...</span
                >
              </div>
            </div>
          {/if}
        </div>
      </div>

      <div class="flex justify-between items-center mt-4 pt-4 border-t">
        <div class="flex gap-2">
          <button
            type="button"
            class="btn btn-sm btn-circle"
            aria-label="Zoom in"
            aria-disabled={!modalMap || mapLibraryLoading}
            disabled={!modalMap || mapLibraryLoading}
            onclick={() => modalMap?.zoomIn({ duration: 300 })}
          >
            +
          </button>
          <button
            type="button"
            class="btn btn-sm btn-circle"
            aria-label="Zoom out"
            aria-disabled={!modalMap || mapLibraryLoading}
            disabled={!modalMap || mapLibraryLoading}
            onclick={() => modalMap?.zoomOut({ duration: 300 })}
          >
            -
          </button>
        </div>
        <div class="text-sm text-base-content/60">
          💡 {t('settings.main.sections.rangeFilter.stationLocation.helpText')}
        </div>
        <button type="button" class="btn btn-primary" onclick={closeMapModal}>
          {t('common.done')}
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- Range Filter Species Modal -->
{#if rangeFilterState.showModal}
  <div
    class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center"
    style:z-index="9999"
    role="dialog"
    aria-modal="true"
    aria-labelledby="modal-title"
    tabindex="-1"
    onclick={e => e.target === e.currentTarget && (rangeFilterState.showModal = false)}
    onkeydown={e => e.key === 'Escape' && (rangeFilterState.showModal = false)}
  >
    <div
      class="bg-base-100 rounded-lg p-6 max-w-4xl max-h-[80vh] overflow-hidden flex flex-col"
      role="document"
    >
      <div class="flex justify-between items-center mb-4">
        <h3 id="modal-title" class="text-lg font-bold">
          {t('settings.main.sections.rangeFilter.modal.title')}
        </h3>
        <button
          type="button"
          class="btn btn-sm btn-circle btn-ghost"
          aria-label="Close modal"
          onclick={() => (rangeFilterState.showModal = false)}
        >
          {@html navigationIcons.close}
        </button>
      </div>

      <div class="mb-4 text-sm text-base-content/70">
        <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
          <div>
            <span class="font-medium"
              >{t('settings.main.sections.rangeFilter.modal.speciesCount')}</span
            >
            <span> {rangeFilterState.speciesCount}</span>
          </div>
          <div>
            <span class="font-medium"
              >{t('settings.main.sections.rangeFilter.modal.threshold')}</span
            >
            <span> {settings.birdnet.rangeFilter.threshold}</span>
          </div>
          <div>
            <span class="font-medium">{t('settings.main.sections.rangeFilter.modal.latitude')}</span
            >
            <span> {settings.birdnet.latitude}</span>
          </div>
          <div>
            <span class="font-medium"
              >{t('settings.main.sections.rangeFilter.modal.longitude')}</span
            >
            <span> {settings.birdnet.longitude}</span>
          </div>
        </div>
      </div>

      {#if rangeFilterState.error}
        <div class="alert alert-error mb-4" role="alert">
          {@html alertIconsSvg.error}
          <span>{rangeFilterState.error}</span>
          <button
            type="button"
            class="btn btn-sm btn-ghost ml-auto"
            aria-label="Dismiss error"
            onclick={() => (rangeFilterState.error = null)}
          >
            {@html navigationIcons.close}
          </button>
        </div>
      {/if}

      <div class="flex-1 overflow-auto">
        {#if rangeFilterState.loading}
          <div class="text-center py-8">
            <div class="loading loading-spinner loading-lg"></div>
            <p class="mt-2">{t('settings.main.sections.rangeFilter.modal.loadingSpecies')}</p>
          </div>
        {:else if rangeFilterState.species.length > 0}
          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2">
            {#each rangeFilterState.species as species}
              <div class="p-2 rounded hover:bg-base-200">
                <div class="font-medium">{species.commonName}</div>
                <div class="text-sm text-base-content/70">{species.scientificName}</div>
              </div>
            {/each}
          </div>
        {:else}
          <div class="text-center py-8 text-base-content/60">
            {t('settings.main.sections.rangeFilter.modal.noSpeciesFound')}
          </div>
        {/if}
      </div>

      <div class="flex justify-end items-center mt-4 pt-4 border-t">
        <button
          type="button"
          class="btn btn-outline"
          onclick={() => (rangeFilterState.showModal = false)}
        >
          {t('settings.main.sections.rangeFilter.modal.close')}
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- MapLibre GL JS is imported via npm package in script section -->
