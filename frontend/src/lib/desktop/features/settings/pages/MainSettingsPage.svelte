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
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import FlagIcon, { type FlagLocale } from '$lib/desktop/components/ui/FlagIcon.svelte';
  import WeatherIcon, { type WeatherProvider } from '$lib/desktop/components/ui/WeatherIcon.svelte';
  import DatabaseIcon, { type DatabaseType } from '$lib/desktop/components/ui/DatabaseIcon.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { cn } from '$lib/utils/cn.js';
  import PasswordField from '$lib/desktop/components/forms/PasswordField.svelte';
  import MultiStageOperation from '$lib/desktop/components/ui/MultiStageOperation.svelte';
  import type { Stage } from '$lib/desktop/components/ui/MultiStageOperation.types';
  import TestSuccessNote from '$lib/desktop/components/ui/TestSuccessNote.svelte';
  import SettingsButton from '$lib/desktop/features/settings/components/SettingsButton.svelte';
  import {
    settingsStore,
    settingsActions,
    mainSettings,
    birdnetSettings,
    dynamicThresholdSettings,
    outputSettings,
    dashboardSettings,
    realtimeSettings,
    DEFAULT_SPECTROGRAM_SETTINGS,
    type SpectrogramPreRender,
    type SpectrogramStyle,
    type SpectrogramDynamicRange,
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
    RefreshCw,
  } from '@lucide/svelte';
  import { t, getLocale } from '$lib/i18n';
  import { LOCALES } from '$lib/i18n/config';
  import { loggers } from '$lib/utils/logger';
  import { safeArrayAccess } from '$lib/utils/security';
  import { formatBytes } from '$lib/utils/formatters';
  import { wundergroundDefaults, weatherDefaults } from '$lib/utils/weatherDefaults';
  import {
    MAP_CONFIG,
    createMapStyle as createMapStyleFromConfig,
    getInitialZoom,
  } from '../utils/mapConfig';

  const logger = loggers.settings;

  // Tab state
  let activeTab = $state('general');

  // Extended option type for locale with typed locale code
  interface LocaleOption extends SelectOption {
    localeCode: FlagLocale;
  }

  // PERFORMANCE OPTIMIZATION: Static UI locales computed once
  const uiLocales: LocaleOption[] = Object.entries(LOCALES).map(([code, info]) => ({
    value: code,
    label: info.name,
    localeCode: code as FlagLocale,
  }));

  // Spectrogram style definitions with value→translationKey mapping
  const SPECTROGRAM_STYLES: { value: SpectrogramStyle; labelKey: string }[] = [
    { value: 'default', labelKey: 'default' },
    { value: 'scientific_dark', labelKey: 'scientificDark' },
    { value: 'high_contrast_dark', labelKey: 'highContrastDark' },
    { value: 'scientific', labelKey: 'scientific' },
  ];

  // Spectrogram style options - computed reactively to support locale changes
  let spectrogramStyleOptions = $derived.by(() => {
    getLocale(); // Trigger re-computation on locale change
    return SPECTROGRAM_STYLES.map(style => ({
      value: style.value,
      label: t(
        `settings.main.sections.userInterface.dashboard.spectrogram.style.options.${style.labelKey}`
      ),
    }));
  });

  // Get translation key for style description
  function getStyleDescriptionKey(style: SpectrogramStyle): string {
    return SPECTROGRAM_STYLES.find(s => s.value === style)?.labelKey ?? 'default';
  }

  // Dynamic range preset definitions with value→translationKey mapping
  const DYNAMIC_RANGE_PRESETS: { value: SpectrogramDynamicRange; labelKey: string }[] = [
    { value: '80', labelKey: 'highContrast' },
    { value: '100', labelKey: 'standard' },
    { value: '120', labelKey: 'extended' },
  ];

  // Dynamic range options - computed reactively to support locale changes
  let dynamicRangeOptions = $derived.by(() => {
    getLocale(); // Trigger re-computation on locale change
    return DYNAMIC_RANGE_PRESETS.map(preset => ({
      value: preset.value,
      label: t(
        `settings.main.sections.userInterface.dashboard.spectrogram.dynamicRange.options.${preset.labelKey}`
      ),
    }));
  });

  // Get translation key for dynamic range description
  function getDynamicRangeDescriptionKey(value: SpectrogramDynamicRange): string {
    return DYNAMIC_RANGE_PRESETS.find(p => p.value === value)?.labelKey ?? 'standard';
  }

  // Extended option type for weather provider
  interface WeatherOption extends SelectOption {
    providerCode: WeatherProvider;
  }

  // Extended option type for BirdNET locale
  interface BirdnetLocaleOption extends SelectOption {
    localeCode: FlagLocale;
  }

  // Extended option type for database
  interface DatabaseOption extends SelectOption {
    databaseType: DatabaseType;
  }

  // Database options with icons
  const databaseOptions: DatabaseOption[] = [
    { value: 'sqlite', label: 'SQLite', databaseType: 'sqlite' },
    { value: 'mysql', label: 'MySQL', databaseType: 'mysql' },
  ];

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
    falsePositiveFilter: $realtimeSettings?.falsePositiveFilter ?? {
      level: 0,
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
      ...($dashboardSettings ?? {
        thumbnails: {
          summary: true,
          recent: true,
          imageProvider: 'wikimedia',
          fallbackPolicy: 'all',
        },
        summaryLimit: 100,
      }),
      locale: $dashboardSettings?.locale ?? (getLocale() as string),
      spectrogram: $dashboardSettings?.spectrogram ?? DEFAULT_SPECTROGRAM_SETTINGS,
    },
    weather: $realtimeSettings?.weather || weatherDefaults,
    sentry: $settingsStore.formData.sentry || {
      enabled: false,
      dsn: '',
      environment: 'production',
      includePrivateInfo: false,
    },
  });

  let store = $derived($settingsStore);

  // Current spectrogram style for preview
  let currentSpectrogramStyle = $derived<SpectrogramStyle>(
    (settings.dashboard.spectrogram?.style as SpectrogramStyle) ?? 'default'
  );

  // Current dynamic range for description
  let currentDynamicRange = $derived<SpectrogramDynamicRange>(
    (settings.dashboard.spectrogram?.dynamicRange as SpectrogramDynamicRange) ?? '100'
  );

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

  // Change detection per tab - General now includes UI settings and telemetry
  let generalTabHasChanges = $derived(
    hasSettingsChanged(store.originalData.main, store.formData.main) ||
      hasSettingsChanged(
        {
          locale: store.originalData.realtime?.dashboard?.locale,
          summaryLimit: store.originalData.realtime?.dashboard?.summaryLimit,
          thumbnails: store.originalData.realtime?.dashboard?.thumbnails,
          spectrogram: store.originalData.realtime?.dashboard?.spectrogram,
        },
        {
          locale: store.formData.realtime?.dashboard?.locale,
          summaryLimit: store.formData.realtime?.dashboard?.summaryLimit,
          thumbnails: store.formData.realtime?.dashboard?.thumbnails,
          spectrogram: store.formData.realtime?.dashboard?.spectrogram,
        }
      ) ||
      hasSettingsChanged(store.originalData.sentry, store.formData.sentry)
  );

  // Detection tab includes BirdNET params, False Positive Filter, and Range Filter
  let detectionTabHasChanges = $derived(
    hasSettingsChanged(
      {
        sensitivity: store.originalData.birdnet?.sensitivity,
        threshold: store.originalData.birdnet?.threshold,
        overlap: store.originalData.birdnet?.overlap,
        locale: store.originalData.birdnet?.locale,
        threads: store.originalData.birdnet?.threads,
        modelPath: store.originalData.birdnet?.modelPath,
        labelPath: store.originalData.birdnet?.labelPath,
        rangeFilter: store.originalData.birdnet?.rangeFilter,
      },
      {
        sensitivity: store.formData.birdnet?.sensitivity,
        threshold: store.formData.birdnet?.threshold,
        overlap: store.formData.birdnet?.overlap,
        locale: store.formData.birdnet?.locale,
        threads: store.formData.birdnet?.threads,
        modelPath: store.formData.birdnet?.modelPath,
        labelPath: store.formData.birdnet?.labelPath,
        rangeFilter: store.formData.birdnet?.rangeFilter,
      }
    ) ||
      hasSettingsChanged(
        store.originalData.realtime?.dynamicThreshold,
        store.formData.realtime?.dynamicThreshold
      ) ||
      hasSettingsChanged(
        store.originalData.realtime?.falsePositiveFilter,
        store.formData.realtime?.falsePositiveFilter
      )
  );

  // Location tab includes station location and weather settings
  let locationTabHasChanges = $derived(
    hasSettingsChanged(
      {
        latitude: store.originalData.birdnet?.latitude,
        longitude: store.originalData.birdnet?.longitude,
      },
      {
        latitude: store.formData.birdnet?.latitude,
        longitude: store.formData.birdnet?.longitude,
      }
    ) || hasSettingsChanged(store.originalData.realtime?.weather, store.formData.realtime?.weather)
  );

  let databaseTabHasChanges = $derived(
    hasSettingsChanged(store.originalData.output, store.formData.output)
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

  // Image provider options for dashboard thumbnails
  let providerOptions = $state<ApiState<Array<{ value: string; label: string }>>>({
    loading: true,
    error: null,
    data: [],
  });
  let multipleProvidersAvailable = $derived(providerOptions.data.length > 1);

  // Transform birdnetLocales to include flag locale code
  let birdnetLocaleOptions = $derived<BirdnetLocaleOption[]>(
    birdnetLocales.data.map(locale => ({
      value: locale.value,
      label: locale.label,
      localeCode: locale.value as FlagLocale,
    }))
  );

  // Weather test state
  let weatherTestState = $state<{
    stages: Stage[];
    isRunning: boolean;
    showSuccessNote: boolean;
  }>({
    stages: [],
    isRunning: false,
    showSuccessNote: false,
  });

  // Database stats interface and state
  interface DatabaseStats {
    type: DatabaseType;
    size_bytes: number;
    total_detections: number;
    connected: boolean;
    location: string;
  }

  let databaseStats = $state<{
    loading: boolean;
    error: string | null;
    data: DatabaseStats | null;
  }>({
    loading: false,
    error: null,
    data: null,
  });

  // Species type for range filter API responses
  interface RangeFilterSpecies {
    commonName?: string;
    scientificName?: string;
    label?: string;
  }

  // Range filter state
  let rangeFilterState = $state<{
    speciesCount: number | null;
    loading: boolean;
    testing: boolean;
    downloading: boolean;
    error: string | null;
    showModal: boolean;
    species: RangeFilterSpecies[];
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
    await Promise.all([loadBirdnetLocales(), loadRangeFilterCount(), loadImageProviders()]);
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

  async function loadImageProviders() {
    providerOptions.loading = true;
    providerOptions.error = null;

    try {
      const providersData = await api.get<{
        providers?: Array<{ value: string; display: string }>;
      }>('/api/v2/settings/imageproviders');

      // Map v2 API response format to client format
      providerOptions.data = (providersData?.providers || []).map(
        (provider: { value: string; display: string }) => ({
          value: provider.value,
          label: provider.display,
        })
      );
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

  // Load database statistics from the API
  async function loadDatabaseStats() {
    databaseStats.loading = true;
    databaseStats.error = null;

    try {
      const stats = await api.get<DatabaseStats>('/api/v2/system/database/stats');
      databaseStats.data = stats;
    } catch (error) {
      if (error instanceof ApiError) {
        databaseStats.error = error.message;
      } else if (error instanceof Error) {
        databaseStats.error = error.message;
      } else {
        databaseStats.error = 'Failed to load database statistics';
      }
      logger.error('Failed to load database stats:', error);
    } finally {
      databaseStats.loading = false;
    }
  }

  // Load database stats when database tab becomes active (only on first load, not after errors)
  $effect(() => {
    if (
      activeTab === 'database' &&
      !databaseStats.data &&
      !databaseStats.loading &&
      !databaseStats.error
    ) {
      loadDatabaseStats();
    }
  });

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
  let debounceTimer: ReturnType<typeof setTimeout> | undefined;
  let loadingDelayTimer: ReturnType<typeof setTimeout> | undefined;

  function debouncedTestRangeFilter() {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      testCurrentRangeFilter();
    }, 150);
  }

  async function loadRangeFilterCount() {
    try {
      interface CountResponse {
        count: number;
      }
      const data = await api.get<CountResponse>('/api/v2/range/species/count');
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
      const data = await api.post<{ count: number; species?: RangeFilterSpecies[] }>(
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

      const data = await api.get<{ count: number; species: RangeFilterSpecies[] }>(
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

  function updateBirdnetSetting(key: string, value: string | number | boolean | null) {
    settingsActions.updateSection('birdnet', { [key]: value });
  }

  function updateDynamicThreshold(key: string, value: number | boolean) {
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

  // Dashboard settings update handlers
  function updateDashboardSetting(key: string, value: string | number | boolean) {
    settingsActions.updateSection('realtime', {
      dashboard: { ...settings.dashboard, [key]: value },
    });
  }

  function updateThumbnailSetting(key: string, value: string | boolean) {
    settingsActions.updateSection('realtime', {
      dashboard: {
        ...settings.dashboard,
        thumbnails: { ...settings.dashboard.thumbnails, [key]: value },
      },
    });
  }

  function updateSpectrogramSetting(key: keyof SpectrogramPreRender, value: boolean | string) {
    settingsActions.updateSection('realtime', {
      dashboard: {
        ...settings.dashboard,
        spectrogram: { ...settings.dashboard.spectrogram, [key]: value },
      },
    });
  }

  function updateUILocale(locale: string) {
    settingsActions.updateSection('realtime', {
      dashboard: { ...settings.dashboard, locale },
    });
  }

  // Telemetry update handler
  function updateTelemetryEnabled(enabled: boolean) {
    settingsActions.updateSection('sentry', {
      ...settings.sentry,
      enabled,
    });
  }

  // False Positive Filter helpers and update handler
  // Minimum overlap values must match backend: internal/analysis/processor/false_positive_filter.go
  const falsePositiveFilterLevels = [
    {
      value: 0,
      name: 'Off',
      description: 'No filtering - accepts first detection immediately',
      minOverlap: 0.0,
    },
    {
      value: 1,
      name: 'Lenient',
      description: '~2 confirmations required. For low-quality audio (RTSP cameras, webcam mics)',
      minOverlap: 2.0,
    },
    {
      value: 2,
      name: 'Moderate',
      description: '~3 confirmations required. Balanced for typical hobby setups',
      minOverlap: 2.2,
    },
    {
      value: 3,
      name: 'Balanced',
      description: '~5 confirmations required. Original pre-Sept 2025 behavior',
      minOverlap: 2.4,
    },
    {
      value: 4,
      name: 'Strict',
      description: '~12 confirmations required. RPi 4+ needed. For high-quality microphones',
      minOverlap: 2.7,
    },
    {
      value: 5,
      name: 'Maximum',
      description: '~21 confirmations required. RPi 4+ needed. For professional-grade microphones',
      minOverlap: 2.8,
    },
  ];

  function getFalsePositiveFilterLevelName(level: number): string {
    return safeArrayAccess(falsePositiveFilterLevels, level)?.name ?? 'Unknown';
  }

  function getFalsePositiveFilterDescription(level: number): string {
    return safeArrayAccess(falsePositiveFilterLevels, level)?.description ?? '';
  }

  function getMinimumOverlapForLevel(level: number): number {
    return safeArrayAccess(falsePositiveFilterLevels, level)?.minOverlap ?? 0.0;
  }

  function getFalsePositiveFilterBadgeClass(level: number): string {
    switch (level) {
      case 0:
        return 'badge-ghost'; // Off - muted/neutral
      case 1:
        return 'badge-success'; // Lenient - green (easy on resources)
      case 2:
        return 'badge-info'; // Moderate - blue
      case 3:
        return 'badge-warning'; // Balanced - yellow/amber
      case 4:
        return 'badge-error'; // Strict - red (requires RPi 4+)
      case 5:
        return 'badge-error'; // Maximum - red (requires RPi 4+)
      default:
        return 'badge-ghost';
    }
  }

  function updateFalsePositiveFilterLevel(level: number) {
    // Get minimum overlap required for this level
    const minOverlap = getMinimumOverlapForLevel(level);
    const currentOverlap = settings.birdnet.overlap;

    // Update the filter level
    settingsActions.updateSection('realtime', {
      falsePositiveFilter: { level },
    });

    // Auto-adjust overlap if current value is below minimum required
    if (currentOverlap < minOverlap) {
      settingsActions.updateSection('birdnet', { overlap: minOverlap });
      toastActions.info(
        t('settings.main.sections.falsePositiveFilter.overlapAdjusted', {
          overlap: minOverlap.toFixed(1),
        })
      );
    }
  }

  // Weather update handlers
  function updateWeatherProvider(provider: string) {
    settingsActions.updateSection('realtime', {
      weather: {
        ...settings.weather,
        provider: provider as 'none' | 'yrno' | 'openweather' | 'wunderground',
      },
    });
  }

  function updateWeatherApiKey(apiKey: string) {
    settingsActions.updateSection('realtime', {
      weather: { ...settings.weather, openWeather: { ...settings.weather.openWeather, apiKey } },
    });
  }

  function updateWundergroundSetting(key: keyof typeof wundergroundDefaults, value: string) {
    settingsActions.updateSection('realtime', {
      weather: {
        ...settings.weather,
        wunderground: {
          ...(settings.weather?.wunderground ?? wundergroundDefaults),
          [key]: value,
        },
      },
    });
  }

  // Weather test function
  async function testWeather() {
    weatherTestState.isRunning = true;
    weatherTestState.stages = [];

    try {
      // Get current form values (unsaved changes) instead of saved settings
      const currentWeather = store.formData?.realtime?.weather || settings.weather;

      // Prepare test payload
      const testPayload = {
        provider: currentWeather.provider || 'none',
        pollInterval: currentWeather.pollInterval || 60,
        debug: currentWeather.debug || false,
        openWeather: {
          apiKey: currentWeather.openWeather?.apiKey || '',
          endpoint: currentWeather.openWeather?.endpoint || '',
          units: currentWeather.openWeather?.units || 'metric',
          language: currentWeather.openWeather?.language || 'en',
        },
        wunderground: {
          apiKey: currentWeather.wunderground?.apiKey ?? '',
          stationId: currentWeather.wunderground?.stationId ?? '',
          endpoint: currentWeather.wunderground?.endpoint ?? '',
          units: currentWeather.wunderground?.units ?? 'm',
        },
      };

      // Make request to the real API with CSRF token
      const headers = new Headers({
        'Content-Type': 'application/json',
      });

      const token = getCsrfToken();
      if (token) {
        headers.set('X-CSRF-Token', token);
      }

      const response = await fetch('/api/v2/integrations/weather/test', {
        method: 'POST',
        headers,
        credentials: 'same-origin',
        body: JSON.stringify(testPayload),
      });

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      // Read the streaming response
      const reader = response.body?.getReader();
      const decoder = new TextDecoder();

      if (!reader) {
        throw new Error('Failed to read response stream');
      }

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        // Parse each line as JSON
        const chunk = decoder.decode(value);
        const lines = chunk.split('\n').filter(line => line.trim());

        for (const line of lines) {
          try {
            const stageResult = JSON.parse(line);

            // Find existing stage or create new one
            let existingIndex = weatherTestState.stages.findIndex(s => s.id === stageResult.id);
            if (existingIndex === -1) {
              // Add new stage
              weatherTestState.stages.push({
                id: stageResult.id,
                title: stageResult.title,
                status: stageResult.status,
                message: stageResult.message,
                error: stageResult.error,
              });
            } else {
              // Update existing stage safely
              const existingStage = safeArrayAccess(weatherTestState.stages, existingIndex);
              if (
                existingStage &&
                existingIndex >= 0 &&
                existingIndex < weatherTestState.stages.length
              ) {
                weatherTestState.stages.splice(existingIndex, 1, {
                  ...existingStage,
                  status: stageResult.status,
                  message: stageResult.message,
                  error: stageResult.error,
                });
              }
            }
          } catch (parseError) {
            logger.error('Failed to parse stage result:', parseError, line);
          }
        }
      }
    } catch (error) {
      logger.error('Weather test failed:', error);

      // Add error stage if no stages exist
      if (weatherTestState.stages.length === 0) {
        weatherTestState.stages.push({
          id: 'error',
          title: 'Connection Error',
          status: 'error',
          error: error instanceof Error ? error.message : 'Unknown error occurred',
        });
      } else {
        // Mark current stage as failed
        const lastIndex = weatherTestState.stages.length - 1;
        const lastStage = safeArrayAccess(weatherTestState.stages, lastIndex);
        if (lastStage && lastStage.status === 'in_progress') {
          const updatedStage = {
            ...lastStage,
            status: 'error' as const,
            error: error instanceof Error ? error.message : 'Unknown error occurred',
          };
          weatherTestState.stages.splice(lastIndex, 1, updatedStage);
        }
      }
    } finally {
      weatherTestState.isRunning = false;

      // Check if all stages completed successfully and there are unsaved changes
      const allStagesCompleted =
        weatherTestState.stages.length > 0 &&
        weatherTestState.stages.every(stage => stage.status === 'completed');
      weatherTestState.showSuccessNote = allStagesCompleted && locationTabHasChanges;

      setTimeout(() => {
        weatherTestState.stages = [];
        weatherTestState.showSuccessNote = false;
      }, 15000);
    }
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
      id: 'location',
      label: t('settings.main.tabs.location'),
      icon: MapPin,
      hasChanges: locationTabHasChanges,
      content: locationTabContent,
    },
    {
      id: 'detection',
      label: t('settings.main.tabs.detection'),
      icon: Radar,
      hasChanges: detectionTabHasChanges,
      content: detectionTabContent,
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
    <!-- Node Identity Card -->
    <SettingsSection
      title={t('settings.main.sections.main.title')}
      description={t('settings.main.sections.main.description')}
      originalData={store.originalData.main}
      currentData={store.formData.main}
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

    <!-- Interface Card (combines UI settings + display settings) -->
    <SettingsSection
      title={t('settings.main.sections.interface.title')}
      description={t('settings.main.sections.interface.description')}
      originalData={{
        locale: store.originalData.realtime?.dashboard?.locale,
        summaryLimit: store.originalData.realtime?.dashboard?.summaryLimit,
      }}
      currentData={{
        locale: store.formData.realtime?.dashboard?.locale,
        summaryLimit: store.formData.realtime?.dashboard?.summaryLimit,
      }}
    >
      <div class="space-y-6">
        <!-- Language Settings -->
        <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
          <SelectDropdown
            options={uiLocales}
            value={settings.dashboard.locale}
            label={t('settings.main.sections.userInterface.interface.locale.label')}
            helpText={t('settings.main.sections.userInterface.interface.locale.helpText')}
            disabled={store.isLoading || store.isSaving}
            variant="select"
            groupBy={false}
            onChange={value => updateUILocale(value as string)}
          >
            {#snippet renderOption(option)}
              {@const localeOption = option as LocaleOption}
              <div class="flex items-center gap-2">
                <FlagIcon locale={localeOption.localeCode} className="size-4" />
                <span>{localeOption.label}</span>
              </div>
            {/snippet}
            {#snippet renderSelected(options)}
              {@const localeOption = options[0] as LocaleOption}
              <span class="flex items-center gap-2">
                <FlagIcon locale={localeOption.localeCode} className="size-4" />
                <span>{localeOption.label}</span>
              </span>
            {/snippet}
          </SelectDropdown>
        </div>

        <!-- Summary Limit -->
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
      </div>
    </SettingsSection>

    <!-- Visual Content Card (combines bird images + spectrograms) -->
    <SettingsSection
      title={t('settings.main.sections.visualContent.title')}
      description={t('settings.main.sections.visualContent.description')}
      originalData={{
        thumbnails: store.originalData.realtime?.dashboard?.thumbnails,
        spectrogram: store.originalData.realtime?.dashboard?.spectrogram,
      }}
      currentData={{
        thumbnails: store.formData.realtime?.dashboard?.thumbnails,
        spectrogram: store.formData.realtime?.dashboard?.spectrogram,
      }}
    >
      <div class="space-y-6">
        <!-- Bird Images Section -->
        <div class="space-y-4">
          <h4 class="text-sm font-medium text-base-content/70">
            {t('settings.main.sections.userInterface.dashboard.birdImages.title')}
          </h4>

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

          <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
            <div class:opacity-50={!multipleProvidersAvailable}>
              <SelectDropdown
                options={providerOptions.data}
                value={settings.dashboard.thumbnails.imageProvider}
                label={t(
                  'settings.main.sections.userInterface.dashboard.thumbnails.imageProvider.label'
                )}
                helpText={t(
                  'settings.main.sections.userInterface.dashboard.thumbnails.imageProvider.helpText'
                )}
                disabled={store.isLoading ||
                  store.isSaving ||
                  !multipleProvidersAvailable ||
                  providerOptions.loading}
                variant="select"
                groupBy={false}
                menuSize="sm"
                onChange={value => updateThumbnailSetting('imageProvider', value as string)}
              />
            </div>

            {#if multipleProvidersAvailable}
              <SelectDropdown
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
                value={settings.dashboard.thumbnails.fallbackPolicy}
                label={t(
                  'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.label'
                )}
                helpText={t(
                  'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.helpText'
                )}
                disabled={store.isLoading || store.isSaving}
                variant="select"
                groupBy={false}
                menuSize="sm"
                onChange={value => updateThumbnailSetting('fallbackPolicy', value as string)}
              />
            {/if}
          </div>
        </div>

        <!-- Divider -->
        <div class="border-t border-base-200"></div>

        <!-- Spectrograms Section -->
        <div class="space-y-4">
          <h4 class="text-sm font-medium text-base-content/70">
            {t('settings.main.sections.userInterface.dashboard.spectrogram.title')}
          </h4>

          <!-- Generation Mode with contextual note -->
          <div class="space-y-3">
            <SelectDropdown
              options={[
                {
                  value: 'auto',
                  label: t(
                    'settings.main.sections.userInterface.dashboard.spectrogram.mode.auto.label'
                  ),
                },
                {
                  value: 'prerender',
                  label: t(
                    'settings.main.sections.userInterface.dashboard.spectrogram.mode.prerender.label'
                  ),
                },
                {
                  value: 'user-requested',
                  label: t(
                    'settings.main.sections.userInterface.dashboard.spectrogram.mode.userRequested.label'
                  ),
                },
              ]}
              value={settings.dashboard.spectrogram?.mode ?? 'auto'}
              label={t('settings.main.sections.userInterface.dashboard.spectrogram.mode.label')}
              disabled={store.isLoading || store.isSaving}
              variant="select"
              groupBy={false}
              menuSize="sm"
              onChange={value => updateSpectrogramSetting('mode', value as string)}
            />

            <!-- Mode-specific note directly under dropdown -->
            {#if (settings.dashboard.spectrogram?.mode ?? 'auto') === 'auto'}
              <SettingsNote>
                <span>
                  {t(
                    'settings.main.sections.userInterface.dashboard.spectrogram.mode.auto.helpText'
                  )}
                </span>
              </SettingsNote>
            {:else if (settings.dashboard.spectrogram?.mode ?? 'auto') === 'prerender'}
              <SettingsNote>
                <span>
                  {t(
                    'settings.main.sections.userInterface.dashboard.spectrogram.mode.prerender.helpText'
                  )}
                </span>
              </SettingsNote>
            {:else if (settings.dashboard.spectrogram?.mode ?? 'auto') === 'user-requested'}
              <SettingsNote>
                <span>
                  {t(
                    'settings.main.sections.userInterface.dashboard.spectrogram.mode.userRequested.helpText'
                  )}
                </span>
              </SettingsNote>
            {/if}
          </div>

          <!-- Style Selection as visual cards (full width row) -->
          <div class="mt-6">
            <span class="text-sm font-medium">
              {t('settings.main.sections.userInterface.dashboard.spectrogram.style.label')}
            </span>
            <p class="text-xs text-base-content/60 mt-1">
              {t('settings.main.sections.userInterface.dashboard.spectrogram.style.helpText')}
            </p>

            <!-- Style Cards Grid - 4 cards in a row with good sizing -->
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 mt-4">
              {#each spectrogramStyleOptions as style (style.value)}
                {@const isSelected = currentSpectrogramStyle === style.value}
                <button
                  type="button"
                  class="group relative flex flex-col items-center p-2 rounded-lg border transition-all focus:outline-none focus:ring-2 focus:ring-primary/50 {isSelected
                    ? 'border-primary/60 bg-primary/10 shadow-[0_0_0_1px_rgba(37,99,235,0.3)]'
                    : 'border-base-300 bg-base-100 hover:border-base-content/30'}"
                  disabled={store.isLoading || store.isSaving}
                  onclick={() => updateSpectrogramSetting('style', style.value)}
                >
                  <!-- Preview thumbnail -->
                  <img
                    src={`/ui/assets/images/spectrogram-preview-${style.value}.png`}
                    alt={style.label}
                    class="w-full aspect-[4/3] object-cover rounded"
                  />
                  <!-- Label -->
                  <span
                    class="mt-2 text-xs leading-tight text-center {isSelected
                      ? 'text-primary font-medium'
                      : 'text-base-content/70'}"
                  >
                    {style.label}
                  </span>
                </button>
              {/each}
            </div>

            <!-- Selected style description -->
            <p class="text-sm text-base-content/60 italic mt-4">
              {t(
                `settings.main.sections.userInterface.dashboard.spectrogram.style.descriptions.${getStyleDescriptionKey(currentSpectrogramStyle)}`
              )}
            </p>
          </div>

          <!-- Dynamic Range Selection -->
          <div class="mt-6">
            <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
              <SelectDropdown
                options={dynamicRangeOptions}
                value={currentDynamicRange}
                label={t(
                  'settings.main.sections.userInterface.dashboard.spectrogram.dynamicRange.label'
                )}
                helpText={t(
                  'settings.main.sections.userInterface.dashboard.spectrogram.dynamicRange.helpText'
                )}
                disabled={store.isLoading || store.isSaving}
                variant="select"
                groupBy={false}
                menuSize="sm"
                onChange={value => updateSpectrogramSetting('dynamicRange', value as string)}
              />
            </div>

            <!-- Selected dynamic range description -->
            <p class="text-sm text-base-content/60 italic mt-3">
              {t(
                `settings.main.sections.userInterface.dashboard.spectrogram.dynamicRange.descriptions.${getDynamicRangeDescriptionKey(currentDynamicRange)}`
              )}
            </p>
          </div>
        </div>
      </div>
    </SettingsSection>

    <!-- Privacy & Telemetry Card -->
    <SettingsSection
      title={t('settings.support.sections.telemetry.title')}
      description={t('settings.support.sections.telemetry.description')}
      originalData={store.originalData.sentry}
      currentData={store.formData.sentry}
    >
      <div class="space-y-4">
        <!-- Privacy Notice -->
        <div class="p-4 bg-base-200 rounded-lg shadow-xs">
          <div>
            <h4 class="font-bold">{t('settings.support.telemetry.privacyNotice')}</h4>
            <div class="text-sm mt-1">
              <ul class="list-disc list-inside mt-2 space-y-1">
                <li>{t('settings.support.telemetry.privacyPoints.noPersonalData')}</li>
                <li>{t('settings.support.telemetry.privacyPoints.anonymousData')}</li>
                <li>{t('settings.support.telemetry.privacyPoints.helpImprove')}</li>
              </ul>
            </div>
          </div>
        </div>

        <!-- Enable Error Tracking -->
        <Checkbox
          checked={settings.sentry.enabled}
          label={t('settings.support.telemetry.enableTracking')}
          disabled={store.isLoading || store.isSaving}
          onchange={enabled => updateTelemetryEnabled(enabled)}
        />
      </div>
    </SettingsSection>
  </div>
{/snippet}

{#snippet detectionTabContent()}
  <div class="space-y-6">
    <!-- BirdNET Parameters Card -->
    <SettingsSection
      title={t('settings.main.sections.birdnet.title')}
      description={t('settings.main.sections.birdnet.description')}
      originalData={{
        sensitivity: store.originalData.birdnet?.sensitivity,
        threshold: store.originalData.birdnet?.threshold,
        overlap: store.originalData.birdnet?.overlap,
        locale: store.originalData.birdnet?.locale,
        threads: store.originalData.birdnet?.threads,
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

        <SelectDropdown
          options={birdnetLocaleOptions}
          value={settings.birdnet.locale}
          label={t('settings.main.fields.locale.label')}
          helpText={t('settings.main.fields.locale.helpText')}
          disabled={store.isLoading || store.isSaving || birdnetLocales.loading}
          variant="select"
          groupBy={false}
          searchable={true}
          onChange={value => updateBirdnetSetting('locale', value as string)}
        >
          {#snippet renderOption(option)}
            {@const localeOption = option as BirdnetLocaleOption}
            <div class="flex items-center gap-2">
              <FlagIcon locale={localeOption.localeCode} className="size-4" />
              <span>{localeOption.label}</span>
            </div>
          {/snippet}
          {#snippet renderSelected(options)}
            {@const localeOption = options[0] as BirdnetLocaleOption}
            <span class="flex items-center gap-2">
              <FlagIcon locale={localeOption.localeCode} className="size-4" />
              <span>{localeOption.label}</span>
            </span>
          {/snippet}
        </SelectDropdown>

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

    <!-- False Positive Filter Card -->
    <SettingsSection
      title={t('settings.main.sections.falsePositiveFilter.title')}
      description={t('settings.main.sections.falsePositiveFilter.description')}
      originalData={store.originalData.realtime?.falsePositiveFilter}
      currentData={store.formData.realtime?.falsePositiveFilter}
    >
      <div class="space-y-4">
        <!-- Custom implementation with colored badge -->
        <div class="form-control min-w-0">
          <label for="false-positive-filter-level" class="label">
            <span class="label-text font-medium">
              {t('settings.main.sections.falsePositiveFilter.level.label')}
            </span>
            <span
              class={cn(
                'badge badge-sm font-medium',
                getFalsePositiveFilterBadgeClass(settings.falsePositiveFilter.level)
              )}
            >
              {getFalsePositiveFilterLevelName(settings.falsePositiveFilter.level)}
            </span>
          </label>
          <input
            id="false-positive-filter-level"
            type="range"
            class="range range-primary"
            min={0}
            max={5}
            step={1}
            value={settings.falsePositiveFilter.level}
            oninput={e => updateFalsePositiveFilterLevel(parseInt(e.currentTarget.value))}
            disabled={store.isLoading || store.isSaving}
          />
          <div class="label">
            <span class="label-text-alt">
              {getFalsePositiveFilterDescription(settings.falsePositiveFilter.level)}
            </span>
          </div>
        </div>

        <!-- Hardware note for strict/maximum levels -->
        {#if settings.falsePositiveFilter.level >= 4}
          <SettingsNote>
            <span>{t('settings.main.sections.falsePositiveFilter.hardwareNote')}</span>
          </SettingsNote>
        {/if}
      </div>
    </SettingsSection>

    <!-- Range Filter Card -->
    <SettingsSection
      title={t('settings.main.sections.rangeFilter.title')}
      description={t('settings.main.sections.rangeFilter.description')}
      originalData={store.originalData.birdnet?.rangeFilter}
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

    <!-- Custom BirdNET Classifier Card -->
    <SettingsSection
      title={t('settings.main.sections.customClassifier.title')}
      description={t('settings.main.sections.customClassifier.description')}
      originalData={{
        modelPath: store.originalData.birdnet?.modelPath,
        labelPath: store.originalData.birdnet?.labelPath,
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
      originalData={store.originalData.realtime?.dynamicThreshold}
      currentData={store.formData.realtime?.dynamicThreshold}
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
        latitude: store.originalData.birdnet?.latitude,
        longitude: store.originalData.birdnet?.longitude,
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

    <!-- Weather Provider Card -->
    <SettingsSection
      title={t('settings.integration.weather.title')}
      description={t('settings.integration.weather.description')}
      originalData={store.originalData.realtime?.weather}
      currentData={store.formData.realtime?.weather}
    >
      <div class="space-y-4">
        <SelectDropdown
          options={[
            {
              value: 'none',
              label: t('settings.integration.weather.provider.options.none'),
              providerCode: 'none',
            },
            {
              value: 'yrno',
              label: t('settings.integration.weather.provider.options.yrno'),
              providerCode: 'yrno',
            },
            {
              value: 'openweather',
              label: t('settings.integration.weather.provider.options.openweather'),
              providerCode: 'openweather',
            },
            {
              value: 'wunderground',
              label: t('settings.integration.weather.provider.options.wunderground'),
              providerCode: 'wunderground',
            },
          ] as WeatherOption[]}
          value={settings.weather.provider}
          label={t('settings.integration.weather.provider.label')}
          disabled={store.isLoading || store.isSaving}
          variant="select"
          groupBy={false}
          onChange={value => updateWeatherProvider(value as string)}
        >
          {#snippet renderOption(option)}
            {@const weatherOption = option as WeatherOption}
            <div class="flex items-center gap-2">
              <WeatherIcon provider={weatherOption.providerCode} className="size-4" />
              <span>{weatherOption.label}</span>
            </div>
          {/snippet}
          {#snippet renderSelected(options)}
            {@const weatherOption = options[0] as WeatherOption}
            <span class="flex items-center gap-2">
              <WeatherIcon provider={weatherOption.providerCode} className="size-4" />
              <span>{weatherOption.label}</span>
            </span>
          {/snippet}
        </SelectDropdown>

        <!-- Provider-specific notes -->
        {#if settings.weather.provider === 'none'}
          <SettingsNote>
            <span>{t('settings.integration.weather.notes.none')}</span>
          </SettingsNote>
        {:else if settings.weather.provider === 'yrno'}
          <SettingsNote>
            <p>
              {t('settings.integration.weather.notes.yrno.description')}
            </p>
            <p class="mt-2">
              {@html t('settings.integration.weather.notes.yrno.freeService')}
            </p>
          </SettingsNote>
        {:else if settings.weather.provider === 'openweather'}
          <SettingsNote>
            <span>{@html t('settings.integration.weather.notes.openweather')}</span>
          </SettingsNote>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <PasswordField
              label={t('settings.integration.weather.apiKey.label')}
              value={settings.weather.openWeather?.apiKey || ''}
              onUpdate={updateWeatherApiKey}
              placeholder=""
              helpText={t('settings.integration.weather.apiKey.helpText')}
              disabled={store.isLoading || store.isSaving}
              allowReveal={true}
            />
          </div>
        {:else if settings.weather.provider === 'wunderground'}
          <SettingsNote>
            <span>{@html t('settings.integration.weather.notes.wunderground')}</span>
          </SettingsNote>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <PasswordField
              label={t('settings.integration.weather.wunderground.apiKey.label')}
              value={settings.weather.wunderground?.apiKey ?? ''}
              onUpdate={apiKey => updateWundergroundSetting('apiKey', apiKey)}
              placeholder=""
              helpText={t('settings.integration.weather.wunderground.apiKey.helpText')}
              disabled={store.isLoading || store.isSaving}
              allowReveal={true}
            />

            <TextInput
              label={t('settings.integration.weather.wunderground.stationId.label')}
              value={settings.weather.wunderground?.stationId ?? ''}
              onchange={stationId => updateWundergroundSetting('stationId', stationId)}
              placeholder=""
              helpText={t('settings.integration.weather.wunderground.stationId.helpText')}
              disabled={store.isLoading || store.isSaving}
            />

            <TextInput
              label={t('settings.integration.weather.wunderground.endpoint.label')}
              value={settings.weather.wunderground?.endpoint ?? ''}
              onchange={endpoint => updateWundergroundSetting('endpoint', endpoint)}
              placeholder="https://api.weather.com/v2/pws/observations/current"
              helpText={t('settings.integration.weather.wunderground.endpoint.helpText')}
              disabled={store.isLoading || store.isSaving}
            />
          </div>
        {/if}

        {#if settings.weather.provider !== 'none'}
          <!-- Temperature Display Unit -->
          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <SelectDropdown
              options={[
                {
                  value: 'celsius',
                  label: t('settings.integration.weather.temperatureUnit.options.celsius'),
                },
                {
                  value: 'fahrenheit',
                  label: t('settings.integration.weather.temperatureUnit.options.fahrenheit'),
                },
              ]}
              value={settings.dashboard.temperatureUnit || 'celsius'}
              label={t('settings.integration.weather.temperatureUnit.label')}
              helpText={t('settings.integration.weather.temperatureUnit.helpText')}
              disabled={store.isLoading || store.isSaving}
              variant="select"
              groupBy={false}
              menuSize="sm"
              onChange={value => updateDashboardSetting('temperatureUnit', value as string)}
            />
          </div>

          <!-- Test Weather Provider -->
          <div class="space-y-4">
            <div class="flex items-center gap-3">
              <SettingsButton
                onclick={testWeather}
                loading={weatherTestState.isRunning}
                loadingText={t('settings.integration.weather.test.loading')}
                disabled={(settings.weather.provider === 'openweather' &&
                  !settings.weather.openWeather?.apiKey) ||
                  (settings.weather.provider === 'wunderground' &&
                    (!settings.weather.wunderground?.apiKey ||
                      !settings.weather.wunderground?.stationId)) ||
                  weatherTestState.isRunning}
              >
                {t('settings.integration.weather.test.button')}
              </SettingsButton>
              <span class="text-sm text-[color:var(--color-base-content)] opacity-70">
                {#if settings.weather.provider === 'openweather' && !settings.weather.openWeather?.apiKey}
                  {t('settings.integration.weather.test.apiKeyRequired')}
                {:else if settings.weather.provider === 'wunderground' && (!settings.weather.wunderground?.apiKey || !settings.weather.wunderground?.stationId)}
                  {t('settings.integration.weather.test.apiKeyRequired')}
                {:else if weatherTestState.isRunning}
                  {t('settings.integration.weather.test.inProgress')}
                {:else}
                  {t('settings.integration.weather.test.description')}
                {/if}
              </span>
            </div>

            {#if weatherTestState.stages.length > 0}
              <MultiStageOperation
                stages={weatherTestState.stages}
                variant="compact"
                showProgress={false}
              />
            {/if}

            <TestSuccessNote show={weatherTestState.showSuccessNote} />
          </div>
        {/if}
      </div>
    </SettingsSection>
  </div>
{/snippet}

{#snippet databaseTabContent()}
  <div class="space-y-6">
    <!-- Database Settings Card -->
    <SettingsSection
      title={t('settings.main.sections.database.title')}
      description={t('settings.main.sections.database.description')}
      originalData={store.originalData.output}
      currentData={store.formData.output}
    >
      <div class="space-y-6">
        <!-- Database Type Selector -->
        <div class="max-w-md">
          <SelectDropdown
            options={databaseOptions}
            value={selectedDatabaseType}
            label={t('settings.main.sections.database.type.label')}
            helpText={t('settings.main.sections.database.type.helpText')}
            disabled={store.isLoading || store.isSaving}
            variant="select"
            groupBy={false}
            onChange={value => {
              selectedDatabaseType = value as string;
              updateDatabaseType(value as 'sqlite' | 'mysql');
            }}
          >
            {#snippet renderOption(option)}
              {@const dbOption = option as DatabaseOption}
              <div class="flex items-center gap-2">
                <DatabaseIcon database={dbOption.databaseType} className="size-4" />
                <span>{dbOption.label}</span>
              </div>
            {/snippet}
            {#snippet renderSelected(options)}
              {@const dbOption = options[0] as DatabaseOption}
              <span class="flex items-center gap-2">
                <DatabaseIcon database={dbOption.databaseType} className="size-4" />
                <span>{dbOption.label}</span>
              </span>
            {/snippet}
          </SelectDropdown>
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

    <!-- Database Statistics Section -->
    <SettingsSection
      title={t('settings.main.sections.database.stats.title')}
      description={t('settings.main.sections.database.stats.description')}
    >
      <div class="space-y-4">
        {#if databaseStats.loading}
          <div
            class="flex items-center gap-2 text-base-content/60"
            role="status"
            aria-live="polite"
          >
            <span class="loading loading-spinner loading-sm"></span>
            <span>{t('settings.main.sections.database.stats.loading')}</span>
          </div>
        {:else if databaseStats.error}
          <div class="alert alert-error" role="alert">
            <XCircle class="size-5" />
            <span>{databaseStats.error}</span>
            <button type="button" class="btn btn-sm btn-ghost" onclick={() => loadDatabaseStats()}>
              {t('settings.main.sections.database.stats.retry')}
            </button>
          </div>
        {:else if databaseStats.data}
          <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <!-- Database Type -->
            <div class="stat bg-base-200 rounded-box p-4">
              <div class="stat-title text-xs">
                {t('settings.main.sections.database.stats.type')}
              </div>
              <div class="stat-value text-lg flex items-center gap-2">
                <DatabaseIcon database={databaseStats.data.type} className="size-5" />
                <span class="capitalize">{databaseStats.data.type}</span>
              </div>
            </div>

            <!-- Connection Status -->
            <div class="stat bg-base-200 rounded-box p-4">
              <div class="stat-title text-xs">
                {t('settings.main.sections.database.stats.status')}
              </div>
              <div class="stat-value text-lg">
                {#if databaseStats.data.connected}
                  <span class="text-success flex items-center gap-2">
                    <span class="badge badge-success badge-xs"></span>
                    {t('settings.main.sections.database.stats.connected')}
                  </span>
                {:else}
                  <span class="text-error flex items-center gap-2">
                    <span class="badge badge-error badge-xs"></span>
                    {t('settings.main.sections.database.stats.disconnected')}
                  </span>
                {/if}
              </div>
            </div>

            <!-- Database Size -->
            <div class="stat bg-base-200 rounded-box p-4">
              <div class="stat-title text-xs">
                {t('settings.main.sections.database.stats.size')}
              </div>
              <div class="stat-value text-lg">
                {formatBytes(databaseStats.data.size_bytes)}
              </div>
            </div>

            <!-- Total Detections -->
            <div class="stat bg-base-200 rounded-box p-4">
              <div class="stat-title text-xs">
                {t('settings.main.sections.database.stats.totalDetections')}
              </div>
              <div class="stat-value text-lg">
                {databaseStats.data.total_detections.toLocaleString()}
              </div>
            </div>
          </div>

          <!-- Location/Path -->
          <div class="bg-base-200 rounded-box p-4">
            <div class="text-xs text-base-content/60 mb-1">
              {t('settings.main.sections.database.stats.location')}
            </div>
            <div class="font-mono text-sm break-all">{databaseStats.data.location}</div>
          </div>

          <!-- Refresh Button -->
          <div class="flex justify-end">
            <button
              type="button"
              class="btn btn-sm btn-ghost gap-2"
              onclick={() => loadDatabaseStats()}
              disabled={databaseStats.loading}
            >
              <RefreshCw class={cn('size-4', databaseStats.loading && 'animate-spin')} />
              {t('settings.main.sections.database.stats.refresh')}
            </button>
          </div>
        {:else}
          <div class="text-base-content/60">
            {t('settings.main.sections.database.stats.noData')}
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
            <span class="text-[color:var(--color-base-content)] opacity-60"
              >{t('settings.main.sections.rangeFilter.latitude.label')}</span
            >
            <span class="font-medium ml-2">{settings.birdnet.latitude}</span>
          </div>
          <div>
            <span class="text-[color:var(--color-base-content)] opacity-60"
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
        <p class="text-sm text-[color:var(--color-base-content)] opacity-60">
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
            <span class="text-base-content opacity-60"
              >{t('settings.main.sections.rangeFilter.modal.speciesCount')}</span
            >
            <span class="font-medium ml-1">{rangeFilterState.speciesCount}</span>
          </div>
          <div>
            <span class="text-base-content opacity-60"
              >{t('settings.main.sections.rangeFilter.modal.threshold')}</span
            >
            <span class="font-medium ml-1">{settings.birdnet.rangeFilter.threshold}</span>
          </div>
          <div>
            <span class="text-base-content opacity-60"
              >{t('settings.main.sections.rangeFilter.modal.latitude')}</span
            >
            <span class="font-medium ml-1">{settings.birdnet.latitude}</span>
          </div>
          <div>
            <span class="text-base-content opacity-60"
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
            <p class="mt-3 text-[color:var(--color-base-content)] opacity-90">
              {t('settings.main.sections.rangeFilter.modal.loadingSpecies')}
            </p>
          </div>
        {:else if rangeFilterState.species.length > 0}
          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2">
            {#each rangeFilterState.species as species (species.scientificName)}
              <div class="p-3 rounded-lg hover:bg-base-200/50 transition-colors">
                <div class="font-medium">{species.commonName}</div>
                <div class="text-sm text-[color:var(--color-base-content)] opacity-60 italic">
                  {species.scientificName}
                </div>
              </div>
            {/each}
          </div>
        {:else}
          <div class="text-center py-12 text-[color:var(--color-base-content)] opacity-60">
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
