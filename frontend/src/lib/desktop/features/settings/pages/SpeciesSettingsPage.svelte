<!--
  Species Settings Page Component
  
  Purpose: Configure species-specific settings for BirdNET-Go including always include/exclude
  lists and custom configurations with thresholds, intervals, and actions.
  
  Features:
  - Always include species list management
  - Always exclude species list management
  - Custom species configurations with threshold and interval settings
  - Action configuration for species-specific commands
  - Species autocomplete with API-loaded predictions
  - Real-time validation and change detection
  
  Props: None - This is a page component that uses global settings stores
  
  Performance Optimizations:
  - Removed page-level loading spinner to prevent flickering
  - Reactive settings with $derived instead of $state + $effect
  - Cached CSRF token to avoid repeated DOM queries
  - API state management for species list loading
  - Reactive change detection with $derived
  - Efficient prediction filtering
  
  @component
-->
<script lang="ts">
  import { onMount, onDestroy, untrack } from 'svelte';
  import SpeciesInput from '$lib/desktop/components/forms/SpeciesInput.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import {
    settingsStore,
    settingsActions,
    speciesSettings,
    realtimeSettings,
    speciesTrackingSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import type { SpeciesConfig, SpeciesSettings } from '$lib/stores/settings';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import DynamicThresholdTab from '$lib/desktop/features/settings/components/DynamicThresholdTab.svelte';
  import SpeciesTable from '$lib/desktop/features/settings/components/SpeciesTable.svelte';
  import ResizableContainer from '$lib/desktop/components/ui/ResizableContainer.svelte';
  import SpeciesListCard from '$lib/desktop/features/settings/components/SpeciesListCard.svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { safeGet } from '$lib/utils/security';
  import { api, ApiError } from '$lib/utils/api';
  import { getLocalDateString } from '$lib/utils/date';
  import {
    ChevronRight,
    Plus,
    SquarePen,
    Trash2,
    CirclePlus,
    CircleMinus,
    Settings2,
    ListCheck,
    MapPin,
    SlidersHorizontal,
    Clock,
    Bird,
    CalendarClock,
    Activity,
  } from '@lucide/svelte';
  import { toastActions } from '$lib/stores/toast';

  const logger = loggers.settings;

  // ============================================================================
  // TRACKING SETTINGS CONSTANTS
  // Centralized defaults to avoid magic numbers and duplication
  // ============================================================================

  /** Month names for dropdown options */
  const MONTH_NAMES = [
    'january',
    'february',
    'march',
    'april',
    'may',
    'june',
    'july',
    'august',
    'september',
    'october',
    'november',
    'december',
  ] as const;

  /** Default season start dates (Northern Hemisphere astronomical seasons) */
  const SEASON_DEFAULTS = {
    spring: { startMonth: 3, startDay: 20 },
    summer: { startMonth: 6, startDay: 21 },
    fall: { startMonth: 9, startDay: 22 },
    winter: { startMonth: 12, startDay: 21 },
  } as const;

  /** Input validation limits */
  const TRACKING_LIMITS = {
    days: { min: 1, max: 365 },
    syncMinutes: { min: 1, max: 1440 },
    suppressionHours: { min: 0, max: 8760 },
    dayOfMonth: { min: 1, max: 31 },
  } as const;

  /** Default values for tracking settings */
  const TRACKING_DEFAULTS = {
    newSpeciesWindowDays: 7,
    syncIntervalMinutes: 60,
    notificationSuppressionHours: 24,
    yearlyTracking: {
      enabled: false,
      resetMonth: 1,
      resetDay: 1,
      windowDays: 7,
    },
    seasonalTracking: {
      enabled: false,
      windowDays: 7,
      seasons: SEASON_DEFAULTS,
    },
  } as const;

  // ============================================================================
  // END TRACKING SETTINGS CONSTANTS
  // ============================================================================

  // Helper function to check if a value is a plain object
  function isPlainObject(value: unknown): value is Record<string, unknown> {
    return value !== null && typeof value === 'object' && !Array.isArray(value);
  }

  // Load species data on mount
  onMount(() => {
    loadSpeciesData();
  });

  // PERFORMANCE OPTIMIZATION: Reactive settings with proper defaults
  let settings = $derived(
    (() => {
      const base = $speciesSettings ?? {
        include: [] as string[],
        exclude: [] as string[],
        config: {} as Record<string, SpeciesConfig>,
      };

      // Ensure config is always a valid object to prevent Object.keys() errors
      return {
        include: base.include ?? [],
        exclude: base.exclude ?? [],
        config: isPlainObject(base.config) ? base.config : {},
      } as SpeciesSettings;
    })()
  );

  let store = $derived($settingsStore);

  // API State Management
  interface ApiState<T> {
    loading: boolean;
    error: string | null;
    data: T;
  }

  // Species list API state
  let speciesListState = $state<ApiState<string[]>>({
    loading: true,
    error: null,
    data: [],
  });

  // Active species types
  interface ActiveSpecies {
    commonName: string;
    scientificName: string;
    score: number;
    isManuallyIncluded: boolean;
    hasCustomConfig: boolean;
  }

  interface ActiveSpeciesData {
    species: ActiveSpecies[];
    count: number;
    threshold: number;
    location: { latitude: number; longitude: number };
    lastUpdated: Date;
  }

  // Active species state - start with loading: true to prevent flash of "location not configured"
  let activeSpeciesState = $state<{
    loading: boolean;
    error: string | null;
    data: ActiveSpeciesData | null;
    locationNotConfigured: boolean;
    initialized: boolean; // Track if we've ever attempted to load
  }>({
    loading: true,
    error: null,
    data: null,
    locationNotConfigured: false,
    initialized: false,
  });

  // Search/filter/expand state is now handled inside SpeciesTable component

  // Lookup map: commonName (lowercase) → scientificName, built from species API
  let speciesScientificNameMap = $state(new Map<string, string>());

  // PERFORMANCE OPTIMIZATION: Derived species list
  let allSpecies = $derived(speciesListState.data);

  // Species predictions state
  let includePredictions = $state<string[]>([]);
  let excludePredictions = $state<string[]>([]);
  let configPredictions = $state<string[]>([]);

  // Input values for species inputs
  let includeInputValue = $state('');
  let excludeInputValue = $state('');
  let configInputValue = $state('');

  // Configuration form state
  let newThreshold = $state(0.5);
  let newInterval = $state(0);
  let showAddForm = $state(false);
  let editingSpecies = $state<string | null>(null);
  let showActions = $state(false);

  // Actions configuration state
  let actionCommand = $state('');
  let actionParameters = $state('');
  let actionExecuteDefaults = $state(true);

  // Helper function to add parameter to the list
  function addParameter(param: string) {
    if (actionParameters) {
      actionParameters += ',' + param;
    } else {
      actionParameters = param;
    }
  }

  // Helper function to clear parameters
  function clearParameters() {
    actionParameters = '';
  }

  // Helper function to handle species selection with proper timing
  function handleSpeciesPicked(species: string) {
    queueMicrotask(() => {
      configInputValue = species;
    });
  }

  // PERFORMANCE OPTIMIZATION: Reactive change detection with $derived
  let includeHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.realtime?.species?.include,
      store.formData.realtime?.species?.include
    )
  );

  let excludeHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.realtime?.species?.exclude,
      store.formData.realtime?.species?.exclude
    )
  );

  let configHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.realtime?.species?.config,
      store.formData.realtime?.species?.config
    )
  );

  let trackingHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.realtime?.speciesTracking,
      store.formData.realtime?.speciesTracking
    )
  );

  // Tracking settings state
  let trackingSettings = $derived($speciesTrackingSettings);

  // Month options for dropdown menus (derived once, used multiple times)
  let monthOptions = $derived(
    MONTH_NAMES.map((name, i) => ({
      value: String(i + 1),
      label: t(`settings.species.tracking.months.${name}`),
    })) as SelectOption[]
  );

  /**
   * Helper function to update tracking settings with proper defaults.
   * Reduces code duplication by centralizing the object construction.
   */
  function updateTrackingSettings(
    updates: Partial<{
      enabled: boolean;
      newSpeciesWindowDays: number;
      syncIntervalMinutes: number;
      notificationSuppressionHours: number;
      yearlyTracking: {
        enabled?: boolean;
        resetMonth?: number;
        resetDay?: number;
        windowDays?: number;
      };
      seasonalTracking: {
        enabled?: boolean;
        windowDays?: number;
        seasons?: Record<string, { startMonth: number; startDay: number }>;
      };
    }>
  ) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      speciesTracking: {
        enabled: updates.enabled ?? trackingSettings?.enabled ?? true,
        newSpeciesWindowDays:
          updates.newSpeciesWindowDays ??
          trackingSettings?.newSpeciesWindowDays ??
          TRACKING_DEFAULTS.newSpeciesWindowDays,
        syncIntervalMinutes:
          updates.syncIntervalMinutes ??
          trackingSettings?.syncIntervalMinutes ??
          TRACKING_DEFAULTS.syncIntervalMinutes,
        notificationSuppressionHours:
          updates.notificationSuppressionHours ??
          trackingSettings?.notificationSuppressionHours ??
          TRACKING_DEFAULTS.notificationSuppressionHours,
        yearlyTracking: updates.yearlyTracking
          ? {
              enabled:
                updates.yearlyTracking.enabled ??
                trackingSettings?.yearlyTracking?.enabled ??
                TRACKING_DEFAULTS.yearlyTracking.enabled,
              resetMonth:
                updates.yearlyTracking.resetMonth ??
                trackingSettings?.yearlyTracking?.resetMonth ??
                TRACKING_DEFAULTS.yearlyTracking.resetMonth,
              resetDay:
                updates.yearlyTracking.resetDay ??
                trackingSettings?.yearlyTracking?.resetDay ??
                TRACKING_DEFAULTS.yearlyTracking.resetDay,
              windowDays:
                updates.yearlyTracking.windowDays ??
                trackingSettings?.yearlyTracking?.windowDays ??
                TRACKING_DEFAULTS.yearlyTracking.windowDays,
            }
          : (trackingSettings?.yearlyTracking ?? { ...TRACKING_DEFAULTS.yearlyTracking }),
        seasonalTracking: updates.seasonalTracking
          ? {
              enabled:
                updates.seasonalTracking.enabled ??
                trackingSettings?.seasonalTracking?.enabled ??
                TRACKING_DEFAULTS.seasonalTracking.enabled,
              windowDays:
                updates.seasonalTracking.windowDays ??
                trackingSettings?.seasonalTracking?.windowDays ??
                TRACKING_DEFAULTS.seasonalTracking.windowDays,
              seasons:
                updates.seasonalTracking.seasons ??
                trackingSettings?.seasonalTracking?.seasons ??
                SEASON_DEFAULTS,
            }
          : (trackingSettings?.seasonalTracking ?? { ...TRACKING_DEFAULTS.seasonalTracking }),
      },
    });
  }

  /**
   * Clamp a value between min and max with fallback default.
   */
  function clampValue(value: string, min: number, max: number, fallback: number): number {
    const parsed = parseInt(value);
    return isNaN(parsed) ? fallback : Math.max(min, Math.min(max, parsed));
  }

  /**
   * Creates an onchange handler for number inputs that updates tracking settings.
   * Reduces duplication of the clamp + update pattern.
   */
  function createNumberInputHandler(
    limits: { min: number; max: number },
    fallback: number,
    updateFn: (_value: number) => void
  ) {
    return (e: Event) => {
      const target = e.target as HTMLInputElement;
      const value = clampValue(target.value, limits.min, limits.max, fallback);
      updateFn(value);
    };
  }

  /**
   * Get season defaults safely.
   * Safe: season is typed literal from SEASON_DEFAULTS constant.
   */
  function getSeasonDefaults(season: keyof typeof SEASON_DEFAULTS) {
    // eslint-disable-next-line security/detect-object-injection
    return SEASON_DEFAULTS[season];
  }

  /**
   * Get current season data safely.
   * Safe: season is typed literal from SEASON_DEFAULTS constant.
   */
  function getCurrentSeasonData(season: keyof typeof SEASON_DEFAULTS) {
    const seasons = trackingSettings?.seasonalTracking?.seasons ?? {};
    // eslint-disable-next-line security/detect-object-injection
    return seasons[season];
  }

  /**
   * Helper function to update a specific season's start month or day.
   * Handles merging with existing seasons and uses SEASON_DEFAULTS for missing values.
   */
  function updateSeasonDate(
    season: keyof typeof SEASON_DEFAULTS,
    field: 'startMonth' | 'startDay',
    value: number
  ) {
    const currentSeasons = trackingSettings?.seasonalTracking?.seasons ?? {};
    const seasonDefaults = getSeasonDefaults(season);
    const currentSeason = getCurrentSeasonData(season);

    updateTrackingSettings({
      seasonalTracking: {
        seasons: {
          ...currentSeasons,
          [season]: {
            startMonth:
              field === 'startMonth'
                ? value
                : (currentSeason?.startMonth ?? seasonDefaults.startMonth),
            startDay:
              field === 'startDay' ? value : (currentSeason?.startDay ?? seasonDefaults.startDay),
          },
        },
      },
    });
  }

  // Species data will be loaded in onMount after CSRF token is available

  async function loadSpeciesData() {
    speciesListState.loading = true;
    speciesListState.error = null;

    try {
      interface SpeciesListResponse {
        species?: Array<{ commonName?: string; scientificName?: string; label: string }>;
      }
      const data = await api.get<SpeciesListResponse>('/api/v2/species/all');
      const speciesList = data.species ?? [];
      speciesListState.data = speciesList.map(s => s.commonName || s.label);

      // Build lookup map: commonName (lowercase) → scientificName
      const map = new Map<string, string>();
      for (const s of speciesList) {
        const name = s.commonName || s.label;
        if (s.scientificName) {
          map.set(name.toLowerCase(), s.scientificName);
        }
      }
      speciesScientificNameMap = map;
    } catch (error) {
      logger.error('Failed to load species data:', error);
      // Provide specific error messages based on status code
      if (error instanceof ApiError) {
        speciesListState.error = t('settings.species.errors.speciesLoadFailed');
      } else {
        speciesListState.error = t('settings.species.errors.speciesLoadFailed');
      }
      speciesListState.data = [];
      speciesScientificNameMap = new Map();
    } finally {
      speciesListState.loading = false;
    }
  }

  // Re-entrancy guard for loadActiveSpecies to prevent concurrent API calls
  // and state corruption during Svelte's update cycle
  let isLoadingActiveSpecies = false;

  // Load active species list from API
  // Takes birdnet settings as parameter to ensure we have the correct values
  async function loadActiveSpecies(birdnetData: {
    latitude: number;
    longitude: number;
    rangeFilter?: { threshold: number };
  }) {
    // Prevent re-entrant calls that can corrupt Svelte's internal state
    if (isLoadingActiveSpecies) {
      return;
    }
    isLoadingActiveSpecies = true;

    // Check if location is configured (not 0,0)
    const locationConfigured = birdnetData.latitude !== 0 || birdnetData.longitude !== 0;

    // IMPORTANT: Capture all reactive values BEFORE any await
    // Reading derived values after await corrupts Svelte's reactivity system
    const currentInclude = settings.include;
    const currentConfig = settings.config;

    activeSpeciesState.loading = true;
    activeSpeciesState.error = null;
    activeSpeciesState.locationNotConfigured = !locationConfigured;
    activeSpeciesState.initialized = true;

    // If location is not configured, don't call the API - show warning instead
    if (!locationConfigured) {
      activeSpeciesState.loading = false;
      activeSpeciesState.data = null;
      isLoadingActiveSpecies = false;
      return;
    }

    try {
      const response = await api.post<{
        species: Array<{
          label: string;
          scientificName: string;
          commonName: string;
          score: number;
        }>;
        count: number;
        threshold: number;
        location: { latitude: number; longitude: number };
      }>('/api/v2/range/species/test', {
        latitude: birdnetData.latitude,
        longitude: birdnetData.longitude,
        threshold: birdnetData.rangeFilter?.threshold ?? 0.01,
      });

      // Cross-reference with include/exclude and config lists (using captured values)
      const includeSet = new Set(currentInclude.map(s => s.toLowerCase()));
      const configKeys = new Set(Object.keys(currentConfig).map(s => s.toLowerCase()));
      const threshold = response.threshold;

      // Filter species that pass the threshold OR are manually included
      const mappedSpecies: ActiveSpecies[] = response.species
        .filter(s => s.score >= threshold || includeSet.has(s.commonName.toLowerCase()))
        .map(s => ({
          commonName: s.commonName,
          scientificName: s.scientificName,
          score: s.score,
          isManuallyIncluded: includeSet.has(s.commonName.toLowerCase()),
          hasCustomConfig: configKeys.has(s.commonName.toLowerCase()),
        }));

      // Sort by score descending
      mappedSpecies.sort((a, b) => b.score - a.score);

      activeSpeciesState.data = {
        species: mappedSpecies,
        count: mappedSpecies.length, // Use filtered count, not total
        threshold: response.threshold,
        location: response.location,
        lastUpdated: new Date(),
      };
    } catch (error) {
      logger.error('Failed to load active species:', error);
      activeSpeciesState.error = t('settings.species.activeSpecies.errors.loadFailed');
    } finally {
      activeSpeciesState.loading = false;
      isLoadingActiveSpecies = false;
    }
  }

  // Auto-load active species when tab becomes active and settings are loaded
  $effect(() => {
    // Track these as dependencies - re-run when they change
    // IMPORTANT: Use store.formData.birdnet directly instead of derived $birdnetSettings
    // to avoid timing issues where the derived store hasn't recalculated yet
    const currentTab = activeTab;
    const settingsLoading = store.isLoading;
    const birdnetData = store.formData?.birdnet;
    const hasOriginalData = store.originalData?.birdnet !== undefined;

    // Wait for main settings to actually be loaded (originalData is set when settings are fetched)
    // This prevents flash of "location not configured" with default empty values
    if (settingsLoading || !hasOriginalData) {
      return;
    }

    // Don't track state reads to avoid infinite loops - read values INSIDE untrack
    const loading = untrack(() => activeSpeciesState.loading);
    const data = untrack(() => activeSpeciesState.data);
    const locationNotConfigured = untrack(() => activeSpeciesState.locationNotConfigured);
    const initialized = untrack(() => activeSpeciesState.initialized);

    const hasLocationData =
      birdnetData && birdnetData.latitude !== undefined && birdnetData.longitude !== undefined;
    const hasRealCoordinates =
      hasLocationData && (birdnetData.latitude !== 0 || birdnetData.longitude !== 0);
    const isActiveTab = currentTab === 'active';
    const canLoad = !loading || !initialized; // Allow first load even if loading=true initially
    const noDataYet = !data;

    // Also reload if we previously showed "location not configured" but now have real coordinates
    const needsRetryWithRealCoords = locationNotConfigured && hasRealCoordinates;

    if (hasLocationData && isActiveTab && canLoad && (noDataYet || needsRetryWithRealCoords)) {
      // CRITICAL: Use queueMicrotask to defer the call out of the $effect's synchronous context
      // This prevents state modifications from happening during Svelte's reactive update cycle,
      // which can corrupt Svelte's internal linked list and cause "Cannot read prev" errors
      queueMicrotask(() => {
        loadActiveSpecies(birdnetData);
      });
    }
  });

  // CSV download function for active species
  // Escape CSV field: wrap in quotes, escape internal quotes by doubling
  function escapeCsvField(field: string): string {
    // Always wrap in quotes and escape internal quotes
    return `"${field.replace(/"/g, '""')}"`;
  }

  function downloadActiveSpeciesCSV() {
    if (!activeSpeciesState.data?.species.length) return;

    const headers = ['Common Name', 'Scientific Name', 'Score', 'Included', 'Configured'];
    const rows = activeSpeciesState.data.species.map(s => [
      escapeCsvField(s.commonName),
      escapeCsvField(s.scientificName),
      escapeCsvField(s.score.toFixed(4)),
      escapeCsvField(s.isManuallyIncluded ? 'Yes' : 'No'),
      escapeCsvField(s.hasCustomConfig ? 'Yes' : 'No'),
    ]);

    const csvContent = [headers.map(escapeCsvField).join(','), ...rows.map(r => r.join(','))].join(
      '\n'
    );
    const blob = new Blob([csvContent], { type: 'text/csv;charset=utf-8;' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `active-species-${getLocalDateString()}.csv`;
    link.click();
    URL.revokeObjectURL(url);
  }

  // Format relative time for last updated
  function formatRelativeTime(date: Date): string {
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);

    if (diffMins < 1) return t('settings.species.activeSpecies.stats.justNow') || 'Just now';
    if (diffMins < 60)
      return (
        t('settings.species.activeSpecies.stats.minutesAgo', { count: diffMins }) ||
        `${diffMins}m ago`
      );

    const diffHours = Math.floor(diffMins / 60);
    return (
      t('settings.species.activeSpecies.stats.hoursAgo', { count: diffHours }) ||
      `${diffHours}h ago`
    );
  }

  // PERFORMANCE OPTIMIZATION: Debounced prediction functions with memoization
  let debounceTimeouts = { include: 0, exclude: 0, config: 0 };

  // Clean up timeouts on component destroy to prevent memory leaks
  onDestroy(() => {
    clearTimeout(debounceTimeouts.include);
    clearTimeout(debounceTimeouts.exclude);
    clearTimeout(debounceTimeouts.config);
  });

  function updateIncludePredictions(input: string) {
    clearTimeout(debounceTimeouts.include);
    debounceTimeouts.include = window.setTimeout(() => {
      if (!input || input.length < 2) {
        includePredictions = [];
        return;
      }

      const inputLower = input.toLowerCase();
      const includeSet = new Set(settings.include.map(s => s.toLowerCase())); // Use Set with lowercase for case-insensitive comparison
      includePredictions = allSpecies
        .filter(
          species =>
            species.toLowerCase().includes(inputLower) && !includeSet.has(species.toLowerCase())
        )
        .slice(0, 10);
    }, 150); // Debounce by 150ms
  }

  function updateExcludePredictions(input: string) {
    clearTimeout(debounceTimeouts.exclude);
    debounceTimeouts.exclude = window.setTimeout(() => {
      if (!input || input.length < 2) {
        excludePredictions = [];
        return;
      }

      const inputLower = input.toLowerCase();
      const excludeSet = new Set(settings.exclude.map(s => s.toLowerCase())); // Use Set with lowercase for case-insensitive comparison
      excludePredictions = allSpecies
        .filter(
          species =>
            species.toLowerCase().includes(inputLower) && !excludeSet.has(species.toLowerCase())
        )
        .slice(0, 10);
    }, 150); // Debounce by 150ms
  }

  function updateConfigPredictions(input: string) {
    clearTimeout(debounceTimeouts.config);
    debounceTimeouts.config = window.setTimeout(() => {
      if (!input || input.length < 2) {
        configPredictions = [];
        return;
      }

      const inputLower = input.toLowerCase();
      const existingConfigs = new Set(Object.keys(settings.config).map(s => s.toLowerCase())); // Use Set
      configPredictions = allSpecies
        .filter(species => {
          const speciesLower = species.toLowerCase();
          return speciesLower.includes(inputLower) && !existingConfigs.has(speciesLower);
        })
        .slice(0, 10);
    }, 150); // Debounce by 150ms
  }

  // Species management functions
  function addIncludeSpecies(species: string) {
    if (!species.trim() || settings.include.includes(species)) return;

    const updatedSpecies = [...settings.include, species];
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      species: {
        ...settings,
        include: updatedSpecies,
      },
    });
  }

  function removeIncludeSpecies(species: string) {
    const updatedSpecies = settings.include.filter(s => s !== species);
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      species: {
        ...settings,
        include: updatedSpecies,
      },
    });
  }

  function addExcludeSpecies(species: string) {
    if (!species.trim() || settings.exclude.includes(species)) return;

    const updatedSpecies = [...settings.exclude, species];
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      species: {
        ...settings,
        exclude: updatedSpecies,
      },
    });
  }

  function removeExcludeSpecies(species: string) {
    const updatedSpecies = settings.exclude.filter(s => s !== species);
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      species: {
        ...settings,
        exclude: updatedSpecies,
      },
    });
  }

  function removeConfig(species: string) {
    const newConfig = Object.fromEntries(
      Object.entries(settings.config).filter(([key]) => key !== species)
    );
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      species: {
        ...settings,
        config: newConfig,
      },
    });
  }

  function startEdit(species: string) {
    const config = safeGet(settings.config, species, { threshold: 0.5, interval: 0, actions: [] });
    configInputValue = species;
    newThreshold = config.threshold;
    newInterval = config.interval || 0;
    editingSpecies = species;
    showAddForm = true;

    // Load existing action if present
    const existingAction = config.actions?.[0];
    if (existingAction) {
      actionCommand = existingAction.command || '';
      actionParameters = Array.isArray(existingAction.parameters)
        ? existingAction.parameters.join(',')
        : '';
      actionExecuteDefaults = existingAction.executeDefaults !== false;
    } else {
      // Reset action fields
      actionCommand = '';
      actionParameters = '';
      actionExecuteDefaults = true;
    }
    showActions = false; // Start with actions collapsed
  }

  async function saveConfig() {
    const species = configInputValue.trim();
    if (!species) return;

    const threshold = Number(newThreshold);
    if (threshold < 0 || threshold > 1) return;

    const interval = Number(newInterval) || 0;

    // Build actions array if command is provided
    const actions = [];
    if (actionCommand.trim()) {
      actions.push({
        type: 'ExecuteCommand' as const,
        command: actionCommand.trim(),
        parameters: actionParameters
          .split(',')
          .map(p => p.trim())
          .filter(p => p),
        executeDefaults: actionExecuteDefaults,
      });
    }

    let updatedConfig = { ...settings.config };

    if (editingSpecies && editingSpecies !== species) {
      // Check if new species name already exists
      if (species in updatedConfig) {
        // Prevent overwriting existing configuration
        toastActions.error(t('settings.species.duplicateConfigError', { species }));
        return;
      }
      // Rename: delete old entry and create new
      // eslint-disable-next-line security/detect-object-injection -- editingSpecies is controlled component state
      delete updatedConfig[editingSpecies];
    }

    // Add/update species configuration
    // eslint-disable-next-line security/detect-object-injection -- species is controlled component state
    updatedConfig[species] = {
      threshold,
      interval,
      actions,
    };

    try {
      // Update the section in form data
      settingsActions.updateSection('realtime', {
        ...$realtimeSettings,
        species: {
          ...settings,
          config: updatedConfig,
        },
      });

      // Actually save the settings to the server
      await settingsActions.saveSettings();

      // Show success feedback
      toastActions.success(
        editingSpecies
          ? t('settings.species.configUpdated', { species })
          : t('settings.species.configAdded', { species })
      );

      // Reset form only after successful save
      cancelEdit();
    } catch (error) {
      logger.error('Failed to save species configuration:', error);
      toastActions.error(t('settings.species.saveError', { species }));
      // Don't reset the form on error so user can retry
    }
  }

  function cancelEdit() {
    configInputValue = '';
    newThreshold = 0.5;
    newInterval = 0;
    editingSpecies = null;
    showAddForm = false;
    showActions = false;
    actionCommand = '';
    actionParameters = '';
    actionExecuteDefaults = true;
  }

  // Tab state
  let activeTab = $state('active');

  // Tab definitions for the species settings page
  let tabs = $derived<TabDefinition[]>([
    {
      id: 'active',
      label: t('settings.species.activeSpecies.tabLabel'),
      icon: ListCheck,
      content: activeTabContent,
      // No hasChanges - this is read-only
    },
    {
      id: 'include',
      label: t('settings.species.alwaysInclude.tabLabel'),
      icon: CirclePlus,
      content: includeTabContent,
      hasChanges: includeHasChanges,
    },
    {
      id: 'exclude',
      label: t('settings.species.alwaysExclude.tabLabel'),
      icon: CircleMinus,
      content: excludeTabContent,
      hasChanges: excludeHasChanges,
    },
    {
      id: 'config',
      label: t('settings.species.customConfiguration.tabLabel'),
      icon: Settings2,
      content: configTabContent,
      hasChanges: configHasChanges,
    },
    {
      id: 'tracking',
      label: t('settings.species.tracking.tabLabel'),
      icon: CalendarClock,
      content: trackingTabContent,
      hasChanges: trackingHasChanges,
    },
    {
      id: 'dynamicThreshold',
      label: t('settings.species.dynamicThreshold.tabLabel'),
      icon: Activity,
      content: dynamicThresholdTabContent,
      // No hasChanges - this is read-only runtime data with its own save/reset actions
    },
  ]);
