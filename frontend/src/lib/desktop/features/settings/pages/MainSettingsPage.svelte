<!--
  Main Settings Page Component

  Purpose: Main configuration settings for BirdNET-Go including node settings,
  BirdNET parameters, database configuration, and location-based filtering.

  Features:
  - Tabbed interface: General, Detection, Location, Database
  - Node identification and settings
  - BirdNET analysis parameters (sensitivity, threshold, overlap)
  - Dynamic threshold configuration
  - Range filter with interactive map (lazy loaded)
  - Database type selection (SQLite/MySQL)

  Props: None - This is a page component that uses global settings stores

  Performance Optimizations:
  - Map lazy loading - only initialized when Location tab is active
  - Cached CSRF token with $derived to avoid repeated DOM queries
  - Reactive computed properties for change detection
  - Async API loading for non-critical data

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
    dynamicThresholdSettings,
    outputSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import { api, ApiError, getCsrfToken } from '$lib/utils/api';
  import { toastActions } from '$lib/stores/toast';
  import {
    Settings,
    Radar,
    MapPin,
    Database,
    XCircle,
    X,
    Maximize2,
    Download,
  } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import {
    MAP_CONFIG,
    createMapStyle as createMapStyleFromConfig,
    getInitialZoom,
  } from '../utils/mapConfig';

  const logger = loggers.settings;

  // Tab state
  let activeTab = $state('general');

  // PERFORMANCE OPTIMIZATION: Reactive settings with proper defaults
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
  });

  let store = $derived($settingsStore);

  // Database type selection
  let selectedDatabaseType = $state('sqlite');

  $effect(() => {
    if (settings.output.mysql.enabled) {
      selectedDatabaseType = 'mysql';
    } else if (settings.output.sqlite.enabled) {
      selectedDatabaseType = 'sqlite';
    } else {
      selectedDatabaseType = 'sqlite';
    }
  });

  // Change detection per tab
  let generalTabHasChanges = $derived(
    hasSettingsChanged((store.originalData as any)?.main, (store.formData as any)?.main) ||
      hasSettingsChanged(
        {
          sensitivity: (store.originalData as any)?.birdnet?.sensitivity,
          threshold: (store.originalData as any)?.birdnet?.threshold,
          overlap: (store.originalData as any)?.birdnet?.overlap,
          locale: (store.originalData as any)?.birdnet?.locale,
          threads: (store.originalData as any)?.birdnet?.threads,
        },
        {
          sensitivity: (store.formData as any)?.birdnet?.sensitivity,
          threshold: (store.formData as any)?.birdnet?.threshold,
          overlap: (store.formData as any)?.birdnet?.overlap,
          locale: (store.formData as any)?.birdnet?.locale,
          threads: (store.formData as any)?.birdnet?.threads,
        }
      )
  );

  let detectionTabHasChanges = $derived(
    hasSettingsChanged(
      {
        modelPath: (store.originalData as any)?.birdnet?.modelPath,
        labelPath: (store.originalData as any)?.birdnet?.labelPath,
      },
      {
        modelPath: (store.formData as any)?.birdnet?.modelPath,
        labelPath: (store.formData as any)?.birdnet?.labelPath,
      }
    ) ||
      hasSettingsChanged(
        (store.originalData as any)?.realtime?.dynamicThreshold,
        (store.formData as any)?.realtime?.dynamicThreshold
      )
  );

  let locationTabHasChanges = $derived(
    hasSettingsChanged(
      {
        latitude: (store.originalData as any)?.birdnet?.latitude,
        longitude: (store.originalData as any)?.birdnet?.longitude,
        rangeFilter: (store.originalData as any)?.birdnet?.rangeFilter,
      },
      {
        latitude: (store.formData as any)?.birdnet?.latitude,
        longitude: (store.formData as any)?.birdnet?.longitude,
        rangeFilter: (store.formData as any)?.birdnet?.rangeFilter,
      }
    )
  );

  let databaseTabHasChanges = $derived(
    hasSettingsChanged((store.originalData as any)?.output, (store.formData as any)?.output)
  );

  // API State Management
  interface ApiState<T> {
    loading: boolean;
    error: string | null;
    data: T;
  }

  let birdnetLocales = $state<ApiState<Array<{ value: string; label: string }>>>({
    loading: true,
    error: null,
    data: [],
  });

  // Range filter state
  let rangeFilterState = $state<{
    speciesCount: number | null;
    loading: boolean;
    testing: boolean;
    downloading: boolean;
    error: string | null;
    showModal: boolean;
    species: any[];
  }>({
    speciesCount: null,
    loading: false,
    testing: false,
    downloading: false,
    error: null,
    showModal: false,
    species: [],
  });

  // Focus management for modal accessibility
  let previouslyFocusedElement: HTMLElement | null = null;

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

  function handleFocusTrap(event: KeyboardEvent, modal: HTMLElement) {
    if (event.key !== 'Tab') return;

    const focusableElements = getFocusableElements(modal);
    if (focusableElements.length === 0) return;

    const firstElement = focusableElements[0];
    const lastElement = focusableElements[focusableElements.length - 1];

    if (event.shiftKey) {
      if (document.activeElement === firstElement) {
        event.preventDefault();
        lastElement.focus();
      }
    } else {
      if (document.activeElement === lastElement) {
        event.preventDefault();
        firstElement.focus();
      }
    }
  }

  $effect(() => {
    if (rangeFilterState.showModal) {
      previouslyFocusedElement = document.activeElement as HTMLElement;

      setTimeout(() => {
        const modal = document.querySelector(
          '[role="dialog"][aria-labelledby="modal-title"]'
        ) as HTMLElement;
        if (modal) {
          const focusableElements = getFocusableElements(modal);
          if (focusableElements.length > 0) {
            focusableElements[0].focus();
          } else {
            modal.focus();
          }

          const trapHandler = (event: KeyboardEvent) => handleFocusTrap(event, modal);
          modal.addEventListener('keydown', trapHandler);

          return () => {
            modal.removeEventListener('keydown', trapHandler);
          };
        }
      }, 0);
    } else if (previouslyFocusedElement) {
      previouslyFocusedElement.focus();
      previouslyFocusedElement = null;
    }
  });

  // Map state - LAZY LOADED when Location tab is active
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

  // Load initial data
  $effect(() => {
    loadInitialData();
  });

  // LAZY LOADING: Initialize map only when Location tab becomes active
  $effect(() => {
    const isLocationTab = activeTab === 'location';
    const hasActualCoordinates =
      $birdnetSettings &&
      $birdnetSettings.latitude !== undefined &&
      $birdnetSettings.longitude !== undefined;

    if (
      isLocationTab &&
      !store.isLoading &&
      mapElement &&
      !mapInitialized &&
      hasActualCoordinates
    ) {
      logger.debug('Location tab active - initializing map lazily');
      initializeMap();
      mapInitialized = true;
    }
  });

  let initialLoadComplete = $state(false);

  $effect(() => {
    if (!store.isLoading && mapInitialized && !initialLoadComplete) {
      initialLoadComplete = true;
    }
  });

  // Update map when coordinates change via input fields
  let coordinateUpdateTimer: ReturnType<typeof setTimeout> | undefined;

  $effect(() => {
    if (initialLoadComplete && map && !mapModalOpen) {
      const lat = settings.birdnet.latitude;
      const lng = settings.birdnet.longitude;

      clearTimeout(coordinateUpdateTimer);
      coordinateUpdateTimer = setTimeout(() => {
        if (lat !== undefined && lng !== undefined && !isNaN(lat) && !isNaN(lng)) {
          const currentZoom = map!.getZoom();
          map!.easeTo({
            center: [lng, lat],
            zoom: currentZoom,
            duration: 300,
          });

          if (marker) {
            marker.setLngLat([lng, lat]);
          } else if (maplibregl && (lat !== 0 || lng !== 0)) {
            marker = new maplibregl.Marker({ draggable: true }).setLngLat([lng, lat]).addTo(map!);
            marker.on('dragend', () => {
              const lngLat = marker!.getLngLat();
              updateMarker(lngLat.lat, lngLat.lng);
            });
          }
        }
      }, 500);
    }

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

  // Clean up map when switching away from Location tab
  // This ensures the map reinitializes when returning to the tab
  $effect(() => {
    const isLocationTab = activeTab === 'location';

    if (!isLocationTab && map) {
      logger.debug('Leaving Location tab - cleaning up map');
      map.remove();
      map = null;
      marker = null;
      mapInitialized = false;
    }
  });

  // Modal map lifecycle
  $effect(() => {
    let cleanup: (() => void) | undefined;

    if (mapModalOpen && modalMapElement && initialLoadComplete) {
      initializeModalMap().then(cleanupFn => {
        cleanup = cleanupFn;
      });
    }

    return () => {
      if (cleanup) {
        cleanup();
      }
      if (!mapModalOpen && modalMap) {
        modalMap = null;
        modalMarker = null;
      }
    };
  });

  async function loadInitialData() {
    await Promise.all([loadBirdnetLocales(), loadRangeFilterCount()]);
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
      birdnetLocales.data = [{ value: 'en', label: 'English' }];
    } finally {
      birdnetLocales.loading = false;
    }
  }

  const createMapStyle = createMapStyleFromConfig;

  async function initializeMap() {
    if (!mapElement || mapInitialized) return;

    try {
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
          logger.error('Failed to load MapLibre GL JS:', importError);
          toastActions.error('Failed to load map library. Please refresh the page.');
          mapInitialized = false;
          return;
        }
      }

      const initialLat = $birdnetSettings?.latitude ?? 0;
      const initialLng = $birdnetSettings?.longitude ?? 0;
      const initialZoom = getInitialZoom(initialLat, initialLng);

      map = new maplibregl.Map({
        container: mapElement,
        style: createMapStyle(),
        center: [initialLng, initialLat],
        zoom: initialZoom,
        scrollZoom: MAP_CONFIG.SCROLL_ZOOM,
        keyboard: MAP_CONFIG.KEYBOARD_NAV,
        fadeDuration: MAP_CONFIG.FADE_DURATION,
        pitchWithRotate: MAP_CONFIG.PITCH_WITH_ROTATE,
        touchZoomRotate: MAP_CONFIG.TOUCH_ZOOM_ROTATE,
      });

      map.on('load', () => {
        if (map) {
          map.resize();
        }
      });

      const handleWheel = (e: globalThis.WheelEvent) => {
        if ((e.ctrlKey || e.metaKey) && map) {
          e.preventDefault();
          if (e.deltaY > 0) {
            map.zoomOut({ duration: 300 });
          } else {
            map.zoomIn({ duration: 300 });
          }
        }
      };
      mapElement.addEventListener('wheel', handleWheel as globalThis.EventListener, false);

      if ((initialLat !== 0 || initialLng !== 0) && maplibregl) {
        marker = new maplibregl.Marker({ draggable: true })
          .setLngLat([initialLng, initialLat])
          .addTo(map);

        marker.on('dragend', () => {
          const lngLat = marker!.getLngLat();
          updateMarker(lngLat.lat, lngLat.lng);
        });
      }

      if (map) {
        map.on('click', (e: import('maplibre-gl').MapMouseEvent) => {
          const lngLat = e.lngLat;
          updateMarker(lngLat.lat, lngLat.lng);
          const currentZoom = map?.getZoom();
          map?.easeTo({
            center: [lngLat.lng, lngLat.lat],
            zoom: currentZoom,
            duration: MAP_CONFIG.ANIMATION_DURATION,
          });
        });
      }
    } catch (error) {
      logger.error('Failed to initialize map:', error);
      toastActions.error('Failed to load map. Please try refreshing the page.');
      mapInitialized = false;
    }
  }

  function updateMarker(lat: number, lng: number) {
    if (!map) return;

    const roundedLat = parseFloat(lat.toFixed(3));
    const roundedLng = parseFloat(lng.toFixed(3));

    settingsActions.updateSection('birdnet', {
      latitude: roundedLat,
      longitude: roundedLng,
    });

    updateMapView(roundedLat, roundedLng);
    debouncedTestRangeFilter();
  }

  function updateMapView(lat: number, lng: number) {
    if (!map) return;

    const currentZoom = map.getZoom();
    map.easeTo({
      center: [lng, lat],
      zoom: currentZoom,
      duration: MAP_CONFIG.ANIMATION_DURATION,
    });

    if (marker) {
      marker.setLngLat([lng, lat]);
    } else if (maplibregl) {
      marker = new maplibregl.Marker({ draggable: true }).setLngLat([lng, lat]).addTo(map);
      marker.on('dragend', () => {
        const lngLat = marker!.getLngLat();
        updateMarker(lngLat.lat, lngLat.lng);
      });
    }

    if (modalMap) {
      const modalCurrentZoom = modalMap.getZoom();
      modalMap.easeTo({
        center: [lng, lat],
        zoom: modalCurrentZoom,
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
          const roundedLat = parseFloat(lngLat.lat.toFixed(3));
          const roundedLng = parseFloat(lngLat.lng.toFixed(3));

          settingsActions.updateSection('birdnet', {
            latitude: roundedLat,
            longitude: roundedLng,
          });

          if (map) {
            const mainCurrentZoom = map.getZoom();
            map.easeTo({
              center: [roundedLng, roundedLat],
              zoom: mainCurrentZoom,
              duration: MAP_CONFIG.ANIMATION_DURATION,
            });
            if (marker) {
              marker.setLngLat([roundedLng, roundedLat]);
            }
          }

          debouncedTestRangeFilter();
        });
      }
    }
  }

  async function initializeModalMap() {
    if (!modalMapElement || modalMap || !maplibregl) return;

    const handleModalWheel = (e: globalThis.WheelEvent) => {
      if (modalMap) {
        e.preventDefault();
        if (e.deltaY > 0) {
          modalMap.zoomOut({ duration: 300 });
        } else {
          modalMap.zoomIn({ duration: 300 });
        }
      }
    };

    try {
      const currentLat = $birdnetSettings?.latitude ?? 0;
      const currentLng = $birdnetSettings?.longitude ?? 0;
      const currentZoom = map?.getZoom() || getInitialZoom(currentLat, currentLng);

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

      if (modalMap) {
        modalMap.scrollZoom.enable();
      }
      modalMapElement.addEventListener(
        'wheel',
        handleModalWheel as globalThis.EventListener,
        false
      );

      if (currentLat !== 0 || currentLng !== 0) {
        modalMarker = new maplibregl!.Marker({ draggable: true })
          .setLngLat([currentLng, currentLat])
          .addTo(modalMap);

        modalMarker.on('dragend', () => {
          const lngLat = modalMarker!.getLngLat();
          const roundedLat = parseFloat(lngLat.lat.toFixed(3));
          const roundedLng = parseFloat(lngLat.lng.toFixed(3));

          settingsActions.updateSection('birdnet', {
            latitude: roundedLat,
            longitude: roundedLng,
          });

          if (map) {
            const mainCurrentZoom = map.getZoom();
            map.easeTo({
              center: [roundedLng, roundedLat],
              zoom: mainCurrentZoom,
              duration: MAP_CONFIG.ANIMATION_DURATION,
            });
            if (marker) {
              marker.setLngLat([roundedLng, roundedLat]);
            }
          }

          debouncedTestRangeFilter();
        });
      }

      if (modalMap) {
        modalMap.on('click', (e: import('maplibre-gl').MapMouseEvent) => {
          const lngLat = e.lngLat;
          const roundedLat = parseFloat(lngLat.lat.toFixed(3));
          const roundedLng = parseFloat(lngLat.lng.toFixed(3));

          settingsActions.updateSection('birdnet', {
            latitude: roundedLat,
            longitude: roundedLng,
          });

          if (modalMarker) {
            modalMarker.setLngLat([roundedLng, roundedLat]);
          }

          if (map) {
            const mainCurrentZoom = map.getZoom();
            map.easeTo({
              center: [roundedLng, roundedLat],
              zoom: mainCurrentZoom,
              duration: MAP_CONFIG.ANIMATION_DURATION,
            });
            if (marker) {
              marker.setLngLat([roundedLng, roundedLat]);
            }
          }

          debouncedTestRangeFilter();
        });
      }
    } catch (error) {
      logger.error('Failed to initialize modal map:', error);
      toastActions.error('Failed to load modal map. Please try closing and reopening the modal.');
      mapModalOpen = false;
      return () => {};
    }

    return () => {
      modalMapElement?.removeEventListener(
        'wheel',
        handleModalWheel as globalThis.EventListener,
        false
      );
      if (modalMap) {
        modalMap.remove();
      }
    };
  }

  function openMapModal() {
    mapModalOpen = true;
  }

  function closeMapModal() {
    mapModalOpen = false;
  }

  // Range filter functions
  let debounceTimer: any;
  let loadingDelayTimer: any;

  function debouncedTestRangeFilter() {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      testCurrentRangeFilter();
    }, 150);
  }

  async function loadRangeFilterCount() {
    try {
      const response = await fetch('/api/v2/range/species/count');
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

    clearTimeout(loadingDelayTimer);

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
      rangeFilterState.speciesCount = null;
    } finally {
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

  $effect(() => {
    const lat = settings.birdnet.latitude;
    const lng = settings.birdnet.longitude;

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

  async function downloadSpeciesCSV() {
    if (rangeFilterState.downloading) return;

    try {
      rangeFilterState.downloading = true;

      const params = new URLSearchParams({
        latitude: settings.birdnet.latitude.toString(),
        longitude: settings.birdnet.longitude.toString(),
        threshold: settings.birdnet.rangeFilter.threshold.toString(),
      });

      const response = await fetch(`/api/v2/range/species/csv?${params}`, {
        headers: {
          'X-CSRF-Token': getCsrfToken() || '',
          Accept: 'text/csv',
        },
      });

      if (!response.ok) {
        let msg = 'Failed to download CSV';
        try {
          const data = await response.clone().json();
          if (data?.message) msg = data.message;
        } catch {
          // ignore
        }
        throw new Error(msg);
      }

      const cd =
        response.headers.get('Content-Disposition') ||
        response.headers.get('content-disposition') ||
        '';
      let filename = 'birdnet_species.csv';
      const fnStar = cd.match(/filename\*\s*=\s*([^']*)''([^;]+)/i);
      if (fnStar && fnStar[2]) {
        try {
          filename = decodeURIComponent(fnStar[2]);
        } catch {
          /* keep default */
        }
      } else {
        const fn = cd.match(/filename\s*=\s*"([^"]+)"/i) || cd.match(/filename\s*=\s*([^;]+)/i);
        if (fn && fn[1]) filename = fn[1].trim();
      }

      const blob = await response.blob();
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      window.URL.revokeObjectURL(url);

      toastActions.success(t('settings.main.sections.rangeFilter.csvDownloaded'));
    } catch (error) {
      logger.error('Failed to download species CSV:', error);
      toastActions.error(t('settings.main.sections.rangeFilter.csvDownloadFailed'));
    } finally {
      rangeFilterState.downloading = false;
    }
  }

  // Tab definitions with content snippets
  let tabs: TabDefinition[] = $derived([
    {
      id: 'general',
      label: t('settings.main.tabs.general'),
      icon: Settings,
      hasChanges: generalTabHasChanges,
      content: generalTabContent,
    },
    {
      id: 'detection',
      label: t('settings.main.tabs.detection'),
      icon: Radar,
      hasChanges: detectionTabHasChanges,
      content: detectionTabContent,
    },
    {
      id: 'location',
      label: t('settings.main.tabs.location'),
      icon: MapPin,
      hasChanges: locationTabHasChanges,
      content: locationTabContent,
    },
    {
      id: 'database',
      label: t('settings.main.tabs.database'),
      icon: Database,
      hasChanges: databaseTabHasChanges,
      content: databaseTabContent,
    },
  ]);
</script>

<!-- Tab Content Snippets -->
{#snippet generalTabContent()}
  <div class="space-y-6">
    <!-- Node Name Card -->
    <SettingsSection
      title={t('settings.main.sections.main.title')}
      description={t('settings.main.sections.main.description')}
      originalData={(store.originalData as any)?.main}
      currentData={(store.formData as any)?.main}
    >
      <TextInput
        id="node-name"
        value={settings.main.name}
        label={t('settings.main.fields.nodeName.label')}
        placeholder={t('settings.main.fields.nodeName.placeholder')}
        helpText={t('settings.main.fields.nodeName.helpText')}
        disabled={store.isLoading || store.isSaving}
        onchange={updateMainName}
      />
    </SettingsSection>

    <!-- BirdNET Parameters Card -->
    <SettingsSection
      title={t('settings.main.sections.birdnet.title')}
      description={t('settings.main.sections.birdnet.description')}
      originalData={{
        sensitivity: (store.originalData as any)?.birdnet?.sensitivity,
        threshold: (store.originalData as any)?.birdnet?.threshold,
        overlap: (store.originalData as any)?.birdnet?.overlap,
        locale: (store.originalData as any)?.birdnet?.locale,
        threads: (store.originalData as any)?.birdnet?.threads,
      }}
      currentData={{
        sensitivity: settings.birdnet.sensitivity,
        threshold: settings.birdnet.threshold,
        overlap: settings.birdnet.overlap,
        locale: settings.birdnet.locale,
        threads: settings.birdnet.threads,
      }}
    >
      <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
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
    </SettingsSection>
  </div>
{/snippet}

{#snippet detectionTabContent()}
  <div class="space-y-6">
    <!-- Custom BirdNET Classifier Card -->
    <SettingsSection
      title={t('settings.main.sections.customClassifier.title')}
      description={t('settings.main.sections.customClassifier.description')}
      originalData={{
        modelPath: (store.originalData as any)?.birdnet?.modelPath,
        labelPath: (store.originalData as any)?.birdnet?.labelPath,
      }}
      currentData={{
        modelPath: settings.birdnet.modelPath,
        labelPath: settings.birdnet.labelPath,
      }}
    >
      <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
        <TextInput
          id="model-path"
          value={settings.birdnet.modelPath ?? ''}
          label={t('settings.main.sections.customClassifier.modelPath.label')}
          placeholder={t('settings.main.sections.customClassifier.modelPath.placeholder')}
          helpText={t('settings.main.sections.customClassifier.modelPath.helpText')}
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateBirdnetSetting('modelPath', value)}
        />

        <TextInput
          id="label-path"
          value={settings.birdnet.labelPath ?? ''}
          label={t('settings.main.sections.customClassifier.labelPath.label')}
          placeholder={t('settings.main.sections.customClassifier.labelPath.placeholder')}
          helpText={t('settings.main.sections.customClassifier.labelPath.helpText')}
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateBirdnetSetting('labelPath', value)}
        />
      </div>
    </SettingsSection>

    <!-- Dynamic Threshold Card -->
    <SettingsSection
      title={t('settings.main.sections.dynamicThreshold.title')}
      description={t('settings.main.sections.dynamicThreshold.description')}
      originalData={(store.originalData as any)?.realtime?.dynamicThreshold}
      currentData={(store.formData as any)?.realtime?.dynamicThreshold}
    >
      <Checkbox
        checked={settings.dynamicThreshold.enabled}
        label={t('settings.main.sections.dynamicThreshold.enable.label')}
        helpText={t('settings.main.sections.dynamicThreshold.enable.helpText')}
        disabled={store.isLoading || store.isSaving}
        onchange={value => updateDynamicThreshold('enabled', value)}
      />

      {#if settings.dynamicThreshold.enabled}
        <div class="grid grid-cols-1 md:grid-cols-2 gap-6 mt-4">
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
    </SettingsSection>
  </div>
{/snippet}

{#snippet locationTabContent()}
  <div class="space-y-6">
    <!-- Station Location Card -->
    <SettingsSection
      title={t('settings.main.sections.rangeFilter.stationLocation.label')}
      description={t('settings.main.sections.rangeFilter.stationLocation.helpText')}
      originalData={{
        latitude: (store.originalData as any)?.birdnet?.latitude,
        longitude: (store.originalData as any)?.birdnet?.longitude,
      }}
      currentData={{
        latitude: settings.birdnet.latitude,
        longitude: settings.birdnet.longitude,
      }}
    >
      <!-- Coordinates -->
      <div class="grid grid-cols-1 md:grid-cols-2 gap-6 mb-4">
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

      <!-- Map -->
      <div class="form-control">
        <div
          bind:this={mapElement}
          id="location-map"
          class="h-[350px] rounded-xl border border-base-300 relative overflow-hidden"
          role="application"
          aria-label="Map for selecting station location"
        >
          {#if mapLibraryLoading}
            <div
              class="absolute inset-0 flex items-center justify-center bg-base-100/75 rounded-xl"
            >
              <div class="flex flex-col items-center gap-2">
                <span class="loading loading-spinner loading-lg" aria-hidden="true"></span>
                <span class="text-sm text-base-content">Loading map...</span>
              </div>
            </div>
          {/if}
        </div>
        <div class="flex gap-2 mt-3">
          <button
            type="button"
            class="btn btn-sm btn-circle btn-ghost bg-base-300"
            aria-label="Zoom in"
            disabled={!map || mapLibraryLoading}
            onclick={() => map?.zoomIn({ duration: 300 })}
          >
            +
          </button>
          <button
            type="button"
            class="btn btn-sm btn-circle btn-ghost bg-base-300"
            aria-label="Zoom out"
            disabled={!map || mapLibraryLoading}
            onclick={() => map?.zoomOut({ duration: 300 })}
          >
            -
          </button>
          <button
            type="button"
            class="btn btn-sm btn-circle btn-ghost bg-base-300"
            aria-label="Expand map to full screen"
            disabled={!map || mapLibraryLoading}
            onclick={openMapModal}
          >
            <Maximize2 class="size-4" />
          </button>
        </div>
        <p class="text-xs text-info mt-2">
          Hold Ctrl (or Cmd on Mac) + scroll to zoom. Click to set location.
        </p>
      </div>
    </SettingsSection>

    <!-- Range Filter Card -->
    <SettingsSection
      title={t('settings.main.sections.rangeFilter.title')}
      description={t('settings.main.sections.rangeFilter.description')}
      originalData={(store.originalData as any)?.birdnet?.rangeFilter}
      currentData={settings.birdnet.rangeFilter}
    >
      <!-- Threshold and Species Count on same row -->
      <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
        <!-- Threshold Setting -->
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

        <!-- Species Count Display -->
        <div class="form-control">
          <div class="label justify-start">
            <span class="label-text"
              >{t('settings.main.sections.rangeFilter.speciesCount.label')}</span
            >
          </div>
          <div class="flex items-center gap-3">
            <div
              class="text-2xl font-bold text-primary tabular-nums"
              class:opacity-60={rangeFilterState.testing}
            >
              {rangeFilterState.speciesCount !== null
                ? rangeFilterState.speciesCount.toLocaleString()
                : '—'}
            </div>
            {#if rangeFilterState.testing}
              <span class="loading loading-spinner loading-sm text-primary"></span>
            {/if}
          </div>
          <div class="flex gap-2 mt-2">
            <button
              type="button"
              class="btn btn-sm btn-outline"
              disabled={!rangeFilterState.speciesCount || rangeFilterState.loading}
              onclick={() => {
                rangeFilterState.showModal = true;
                loadRangeFilterSpecies();
              }}
            >
              {t('settings.main.sections.rangeFilter.speciesCount.viewSpecies')}
            </button>
            <button
              type="button"
              class="btn btn-sm btn-primary"
              disabled={!rangeFilterState.speciesCount || rangeFilterState.downloading}
              onclick={downloadSpeciesCSV}
              aria-label={t('common.aria.downloadCsv')}
            >
              <Download class="size-4" />
              {t('analytics.filters.exportCsv')}
            </button>
          </div>
          <span class="help-text mt-1">
            {t('settings.main.sections.rangeFilter.speciesCount.helpText')}
          </span>
        </div>
      </div>

      {#if rangeFilterState.error}
        <div class="alert alert-error mt-4" role="alert">
          <XCircle class="size-5 shrink-0" />
          <span>{rangeFilterState.error}</span>
          <button
            type="button"
            class="btn btn-sm btn-ghost ml-auto"
            onclick={() => (rangeFilterState.error = null)}
          >
            <X class="size-4" />
          </button>
        </div>
      {/if}
    </SettingsSection>
  </div>
{/snippet}

{#snippet databaseTabContent()}
  <div class="space-y-6">
    <!-- Database Settings Card -->
    <SettingsSection
      title={t('settings.main.sections.database.title')}
      description={t('settings.main.sections.database.description')}
      originalData={(store.originalData as any)?.output}
      currentData={(store.formData as any)?.output}
    >
      <div class="space-y-6">
        <!-- Database Type Selector -->
        <div class="max-w-md">
          <SelectField
            id="database-type"
            bind:value={selectedDatabaseType}
            label={t('settings.main.sections.database.type.label')}
            options={[
              { value: 'sqlite', label: 'SQLite' },
              { value: 'mysql', label: 'MySQL' },
            ]}
            helpText={t('settings.main.sections.database.type.helpText')}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateDatabaseType(value as 'sqlite' | 'mysql')}
          />
        </div>

        <!-- SQLite Settings -->
        {#if selectedDatabaseType === 'sqlite'}
          <SettingsNote>
            {t('settings.main.sections.database.sqlite.note')}
          </SettingsNote>

          <div class="max-w-md">
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
        {/if}

        <!-- MySQL Settings -->
        {#if selectedDatabaseType === 'mysql'}
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
        {/if}
      </div>
    </SettingsSection>
  </div>
{/snippet}

<!-- Main Content -->
<main class="settings-page-content" aria-label="Main settings configuration">
  <SettingsTabs {tabs} bind:activeTab />
</main>

<!-- Map Modal -->
{#if mapModalOpen}
  <div
    class="fixed inset-0 bg-black/50 flex items-center justify-center backdrop-blur-sm"
    style:z-index="9999"
    role="dialog"
    aria-modal="true"
    aria-labelledby="map-modal-title"
    tabindex="-1"
    onclick={e => e.target === e.currentTarget && closeMapModal()}
    onkeydown={e => e.key === 'Escape' && closeMapModal()}
  >
    <div
      class="bg-base-100 rounded-2xl p-6 max-w-[95vw] max-h-[95vh] w-full h-full md:max-w-[90vw] md:max-h-[90vh] overflow-hidden flex flex-col shadow-2xl"
      role="document"
    >
      <div class="flex justify-between items-center mb-4">
        <h3 id="map-modal-title" class="text-xl font-semibold">
          {t('settings.main.sections.rangeFilter.stationLocation.label')}
        </h3>
        <button
          type="button"
          class="btn btn-sm btn-circle btn-ghost"
          aria-label="Close modal"
          onclick={closeMapModal}
        >
          <X class="size-5" />
        </button>
      </div>

      <div class="mb-4 p-3 bg-base-200/50 rounded-lg">
        <div class="grid grid-cols-2 gap-4 text-sm">
          <div>
            <span class="text-base-content/60"
              >{t('settings.main.sections.rangeFilter.latitude.label')}</span
            >
            <span class="font-medium ml-2">{settings.birdnet.latitude}</span>
          </div>
          <div>
            <span class="text-base-content/60"
              >{t('settings.main.sections.rangeFilter.longitude.label')}</span
            >
            <span class="font-medium ml-2">{settings.birdnet.longitude}</span>
          </div>
        </div>
      </div>

      <div class="flex-1 min-h-0">
        <div
          bind:this={modalMapElement}
          class="w-full h-full rounded-xl border border-base-300 relative overflow-hidden"
          role="application"
          aria-label="Full screen map for selecting station location"
        >
          {#if mapLibraryLoading}
            <div
              class="absolute inset-0 flex items-center justify-center bg-base-100/75 rounded-xl"
            >
              <div class="flex flex-col items-center gap-2">
                <span class="loading loading-spinner loading-lg" aria-hidden="true"></span>
                <span class="text-sm text-base-content">Loading map...</span>
              </div>
            </div>
          {/if}
        </div>
      </div>

      <div class="flex justify-between items-center mt-4 pt-4 border-t border-base-200">
        <div class="flex gap-2">
          <button
            type="button"
            class="btn btn-sm btn-circle btn-ghost bg-base-200"
            aria-label="Zoom in"
            disabled={!modalMap || mapLibraryLoading}
            onclick={() => modalMap?.zoomIn({ duration: 300 })}
          >
            +
          </button>
          <button
            type="button"
            class="btn btn-sm btn-circle btn-ghost bg-base-200"
            aria-label="Zoom out"
            disabled={!modalMap || mapLibraryLoading}
            onclick={() => modalMap?.zoomOut({ duration: 300 })}
          >
            -
          </button>
        </div>
        <p class="text-sm text-base-content/60">
          Click on the map or drag the marker to set location
        </p>
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
    class="fixed inset-0 bg-black/50 flex items-center justify-center backdrop-blur-sm"
    style:z-index="9999"
    role="dialog"
    aria-modal="true"
    aria-labelledby="modal-title"
    tabindex="-1"
    onclick={e => e.target === e.currentTarget && (rangeFilterState.showModal = false)}
    onkeydown={e => e.key === 'Escape' && (rangeFilterState.showModal = false)}
  >
    <div
      class="bg-base-100 rounded-2xl p-6 max-w-4xl max-h-[80vh] overflow-hidden flex flex-col shadow-2xl"
      role="document"
    >
      <div class="flex justify-between items-center mb-4">
        <h3 id="modal-title" class="text-xl font-semibold">
          {t('settings.main.sections.rangeFilter.modal.title')}
        </h3>
        <button
          type="button"
          class="btn btn-sm btn-circle btn-ghost"
          aria-label="Close modal"
          onclick={() => (rangeFilterState.showModal = false)}
        >
          <X class="size-5" />
        </button>
      </div>

      <div class="mb-4 p-3 bg-base-200/50 rounded-lg">
        <div class="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
          <div>
            <span class="text-base-content/60"
              >{t('settings.main.sections.rangeFilter.modal.speciesCount')}</span
            >
            <span class="font-medium ml-1">{rangeFilterState.speciesCount}</span>
          </div>
          <div>
            <span class="text-base-content/60"
              >{t('settings.main.sections.rangeFilter.modal.threshold')}</span
            >
            <span class="font-medium ml-1">{settings.birdnet.rangeFilter.threshold}</span>
          </div>
          <div>
            <span class="text-base-content/60"
              >{t('settings.main.sections.rangeFilter.modal.latitude')}</span
            >
            <span class="font-medium ml-1">{settings.birdnet.latitude}</span>
          </div>
          <div>
            <span class="text-base-content/60"
              >{t('settings.main.sections.rangeFilter.modal.longitude')}</span
            >
            <span class="font-medium ml-1">{settings.birdnet.longitude}</span>
          </div>
        </div>
      </div>

      {#if rangeFilterState.error}
        <div class="alert alert-error mb-4" role="alert">
          <XCircle class="size-5 shrink-0" />
          <span>{rangeFilterState.error}</span>
          <button
            type="button"
            class="btn btn-sm btn-ghost ml-auto"
            onclick={() => (rangeFilterState.error = null)}
          >
            <X class="size-4" />
          </button>
        </div>
      {/if}

      <div class="flex-1 overflow-auto">
        {#if rangeFilterState.loading}
          <div class="text-center py-12">
            <span class="loading loading-spinner loading-lg"></span>
            <p class="mt-3 text-base-content/90">
              {t('settings.main.sections.rangeFilter.modal.loadingSpecies')}
            </p>
          </div>
        {:else if rangeFilterState.species.length > 0}
          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2">
            {#each rangeFilterState.species as species (species.scientificName)}
              <div class="p-3 rounded-lg hover:bg-base-200/50 transition-colors">
                <div class="font-medium">{species.commonName}</div>
                <div class="text-sm text-base-content/60 italic">{species.scientificName}</div>
              </div>
            {/each}
          </div>
        {:else}
          <div class="text-center py-12 text-base-content/60">
            {t('settings.main.sections.rangeFilter.modal.noSpeciesFound')}
          </div>
        {/if}
      </div>

      <div class="flex justify-between items-center mt-4 pt-4 border-t border-base-200">
        <button
          type="button"
          class="btn btn-sm btn-primary"
          onclick={downloadSpeciesCSV}
          disabled={rangeFilterState.loading ||
            rangeFilterState.downloading ||
            !rangeFilterState.speciesCount}
          aria-label={t('common.aria.downloadCsv')}
        >
          <Download class="size-4" />
          {t('analytics.filters.exportCsv')}
        </button>
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
