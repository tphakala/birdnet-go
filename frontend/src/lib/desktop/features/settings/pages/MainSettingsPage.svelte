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

  // Map state
  let mapElement: HTMLElement;
  let map: any;
  let marker: any;

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

  async function loadInitialData() {
    // Load all API data in parallel for better performance
    await Promise.all([loadBirdnetLocales(), loadImageProviders(), loadRangeFilterCount()]);

    // Initialize map after data loads
    initializeMap();
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

  // Wait for Leaflet library to be available
  async function waitForLeaflet(maxAttempts = 20, interval = 100): Promise<any> {
    for (let i = 0; i < maxAttempts; i++) {
      const L = (window as any).L;
      if (L) {
        return L;
      }
      await new Promise(resolve => setTimeout(resolve, interval));
    }
    return null;
  }

  // Initialize Leaflet map
  async function initializeMap() {
    if (!mapElement) return;

    // Wait for Leaflet to be available
    const L = await waitForLeaflet();
    if (!L) {
      logger.error('Leaflet failed to load after timeout');
      return;
    }

    const initialLat = settings.birdnet.latitude || 0;
    const initialLng = settings.birdnet.longitude || 0;
    const initialZoom = initialLat !== 0 || initialLng !== 0 ? 6 : 2;

    map = L.map(mapElement, {
      scrollWheelZoom: false, // Disable default scroll wheel zoom
    }).setView([initialLat, initialLng], initialZoom);

    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
      attribution: '© OpenStreetMap contributors',
    }).addTo(map);

    // Enable zoom only when Ctrl/Cmd is pressed
    mapElement.addEventListener('wheel', e => {
      if (e.ctrlKey || e.metaKey) {
        // metaKey is Cmd on Mac
        e.preventDefault();
        const zoom = map.getZoom();
        const delta = e.deltaY > 0 ? -1 : 1;
        map.setZoom(zoom + delta, { animate: true });
      }
    });

    // Add marker if coordinates exist
    if (initialLat !== 0 || initialLng !== 0) {
      updateMarker(initialLat, initialLng);
    }

    // Handle map clicks
    map.on('click', (e: any) => {
      updateMarker(e.latlng.lat, e.latlng.lng);
      map.setView(e.latlng);
    });
  }

  function updateMarker(lat: number, lng: number) {
    const L = (window as any).L;
    if (!L || !map) return;

    const roundedLat = parseFloat(lat.toFixed(3));
    const roundedLng = parseFloat(lng.toFixed(3));

    // Update settings
    settingsActions.updateSection('birdnet', {
      latitude: roundedLat,
      longitude: roundedLng,
    });

    if (marker) {
      marker.setLatLng([lat, lng]);
    } else {
      marker = L.marker([lat, lng], { draggable: true }).addTo(map);
      marker.on('dragend', (event: any) => {
        updateMarker(event.target.getLatLng().lat, event.target.getLatLng().lng);
      });
    }

    // Test range filter with new coordinates
    debouncedTestRangeFilter();
  }

  // Range filter functions
  let debounceTimer: any;

  function debouncedTestRangeFilter() {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      testCurrentRangeFilter();
    }, 500);
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

    rangeFilterState.testing = true;
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
    // eslint-disable-next-line no-unused-vars
    const threshold = settings.birdnet.rangeFilter.threshold;

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

<div class="space-y-4">
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
        bind:value={settings.main.name}
        label={t('settings.main.fields.nodeName.label')}
        placeholder={t('settings.main.fields.nodeName.placeholder')}
        helpText={t('settings.main.fields.nodeName.helpText')}
        disabled={store.isLoading || store.isSaving}
        onchange={() => updateMainName(settings.main.name)}
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
          bind:value={settings.birdnet.locale}
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
            bind:value={settings.birdnet.modelPath}
            label={t('settings.main.sections.customClassifier.modelPath.label')}
            placeholder={t('settings.main.sections.customClassifier.modelPath.placeholder')}
            helpText={t('settings.main.sections.customClassifier.modelPath.helpText')}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateBirdnetSetting('modelPath', value)}
          />

          <TextInput
            id="label-path"
            bind:value={settings.birdnet.labelPath}
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
          bind:checked={settings.dynamicThreshold.enabled}
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
        <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6">
          <!-- Map container -->
          <div class="col-span-1 md:col-span-2">
            <label class="label justify-start" for="location-map">
              <span class="label-text"
                >{t('settings.main.sections.rangeFilter.stationLocation.label')}</span
              >
            </label>
            <div class="form-control">
              <div
                bind:this={mapElement}
                id="location-map"
                class="h-[300px] rounded-lg border border-base-300"
                role="application"
                aria-label="Map for selecting station location"
              >
                <!-- Map will be initialized here -->
              </div>
              <div class="label">
                <span class="label-text-alt"
                  >{t('settings.main.sections.rangeFilter.stationLocation.helpText')}</span
                >
              </div>
              <div class="label">
                <span class="label-text-alt text-info"
                  >💡 Hold Ctrl (or Cmd on Mac) + scroll to zoom the map</span
                >
              </div>
            </div>
          </div>

          <!-- Range Filter Settings -->
          <div class="col-span-1 flex flex-col justify-start gap-x-6">
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
                    <span class="loading loading-spinner loading-xs text-primary opacity-60"></span>
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
                <div class="alert alert-error mt-2">
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
          label="Database Type"
          options={[
            { value: 'sqlite', label: 'SQLite' },
            { value: 'mysql', label: 'MySQL' },
          ]}
          helpText="Select the database type for storing detections"
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
              bind:value={settings.output.sqlite.path}
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
              bind:value={settings.output.mysql.host}
              label={t('settings.main.sections.database.mysql.host.label')}
              placeholder={t('settings.main.sections.database.mysql.host.placeholder')}
              helpText={t('settings.main.sections.database.mysql.host.helpText')}
              disabled={store.isLoading || store.isSaving}
              onchange={host => updateMySQLSettings({ host })}
            />

            <TextInput
              id="mysql-port"
              bind:value={settings.output.mysql.port}
              label={t('settings.main.sections.database.mysql.port.label')}
              placeholder="3306"
              helpText={t('settings.main.sections.database.mysql.port.helpText')}
              disabled={store.isLoading || store.isSaving}
              onchange={port => updateMySQLSettings({ port })}
            />

            <TextInput
              id="mysql-username"
              bind:value={settings.output.mysql.username}
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
              bind:value={settings.output.mysql.database}
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
            bind:value={settings.dashboard.locale}
            label={t('settings.main.sections.userInterface.language.locale.label')}
            options={uiLocales}
            helpText={t('settings.main.sections.userInterface.language.locale.helpText')}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateUILocale(value)}
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
            bind:checked={settings.dashboard.thumbnails.summary}
            label={t('settings.main.sections.userInterface.dashboard.thumbnails.summary.label')}
            helpText={t(
              'settings.main.sections.userInterface.dashboard.thumbnails.summary.helpText'
            )}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateThumbnailSetting('summary', value)}
          />

          <Checkbox
            bind:checked={settings.dashboard.thumbnails.recent}
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
              bind:value={settings.dashboard.thumbnails.imageProvider}
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
              bind:value={settings.dashboard.thumbnails.fallbackPolicy}
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
</div>

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
        <div class="alert alert-error mb-4">
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

<!-- Include Leaflet CSS and JS -->
<svelte:head>
  <link rel="stylesheet" href="/assets/leaflet.css" />
  <script src="/assets/leaflet.js"></script>
</svelte:head>