</script>

<!-- Active Species Tab Content -->
{#snippet activeTabContent()}
  <div class="space-y-4">
    <!-- Stats Bar -->
    {#if activeSpeciesState.data}
      <div class="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <div
          class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-3 shadow-sm"
        >
          <div class="flex items-center gap-2 mb-1.5">
            <div
              class="p-1 rounded-md bg-[color-mix(in_srgb,var(--color-success)_10%,transparent)]"
            >
              <Bird class="w-3.5 h-3.5 text-[var(--color-success)]" />
            </div>
            <span class="text-xs font-medium text-muted"
              >{t('settings.species.activeSpecies.stats.species')}</span
            >
          </div>
          <span class="font-mono tabular-nums text-xl font-semibold pl-0.5"
            >{activeSpeciesState.data.count}</span
          >
        </div>

        <div
          class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-3 shadow-sm"
        >
          <div class="flex items-center gap-2 mb-1.5">
            <div class="p-1 rounded-md bg-[color-mix(in_srgb,var(--color-info)_10%,transparent)]">
              <MapPin class="w-3.5 h-3.5 text-[var(--color-info)]" />
            </div>
            <span class="text-xs font-medium text-muted"
              >{t('settings.species.activeSpecies.stats.location')}</span
            >
          </div>
          <span class="font-mono tabular-nums text-xl font-semibold pl-0.5"
            >{activeSpeciesState.data.location.latitude.toFixed(2)}°, {activeSpeciesState.data.location.longitude.toFixed(
              2
            )}°</span
          >
        </div>

        <div
          class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-3 shadow-sm"
        >
          <div class="flex items-center gap-2 mb-1.5">
            <div class="p-1 rounded-md bg-violet-500/10">
              <SlidersHorizontal class="w-3.5 h-3.5 text-violet-500" />
            </div>
            <span class="text-xs font-medium text-muted"
              >{t('settings.species.activeSpecies.stats.threshold')}</span
            >
          </div>
          <span class="font-mono tabular-nums text-xl font-semibold pl-0.5"
            >{activeSpeciesState.data.threshold}</span
          >
        </div>

        <div
          class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-3 shadow-sm"
        >
          <div class="flex items-center gap-2 mb-1.5">
            <div
              class="p-1 rounded-md bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)]"
            >
              <Clock class="w-3.5 h-3.5 text-[var(--color-warning)]" />
            </div>
            <span class="text-xs font-medium text-muted"
              >{t('settings.species.activeSpecies.stats.updated')}</span
            >
          </div>
          <span class="font-mono tabular-nums text-xl font-semibold pl-0.5"
            >{formatRelativeTime(activeSpeciesState.data.lastUpdated)}</span
          >
        </div>
      </div>
    {/if}

    <!-- Main Content -->
    {#if activeSpeciesState.loading || store.isLoading || !store.originalData?.birdnet}
      <div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl shadow-sm">
        <div class="flex items-center justify-center py-12">
          <div
            class="inline-block w-8 h-8 border-4 border-[var(--surface-300)] border-t-[var(--color-primary)] rounded-full animate-spin"
          ></div>
        </div>
      </div>
    {:else if activeSpeciesState.error}
      <div
        class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl shadow-sm p-4"
      >
        <div
          class="flex items-start gap-3 p-4 rounded-lg bg-[color-mix(in_srgb,var(--color-error)_10%,transparent)]"
        >
          <p class="text-sm text-[var(--color-error)]">{activeSpeciesState.error}</p>
          <button
            type="button"
            class="mt-2 inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[color-mix(in_srgb,var(--color-error)_20%,transparent)] hover:bg-[color-mix(in_srgb,var(--color-error)_30%,transparent)] text-[var(--color-error)] transition-colors"
            onclick={() => {
              const birdnetData = store.formData?.birdnet;
              if (birdnetData?.latitude !== undefined && birdnetData?.longitude !== undefined) {
                loadActiveSpecies(birdnetData);
              }
            }}
          >
            {t('settings.species.activeSpecies.retry') || 'Retry'}
          </button>
        </div>
      </div>
    {:else if activeSpeciesState.locationNotConfigured}
      <div
        class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl shadow-sm p-4"
      >
        <div
          class="p-4 rounded-lg bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_20%,transparent)]"
        >
          <div class="flex items-start gap-3">
            <MapPin class="w-5 h-5 text-[var(--color-warning)] shrink-0 mt-0.5" />
            <div>
              <p class="font-medium text-[var(--color-warning)]">
                {t('settings.species.activeSpecies.locationNotConfigured.title') ||
                  'Location Not Configured'}
              </p>
              <p class="text-sm text-muted mt-1">
                {t('settings.species.activeSpecies.locationNotConfigured.description') ||
                  'Set your location in Main Settings to see species available in your area. The range filter uses your location to determine which species are likely to be found nearby.'}
              </p>
              <a
                href="/ui/settings/main"
                class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-warning)] text-[var(--color-warning-content)] hover:bg-[var(--color-warning-hover)] transition-colors mt-3"
              >
                {t('settings.species.activeSpecies.locationNotConfigured.action') ||
                  'Configure Location'}
              </a>
            </div>
          </div>
        </div>
      </div>
    {:else if activeSpeciesState.data}
      <SpeciesTable
        species={activeSpeciesState.data.species}
        title={t('settings.species.activeSpecies.title')}
        description={t('settings.species.activeSpecies.description')}
        onDownloadCsv={downloadActiveSpeciesCSV}
      />

      <SettingsNote>
        {t('settings.species.activeSpecies.infoNote')}
      </SettingsNote>
    {:else}
      <div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl shadow-sm">
        <div class="text-center py-8 text-muted">
          <Bird class="size-12 mx-auto mb-3 opacity-30" />
          <p class="text-sm font-medium">{t('settings.species.activeSpecies.empty.title')}</p>
          <p class="text-xs mt-1">{t('settings.species.activeSpecies.empty.description')}</p>
        </div>
      </div>
    {/if}
  </div>
{/snippet}

<!-- Include Species Tab Content -->
{#snippet includeTabContent()}
  <SpeciesListCard
    title={t('settings.species.alwaysInclude.title')}
    species={settings.include}
    icon={CirclePlus}
    iconColorClass="emerald"
    scientificNameMap={speciesScientificNameMap}
    predictions={includePredictions}
    bind:inputValue={includeInputValue}
    inputLabel={t('settings.species.addSpeciesToIncludeLabel')}
    inputPlaceholder={t('settings.species.addSpeciesToInclude')}
    emptyMessage={t('settings.species.alwaysInclude.noSpeciesMessage')}
    disabled={store.isLoading || store.isSaving}
    onAdd={addIncludeSpecies}
    onRemove={removeIncludeSpecies}
    onInput={updateIncludePredictions}
  />
{/snippet}

<!-- Exclude Species Tab Content -->
{#snippet excludeTabContent()}
  <SpeciesListCard
    title={t('settings.species.alwaysExclude.title')}
    species={settings.exclude}
    icon={CircleMinus}
    iconColorClass="red"
    scientificNameMap={speciesScientificNameMap}
    predictions={excludePredictions}
    bind:inputValue={excludeInputValue}
    inputLabel={t('settings.species.addSpeciesToExcludeLabel')}
    inputPlaceholder={t('settings.species.addSpeciesToExclude')}
    emptyMessage={t('settings.species.alwaysExclude.noSpeciesMessage')}
    disabled={store.isLoading || store.isSaving}
    onAdd={addExcludeSpecies}
    onRemove={removeExcludeSpecies}
    onInput={updateExcludePredictions}
  />
{/snippet}

<!-- Custom Configuration Tab Content -->
{#snippet configTabContent()}
  <div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl shadow-sm">
    <!-- Header -->
    <div class="flex items-center justify-between px-4 py-3 border-b border-[var(--border-100)]">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-teal-500/10">
          <Settings2 class="w-4 h-4 text-teal-500" />
        </div>
        <h3 class="text-xs font-semibold uppercase tracking-wider text-muted">
          {t('settings.species.customConfiguration.title')}
        </h3>
        {#if Object.keys(settings.config).length > 0}
          <span
            class="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium bg-slate-500/10 text-muted"
          >
            {Object.keys(settings.config).length}
          </span>
        {/if}
      </div>
      {#if !showAddForm}
        <button
          type="button"
          class="inline-flex items-center justify-center gap-2 h-8 px-3 text-xs font-medium rounded-lg bg-teal-500 text-white hover:bg-teal-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          data-testid="add-configuration-button"
          onclick={() => (showAddForm = true)}
          disabled={store.isLoading || store.isSaving}
        >
          <Plus class="size-3.5" />
          {t('settings.species.customConfiguration.addConfiguration')}
        </button>
      {:else}
        <span class="text-xs font-medium text-muted">
          {editingSpecies
            ? t('settings.species.customConfiguration.editing', { species: editingSpecies })
            : t('settings.species.customConfiguration.newConfiguration')}
        </span>
      {/if}
    </div>

    <div class="p-4 space-y-4">
      <!-- Compact Add/Edit Form -->
      {#if showAddForm}
        <div
          class="border border-[var(--border-100)] rounded-xl p-3 bg-[var(--surface-100)] space-y-3"
        >
          <!-- Main configuration row -->
          <div class="grid grid-cols-12 gap-3 items-end">
            <!-- Species Input -->
            <div class="col-span-4">
              <label class="flex flex-col gap-1 py-1" for="config-species">
                <span class="text-xs font-medium text-muted"
                  >{t('settings.species.customConfiguration.columnHeaders.species')}</span
                >
              </label>
              <SpeciesInput
                id="config-species"
                bind:value={configInputValue}
                placeholder={t('settings.species.customConfiguration.searchPlaceholder')}
                predictions={configPredictions}
                onInput={updateConfigPredictions}
                onPredictionSelect={handleSpeciesPicked}
                onAdd={handleSpeciesPicked}
                buttonText=""
                buttonIcon={false}
                size="xs"
                disabled={store.isLoading || store.isSaving}
              />
            </div>

            <!-- Threshold -->
            <div class="col-span-3">
              <label class="flex items-center justify-between py-1" for="config-threshold">
                <span class="text-xs font-medium text-muted"
                  >{t('settings.species.customConfiguration.labels.threshold')}</span
                >
                <span class="text-xs text-muted">{newThreshold.toFixed(2)}</span>
              </label>
              <input
                id="config-threshold"
                type="range"
                min="0"
                max="1"
                step="0.01"
                value={newThreshold}
                oninput={e => (newThreshold = Number(e.currentTarget.value))}
                class="w-full h-2 bg-[var(--surface-300)] rounded-lg appearance-none cursor-pointer accent-[var(--color-primary)]"
              />
            </div>

            <!-- Interval -->
            <div class="col-span-3">
              <label class="flex flex-col gap-1 py-1" for="config-interval">
                <span class="text-xs font-medium text-muted"
                  >{t('settings.species.customConfiguration.labels.intervalSeconds')}</span
                >
              </label>
              <input
                id="config-interval"
                type="number"
                value={newInterval}
                onchange={e => (newInterval = Number(e.currentTarget.value))}
                min="0"
                max="3600"
                class="w-full h-7 px-2 text-sm rounded-md border border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]"
                placeholder="0"
              />
            </div>

            <!-- Buttons -->
            <div class="col-span-2 flex gap-1">
              <button
                type="button"
                class="inline-flex items-center justify-center flex-1 h-7 px-2 text-xs font-medium rounded-md bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:opacity-90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                data-testid="save-config-button"
                onclick={saveConfig}
                disabled={!configInputValue.trim() ||
                  newThreshold < 0 ||
                  newThreshold > 1 ||
                  store.isLoading ||
                  store.isSaving}
              >
                {#if store.isSaving}
                  <span
                    class="inline-block w-3 h-3 border-2 border-[var(--color-primary-content)] border-t-transparent rounded-full animate-spin mr-1"
                  ></span>
                  {t('settings.species.customConfiguration.saving')}
                {:else}
                  {editingSpecies
                    ? t('settings.species.customConfiguration.save')
                    : t('settings.species.customConfiguration.labels.addButton')}
                {/if}
              </button>
              <button
                type="button"
                class="inline-flex items-center justify-center flex-1 h-7 px-2 text-xs font-medium rounded-md bg-transparent hover:bg-[var(--surface-200)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                onclick={cancelEdit}
                disabled={store.isSaving}
              >
                {t('settings.species.customConfiguration.cancel')}
              </button>
            </div>
          </div>

          <!-- Actions Toggle -->
          <div class="border-t border-[var(--border-100)] pt-2">
            <button
              type="button"
              class="flex items-center gap-2 text-xs font-medium hover:text-[var(--color-primary)] transition-colors"
              onclick={() => (showActions = !showActions)}
              aria-expanded={showActions}
              aria-controls="actionsSection"
            >
              <span class="transition-transform duration-200" class:rotate-90={showActions}>
                <ChevronRight class="size-4" />
              </span>
              <span>{t('settings.species.customConfiguration.configureActions')}</span>
              {#if actionCommand}
                <span
                  class="inline-flex items-center px-1.5 py-0.5 text-[10px] font-medium rounded-full bg-teal-500/15 text-teal-600 dark:text-teal-400"
                  >{t('settings.species.customConfiguration.actionsConfigured')}</span
                >
              {/if}
            </button>
          </div>

          <!-- Actions Section -->
          {#if showActions}
            <div class="space-y-3 pl-6" id="actionsSection">
              <!-- Command Input -->
              <div>
                <label class="flex flex-col gap-1 py-1" for="action-command">
                  <span class="text-xs font-medium text-muted"
                    >{t('settings.species.actionsModal.command.label')}</span
                  >
                </label>
                <input
                  id="action-command"
                  type="text"
                  bind:value={actionCommand}
                  placeholder={t('settings.species.commandPathPlaceholder')}
                  class="w-full h-7 px-2 text-sm rounded-md border border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]"
                />
                <span class="text-xs text-muted mt-1 block"
                  >{t('settings.species.actionsModal.command.helpText')}</span
                >
              </div>

              <!-- Parameters -->
              <div>
                <label class="flex flex-col gap-1 py-1" for="action-parameters">
                  <span class="text-xs font-medium text-muted flex items-center gap-1">
                    {t('settings.species.actionsModal.parameters.label')}
                    <span
                      class="text-muted"
                      role="img"
                      aria-label={t('settings.species.actionsModal.parameters.tooltip')}
                      title={t('settings.species.actionsModal.parameters.tooltip')}
                    >
                      ⓘ
                    </span>
                  </span>
                </label>
                <input
                  id="action-parameters"
                  type="text"
                  bind:value={actionParameters}
                  placeholder={t('settings.species.actionsModal.parameters.placeholder')}
                  class="w-full h-7 px-2 text-sm rounded-md border border-[var(--border-100)] bg-[var(--surface-200)]/50 focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]"
                  title={t('settings.species.actionsModal.parameters.tooltip')}
                />
                <span class="text-xs text-muted mt-1 block"
                  >{t('settings.species.actionsModal.parameters.helpText')}</span
                >
              </div>

              <!-- Parameter Buttons -->
              <div>
                <div class="text-xs font-medium mb-1">
                  {t('settings.species.actionsModal.parameters.availableTitle')}
                </div>
                <div class="flex flex-wrap gap-1">
                  <button
                    type="button"
                    class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md border border-[var(--border-100)] bg-transparent hover:bg-[var(--surface-200)] transition-colors"
                    onclick={() => addParameter('CommonName')}
                    >{t('settings.species.actionsModal.parameters.buttons.commonName')}</button
                  >
                  <button
                    type="button"
                    class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md border border-[var(--border-100)] bg-transparent hover:bg-[var(--surface-200)] transition-colors"
                    onclick={() => addParameter('ScientificName')}
                    >{t('settings.species.actionsModal.parameters.buttons.scientificName')}</button
                  >
                  <button
                    type="button"
                    class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md border border-[var(--border-100)] bg-transparent hover:bg-[var(--surface-200)] transition-colors"
                    onclick={() => addParameter('Confidence')}
                    >{t('settings.species.actionsModal.parameters.buttons.confidence')}</button
                  >
                  <button
                    type="button"
                    class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md border border-[var(--border-100)] bg-transparent hover:bg-[var(--surface-200)] transition-colors"
                    onclick={() => addParameter('Time')}
                    >{t('settings.species.actionsModal.parameters.buttons.time')}</button
                  >
                  <button
                    type="button"
                    class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md border border-[var(--border-100)] bg-transparent hover:bg-[var(--surface-200)] transition-colors"
                    onclick={() => addParameter('Source')}
                    >{t('settings.species.actionsModal.parameters.buttons.source')}</button
                  >
                  <button
                    type="button"
                    class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md bg-[var(--color-warning)] text-[var(--color-warning-content)] hover:opacity-90 transition-colors"
                    onclick={clearParameters}
                    >{t('settings.species.actionsModal.parameters.buttons.clearParameters')}</button
                  >
                </div>
              </div>

              <!-- Execute Defaults Checkbox -->
              <div>
                <label
                  class="flex items-center cursor-pointer justify-start gap-2"
                  for="action-execute-defaults"
                >
                  <input
                    id="action-execute-defaults"
                    type="checkbox"
                    bind:checked={actionExecuteDefaults}
                    class="appearance-none w-3.5 h-3.5 border-2 border-[var(--border-100)] rounded bg-[var(--surface-100)] cursor-pointer transition-all checked:bg-[var(--color-primary)] checked:border-[var(--color-primary)]"
                  />
                  <span class="text-xs text-muted"
                    >{t('settings.species.actionsModal.executeDefaults.label')}</span
                  >
                </label>
                <span class="text-xs text-muted mt-1 block"
                  >{t('settings.species.actionsModal.executeDefaults.helpText')}</span
                >
              </div>
            </div>
          {/if}
        </div>
      {/if}

      <!-- Configuration List -->
      {#if Object.keys(settings.config).length > 0}
        <ResizableContainer defaultHeight={448} minHeight={200} maxHeight={800}>
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-[var(--border-100)]">
                <th class="text-left py-2 px-3 text-xs font-medium text-muted"
                  >{t('settings.species.customConfiguration.columnHeaders.species')}</th
                >
                <th class="text-left py-2 px-3 text-xs font-medium text-muted">
                  {t('settings.species.activeSpecies.columns.scientificName')}
                </th>
                <th class="text-left py-2 px-3 text-xs font-medium text-muted w-48"
                  >{t('settings.species.customConfiguration.list.threshold')}</th
                >
                <th class="text-left py-2 px-3 text-xs font-medium text-muted"
                  >{t('settings.species.customConfiguration.list.interval')}</th
                >
                <th class="w-24"></th>
              </tr>
            </thead>
            <tbody>
              {#each Object.entries(settings.config) as [species, config] (species)}
                <tr
                  class="border-b last:border-b-0 border-[var(--border-100)]/50 hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition-colors"
                >
                  <td class="py-2.5 px-3">
                    <span class="font-medium text-sm">{species}</span>
                    {#if config.actions?.length > 0}
                      <span
                        class="ml-2 inline-flex items-center px-1.5 py-0.5 text-[10px] font-medium rounded-full bg-teal-500/15 text-teal-600 dark:text-teal-400"
                      >
                        {t('settings.species.customConfiguration.list.actionBadge')}
                      </span>
                    {/if}
                  </td>
                  <td class="py-2.5 px-3">
                    <span class="text-xs text-muted italic"
                      >{speciesScientificNameMap.get(species.toLowerCase()) ?? ''}</span
                    >
                  </td>
                  <td class="py-2.5 px-3">
                    <div class="flex items-center gap-2">
                      <div
                        class="flex-1 h-1.5 rounded-full bg-[var(--surface-300)] overflow-hidden"
                      >
                        <div
                          class="h-full rounded-full bg-teal-500 transition-all"
                          style:width="{((config.threshold ?? 0) * 100).toFixed(0)}%"
                        ></div>
                      </div>
                      <span class="font-mono tabular-nums text-xs font-medium w-8 text-right"
                        >{(config.threshold ?? 0).toFixed(2)}</span
                      >
                    </div>
                  </td>
                  <td class="py-2.5 px-3">
                    <span class="font-mono tabular-nums text-xs font-medium">
                      {config.interval > 0
                        ? `${config.interval}s`
                        : t('settings.species.customConfiguration.list.intervalNone')}
                    </span>
                  </td>
                  <td class="py-2.5 px-3 text-right">
                    <div class="flex items-center justify-end gap-1">
                      <button
                        type="button"
                        class="inline-flex items-center justify-center p-1.5 rounded-md bg-transparent hover:bg-[var(--surface-200)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                        onclick={() => startEdit(species)}
                        title={t('settings.species.customConfiguration.list.editTitle')}
                        aria-label={t('settings.species.customConfiguration.list.editTitle')}
                        disabled={store.isLoading || store.isSaving}
                      >
                        <SquarePen class="size-4" />
                      </button>
                      <button
                        type="button"
                        class="inline-flex items-center justify-center p-1.5 rounded-md transition-colors cursor-pointer hover:bg-[color-mix(in_srgb,var(--color-error)_10%,transparent)] text-muted hover:text-[var(--color-error)] disabled:opacity-50 disabled:cursor-not-allowed"
                        onclick={() => removeConfig(species)}
                        title={t('settings.species.customConfiguration.list.removeTitle')}
                        aria-label={t('settings.species.customConfiguration.list.removeTitle')}
                        disabled={store.isLoading || store.isSaving}
                      >
                        <Trash2 class="w-3.5 h-3.5" />
                      </button>
                    </div>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </ResizableContainer>
      {/if}

      <!-- Empty State -->
      {#if Object.keys(settings.config).length === 0 && !showAddForm}
        <div class="text-center py-8 text-muted">
          <Settings2 class="size-12 mx-auto mb-3 opacity-30" />
          <p class="text-sm font-medium">
            {t('settings.species.customConfiguration.emptyState.title')}
          </p>
          <p class="text-xs mt-1">
            {t('settings.species.customConfiguration.emptyState.description')}
          </p>
        </div>
      {/if}
    </div>
  </div>
{/snippet}

<!-- Tracking Tab Content -->
{#snippet trackingTabContent()}
  <div class="space-y-6">
    <!-- Main Tracking Settings -->
    <SettingsSection
      title={t('settings.species.tracking.title')}
      description={t('settings.species.tracking.description')}
      defaultOpen={true}
      originalData={store.originalData.realtime?.speciesTracking}
      currentData={store.formData.realtime?.speciesTracking}
    >
      <div class="space-y-4">
        <!-- Enable Species Tracking -->
        <Checkbox
          checked={trackingSettings?.enabled ?? false}
          label={t('settings.species.tracking.enabled.label')}
          helpText={t('settings.species.tracking.enabled.helpText')}
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateTrackingSettings({ enabled: value })}
        />

        {#if trackingSettings?.enabled}
          <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
            <!-- New Species Window Days -->
            <div>
              <label class="flex items-center py-2" for="new-species-window">
                <span class="text-sm font-medium">
                  {t('settings.species.tracking.newSpeciesWindowDays.label')}
                </span>
              </label>
              <div class="flex">
                <input
                  id="new-species-window"
                  type="number"
                  min={TRACKING_LIMITS.days.min}
                  max={TRACKING_LIMITS.days.max}
                  value={trackingSettings?.newSpeciesWindowDays ??
                    TRACKING_DEFAULTS.newSpeciesWindowDays}
                  onchange={createNumberInputHandler(
                    TRACKING_LIMITS.days,
                    TRACKING_DEFAULTS.newSpeciesWindowDays,
                    v => updateTrackingSettings({ newSpeciesWindowDays: v })
                  )}
                  class="flex-1 h-9 px-3 text-sm rounded-l-lg border border-r-0 border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                  disabled={store.isLoading || store.isSaving}
                />
                <span
                  class="inline-flex items-center justify-center h-9 px-3 text-sm font-medium rounded-r-lg border border-[var(--border-100)] bg-[var(--surface-200)] text-muted"
                  >{t('settings.species.tracking.units.days')}</span
                >
              </div>
              <p class="text-xs text-muted mt-1">
                {t('settings.species.tracking.newSpeciesWindowDays.helpText')}
              </p>
            </div>

            <!-- Sync Interval Minutes -->
            <div>
              <label class="flex items-center py-2" for="sync-interval">
                <span class="text-sm font-medium">
                  {t('settings.species.tracking.syncIntervalMinutes.label')}
                </span>
              </label>
              <div class="flex">
                <input
                  id="sync-interval"
                  type="number"
                  min={TRACKING_LIMITS.syncMinutes.min}
                  max={TRACKING_LIMITS.syncMinutes.max}
                  value={trackingSettings?.syncIntervalMinutes ??
                    TRACKING_DEFAULTS.syncIntervalMinutes}
                  onchange={createNumberInputHandler(
                    TRACKING_LIMITS.syncMinutes,
                    TRACKING_DEFAULTS.syncIntervalMinutes,
                    v => updateTrackingSettings({ syncIntervalMinutes: v })
                  )}
                  class="flex-1 h-9 px-3 text-sm rounded-l-lg border border-r-0 border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                  disabled={store.isLoading || store.isSaving}
                />
                <span
                  class="inline-flex items-center justify-center h-9 px-3 text-sm font-medium rounded-r-lg border border-[var(--border-100)] bg-[var(--surface-200)] text-muted"
                  >{t('settings.species.tracking.units.min')}</span
                >
              </div>
              <p class="text-xs text-muted mt-1">
                {t('settings.species.tracking.syncIntervalMinutes.helpText')}
              </p>
            </div>

            <!-- Notification Suppression Hours -->
            <div>
              <label class="flex items-center py-2" for="notification-suppression">
                <span class="text-sm font-medium">
                  {t('settings.species.tracking.notificationSuppressionHours.label')}
                </span>
              </label>
              <div class="flex">
                <input
                  id="notification-suppression"
                  type="number"
                  min={TRACKING_LIMITS.suppressionHours.min}
                  max={TRACKING_LIMITS.suppressionHours.max}
                  value={trackingSettings?.notificationSuppressionHours ??
                    TRACKING_DEFAULTS.notificationSuppressionHours}
                  onchange={createNumberInputHandler(
                    TRACKING_LIMITS.suppressionHours,
                    TRACKING_DEFAULTS.notificationSuppressionHours,
                    v => updateTrackingSettings({ notificationSuppressionHours: v })
                  )}
                  class="flex-1 h-9 px-3 text-sm rounded-l-lg border border-r-0 border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                  disabled={store.isLoading || store.isSaving}
                />
                <span
                  class="inline-flex items-center justify-center h-9 px-3 text-sm font-medium rounded-r-lg border border-[var(--border-100)] bg-[var(--surface-200)] text-muted"
                  >{t('settings.species.tracking.units.hours')}</span
                >
              </div>
              <p class="text-xs text-muted mt-1">
                {t('settings.species.tracking.notificationSuppressionHours.helpText')}
              </p>
            </div>
          </div>
        {/if}
      </div>
    </SettingsSection>

    {#if trackingSettings?.enabled}
      <!-- Yearly Tracking Settings -->
      <SettingsSection
        title={t('settings.species.tracking.yearly.title')}
        description={t('settings.species.tracking.yearly.description')}
        defaultOpen={false}
      >
        <div class="space-y-4">
          <!-- Enable Yearly Tracking -->
          <Checkbox
            checked={trackingSettings?.yearlyTracking?.enabled ?? false}
            label={t('settings.species.tracking.yearly.enabled.label')}
            helpText={t('settings.species.tracking.yearly.enabled.helpText')}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateTrackingSettings({ yearlyTracking: { enabled: value } })}
          />

          {#if trackingSettings?.yearlyTracking?.enabled}
            <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
              <!-- Reset Month -->
              <div>
                <label class="flex items-center py-2" for="yearly-reset-month">
                  <span class="text-sm font-medium">
                    {t('settings.species.tracking.yearly.resetMonth.label')}
                  </span>
                </label>
                <SelectDropdown
                  value={String(
                    trackingSettings?.yearlyTracking?.resetMonth ??
                      TRACKING_DEFAULTS.yearlyTracking.resetMonth
                  )}
                  options={monthOptions}
                  disabled={store.isLoading || store.isSaving}
                  menuSize="sm"
                  onChange={value =>
                    updateTrackingSettings({ yearlyTracking: { resetMonth: Number(value) } })}
                />
                <p class="text-xs text-muted mt-1">
                  {t('settings.species.tracking.yearly.resetMonth.helpText')}
                </p>
              </div>

              <!-- Reset Day -->
              <div>
                <label class="flex items-center py-2" for="yearly-reset-day">
                  <span class="text-sm font-medium">
                    {t('settings.species.tracking.yearly.resetDay.label')}
                  </span>
                </label>
                <input
                  id="yearly-reset-day"
                  type="number"
                  min={TRACKING_LIMITS.dayOfMonth.min}
                  max={TRACKING_LIMITS.dayOfMonth.max}
                  value={trackingSettings?.yearlyTracking?.resetDay ??
                    TRACKING_DEFAULTS.yearlyTracking.resetDay}
                  onchange={createNumberInputHandler(
                    TRACKING_LIMITS.dayOfMonth,
                    TRACKING_DEFAULTS.yearlyTracking.resetDay,
                    v => updateTrackingSettings({ yearlyTracking: { resetDay: v } })
                  )}
                  class="w-full h-9 px-3 text-sm rounded-lg border border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                  disabled={store.isLoading || store.isSaving}
                />
                <p class="text-xs text-muted mt-1">
                  {t('settings.species.tracking.yearly.resetDay.helpText')}
                </p>
              </div>

              <!-- Window Days -->
              <div>
                <label class="flex items-center py-2" for="yearly-window-days">
                  <span class="text-sm font-medium">
                    {t('settings.species.tracking.yearly.windowDays.label')}
                  </span>
                </label>
                <div class="flex">
                  <input
                    id="yearly-window-days"
                    type="number"
                    min={TRACKING_LIMITS.days.min}
                    max={TRACKING_LIMITS.days.max}
                    value={trackingSettings?.yearlyTracking?.windowDays ??
                      TRACKING_DEFAULTS.yearlyTracking.windowDays}
                    onchange={createNumberInputHandler(
                      TRACKING_LIMITS.days,
                      TRACKING_DEFAULTS.yearlyTracking.windowDays,
                      v => updateTrackingSettings({ yearlyTracking: { windowDays: v } })
                    )}
                    class="flex-1 h-9 px-3 text-sm rounded-l-lg border border-r-0 border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                    disabled={store.isLoading || store.isSaving}
                  />
                  <span
                    class="inline-flex items-center justify-center h-9 px-3 text-sm font-medium rounded-r-lg border border-[var(--border-100)] bg-[var(--surface-200)] text-muted"
                    >{t('settings.species.tracking.units.days')}</span
                  >
                </div>
                <p class="text-xs text-muted mt-1">
                  {t('settings.species.tracking.yearly.windowDays.helpText')}
                </p>
              </div>
            </div>
          {/if}
        </div>
      </SettingsSection>

      <!-- Seasonal Tracking Settings -->
      <SettingsSection
        title={t('settings.species.tracking.seasonal.title')}
        description={t('settings.species.tracking.seasonal.description')}
        defaultOpen={false}
      >
        <div class="space-y-4">
          <!-- Enable Seasonal Tracking -->
          <Checkbox
            checked={trackingSettings?.seasonalTracking?.enabled ?? false}
            label={t('settings.species.tracking.seasonal.enabled.label')}
            helpText={t('settings.species.tracking.seasonal.enabled.helpText')}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateTrackingSettings({ seasonalTracking: { enabled: value } })}
          />

          {#if trackingSettings?.seasonalTracking?.enabled}
            <!-- Window Days -->
            <div class="max-w-xs">
              <label class="flex items-center py-2" for="seasonal-window-days">
                <span class="text-sm font-medium">
                  {t('settings.species.tracking.seasonal.windowDays.label')}
                </span>
              </label>
              <div class="flex">
                <input
                  id="seasonal-window-days"
                  type="number"
                  min={TRACKING_LIMITS.days.min}
                  max={TRACKING_LIMITS.days.max}
                  value={trackingSettings?.seasonalTracking?.windowDays ??
                    TRACKING_DEFAULTS.seasonalTracking.windowDays}
                  onchange={createNumberInputHandler(
                    TRACKING_LIMITS.days,
                    TRACKING_DEFAULTS.seasonalTracking.windowDays,
                    v => updateTrackingSettings({ seasonalTracking: { windowDays: v } })
                  )}
                  class="flex-1 h-9 px-3 text-sm rounded-l-lg border border-r-0 border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                  disabled={store.isLoading || store.isSaving}
                />
                <span
                  class="inline-flex items-center justify-center h-9 px-3 text-sm font-medium rounded-r-lg border border-[var(--border-100)] bg-[var(--surface-200)] text-muted"
                  >{t('settings.species.tracking.units.days')}</span
                >
              </div>
              <p class="text-xs text-muted mt-1">
                {t('settings.species.tracking.seasonal.windowDays.helpText')}
              </p>
            </div>

            <!-- Season Definitions -->
            <div class="mt-6">
              <h4 class="font-medium text-sm mb-2">
                {t('settings.species.tracking.seasonal.seasons.title')}
              </h4>
              <p class="text-xs text-muted mb-4">
                {t('settings.species.tracking.seasonal.seasons.description')}
              </p>

              <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
                {#each ['spring', 'summer', 'fall', 'winter'] as season (season)}
                  {@const seasonKey = season as keyof typeof SEASON_DEFAULTS}
                  {@const seasonDefaults = getSeasonDefaults(seasonKey)}
                  {@const currentSeasonData = getCurrentSeasonData(seasonKey)}
                  <div
                    class="rounded-lg bg-[var(--surface-100)] border border-[var(--border-100)] p-4"
                  >
                    <h5 class="font-medium text-sm mb-3">
                      {t(`settings.species.tracking.seasonal.seasons.${season}`)}
                    </h5>
                    <div class="grid grid-cols-2 gap-3 items-end">
                      <div>
                        <label class="flex items-center py-2" for={`${season}-start-month`}>
                          <span class="text-sm font-medium">
                            {t('settings.species.tracking.seasonal.seasons.startMonth')}
                          </span>
                        </label>
                        <SelectDropdown
                          value={String(currentSeasonData?.startMonth ?? seasonDefaults.startMonth)}
                          options={monthOptions}
                          disabled={store.isLoading || store.isSaving}
                          menuSize="sm"
                          onChange={value =>
                            updateSeasonDate(seasonKey, 'startMonth', Number(value))}
                        />
                      </div>
                      <div>
                        <label class="flex items-center py-2" for={`${season}-start-day`}>
                          <span class="text-sm font-medium">
                            {t('settings.species.tracking.seasonal.seasons.startDay')}
                          </span>
                        </label>
                        <input
                          id={`${season}-start-day`}
                          type="number"
                          min={TRACKING_LIMITS.dayOfMonth.min}
                          max={TRACKING_LIMITS.dayOfMonth.max}
                          value={currentSeasonData?.startDay ?? seasonDefaults.startDay}
                          onchange={createNumberInputHandler(
                            TRACKING_LIMITS.dayOfMonth,
                            seasonDefaults.startDay,
                            v => updateSeasonDate(seasonKey, 'startDay', v)
                          )}
                          class="w-full h-9 px-3 text-sm rounded-lg border border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                          disabled={store.isLoading || store.isSaving}
                        />
                      </div>
                    </div>
                  </div>
                {/each}
              </div>

              <SettingsNote>
                {t('settings.species.tracking.seasonal.seasons.hemisphereNote')}
              </SettingsNote>
            </div>
          {/if}
        </div>
      </SettingsSection>
    {/if}
  </div>
{/snippet}

<!-- Dynamic Threshold Tab Content -->
{#snippet dynamicThresholdTabContent()}
  <DynamicThresholdTab />
{/snippet}

<main class="settings-page-content" aria-label={t('settings.species.pageLabel')}>
  <SettingsTabs {tabs} bind:activeTab />
</main>
