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
  import StatsCard from '$lib/desktop/features/settings/components/StatsCard.svelte';
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
    Download,
    MapPin,
    SlidersHorizontal,
    Clock,
    Bird,
    Search,
    Maximize2,
    Minimize2,
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

  // Search query for active species filtering
  // Use separate input value and debounced query to prevent rapid reactive updates
  // that can corrupt Svelte's internal state
  let searchInputValue = $state('');
  let activeSearchQuery = $state('');
  let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null;

  // Debounced search update to prevent Svelte reactive corruption during rapid typing
  function handleSearchInput(event: Event) {
    const value = (event.target as HTMLInputElement).value;
    searchInputValue = value;

    // Clear existing timer
    if (searchDebounceTimer) {
      clearTimeout(searchDebounceTimer);
    }

    // Debounce the actual search query update
    searchDebounceTimer = setTimeout(() => {
      searchDebounceTimer = null;
      activeSearchQuery = value;
    }, 150);
  }

  // List view state: expanded or compact with scroll
  let isListExpanded = $state(false);

  // Filtered active species list - use pure $derived.by() to avoid any state modifications
  // during Svelte's reactive update cycle which can corrupt internal linked list state
  // This is a read-only computation, not a state change, so it's safe
  let filteredActiveSpecies = $derived.by(() => {
    const data = activeSpeciesState.data;
    const searchQuery = activeSearchQuery;

    if (!data?.species) {
      return [] as ActiveSpecies[];
    }

    const query = searchQuery.trim().toLowerCase();
    if (!query) {
      // Return the original array reference when no search query
      // This is safe because we're not mutating it
      return data.species;
    }

    // Filter creates a new array, which is what we want for search results
    return data.species.filter(
      s =>
        s.commonName.toLowerCase().includes(query) || s.scientificName.toLowerCase().includes(query)
    );
  });

  // PERFORMANCE OPTIMIZATION: Derived species lists
  let allSpecies = $derived(speciesListState.data);
  let filteredSpecies = $derived([...allSpecies]);

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
        species?: Array<{ commonName?: string; label: string }>;
      }
      const data = await api.get<SpeciesListResponse>('/api/v2/range/species/list');
      speciesListState.data =
        data.species?.map((s: { commonName?: string; label: string }) => s.commonName || s.label) ||
        [];
    } catch (error) {
      logger.error('Failed to load species data:', error);
      // Provide specific error messages based on status code
      if (error instanceof ApiError) {
        switch (error.status) {
          case 404:
            speciesListState.error = 'Species data not found';
            break;
          case 500:
          case 502:
          case 503:
            speciesListState.error = 'Server error occurred while loading species data';
            break;
          case 401:
            speciesListState.error = 'Unauthorized access to species data';
            break;
          case 403:
            speciesListState.error = 'Access to species data is forbidden';
            break;
          default:
            speciesListState.error = t('settings.species.errors.speciesLoadFailed');
        }
      } else {
        speciesListState.error = t('settings.species.errors.speciesLoadFailed');
      }
      speciesListState.data = [];
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
    if (searchDebounceTimer) {
      clearTimeout(searchDebounceTimer);
    }
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
      excludePredictions = filteredSpecies
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
        toastActions.error(
          `Species "${species}" already has a configuration. Please choose a different name.`
        );
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
          ? `Updated configuration for "${species}"`
          : `Added configuration for "${species}"`
      );

      // Reset form only after successful save
      cancelEdit();
    } catch (error) {
      logger.error('Failed to save species configuration:', error);
      toastActions.error(`Failed to save configuration for "${species}". Please try again.`);
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
    <!-- Stats Bar - Outside the card -->
    {#if activeSpeciesState.data}
      <div class="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <StatsCard
          icon={Bird}
          label={t('settings.species.activeSpecies.stats.species')}
          value={activeSpeciesState.data.count}
        />

        <StatsCard
          icon={MapPin}
          label={t('settings.species.activeSpecies.stats.location')}
          value="{activeSpeciesState.data.location.latitude.toFixed(
            2
          )}°, {activeSpeciesState.data.location.longitude.toFixed(2)}°"
        />

        <StatsCard
          icon={SlidersHorizontal}
          label={t('settings.species.activeSpecies.stats.threshold')}
          value={activeSpeciesState.data.threshold}
        />

        <StatsCard
          icon={Clock}
          label={t('settings.species.activeSpecies.stats.updated')}
          value={formatRelativeTime(activeSpeciesState.data.lastUpdated)}
        />
      </div>
    {/if}

    <!-- Main Content Card -->
    <SettingsSection
      title={t('settings.species.activeSpecies.title')}
      description={t('settings.species.activeSpecies.description')}
      defaultOpen={true}
    >
      <div class="space-y-4">
        <!-- Loading State: Show spinner until both main settings AND active species data are ready -->
        <!-- Guard against flash: Don't show location warning until main settings have truly loaded from API -->
        {#if activeSpeciesState.loading || store.isLoading || !store.originalData?.birdnet}
          <div class="flex items-center justify-center py-12">
            <div
              class="inline-block w-8 h-8 border-4 border-[var(--color-base-300)] border-t-[var(--color-primary)] rounded-full animate-spin"
            ></div>
          </div>
        {:else if activeSpeciesState.error}
          <!-- Error State -->
          <div
            class="flex items-start gap-3 p-4 rounded-lg bg-[color-mix(in_srgb,var(--color-error)_15%,transparent)] text-[var(--color-error)]"
          >
            <p class="text-sm">{activeSpeciesState.error}</p>
            <button
              type="button"
              class="mt-2 inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[color-mix(in_srgb,var(--color-error)_25%,transparent)] hover:bg-[color-mix(in_srgb,var(--color-error)_35%,transparent)] transition-colors"
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
        {:else if activeSpeciesState.locationNotConfigured}
          <!-- Location Not Configured Warning -->
          <div
            class="p-4 rounded-lg bg-[color-mix(in_srgb,var(--color-warning)_15%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]"
          >
            <div class="flex items-start gap-3">
              <MapPin class="size-5 text-[var(--color-warning)] shrink-0 mt-0.5" />
              <div>
                <p class="font-medium text-[var(--color-warning)]">
                  {t('settings.species.activeSpecies.locationNotConfigured.title') ||
                    'Location Not Configured'}
                </p>
                <p class="text-sm text-[var(--color-base-content)] opacity-70 mt-1">
                  {t('settings.species.activeSpecies.locationNotConfigured.description') ||
                    'Set your location in Main Settings to see species available in your area. The range filter uses your location to determine which species are likely to be found nearby.'}
                </p>
                <a
                  href="/ui/settings/main"
                  class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-warning)] text-[var(--color-warning-content)] hover:opacity-90 transition-colors mt-3"
                >
                  {t('settings.species.activeSpecies.locationNotConfigured.action') ||
                    'Configure Location'}
                </a>
              </div>
            </div>
          </div>
        {:else if activeSpeciesState.data}
          <!-- Search & Actions Bar -->
          <div class="flex items-center gap-3">
            <!-- Search Input -->
            <div class="relative flex-1">
              <Search
                class="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-[color:var(--color-base-content)] opacity-40"
                aria-hidden="true"
              />
              <input
                id="active-species-search"
                type="text"
                value={searchInputValue}
                oninput={handleSearchInput}
                placeholder={t('settings.species.activeSpecies.search.placeholder')}
                aria-label={t('settings.species.activeSpecies.search.placeholder')}
                autocomplete="off"
                data-1p-ignore
                data-lpignore="true"
                data-form-type="other"
                class="w-full pl-10 pr-4 py-2 text-sm rounded-lg border border-[var(--border-200)] bg-[var(--color-base-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]"
              />
            </div>

            <!-- Expand/Collapse Button -->
            <button
              type="button"
              class="inline-flex items-center justify-center gap-2 h-8 px-3 text-sm font-medium rounded-lg border border-[var(--border-200)] bg-transparent hover:bg-[var(--color-base-200)] transition-colors"
              onclick={() => (isListExpanded = !isListExpanded)}
              title={isListExpanded
                ? t('settings.species.activeSpecies.collapse') || 'Collapse list'
                : t('settings.species.activeSpecies.expand') || 'Expand list'}
              aria-label={isListExpanded
                ? t('settings.species.activeSpecies.collapse') || 'Collapse list'
                : t('settings.species.activeSpecies.expand') || 'Expand list'}
              aria-expanded={isListExpanded}
            >
              {#if isListExpanded}
                <Minimize2 class="size-4" aria-hidden="true" />
              {:else}
                <Maximize2 class="size-4" aria-hidden="true" />
              {/if}
            </button>

            <!-- CSV Download Button -->
            <button
              type="button"
              class="inline-flex items-center justify-center gap-2 h-8 px-3 text-sm font-medium rounded-lg border border-[var(--border-200)] bg-transparent hover:bg-[var(--color-base-200)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={downloadActiveSpeciesCSV}
              disabled={!activeSpeciesState.data.species.length}
              title={t('settings.species.activeSpecies.downloadCsv') || 'Download CSV'}
              aria-label={t('settings.species.activeSpecies.downloadCsv') || 'Download CSV'}
            >
              <Download class="size-4" aria-hidden="true" />
              <span class="hidden sm:inline">CSV</span>
            </button>
          </div>

          <!-- Species List - Use {#key} to force full recreation instead of diffing -->
          <!-- This prevents Svelte internal state corruption during list transitions -->
          {#key activeSearchQuery}
            {#if filteredActiveSpecies.length > 0}
              <div
                class="divide-y divide-[var(--border-200)] rounded-lg border border-[var(--border-200)] overflow-hidden overflow-y-auto"
                class:max-h-[32rem]={!isListExpanded}
                class:max-h-[80vh]={isListExpanded}
              >
                {#each filteredActiveSpecies as species (species.scientificName)}
                  <div
                    class="flex items-center justify-between p-3 hover:bg-[var(--color-base-200)]/50 transition-colors"
                  >
                    <!-- Left: Names -->
                    <div class="min-w-0 flex-1">
                      <div class="font-medium text-sm truncate">{species.commonName}</div>
                      <div
                        class="text-xs text-[color:var(--color-base-content)] opacity-50 italic truncate"
                      >
                        {species.scientificName}
                      </div>
                    </div>

                    <!-- Right: Badges + Score bar (badges first for consistent score alignment) -->
                    <div class="flex items-center gap-3 ml-3 shrink-0">
                      <!-- Badges - fixed width container for alignment -->
                      <div class="flex items-center gap-1.5 w-36 justify-end">
                        {#if species.isManuallyIncluded}
                          <span
                            class="inline-flex items-center justify-center gap-1 px-2 py-0.5 text-xs font-medium rounded-full bg-[var(--color-success)] text-[var(--color-success-content)]"
                          >
                            + {t('settings.species.activeSpecies.badges.included')}
                          </span>
                        {/if}
                        {#if species.hasCustomConfig}
                          <span
                            class="inline-flex items-center justify-center gap-1 px-2 py-0.5 text-xs font-medium rounded-full bg-[var(--color-secondary)] text-[var(--color-secondary-content)]"
                          >
                            ★ {t('settings.species.activeSpecies.badges.configured')}
                          </span>
                        {/if}
                      </div>

                      <!-- Score Bar - always in same position -->
                      <div class="flex items-center gap-2">
                        <div
                          class="w-20 h-2 bg-[var(--color-base-300)] rounded-full overflow-hidden"
                        >
                          <div
                            class="h-full bg-[var(--color-primary)] rounded-full transition-all"
                            style:width="{species.score * 100}%"
                          ></div>
                        </div>
                        <span
                          class="text-xs font-mono tabular-nums w-10 text-[color:var(--color-base-content)] opacity-60"
                        >
                          {species.score.toFixed(2)}
                        </span>
                      </div>
                    </div>
                  </div>
                {/each}
              </div>
            {:else if searchInputValue}
              <!-- No Search Results -->
              <div class="text-center py-8 text-[color:var(--color-base-content)] opacity-50">
                <p class="text-sm">{t('settings.species.activeSpecies.noResults')}</p>
              </div>
            {:else}
              <!-- Empty State -->
              <div class="text-center py-8 text-[color:var(--color-base-content)] opacity-50">
                <Bird class="size-12 mx-auto mb-3 opacity-30" />
                <p class="text-sm font-medium">{t('settings.species.activeSpecies.empty.title')}</p>
                <p class="text-xs mt-1">{t('settings.species.activeSpecies.empty.description')}</p>
              </div>
            {/if}
          {/key}

          <!-- Info Note -->
          <SettingsNote>
            {t('settings.species.activeSpecies.infoNote')}
          </SettingsNote>
        {:else}
          <!-- Initial Empty State (before load) -->
          <div class="text-center py-8 text-[color:var(--color-base-content)] opacity-50">
            <Bird class="size-12 mx-auto mb-3 opacity-30" />
            <p class="text-sm font-medium">{t('settings.species.activeSpecies.empty.title')}</p>
            <p class="text-xs mt-1">{t('settings.species.activeSpecies.empty.description')}</p>
          </div>
        {/if}
      </div>
    </SettingsSection>
  </div>
{/snippet}

<!-- Include Species Tab Content -->
{#snippet includeTabContent()}
  <SettingsSection
    title={t('settings.species.alwaysInclude.title')}
    description={t('settings.species.alwaysInclude.description')}
    defaultOpen={true}
    originalData={store.originalData.realtime?.species?.include}
    currentData={store.formData.realtime?.species?.include}
  >
    <div class="space-y-4">
      <!-- Species list -->
      <div class="space-y-2">
        {#each settings.include as species (species)}
          <div class="flex items-center justify-between p-2 rounded-md bg-[var(--color-base-200)]">
            <span class="text-sm">{species}</span>
            <button
              type="button"
              class="inline-flex items-center justify-center p-1.5 rounded-md bg-transparent hover:bg-[var(--color-base-300)] text-[var(--color-error)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={() => removeIncludeSpecies(species)}
              disabled={store.isLoading || store.isSaving}
              aria-label="Remove {species}"
              title="Remove species"
            >
              <Trash2 class="size-4" />
            </button>
          </div>
        {/each}

        {#if settings.include.length === 0}
          <div
            class="text-sm text-[color:var(--color-base-content)] opacity-60 italic p-2 text-center"
          >
            {t('settings.species.alwaysInclude.noSpeciesMessage')}
          </div>
        {/if}
      </div>

      <!-- Add species input -->
      <SpeciesInput
        bind:value={includeInputValue}
        label={t('settings.species.addSpeciesToIncludeLabel')}
        placeholder={t('settings.species.addSpeciesToInclude')}
        predictions={includePredictions}
        size="sm"
        onInput={updateIncludePredictions}
        onAdd={addIncludeSpecies}
        disabled={store.isLoading || store.isSaving}
      />
    </div>
  </SettingsSection>
{/snippet}

<!-- Exclude Species Tab Content -->
{#snippet excludeTabContent()}
  <SettingsSection
    title={t('settings.species.alwaysExclude.title')}
    description={t('settings.species.alwaysExclude.description')}
    defaultOpen={true}
    originalData={store.originalData.realtime?.species?.exclude}
    currentData={store.formData.realtime?.species?.exclude}
  >
    <div class="space-y-4">
      <!-- Species list -->
      <div class="space-y-2">
        {#each settings.exclude as species (species)}
          <div class="flex items-center justify-between p-2 rounded-md bg-[var(--color-base-200)]">
            <span class="text-sm">{species}</span>
            <button
              type="button"
              class="inline-flex items-center justify-center p-1.5 rounded-md bg-transparent hover:bg-[var(--color-base-300)] text-[var(--color-error)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={() => removeExcludeSpecies(species)}
              disabled={store.isLoading || store.isSaving}
              aria-label="Remove {species}"
              title="Remove species"
            >
              <Trash2 class="size-4" />
            </button>
          </div>
        {/each}

        {#if settings.exclude.length === 0}
          <div
            class="text-sm text-[color:var(--color-base-content)] opacity-60 italic p-2 text-center"
          >
            {t('settings.species.alwaysExclude.noSpeciesMessage')}
          </div>
        {/if}
      </div>

      <!-- Add species input -->
      <SpeciesInput
        bind:value={excludeInputValue}
        label={t('settings.species.addSpeciesToExcludeLabel')}
        placeholder={t('settings.species.addSpeciesToExclude')}
        predictions={excludePredictions}
        size="sm"
        onInput={updateExcludePredictions}
        onAdd={addExcludeSpecies}
        disabled={store.isLoading || store.isSaving}
      />
    </div>
  </SettingsSection>
{/snippet}

<!-- Custom Configuration Tab Content -->
{#snippet configTabContent()}
  <SettingsSection
    title={t('settings.species.customConfiguration.title')}
    description={t('settings.species.customConfiguration.description')}
    defaultOpen={true}
    originalData={store.originalData.realtime?.species?.config}
    currentData={store.formData.realtime?.species?.config}
  >
    <div class="space-y-4">
      <!-- Header with Add Button -->
      <div class="flex justify-between items-center">
        {#if !showAddForm}
          <button
            type="button"
            class="inline-flex items-center justify-center gap-2 h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:opacity-90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            data-testid="add-configuration-button"
            onclick={() => (showAddForm = true)}
            disabled={store.isLoading || store.isSaving}
          >
            <Plus class="size-4" />
            {t('settings.species.customConfiguration.addConfiguration')}
          </button>
        {:else}
          <span class="text-sm font-medium">
            {editingSpecies
              ? t('settings.species.customConfiguration.editing', { species: editingSpecies })
              : t('settings.species.customConfiguration.newConfiguration')}
          </span>
        {/if}

        {#if Object.keys(settings.config).length > 0}
          <span class="text-xs text-[color:var(--color-base-content)] opacity-60">
            {t('settings.species.customConfiguration.configuredCount', {
              count: Object.keys(settings.config).length,
            })}
          </span>
        {/if}
      </div>

      <!-- Compact Add/Edit Form -->
      {#if showAddForm}
        <div
          class="border border-[var(--border-200)] rounded-lg p-3 bg-[var(--color-base-100)] space-y-3"
        >
          <!-- Main configuration row -->
          <div class="grid grid-cols-12 gap-3 items-end">
            <!-- Species Input -->
            <div class="col-span-4">
              <label class="flex flex-col gap-1 py-1" for="config-species">
                <span class="text-xs font-medium text-[var(--color-base-content)]"
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
                <span class="text-xs font-medium text-[var(--color-base-content)]"
                  >{t('settings.species.customConfiguration.labels.threshold')}</span
                >
                <span class="text-xs text-[var(--color-base-content)] opacity-60"
                  >{newThreshold.toFixed(2)}</span
                >
              </label>
              <input
                id="config-threshold"
                type="range"
                min="0"
                max="1"
                step="0.01"
                value={newThreshold}
                oninput={e => (newThreshold = Number(e.currentTarget.value))}
                class="w-full h-2 bg-[var(--color-base-300)] rounded-lg appearance-none cursor-pointer accent-[var(--color-primary)]"
              />
            </div>

            <!-- Interval -->
            <div class="col-span-3">
              <label class="flex flex-col gap-1 py-1" for="config-interval">
                <span class="text-xs font-medium text-[var(--color-base-content)]"
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
                class="w-full h-7 px-2 text-sm rounded-md border border-[var(--border-200)] bg-[var(--color-base-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]"
                placeholder="0"
              />
            </div>

            <!-- Buttons -->
            <div class="col-span-2 flex gap-1">
              <button
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
                class="inline-flex items-center justify-center flex-1 h-7 px-2 text-xs font-medium rounded-md bg-transparent hover:bg-[var(--color-base-200)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                onclick={cancelEdit}
                disabled={store.isSaving}
              >
                {t('settings.species.customConfiguration.cancel')}
              </button>
            </div>
          </div>

          <!-- Actions Toggle -->
          <div class="border-t border-[var(--border-200)] pt-2">
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
                  class="inline-flex items-center justify-center px-1.5 py-0.5 text-[10px] font-medium rounded-full bg-[var(--color-accent)] text-[var(--color-accent-content)]"
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
                  <span class="text-xs font-medium text-[var(--color-base-content)]"
                    >{t('settings.species.actionsModal.command.label')}</span
                  >
                </label>
                <input
                  id="action-command"
                  type="text"
                  bind:value={actionCommand}
                  placeholder={t('settings.species.commandPathPlaceholder')}
                  class="w-full h-7 px-2 text-sm rounded-md border border-[var(--border-200)] bg-[var(--color-base-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]"
                />
                <span class="text-xs text-[var(--color-base-content)] opacity-60 mt-1 block"
                  >{t('settings.species.actionsModal.command.helpText')}</span
                >
              </div>

              <!-- Parameters -->
              <div>
                <label class="flex flex-col gap-1 py-1" for="action-parameters">
                  <span
                    class="text-xs font-medium text-[var(--color-base-content)] flex items-center gap-1"
                  >
                    {t('settings.species.actionsModal.parameters.label')}
                    <span
                      class="text-[var(--color-base-content)] opacity-60"
                      title="Use buttons below or type directly"
                    >
                      ⓘ
                    </span>
                  </span>
                </label>
                <input
                  id="action-parameters"
                  type="text"
                  bind:value={actionParameters}
                  placeholder="Click buttons below to add parameters or type manually"
                  class="w-full h-7 px-2 text-sm rounded-md border border-[var(--border-200)] bg-[var(--color-base-200)]/50 focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]"
                  title="Add parameters using the buttons below or type directly (comma-separated)"
                />
                <span class="text-xs text-[var(--color-base-content)] opacity-60 mt-1 block"
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
                    class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md border border-[var(--border-200)] bg-transparent hover:bg-[var(--color-base-200)] transition-colors"
                    onclick={() => addParameter('CommonName')}
                    >{t('settings.species.actionsModal.parameters.buttons.commonName')}</button
                  >
                  <button
                    type="button"
                    class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md border border-[var(--border-200)] bg-transparent hover:bg-[var(--color-base-200)] transition-colors"
                    onclick={() => addParameter('ScientificName')}
                    >{t('settings.species.actionsModal.parameters.buttons.scientificName')}</button
                  >
                  <button
                    type="button"
                    class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md border border-[var(--border-200)] bg-transparent hover:bg-[var(--color-base-200)] transition-colors"
                    onclick={() => addParameter('Confidence')}
                    >{t('settings.species.actionsModal.parameters.buttons.confidence')}</button
                  >
                  <button
                    type="button"
                    class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md border border-[var(--border-200)] bg-transparent hover:bg-[var(--color-base-200)] transition-colors"
                    onclick={() => addParameter('Time')}
                    >{t('settings.species.actionsModal.parameters.buttons.time')}</button
                  >
                  <button
                    type="button"
                    class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md border border-[var(--border-200)] bg-transparent hover:bg-[var(--color-base-200)] transition-colors"
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
                    class="appearance-none w-3.5 h-3.5 border-2 border-[var(--border-200)] rounded bg-[var(--color-base-100)] cursor-pointer transition-all checked:bg-[var(--color-primary)] checked:border-[var(--color-primary)]"
                  />
                  <span class="text-xs text-[var(--color-base-content)]"
                    >{t('settings.species.actionsModal.executeDefaults.label')}</span
                  >
                </label>
                <span class="text-xs text-[var(--color-base-content)] opacity-60 mt-1 block"
                  >{t('settings.species.actionsModal.executeDefaults.helpText')}</span
                >
              </div>
            </div>
          {/if}
        </div>
      {/if}

      <!-- Compact Configuration List -->
      <div class="space-y-2">
        {#each Object.entries(settings.config) as [species, config] (species)}
          <div
            class="flex items-center gap-3 p-2 rounded-lg bg-[var(--color-base-100)] border border-[var(--border-200)] hover:border-[var(--color-base-content)]/20 transition-colors"
          >
            <!-- Species Name -->
            <div class="flex-1 min-w-0">
              <span class="font-medium text-sm truncate block">{species}</span>
            </div>

            <!-- Threshold -->
            <div class="flex items-center gap-2">
              <span class="text-xs text-[var(--color-base-content)] opacity-60"
                >{t('settings.species.customConfiguration.list.threshold')}</span
              >
              <span class="font-mono text-xs font-medium">{(config.threshold ?? 0).toFixed(2)}</span
              >
            </div>

            <!-- Interval -->
            <div class="flex items-center gap-2">
              <span class="text-xs text-[var(--color-base-content)] opacity-60"
                >{t('settings.species.customConfiguration.list.interval')}</span
              >
              <span class="font-mono text-xs font-medium">
                {config.interval > 0
                  ? `${config.interval}s`
                  : t('settings.species.customConfiguration.list.intervalNone')}
              </span>
            </div>

            <!-- Action Badge -->
            {#if config.actions?.length > 0}
              <span
                class="inline-flex items-center justify-center px-1.5 py-0.5 text-[10px] font-medium rounded-full bg-[var(--color-accent)] text-[var(--color-accent-content)]"
                >{t('settings.species.customConfiguration.list.actionBadge')}</span
              >
            {/if}

            <!-- Actions -->
            <div class="flex items-center gap-1">
              <button
                class="inline-flex items-center justify-center p-1.5 rounded-md bg-transparent hover:bg-[var(--color-base-200)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                onclick={() => startEdit(species)}
                title={t('settings.species.customConfiguration.list.editTitle')}
                aria-label={t('settings.species.customConfiguration.list.editTitle')}
                disabled={store.isLoading || store.isSaving}
              >
                <SquarePen class="size-4" />
              </button>

              <button
                class="inline-flex items-center justify-center p-1.5 rounded-md bg-transparent hover:bg-[var(--color-base-300)] text-[var(--color-error)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                onclick={() => removeConfig(species)}
                title={t('settings.species.customConfiguration.list.removeTitle')}
                aria-label={t('settings.species.customConfiguration.list.removeTitle')}
                disabled={store.isLoading || store.isSaving}
              >
                <Trash2 class="size-4" />
              </button>
            </div>
          </div>
        {/each}
      </div>

      <!-- Empty State -->
      {#if Object.keys(settings.config).length === 0 && !showAddForm}
        <div class="text-center py-8 text-[var(--color-base-content)] opacity-60">
          <p class="text-sm">{t('settings.species.customConfiguration.emptyState.title')}</p>
          <p class="text-xs mt-1">
            {t('settings.species.customConfiguration.emptyState.description')}
          </p>
        </div>
      {/if}
    </div>
  </SettingsSection>
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
              <label for="new-species-window" class="flex flex-col gap-1 mb-1">
                <span class="text-sm font-semibold text-[var(--color-base-content)]">
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
                  class="flex-1 h-10 px-3 text-sm rounded-l-lg border border-r-0 border-[var(--border-200)] bg-[var(--color-base-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                  disabled={store.isLoading || store.isSaving}
                />
                <span
                  class="inline-flex items-center justify-center h-10 px-3 text-sm font-medium rounded-r-lg border border-[var(--border-200)] bg-[var(--color-base-200)] text-[var(--color-base-content)] opacity-70"
                  >{t('settings.species.tracking.units.days')}</span
                >
              </div>
              <p class="text-xs text-[var(--color-base-content)] opacity-60 mt-1">
                {t('settings.species.tracking.newSpeciesWindowDays.helpText')}
              </p>
            </div>

            <!-- Sync Interval Minutes -->
            <div>
              <label for="sync-interval" class="flex flex-col gap-1 mb-1">
                <span class="text-sm font-semibold text-[var(--color-base-content)]">
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
                  class="flex-1 h-10 px-3 text-sm rounded-l-lg border border-r-0 border-[var(--border-200)] bg-[var(--color-base-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                  disabled={store.isLoading || store.isSaving}
                />
                <span
                  class="inline-flex items-center justify-center h-10 px-3 text-sm font-medium rounded-r-lg border border-[var(--border-200)] bg-[var(--color-base-200)] text-[var(--color-base-content)] opacity-70"
                  >{t('settings.species.tracking.units.min')}</span
                >
              </div>
              <p class="text-xs text-[var(--color-base-content)] opacity-60 mt-1">
                {t('settings.species.tracking.syncIntervalMinutes.helpText')}
              </p>
            </div>

            <!-- Notification Suppression Hours -->
            <div>
              <label for="notification-suppression" class="flex flex-col gap-1 mb-1">
                <span class="text-sm font-semibold text-[var(--color-base-content)]">
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
                  class="flex-1 h-10 px-3 text-sm rounded-l-lg border border-r-0 border-[var(--border-200)] bg-[var(--color-base-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                  disabled={store.isLoading || store.isSaving}
                />
                <span
                  class="inline-flex items-center justify-center h-10 px-3 text-sm font-medium rounded-r-lg border border-[var(--border-200)] bg-[var(--color-base-200)] text-[var(--color-base-content)] opacity-70"
                  >{t('settings.species.tracking.units.hours')}</span
                >
              </div>
              <p class="text-xs text-[var(--color-base-content)] opacity-60 mt-1">
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
              <SelectDropdown
                label={t('settings.species.tracking.yearly.resetMonth.label')}
                helpText={t('settings.species.tracking.yearly.resetMonth.helpText')}
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

              <!-- Reset Day -->
              <div>
                <label for="yearly-reset-day" class="flex flex-col gap-1 mb-1">
                  <span class="text-sm font-semibold text-[var(--color-base-content)]">
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
                  class="w-full h-10 px-3 text-sm rounded-lg border border-[var(--border-200)] bg-[var(--color-base-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                  disabled={store.isLoading || store.isSaving}
                />
                <p class="text-xs text-[var(--color-base-content)] opacity-60 mt-1">
                  {t('settings.species.tracking.yearly.resetDay.helpText')}
                </p>
              </div>

              <!-- Window Days -->
              <div>
                <label for="yearly-window-days" class="flex flex-col gap-1 mb-1">
                  <span class="text-sm font-semibold text-[var(--color-base-content)]">
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
                    class="flex-1 h-10 px-3 text-sm rounded-l-lg border border-r-0 border-[var(--border-200)] bg-[var(--color-base-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                    disabled={store.isLoading || store.isSaving}
                  />
                  <span
                    class="inline-flex items-center justify-center h-10 px-3 text-sm font-medium rounded-r-lg border border-[var(--border-200)] bg-[var(--color-base-200)] text-[var(--color-base-content)] opacity-70"
                    >{t('settings.species.tracking.units.days')}</span
                  >
                </div>
                <p class="text-xs text-[var(--color-base-content)] opacity-60 mt-1">
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
              <label for="seasonal-window-days" class="flex flex-col gap-1 mb-1">
                <span class="text-sm font-semibold text-[var(--color-base-content)]">
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
                  class="flex-1 h-10 px-3 text-sm rounded-l-lg border border-r-0 border-[var(--border-200)] bg-[var(--color-base-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
                  disabled={store.isLoading || store.isSaving}
                />
                <span
                  class="inline-flex items-center justify-center h-10 px-3 text-sm font-medium rounded-r-lg border border-[var(--border-200)] bg-[var(--color-base-200)] text-[var(--color-base-content)] opacity-70"
                  >{t('settings.species.tracking.units.days')}</span
                >
              </div>
              <p class="text-xs text-[var(--color-base-content)] opacity-60 mt-1">
                {t('settings.species.tracking.seasonal.windowDays.helpText')}
              </p>
            </div>

            <!-- Season Definitions -->
            <div class="mt-6">
              <h4 class="font-medium text-sm mb-2">
                {t('settings.species.tracking.seasonal.seasons.title')}
              </h4>
              <p class="text-xs text-[var(--color-base-content)] opacity-60 mb-4">
                {t('settings.species.tracking.seasonal.seasons.description')}
              </p>

              <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
                {#each ['spring', 'summer', 'fall', 'winter'] as season (season)}
                  {@const seasonKey = season as keyof typeof SEASON_DEFAULTS}
                  {@const seasonDefaults = getSeasonDefaults(seasonKey)}
                  {@const currentSeasonData = getCurrentSeasonData(seasonKey)}
                  <div
                    class="rounded-lg bg-[var(--color-base-100)] border border-[var(--border-200)] p-4"
                  >
                    <h5 class="font-medium text-sm mb-3">
                      {t(`settings.species.tracking.seasonal.seasons.${season}`)}
                    </h5>
                    <div class="grid grid-cols-2 gap-3">
                      <SelectDropdown
                        label={t('settings.species.tracking.seasonal.seasons.startMonth')}
                        value={String(currentSeasonData?.startMonth ?? seasonDefaults.startMonth)}
                        options={monthOptions}
                        disabled={store.isLoading || store.isSaving}
                        menuSize="sm"
                        onChange={value => updateSeasonDate(seasonKey, 'startMonth', Number(value))}
                      />
                      <div>
                        <label for={`${season}-start-day`} class="flex flex-col gap-1 mb-1">
                          <span class="text-sm font-semibold text-[var(--color-base-content)]">
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
                          class="w-full h-10 px-3 text-sm rounded-lg border border-[var(--border-200)] bg-[var(--color-base-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
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

<main class="settings-page-content" aria-label="Species settings configuration">
  <SettingsTabs {tabs} bind:activeTab />
</main>
