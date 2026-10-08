<!--
  Main Settings Page Component

  Purpose: Main configuration settings for BirdNET-Go including node settings,
  database configuration, and location-based filtering.

  Features:
  - Tabbed interface: General, Location, Database
  - Node identification and settings
  - Interactive map for station location (shared LocationMap component)
  - Database type selection (SQLite/MySQL)

  Props: None - This is a page component that uses global settings stores

  Performance Optimizations:
  - Map lazy loading - LocationMap is mounted with the Location tab and loads MapLibre on first use
  - Cached CSRF token with $derived to avoid repeated DOM queries
  - Reactive computed properties for change detection

  @component
-->
<script lang="ts">
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import LocationMap from '$lib/desktop/components/forms/LocationMap.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import WeatherIcon, { type WeatherProvider } from '$lib/desktop/components/ui/WeatherIcon.svelte';
  import DatabaseIcon, { type DatabaseType } from '$lib/desktop/components/ui/DatabaseIcon.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { cn } from '$lib/utils/cn.js';
  import PasswordField from '$lib/desktop/components/forms/PasswordField.svelte';
  import MultiStageOperation from '$lib/desktop/components/ui/MultiStageOperation.svelte';
  import type { Stage } from '$lib/desktop/components/ui/MultiStageOperation.types';
  import TestSuccessNote from '$lib/desktop/components/ui/TestSuccessNote.svelte';
  import SettingsButton from '$lib/desktop/features/settings/components/SettingsButton.svelte';
  import CurrentLocationButton from '$lib/desktop/features/settings/components/CurrentLocationButton.svelte';
  import {
    settingsStore,
    settingsActions,
    mainSettings,
    birdnetSettings,
    outputSettings,
    realtimeSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import { api, ApiError, getCsrfToken } from '$lib/utils/api';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { formatNumber } from '$lib/utils/formatters';
  import { Settings, MapPin, Database, XCircle, RefreshCw } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { safeArrayAccess } from '$lib/utils/security';
  import { formatBytes } from '$lib/utils/formatters';
  import {
    wundergroundDefaults,
    pirateWeatherDefaults,
    weatherDefaults,
  } from '$lib/utils/weatherDefaults';

  const logger = loggers.settings;

  // Tab state
  let activeTab = $state('general');

  // Extended option type for weather provider
  interface WeatherOption extends SelectOption {
    providerCode: WeatherProvider;
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
    weather: $realtimeSettings?.weather || weatherDefaults,
    sentry: $settingsStore.formData.sentry || {
      enabled: false,
      dsn: '',
      environment: 'production',
      includePrivateInfo: false,
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

  // Change detection per tab - General includes node identity and telemetry
  let generalTabHasChanges = $derived(
    hasSettingsChanged(store.originalData.main, store.formData.main) ||
      hasSettingsChanged(store.originalData.sentry, store.formData.sentry)
  );

  // Location tab includes station location and weather settings
  let locationTabHasChanges = $derived(
    hasSettingsChanged(
      {
        latitude: store.originalData.birdnet?.latitude,
        longitude: store.originalData.birdnet?.longitude,
        locationConfigured: store.originalData.birdnet?.locationConfigured,
      },
      {
        latitude: store.formData.birdnet?.latitude,
        longitude: store.formData.birdnet?.longitude,
        locationConfigured: store.formData.birdnet?.locationConfigured,
      }
    ) || hasSettingsChanged(store.originalData.realtime?.weather, store.formData.realtime?.weather)
  );

  let databaseTabHasChanges = $derived(
    hasSettingsChanged(store.originalData.output, store.formData.output)
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
        databaseStats.error = t('settings.errors.databaseStatsFailed');
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

  // Every user-initiated coordinate update advances this version, including a
  // map action whose rounded values equal the current coordinates. Track raw
  // input separately so pending browser results also cannot overwrite text
  // before a NumberField change/blur commit occurs.
  let coordinateIntentVersion = $state(0);

  function applyLocationSettings(lat: number, lng: number) {
    settingsActions.updateSection('birdnet', {
      latitude: lat,
      longitude: lng,
      locationConfigured: true,
    });
  }

  function updateLocationSettings(lat: number, lng: number) {
    advanceCoordinateIntentVersion();
    applyLocationSettings(lat, lng);
  }

  function updateBrowserLocation(lat: number, lng: number) {
    applyLocationSettings(lat, lng);
  }

  function advanceCoordinateIntentVersion() {
    coordinateIntentVersion += 1;
  }

  // Update handlers
  function updateMainName(name: string) {
    settingsActions.updateSection('main', { name });
  }

  function updateBirdnetSetting(key: string, value: string | number | boolean | null) {
    // When latitude or longitude is updated, mark location as explicitly configured
    if (key === 'latitude' || key === 'longitude') {
      const currentLat = key === 'latitude' ? (value as number) : settings.birdnet.latitude;
      const currentLng = key === 'longitude' ? (value as number) : settings.birdnet.longitude;
      updateLocationSettings(currentLat, currentLng);
    } else {
      settingsActions.updateSection('birdnet', { [key]: value });
    }
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

  // Telemetry update handler
  function updateTelemetryEnabled(enabled: boolean) {
    settingsActions.updateSection('sentry', {
      ...settings.sentry,
      enabled,
    });
  }

  // Weather update handlers
  function updateWeatherProvider(provider: string) {
    settingsActions.updateSection('realtime', {
      weather: {
        ...settings.weather,
        provider: provider as 'none' | 'yrno' | 'openweather' | 'wunderground' | 'pirateweather',
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

  function updatePirateWeatherSetting(key: keyof typeof pirateWeatherDefaults, value: string) {
    settingsActions.updateSection('realtime', {
      weather: {
        ...settings.weather,
        pirateWeather: {
          ...(settings.weather?.pirateWeather ?? pirateWeatherDefaults),
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
        pirateWeather: {
          apiKey: currentWeather.pirateWeather?.apiKey ?? '',
          endpoint: currentWeather.pirateWeather?.endpoint ?? '',
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

      const response = await fetch(buildAppUrl('/api/v2/integrations/weather/test'), {
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
        throw new Error(t('settings.integration.errors.responseStreamFailed'));
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
          title: t('settings.integration.errors.connectionError'),
          status: 'error',
          error: error instanceof Error ? error.message : t('common.errors.unknownError'),
        });
      } else {
        // Mark current stage as failed
        const lastIndex = weatherTestState.stages.length - 1;
        const lastStage = safeArrayAccess(weatherTestState.stages, lastIndex);
        if (lastStage && lastStage.status === 'in_progress') {
          const updatedStage = {
            ...lastStage,
            status: 'error' as const,
            error: error instanceof Error ? error.message : t('common.errors.unknownError'),
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

    <!-- Privacy & Telemetry Card -->
    <SettingsSection
      title={t('settings.support.sections.telemetry.title')}
      description={t('settings.support.sections.telemetry.description')}
      originalData={store.originalData.sentry}
      currentData={store.formData.sentry}
    >
      <div class="space-y-4">
        <!-- Privacy Notice -->
        <div class="p-4 bg-[var(--color-base-200)] rounded-lg shadow-xs">
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

{#snippet locationTabContent()}
  <div class="space-y-6">
    <!-- Station Location Card -->
    <SettingsSection
      title={t('settings.main.sections.rangeFilter.stationLocation.label')}
      description={t('settings.main.sections.rangeFilter.stationLocation.helpText')}
      originalData={{
        latitude: store.originalData.birdnet?.latitude,
        longitude: store.originalData.birdnet?.longitude,
        locationConfigured: store.originalData.birdnet?.locationConfigured,
      }}
      currentData={{
        latitude: settings.birdnet.latitude,
        longitude: settings.birdnet.longitude,
        locationConfigured: settings.birdnet.locationConfigured,
      }}
    >
      <!-- Coordinates -->
      <div
        class="mb-4 grid grid-cols-1 gap-6 md:grid-cols-2 xl:grid-cols-3"
        oninput={advanceCoordinateIntentVersion}
      >
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

        <div class="md:col-span-2 xl:col-span-1 xl:border-l xl:border-[var(--border-100)] xl:pl-6">
          <CurrentLocationButton
            latitude={settings.birdnet.latitude}
            longitude={settings.birdnet.longitude}
            {coordinateIntentVersion}
            onLocation={updateBrowserLocation}
            disabled={store.isLoading || store.isSaving}
          />
        </div>
      </div>

      <!-- Map -->
      <LocationMap
        latitude={settings.birdnet.latitude}
        longitude={settings.birdnet.longitude}
        locationSet={!!settings.birdnet.locationConfigured}
        ready={!store.isLoading &&
          $birdnetSettings?.latitude !== undefined &&
          $birdnetSettings?.longitude !== undefined}
        title={t('settings.main.sections.rangeFilter.stationLocation.label')}
        onLocationChange={updateLocationSettings}
      />
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
            {
              value: 'pirateweather',
              label: t('settings.integration.weather.provider.options.pirateweather'),
              providerCode: 'pirateweather',
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
        {:else if settings.weather.provider === 'pirateweather'}
          <SettingsNote>
            <span>{@html t('settings.integration.weather.notes.pirateweather')}</span>
          </SettingsNote>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <PasswordField
              label={t('settings.integration.weather.pirateweather.apiKey.label')}
              value={settings.weather.pirateWeather?.apiKey ?? ''}
              onUpdate={apiKey => updatePirateWeatherSetting('apiKey', apiKey)}
              placeholder=""
              helpText={t('settings.integration.weather.pirateweather.apiKey.helpText')}
              disabled={store.isLoading || store.isSaving}
              allowReveal={true}
            />

            <TextInput
              label={t('settings.integration.weather.pirateweather.endpoint.label')}
              value={settings.weather.pirateWeather?.endpoint ?? ''}
              onchange={endpoint => updatePirateWeatherSetting('endpoint', endpoint)}
              placeholder={pirateWeatherDefaults.endpoint}
              helpText={t('settings.integration.weather.pirateweather.endpoint.helpText')}
              disabled={store.isLoading || store.isSaving}
            />
          </div>
        {/if}

        {#if settings.weather.provider !== 'none'}
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
                  (settings.weather.provider === 'pirateweather' &&
                    !settings.weather.pirateWeather?.apiKey) ||
                  weatherTestState.isRunning}
              >
                {t('settings.integration.weather.test.button')}
              </SettingsButton>
              <span class="text-sm text-[color:var(--color-base-content)] opacity-70">
                {#if settings.weather.provider === 'openweather' && !settings.weather.openWeather?.apiKey}
                  {t('settings.integration.weather.test.apiKeyRequired')}
                {:else if settings.weather.provider === 'wunderground' && (!settings.weather.wunderground?.apiKey || !settings.weather.wunderground?.stationId)}
                  {t('settings.integration.weather.test.apiKeyRequired')}
                {:else if settings.weather.provider === 'pirateweather' && !settings.weather.pirateWeather?.apiKey}
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
            class="flex items-center gap-2 text-[var(--color-base-content)]/60"
            role="status"
            aria-live="polite"
          >
            <span
              class="inline-block w-4 h-4 border-2 border-[var(--color-base-300)] border-t-current rounded-full animate-spin"
            ></span>
            <span>{t('settings.main.sections.database.stats.loading')}</span>
          </div>
        {:else if databaseStats.error}
          <div
            class="flex items-start gap-3 p-4 rounded-lg bg-[color-mix(in_srgb,var(--color-error)_15%,transparent)] text-[var(--color-error)]"
            role="alert"
          >
            <XCircle class="size-5" />
            <span>{databaseStats.error}</span>
            <button
              type="button"
              class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-black/5 dark:hover:bg-white/10 transition-colors"
              onclick={() => loadDatabaseStats()}
            >
              {t('settings.main.sections.database.stats.retry')}
            </button>
          </div>
        {:else if databaseStats.data}
          <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <!-- Database Type -->
            <div class="bg-[var(--color-base-200)] rounded-xl p-4">
              <div class="text-xs text-[var(--color-base-content)]/60">
                {t('settings.main.sections.database.stats.type')}
              </div>
              <div class="text-lg font-semibold flex items-center gap-2 mt-1">
                <DatabaseIcon database={databaseStats.data.type} className="size-5" />
                <span class="capitalize">{databaseStats.data.type}</span>
              </div>
            </div>

            <!-- Connection Status -->
            <div class="bg-[var(--color-base-200)] rounded-xl p-4">
              <div class="text-xs text-[var(--color-base-content)]/60">
                {t('settings.main.sections.database.stats.status')}
              </div>
              <div class="text-lg font-semibold mt-1">
                {#if databaseStats.data.connected}
                  <span class="text-[var(--color-success)] flex items-center gap-2">
                    <span class="w-2 h-2 rounded-full bg-[var(--color-success)]"></span>
                    {t('settings.main.sections.database.stats.connected')}
                  </span>
                {:else}
                  <span class="text-[var(--color-error)] flex items-center gap-2">
                    <span class="w-2 h-2 rounded-full bg-[var(--color-error)]"></span>
                    {t('settings.main.sections.database.stats.disconnected')}
                  </span>
                {/if}
              </div>
            </div>

            <!-- Database Size -->
            <div class="bg-[var(--color-base-200)] rounded-xl p-4">
              <div class="text-xs text-[var(--color-base-content)]/60">
                {t('settings.main.sections.database.stats.size')}
              </div>
              <div class="text-lg font-semibold mt-1">
                {formatBytes(databaseStats.data.size_bytes)}
              </div>
            </div>

            <!-- Total Detections -->
            <div class="bg-[var(--color-base-200)] rounded-xl p-4">
              <div class="text-xs text-[var(--color-base-content)]/60">
                {t('settings.main.sections.database.stats.totalDetections')}
              </div>
              <div class="text-lg font-semibold mt-1">
                {formatNumber(databaseStats.data.total_detections)}
              </div>
            </div>
          </div>

          <!-- Location/Path -->
          <div class="bg-[var(--color-base-200)] rounded-xl p-4">
            <div class="text-xs text-[var(--color-base-content)]/60 mb-1">
              {t('settings.main.sections.database.stats.location')}
            </div>
            <div class="font-mono text-sm break-all">{databaseStats.data.location}</div>
          </div>

          <!-- Refresh Button -->
          <div class="flex justify-end">
            <button
              type="button"
              class="inline-flex items-center justify-center gap-2 h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-black/5 dark:hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-base-content)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={() => loadDatabaseStats()}
              disabled={databaseStats.loading}
            >
              <RefreshCw class={cn('size-4', databaseStats.loading && 'animate-spin')} />
              {t('settings.main.sections.database.stats.refresh')}
            </button>
          </div>
        {:else}
          <div class="text-[var(--color-base-content)]/60">
            {t('settings.main.sections.database.stats.noData')}
          </div>
        {/if}
      </div>
    </SettingsSection>
  </div>
{/snippet}

<!-- Main Content -->
<main class="settings-page-content" aria-label="Main settings configuration">
  <SettingsTabs {tabs} bind:activeTab onReset={advanceCoordinateIntentVersion} />
</main>
