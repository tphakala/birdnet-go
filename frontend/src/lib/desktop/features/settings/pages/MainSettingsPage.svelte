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
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import SettingsSection from '$lib/desktop/components/ui/SettingsSection.svelte';
  import { onMount } from 'svelte';
  import { api, ApiError } from '$lib/utils/api';
  import { toastActions } from '$lib/stores/toast';
  import { alertIconsSvg, navigationIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts

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
        model: 'latest',
        threshold: 0.01,
      },
    },
    dynamicThreshold: $birdnetSettings?.dynamicThreshold || {
      enabled: false,
      debug: false,
      trigger: 0.8,
      min: 0.3,
      validHours: 24,
    },
    database: $birdnetSettings?.database || {
      type: 'sqlite',
      path: 'birds.db',
      host: '',
      port: 3306,
      name: '',
      username: '',
      password: '',
    },
    dashboard: $dashboardSettings || {
      thumbnails: {
        summary: true,
        recent: true,
        imageProvider: 'wikimedia',
        fallbackPolicy: 'all',
      },
      summaryLimit: 100,
    },
  });

  let store = $derived($settingsStore);

  // Check for changes in each section
  let mainSettingsHasChanges = $derived(
    hasSettingsChanged((store.originalData as any)?.main, (store.formData as any)?.main)
  );

  let birdnetSettingsHasChanges = $derived(
    hasSettingsChanged((store.originalData as any)?.birdnet, (store.formData as any)?.birdnet)
  );

  let databaseSettingsHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.birdnet?.database,
      (store.formData as any)?.birdnet?.database
    )
  );

  let dashboardSettingsHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.realtime?.dashboard,
      (store.formData as any)?.realtime?.dashboard
    )
  );

  // Locale options
  let locales = $state<Array<{ value: string; label: string }>>([]);

  // Image provider options
  let providerOptions = $state<Array<{ value: string; label: string }>>([]);
  let multipleProvidersAvailable = $state(false);

  // Range filter state
  let rangeFilterSpeciesCount = $state<number | null>(null);
  let loadingRangeFilter = $state(false);
  let testingRangeFilter = $state(false);
  let rangeFilterError = $state<string | null>(null);
  let showRangeFilterModal = $state(false);
  let rangeFilterSpecies = $state<any[]>([]);

  // Map state
  let mapElement: HTMLElement;
  let map: any;
  let marker: any;

  // Fetch initial data
  onMount(async () => {
    // Fetch locales
    try {
      const localesData = await api.get<Record<string, string>>('/api/v2/settings/locales');
      locales = Object.entries(localesData || {}).map(([value, label]) => ({
        value,
        label: label as string,
      }));
    } catch (error) {
      if (error instanceof ApiError) {
        toastActions.warning('Unable to load language options. Using defaults.');
      }
      // Fallback to basic locales so form still works
      locales = [{ value: 'en', label: 'English' }];
    }

    // Fetch image providers
    try {
      const providersData = await api.get<{ providers?: Array<{ value: string; display: string }> }>('/api/v2/settings/imageproviders');
      
      // Map v2 API response format to client format
      providerOptions = (providersData?.providers || []).map((provider: any) => ({
        value: provider.value,
        label: provider.display,
      }));
      
      multipleProvidersAvailable = providerOptions.length > 1;
    } catch (error) {
      if (error instanceof ApiError) {
        toastActions.warning('Unable to load image providers. Using defaults.');
      }
      // Fallback to basic provider so form still works
      providerOptions = [{ value: 'wikipedia', label: 'Wikipedia' }];
      multipleProvidersAvailable = false;
    }

    // Load initial range filter count
    loadRangeFilterCount();

    // Initialize map after component mounts
    initializeMap();
  });

  // Initialize Leaflet map
  async function initializeMap() {
    if (!mapElement) return;

    // Dynamically import Leaflet
    const L = (window as any).L;
    if (!L) {
      console.error('Leaflet not loaded');
      return;
    }

    const initialLat = settings.birdnet.latitude || 0;
    const initialLng = settings.birdnet.longitude || 0;
    const initialZoom = initialLat !== 0 || initialLng !== 0 ? 6 : 2;

    map = L.map(mapElement).setView([initialLat, initialLng], initialZoom);
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
      attribution: '© OpenStreetMap contributors',
    }).addTo(map);

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
      const response = await fetch('/api/v2/range/species/count');
      if (!response.ok) throw new Error('Failed to load range filter count');
      const data = await response.json();
      rangeFilterSpeciesCount = data.count;
    } catch (error) {
      console.error('Failed to load range filter count:', error);
      rangeFilterError = 'Failed to load species count';
    }
  }

  async function testCurrentRangeFilter() {
    if (testingRangeFilter || !settings.birdnet.latitude || !settings.birdnet.longitude) return;

    testingRangeFilter = true;
    rangeFilterError = null;

    try {
      const data = await api.post<{ count: number; species?: any[] }>(
        '/api/v2/range/species/test',
        {
          latitude: settings.birdnet.latitude,
          longitude: settings.birdnet.longitude,
          threshold: settings.birdnet.rangeFilter.threshold,
        }
      );

      rangeFilterSpeciesCount = data.count;

      if (showRangeFilterModal) {
        rangeFilterSpecies = data.species || [];
      }
    } catch (error) {
      console.error('Failed to test range filter:', error);
      rangeFilterError = 'Failed to test range filter settings';
    } finally {
      testingRangeFilter = false;
    }
  }

  async function loadRangeFilterSpecies() {
    if (loadingRangeFilter) return;

    loadingRangeFilter = true;
    rangeFilterError = null;

    try {
      const params = new URLSearchParams({
        latitude: settings.birdnet.latitude.toString(),
        longitude: settings.birdnet.longitude.toString(),
        threshold: settings.birdnet.rangeFilter.threshold.toString(),
      });

      const data = await api.get<{ count: number; species: any[] }>(
        `/api/v2/range/species/list?${params}`
      );
      rangeFilterSpecies = data.species || [];
      rangeFilterSpeciesCount = data.count;
    } catch (error) {
      console.error('Failed to load species list:', error);
      rangeFilterError = 'Failed to load species list';
    } finally {
      loadingRangeFilter = false;
    }
  }

  // Watch for changes that affect range filter
  $effect(() => {
    debouncedTestRangeFilter();
  });

  // Update handlers
  function updateMainName(name: string) {
    settingsActions.updateSection('main', { name });
  }

  function updateBirdnetSetting(key: string, value: any) {
    settingsActions.updateSection('birdnet', { [key]: value });
  }

  function updateDynamicThreshold(key: string, value: any) {
    settingsActions.updateSection('birdnet', {
      dynamicThreshold: { ...settings.dynamicThreshold, [key]: value },
    });
  }

  function updateDatabaseType(type: 'sqlite' | 'mysql') {
    settingsActions.updateSection('birdnet', {
      database: { ...settings.database, type },
    });
  }

  function updateSQLitePath(path: string) {
    settingsActions.updateSection('birdnet', {
      database: { ...settings.database, path },
    });
  }

  function updateMySQLSetting(key: string, value: any) {
    settingsActions.updateSection('birdnet', {
      database: { ...settings.database, [key]: value },
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
</script>

<div class="space-y-4">
  <!-- Main Settings Section -->
  <SettingsSection
    title="Main Settings"
    description="Configure main application settings"
    defaultOpen={true}
    hasChanges={mainSettingsHasChanges}
  >
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6">
      <TextInput
        id="node-name"
        bind:value={settings.main.name}
        label="Node Name"
        placeholder="Enter node name"
        helpText="Node name is used to identify source system in multi node setup, also used as identifier for MQTT messages."
        disabled={store.isLoading || store.isSaving}
        onchange={() => updateMainName(settings.main.name)}
      />
    </div>
  </SettingsSection>

  <!-- BirdNET Settings Section -->
  <SettingsSection
    title="BirdNET Settings"
    description="Configure BirdNET AI model specific settings"
    defaultOpen={true}
    hasChanges={birdnetSettingsHasChanges}
  >
    <div class="space-y-6">
      <!-- Basic BirdNET Settings -->
      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6">
        <NumberField
          label="Sensitivity"
          value={settings.birdnet.sensitivity}
          onUpdate={value => updateBirdnetSetting('sensitivity', value)}
          min={0.5}
          max={1.5}
          step={0.1}
          helpText="Detection sensitivity. Higher values result in higher sensitivity. Values in 0.5, 1.5."
          disabled={store.isLoading || store.isSaving}
        />

        <NumberField
          label="Threshold"
          value={settings.birdnet.threshold}
          onUpdate={value => updateBirdnetSetting('threshold', value)}
          min={0.01}
          max={0.99}
          step={0.01}
          helpText="Minimum confidence threshold. Values in 0.01, 0.99."
          disabled={store.isLoading || store.isSaving}
        />

        <NumberField
          label="Overlap"
          value={settings.birdnet.overlap}
          onUpdate={value => updateBirdnetSetting('overlap', value)}
          min={0.0}
          max={2.9}
          step={0.1}
          helpText="Overlap of prediction segments. Values in 0.0, 2.9."
          disabled={store.isLoading || store.isSaving}
        />

        <SelectField
          id="locale"
          bind:value={settings.birdnet.locale}
          label="Locale"
          options={locales}
          helpText="Locale for translated species common names."
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateBirdnetSetting('locale', value)}
        />

        <NumberField
          label="TensorFlow CPU Threads"
          value={settings.birdnet.threads}
          onUpdate={value => updateBirdnetSetting('threads', value)}
          min={0}
          max={32}
          step={1}
          helpText="Number of CPU threads. Set to 0 to use all available threads."
          disabled={store.isLoading || store.isSaving}
        />
      </div>

      <!-- Custom BirdNET Classifier -->
      <div>
        <h4 class="text-lg font-medium mt-6 pb-2">Custom BirdNET Classifier</h4>
        <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6">
          <TextInput
            id="model-path"
            bind:value={settings.birdnet.modelPath}
            label="Model Path (Requires restart to apply)"
            placeholder="Path to model file"
            helpText="Path to external BirdNET model file. Enter absolute or relative path to birdnet-go binary. Leave empty to use the default embedded model."
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateBirdnetSetting('modelPath', value)}
          />

          <TextInput
            id="label-path"
            bind:value={settings.birdnet.labelPath}
            label="Label Path (Requires restart to apply)"
            placeholder="Path to labels file"
            helpText="Path to external model labels file, .zip or .txt file. Enter absolute or relative path to birdnet-go binary. Leave empty to use the default embedded labels."
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateBirdnetSetting('labelPath', value)}
          />
        </div>
      </div>

      <!-- Dynamic Threshold -->
      <div>
        <h4 class="text-lg font-medium mt-6 pb-2">Dynamic Threshold</h4>
        <Checkbox
          bind:checked={settings.dynamicThreshold.enabled}
          label="Enable Dynamic Threshold (BirdNET-Go specific feature)"
          helpText="Enables dynamic confidence threshold feature for more adaptive bird call detection."
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateDynamicThreshold('enabled', value)}
        />

        {#if settings.dynamicThreshold.enabled}
          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6 mt-4">
            <NumberField
              label="Trigger Threshold"
              value={settings.dynamicThreshold.trigger}
              onUpdate={value => updateDynamicThreshold('trigger', value)}
              min={0.0}
              max={1.0}
              step={0.01}
              helpText="The confidence level at which the dynamic threshold is activated. If a bird call is detected with confidence over this value, the threshold for positive matches of that species will be lowered for subsequent calls."
              disabled={store.isLoading || store.isSaving}
            />

            <NumberField
              label="Minimum Dynamic Threshold"
              value={settings.dynamicThreshold.min}
              onUpdate={value => updateDynamicThreshold('min', value)}
              min={0.0}
              max={0.99}
              step={0.01}
              helpText="The minimum value to which the dynamic threshold can be lowered. This ensures that the threshold does not drop below an unwanted level."
              disabled={store.isLoading || store.isSaving}
            />

            <NumberField
              label="Dynamic Threshold Expire Time (Hours)"
              value={settings.dynamicThreshold.validHours}
              onUpdate={value => updateDynamicThreshold('validHours', value)}
              min={0}
              max={1000}
              step={1}
              helpText="The number of hours during which the dynamic threshold adjustments remain valid. After this period, the dynamic threshold is reset."
              disabled={store.isLoading || store.isSaving}
            />
          </div>
        {/if}
      </div>

      <!-- Range Filter -->
      <div>
        <h4 class="text-lg font-medium mt-6 pb-2">Range Filter</h4>
        <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6">
          <!-- Map container -->
          <div class="col-span-1 md:col-span-2">
            <label class="label justify-start" for="location-map">
              <span class="label-text">Station Location</span>
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
                  >Station location, used to limit bird species to those probable in the region.</span
                >
              </div>
            </div>
          </div>

          <!-- Range Filter Settings -->
          <div class="col-span-1 flex flex-col justify-start gap-x-6">
            <NumberField
              label="Latitude"
              value={settings.birdnet.latitude}
              onUpdate={value => updateBirdnetSetting('latitude', value)}
              min={-90.0}
              max={90.0}
              step={0.001}
              helpText="Station location latitude, used to limit bird species to those probable in the region."
              disabled={store.isLoading || store.isSaving}
            />

            <NumberField
              label="Longitude"
              value={settings.birdnet.longitude}
              onUpdate={value => updateBirdnetSetting('longitude', value)}
              min={-180.0}
              max={180.0}
              step={0.001}
              helpText="Station location longitude, used to limit bird species to those probable in the region."
              disabled={store.isLoading || store.isSaving}
            />

            <SelectField
              id="range-filter-model"
              bind:value={settings.birdnet.rangeFilter.model}
              label="Range Filter Model"
              options={[
                { value: 'legacy', label: 'legacy' },
                { value: 'latest', label: 'latest' },
              ]}
              helpText="BirdNET range filter model version: latest or legacy."
              disabled={store.isLoading || store.isSaving}
              onchange={value =>
                settingsActions.updateSection('birdnet', {
                  rangeFilter: {
                    ...settings.birdnet.rangeFilter,
                    model: value as 'latest' | 'legacy',
                  },
                })}
            />

            <NumberField
              label="Threshold"
              value={settings.birdnet.rangeFilter.threshold}
              onUpdate={value =>
                settingsActions.updateSection('birdnet', {
                  rangeFilter: { ...settings.birdnet.rangeFilter, threshold: value },
                })}
              min={0.0}
              max={0.99}
              step={0.01}
              helpText="Controls which species are included based on their occurrence probability for your location and time of year. Default (0.01) is recommended for most users. Higher values (0.05-0.3) include fewer species with higher occurrence probability. Very high values (0.5+) include only the most common species."
              disabled={store.isLoading || store.isSaving}
            />

            <!-- Range Filter Species Count Display -->
            <div class="form-control">
              <div class="label justify-start">
                <span class="label-text">Current Species Count</span>
              </div>
              <div class="flex items-center space-x-2">
                <div class="flex items-center space-x-2">
                  <div class="text-lg font-bold text-primary" class:opacity-60={testingRangeFilter}>
                    {rangeFilterSpeciesCount !== null ? rangeFilterSpeciesCount : 'Loading...'}
                  </div>
                  {#if testingRangeFilter}
                    <span class="loading loading-spinner loading-xs text-primary opacity-60"></span>
                  {/if}
                </div>
                <button
                  type="button"
                  class="btn btn-sm btn-outline"
                  disabled={!rangeFilterSpeciesCount || loadingRangeFilter}
                  onclick={() => {
                    showRangeFilterModal = true;
                    loadRangeFilterSpecies();
                  }}
                >
                  {#if loadingRangeFilter}
                    <span class="loading loading-spinner loading-xs mr-1"></span>
                    Loading...
                  {:else}
                    View Species
                  {/if}
                </button>
              </div>
              <div class="label">
                <span class="label-text-alt"
                  >Number of species included in range filter. Updates automatically when threshold,
                  latitude, or longitude changes.</span
                >
              </div>

              {#if rangeFilterError}
                <div class="alert alert-error mt-2">
                  {@html alertIconsSvg.error}
                  <span>{rangeFilterError}</span>
                  <button
                    type="button"
                    class="btn btn-sm btn-ghost ml-auto"
                    aria-label="Dismiss error"
                    onclick={() => (rangeFilterError = null)}
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

  <!-- Database Settings Section -->
  <SettingsSection
    title="Database Settings"
    description="Detections database settings"
    defaultOpen={true}
    hasChanges={databaseSettingsHasChanges}
  >
    <div class="space-y-4">
      <SelectField
        id="database-type"
        bind:value={settings.database.type}
        label="Select Database Type"
        options={[
          { value: 'sqlite', label: 'SQLite' },
          { value: 'mysql', label: 'MySQL' },
        ]}
        helpText="Select the database type to use for storing detections."
        disabled={store.isLoading || store.isSaving}
        onchange={value => updateDatabaseType(value as 'sqlite' | 'mysql')}
      />

      {#if settings.database.type === 'sqlite'}
        <div class="alert alert-info">
          {@html alertIconsSvg.info}
          <span>SQLite is recommended database type for most users.</span>
        </div>

        <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
          <TextInput
            id="sqlite-path"
            bind:value={settings.database.path}
            label="SQLite Database Path"
            placeholder="Enter SQLite database path"
            helpText="SQLite database file path relative to the application directory"
            disabled={store.isLoading || store.isSaving}
            onchange={updateSQLitePath}
          />
        </div>
      {/if}

      {#if settings.database.type === 'mysql'}
        <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
          <TextInput
            id="mysql-host"
            bind:value={settings.database.host}
            label="MySQL Host"
            placeholder="Enter MySQL host"
            helpText="MySQL database host (IP or hostname)"
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateMySQLSetting('host', value)}
          />

          <NumberField
            label="MySQL Port"
            value={settings.database.port}
            onUpdate={value => updateMySQLSetting('port', value)}
            min={1}
            max={65535}
            placeholder="3306"
            helpText="MySQL database port (default 3306/TCP)"
            disabled={store.isLoading || store.isSaving}
          />

          <TextInput
            id="mysql-username"
            bind:value={settings.database.username}
            label="MySQL Username"
            placeholder="Enter MySQL username"
            helpText="MySQL database username"
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateMySQLSetting('username', value)}
          />

          <PasswordField
            id="mysql-password"
            value={settings.database.password}
            label="MySQL Password"
            placeholder="Enter MySQL password"
            helpText="MySQL database password"
            disabled={store.isLoading || store.isSaving}
            onUpdate={value => updateMySQLSetting('password', value)}
          />

          <TextInput
            id="mysql-database"
            bind:value={settings.database.name}
            label="MySQL Database"
            placeholder="Enter MySQL database name"
            helpText="MySQL database name"
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateMySQLSetting('name', value)}
          />
        </div>
      {/if}
    </div>
  </SettingsSection>

  <!-- User Interface Settings Section -->
  <SettingsSection
    title="User Interface Settings"
    description="Customize user interface"
    defaultOpen={true}
    hasChanges={dashboardSettingsHasChanges}
  >
    <div class="space-y-6">
      <div>
        <h4 class="text-lg font-medium pb-2">Dashboard</h4>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
          <NumberField
            label="Max Number of Species on Daily Summary Table"
            value={settings.dashboard.summaryLimit}
            onUpdate={value => updateDashboardSetting('summaryLimit', value)}
            min={10}
            max={1000}
            helpText="Max number of species shown in the daily summary table (Value between 10 and 1000)"
            disabled={store.isLoading || store.isSaving}
          />
        </div>

        <div class="mt-4">
          <Checkbox
            bind:checked={settings.dashboard.thumbnails.summary}
            label="Show Thumbnails on Daily Summary table"
            helpText="Enable to show thumbnails of detected species on the daily summary table"
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateThumbnailSetting('summary', value)}
          />

          <Checkbox
            bind:checked={settings.dashboard.thumbnails.recent}
            label="Show Thumbnails on Recent Detections list"
            helpText="Enable to show thumbnails of detected species on the recent detections list"
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateThumbnailSetting('recent', value)}
          />

          <div class:opacity-50={!multipleProvidersAvailable}>
            <SelectField
              id="image-provider"
              bind:value={settings.dashboard.thumbnails.imageProvider}
              label="Image Provider"
              options={providerOptions}
              helpText="Select the preferred image provider for bird thumbnails"
              disabled={store.isLoading || store.isSaving || !multipleProvidersAvailable}
              onchange={value => updateThumbnailSetting('imageProvider', value)}
            />
          </div>

          {#if multipleProvidersAvailable}
            <SelectField
              id="fallback-policy"
              bind:value={settings.dashboard.thumbnails.fallbackPolicy}
              label="Fallback Policy"
              options={[
                { value: 'all', label: 'Try all providers in sequence' },
                { value: 'none', label: 'Use only selected provider' },
              ]}
              helpText="Select what happens when preferred provider fails to find an image"
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
{#if showRangeFilterModal}
  <div
    class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50"
    role="dialog"
    aria-modal="true"
    aria-labelledby="modal-title"
    tabindex="-1"
    onclick={() => (showRangeFilterModal = false)}
    onkeydown={e => e.key === 'Escape' && (showRangeFilterModal = false)}
  >
    <div
      class="bg-base-100 rounded-lg p-6 max-w-4xl max-h-[80vh] overflow-hidden flex flex-col"
      role="document"
    >
      <div class="flex justify-between items-center mb-4">
        <h3 id="modal-title" class="text-lg font-bold">Range Filter Species</h3>
        <button
          type="button"
          class="btn btn-sm btn-circle btn-ghost"
          aria-label="Close modal"
          onclick={() => (showRangeFilterModal = false)}
        >
          {@html navigationIcons.close}
        </button>
      </div>

      <div class="mb-4 text-sm text-base-content/70">
        <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
          <div>
            <span class="font-medium">Species Count:</span>
            <span> {rangeFilterSpeciesCount}</span>
          </div>
          <div>
            <span class="font-medium">Threshold:</span>
            <span> {settings.birdnet.rangeFilter.threshold}</span>
          </div>
          <div>
            <span class="font-medium">Latitude:</span>
            <span> {settings.birdnet.latitude}</span>
          </div>
          <div>
            <span class="font-medium">Longitude:</span>
            <span> {settings.birdnet.longitude}</span>
          </div>
        </div>
      </div>

      {#if rangeFilterError}
        <div class="alert alert-error mb-4">
          {@html alertIconsSvg.error}
          <span>{rangeFilterError}</span>
          <button
            type="button"
            class="btn btn-sm btn-ghost ml-auto"
            aria-label="Dismiss error"
            onclick={() => (rangeFilterError = null)}
          >
            {@html navigationIcons.close}
          </button>
        </div>
      {/if}

      <div class="flex-1 overflow-auto">
        {#if loadingRangeFilter}
          <div class="text-center py-8">
            <div class="loading loading-spinner loading-lg"></div>
            <p class="mt-2">Loading species...</p>
          </div>
        {:else if rangeFilterSpecies.length > 0}
          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2">
            {#each rangeFilterSpecies as species}
              <div class="p-2 rounded hover:bg-base-200">
                <div class="font-medium">{species.commonName}</div>
                <div class="text-sm text-base-content/70">{species.scientificName}</div>
              </div>
            {/each}
          </div>
        {:else}
          <div class="text-center py-8 text-base-content/60">
            No species found with current settings
          </div>
        {/if}
      </div>

      <div class="flex justify-end items-center mt-4 pt-4 border-t">
        <button
          type="button"
          class="btn btn-outline"
          onclick={() => (showRangeFilterModal = false)}
        >
          Close
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
