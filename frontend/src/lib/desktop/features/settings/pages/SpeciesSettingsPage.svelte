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
  import {
    settingsStore,
    settingsActions,
    speciesSettings,
    realtimeSettings,
    birdnetSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import type { SpeciesConfig, SpeciesSettings } from '$lib/stores/settings';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { safeGet } from '$lib/utils/security';
  import { api } from '$lib/utils/api';
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
  } from '@lucide/svelte';
  import { toastActions } from '$lib/stores/toast';

  const logger = loggers.settings;

  // Helper function to check if a value is a plain object
  function isPlainObject(value: unknown): value is Record<string, unknown> {
    return value !== null && typeof value === 'object' && !Array.isArray(value);
  }

  // PERFORMANCE OPTIMIZATION: Cache CSRF token once at component initialization
  let csrfToken = '';

  // Initialize CSRF token once on mount - prevents stale values across hot-reloads
  onMount(() => {
    const metaElement = document.querySelector('meta[name="csrf-token"]');
    csrfToken = (metaElement as HTMLMetaElement | null)?.getAttribute('content') || '';

    // Load species data after CSRF token is available
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
        s.commonName.toLowerCase().includes(query) ||
        s.scientificName.toLowerCase().includes(query)
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

  // Species data will be loaded in onMount after CSRF token is available

  async function loadSpeciesData() {
    speciesListState.loading = true;
    speciesListState.error = null;

    try {
      const headers = new Headers();
      if (csrfToken) {
        headers.set('X-CSRF-Token', csrfToken);
      }

      const response = await fetch('/api/v2/range/species/list', {
        headers,
        credentials: 'same-origin',
      });

      if (!response.ok) {
        let errorMessage = '';
        switch (response.status) {
          case 404:
            errorMessage = 'Species data not found';
            break;
          case 500:
          case 502:
          case 503:
            errorMessage = 'Server error occurred while loading species data';
            break;
          case 401:
            errorMessage = 'Unauthorized access to species data';
            break;
          case 403:
            errorMessage = 'Access to species data is forbidden';
            break;
          default:
            errorMessage = `Failed to load species (Error ${response.status})`;
        }
        throw new Error(errorMessage);
      }

      const data = await response.json();
      speciesListState.data =
        data.species?.map((s: { commonName?: string; label: string }) => s.commonName || s.label) ||
        [];
    } catch (error) {
      logger.error('Failed to load species data:', error);
      speciesListState.error = t('settings.species.errors.speciesLoadFailed');
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

    if (
      hasLocationData &&
      isActiveTab &&
      canLoad &&
      (noDataYet || needsRetryWithRealCoords)
    ) {
      // CRITICAL: Use queueMicrotask to defer the call out of the $effect's synchronous context
      // This prevents state modifications from happening during Svelte's reactive update cycle,
      // which can corrupt Svelte's internal linked list and cause "Cannot read prev" errors
      queueMicrotask(() => {
        loadActiveSpecies(birdnetData);
      });
    }
  });

  // CSV download function for active species
  function downloadActiveSpeciesCSV() {
    if (!activeSpeciesState.data?.species.length) return;

    const headers = ['Common Name', 'Scientific Name', 'Score', 'Included', 'Configured'];
    const rows = activeSpeciesState.data.species.map(s => [
      s.commonName,
      s.scientificName,
      s.score.toFixed(4),
      s.isManuallyIncluded ? 'Yes' : 'No',
      s.hasCustomConfig ? 'Yes' : 'No',
    ]);

    const csvContent = [headers.join(','), ...rows.map(r => r.join(','))].join('\n');
    const blob = new Blob([csvContent], { type: 'text/csv;charset=utf-8;' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `active-species-${new Date().toISOString().split('T')[0]}.csv`;
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
      label: t('settings.species.activeSpecies.title'),
      icon: ListCheck,
      content: activeTabContent,
      // No hasChanges - this is read-only
    },
    {
      id: 'include',
      label: t('settings.species.alwaysInclude.title'),
      icon: CirclePlus,
      content: includeTabContent,
      hasChanges: includeHasChanges,
    },
    {
      id: 'exclude',
      label: t('settings.species.alwaysExclude.title'),
      icon: CircleMinus,
      content: excludeTabContent,
      hasChanges: excludeHasChanges,
    },
    {
      id: 'config',
      label: t('settings.species.customConfiguration.title'),
      icon: Settings2,
      content: configTabContent,
      hasChanges: configHasChanges,
    },
  ]);
</script>

<!-- Active Species Tab Content -->
{#snippet activeTabContent()}
  <div class="space-y-4">
    <!-- Stats Bar - Outside the card -->
    {#if activeSpeciesState.data}
      <div class="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <!-- Species Count -->
        <div class="card bg-base-100 shadow-sm border border-base-300">
          <div class="card-body p-3">
            <div class="flex items-center gap-2 text-[color:var(--color-base-content)] opacity-60">
              <Bird class="size-4" />
              <span class="text-xs font-medium"
                >{t('settings.species.activeSpecies.stats.species')}</span
              >
            </div>
            <div class="mt-1 text-xl font-semibold">{activeSpeciesState.data.count}</div>
          </div>
        </div>

        <!-- Location -->
        <div class="card bg-base-100 shadow-sm border border-base-300">
          <div class="card-body p-3">
            <div class="flex items-center gap-2 text-[color:var(--color-base-content)] opacity-60">
              <MapPin class="size-4" />
              <span class="text-xs font-medium"
                >{t('settings.species.activeSpecies.stats.location')}</span
              >
            </div>
            <div
              class="mt-1 text-sm font-semibold truncate"
              title="{activeSpeciesState.data.location.latitude.toFixed(
                2
              )}°, {activeSpeciesState.data.location.longitude.toFixed(2)}°"
            >
              {activeSpeciesState.data.location.latitude.toFixed(2)}°, {activeSpeciesState.data.location.longitude.toFixed(
                2
              )}°
            </div>
          </div>
        </div>

        <!-- Threshold -->
        <div class="card bg-base-100 shadow-sm border border-base-300">
          <div class="card-body p-3">
            <div class="flex items-center gap-2 text-[color:var(--color-base-content)] opacity-60">
              <SlidersHorizontal class="size-4" />
              <span class="text-xs font-medium"
                >{t('settings.species.activeSpecies.stats.threshold')}</span
              >
            </div>
            <div class="mt-1 text-xl font-semibold">{activeSpeciesState.data.threshold}</div>
          </div>
        </div>

        <!-- Last Updated -->
        <div class="card bg-base-100 shadow-sm border border-base-300">
          <div class="card-body p-3">
            <div class="flex items-center gap-2 text-[color:var(--color-base-content)] opacity-60">
              <Clock class="size-4" />
              <span class="text-xs font-medium"
                >{t('settings.species.activeSpecies.stats.updated')}</span
              >
            </div>
            <div class="mt-1 text-sm font-semibold">
              {formatRelativeTime(activeSpeciesState.data.lastUpdated)}
            </div>
          </div>
        </div>
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
              class="animate-spin h-8 w-8 border-2 border-primary border-t-transparent rounded-full"
            ></div>
          </div>
        {:else if activeSpeciesState.error}
          <!-- Error State -->
          <div class="p-4 rounded-lg bg-error/10 text-error">
            <p class="text-sm">{activeSpeciesState.error}</p>
            <button
              type="button"
              class="mt-2 px-3 py-1.5 text-sm rounded-lg bg-error/20 hover:bg-error/30 transition-colors"
              onclick={() => {
                const birdnetData = $birdnetSettings;
                if (birdnetData) loadActiveSpecies(birdnetData);
              }}
            >
              {t('settings.species.activeSpecies.retry') || 'Retry'}
            </button>
          </div>
        {:else if activeSpeciesState.locationNotConfigured}
          <!-- Location Not Configured Warning -->
          <div class="p-4 rounded-lg bg-warning/10 border border-warning/30">
            <div class="flex items-start gap-3">
              <MapPin class="size-5 text-warning shrink-0 mt-0.5" />
              <div>
                <p class="font-medium text-warning">
                  {t('settings.species.activeSpecies.locationNotConfigured.title') ||
                    'Location Not Configured'}
                </p>
                <p class="text-sm text-[color:var(--color-base-content)] opacity-70 mt-1">
                  {t('settings.species.activeSpecies.locationNotConfigured.description') ||
                    'Set your location in Main Settings to see species available in your area. The range filter uses your location to determine which species are likely to be found nearby.'}
                </p>
                <a href="/ui/settings/main" class="btn btn-warning btn-sm mt-3">
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
              />
              <input
                type="text"
                value={searchInputValue}
                oninput={handleSearchInput}
                placeholder={t('settings.species.activeSpecies.search.placeholder')}
                autocomplete="off"
                data-1p-ignore
                data-lpignore="true"
                data-form-type="other"
                class="w-full pl-10 pr-4 py-2 text-sm rounded-lg border border-base-300 bg-base-100 focus:outline-none focus:ring-2 focus:ring-primary"
              />
            </div>

            <!-- Expand/Collapse Button -->
            <button
              type="button"
              class="btn btn-outline btn-sm gap-2"
              onclick={() => (isListExpanded = !isListExpanded)}
              title={isListExpanded ? 'Collapse list' : 'Expand list'}
            >
              {#if isListExpanded}
                <Minimize2 class="size-4" />
              {:else}
                <Maximize2 class="size-4" />
              {/if}
            </button>

            <!-- CSV Download Button -->
            <button
              type="button"
              class="btn btn-outline btn-sm gap-2"
              onclick={downloadActiveSpeciesCSV}
              disabled={!activeSpeciesState.data.species.length}
              title="Download CSV"
            >
              <Download class="size-4" />
              <span class="hidden sm:inline">CSV</span>
            </button>
          </div>

          <!-- Species List - Use {#key} to force full recreation instead of diffing -->
          <!-- This prevents Svelte internal state corruption during list transitions -->
          {#key activeSearchQuery}
            {#if filteredActiveSpecies.length > 0}
              <div
                class="divide-y divide-base-300 rounded-lg border border-base-300 overflow-hidden overflow-y-auto"
                class:max-h-[32rem]={!isListExpanded}
              >
                {#each filteredActiveSpecies as species (species.scientificName)}
                  <div
                    class="flex items-center justify-between p-3 hover:bg-base-200/50 transition-colors"
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
                          <span class="badge badge-success badge-sm gap-1">
                            + {t('settings.species.activeSpecies.badges.included')}
                          </span>
                        {/if}
                        {#if species.hasCustomConfig}
                          <span class="badge badge-secondary badge-sm gap-1">
                            ★ {t('settings.species.activeSpecies.badges.configured')}
                          </span>
                        {/if}
                      </div>

                      <!-- Score Bar - always in same position -->
                      <div class="flex items-center gap-2">
                        <div class="w-20 h-2 bg-base-300 rounded-full overflow-hidden">
                          <div
                            class="h-full bg-primary rounded-full transition-all"
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
          <div class="flex items-center justify-between p-2 rounded-md bg-base-200">
            <span class="text-sm">{species}</span>
            <button
              type="button"
              class="btn btn-ghost btn-xs text-error"
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
          <div class="flex items-center justify-between p-2 rounded-md bg-base-200">
            <span class="text-sm">{species}</span>
            <button
              type="button"
              class="btn btn-ghost btn-xs text-error"
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
            class="btn btn-sm btn-primary gap-2"
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
        <div class="border border-base-300 rounded-lg p-3 bg-base-100 space-y-3">
          <!-- Main configuration row -->
          <div class="grid grid-cols-12 gap-3 items-end">
            <!-- Species Input -->
            <div class="col-span-4">
              <label class="label py-1" for="config-species">
                <span class="label-text text-xs"
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
              <label class="label py-1" for="config-threshold">
                <span class="label-text text-xs"
                  >{t('settings.species.customConfiguration.labels.threshold')}</span
                >
                <span class="label-text-alt text-xs">{newThreshold.toFixed(2)}</span>
              </label>
              <input
                id="config-threshold"
                type="range"
                min="0"
                max="1"
                step="0.01"
                value={newThreshold}
                oninput={e => (newThreshold = Number(e.currentTarget.value))}
                class="range range-xs range-primary"
              />
            </div>

            <!-- Interval -->
            <div class="col-span-3">
              <label class="label py-1" for="config-interval">
                <span class="label-text text-xs"
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
                class="input input-xs w-full"
                placeholder="0"
              />
            </div>

            <!-- Buttons -->
            <div class="col-span-2 flex gap-1">
              <button
                class="btn btn-xs btn-primary flex-1"
                class:loading={store.isSaving}
                data-testid="save-config-button"
                onclick={saveConfig}
                disabled={!configInputValue.trim() ||
                  newThreshold < 0 ||
                  newThreshold > 1 ||
                  store.isLoading ||
                  store.isSaving}
              >
                {#if store.isSaving}
                  {t('settings.species.customConfiguration.saving')}
                {:else}
                  {editingSpecies
                    ? t('settings.species.customConfiguration.save')
                    : t('settings.species.customConfiguration.labels.addButton')}
                {/if}
              </button>
              <button
                class="btn btn-xs btn-ghost flex-1"
                onclick={cancelEdit}
                disabled={store.isSaving}
              >
                {t('settings.species.customConfiguration.cancel')}
              </button>
            </div>
          </div>

          <!-- Actions Toggle -->
          <div class="border-t border-base-300 pt-2">
            <button
              type="button"
              class="flex items-center gap-2 text-xs font-medium hover:text-primary transition-colors"
              onclick={() => (showActions = !showActions)}
              aria-expanded={showActions}
              aria-controls="actionsSection"
            >
              <span class="transition-transform duration-200" class:rotate-90={showActions}>
                <ChevronRight class="size-4" />
              </span>
              <span>{t('settings.species.customConfiguration.configureActions')}</span>
              {#if actionCommand}
                <span class="badge badge-xs badge-accent"
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
                <label class="label py-1" for="action-command">
                  <span class="label-text text-xs"
                    >{t('settings.species.actionsModal.command.label')}</span
                  >
                </label>
                <input
                  id="action-command"
                  type="text"
                  bind:value={actionCommand}
                  placeholder={t('settings.species.commandPathPlaceholder')}
                  class="input input-xs w-full"
                />
                <span class="help-text mt-1"
                  >{t('settings.species.actionsModal.command.helpText')}</span
                >
              </div>

              <!-- Parameters -->
              <div>
                <label class="label py-1" for="action-parameters">
                  <span class="label-text text-xs flex items-center gap-1">
                    {t('settings.species.actionsModal.parameters.label')}
                    <span
                      class="text-base-content opacity-60"
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
                  class="input input-xs w-full bg-base-200/50"
                  title="Add parameters using the buttons below or type directly (comma-separated)"
                />
                <span class="help-text mt-1"
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
                    class="btn btn-xs"
                    onclick={() => addParameter('CommonName')}
                    >{t('settings.species.actionsModal.parameters.buttons.commonName')}</button
                  >
                  <button
                    type="button"
                    class="btn btn-xs"
                    onclick={() => addParameter('ScientificName')}
                    >{t('settings.species.actionsModal.parameters.buttons.scientificName')}</button
                  >
                  <button
                    type="button"
                    class="btn btn-xs"
                    onclick={() => addParameter('Confidence')}
                    >{t('settings.species.actionsModal.parameters.buttons.confidence')}</button
                  >
                  <button type="button" class="btn btn-xs" onclick={() => addParameter('Time')}
                    >{t('settings.species.actionsModal.parameters.buttons.time')}</button
                  >
                  <button type="button" class="btn btn-xs" onclick={() => addParameter('Source')}
                    >{t('settings.species.actionsModal.parameters.buttons.source')}</button
                  >
                  <button type="button" class="btn btn-xs btn-warning" onclick={clearParameters}
                    >{t('settings.species.actionsModal.parameters.buttons.clearParameters')}</button
                  >
                </div>
              </div>

              <!-- Execute Defaults Checkbox -->
              <div class="form-control">
                <label
                  class="label cursor-pointer justify-start gap-2"
                  for="action-execute-defaults"
                >
                  <input
                    id="action-execute-defaults"
                    type="checkbox"
                    bind:checked={actionExecuteDefaults}
                    class="checkbox checkbox-xs checkbox-primary"
                  />
                  <span class="label-text text-xs"
                    >{t('settings.species.actionsModal.executeDefaults.label')}</span
                  >
                </label>
                <span class="help-text mt-1"
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
            class="flex items-center gap-3 p-2 rounded-lg bg-base-100 border border-base-300 hover:border-base-content/20 transition-colors"
          >
            <!-- Species Name -->
            <div class="flex-1 min-w-0">
              <span class="font-medium text-sm truncate block">{species}</span>
            </div>

            <!-- Threshold -->
            <div class="flex items-center gap-2">
              <span class="text-xs text-base-content opacity-60"
                >{t('settings.species.customConfiguration.list.threshold')}</span
              >
              <span class="font-mono text-xs font-medium">{(config.threshold ?? 0).toFixed(2)}</span
              >
            </div>

            <!-- Interval -->
            <div class="flex items-center gap-2">
              <span class="text-xs text-base-content opacity-60"
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
              <span class="badge badge-xs badge-accent"
                >{t('settings.species.customConfiguration.list.actionBadge')}</span
              >
            {/if}

            <!-- Actions -->
            <div class="flex items-center gap-1">
              <button
                class="btn btn-ghost btn-xs"
                onclick={() => startEdit(species)}
                title={t('settings.species.customConfiguration.list.editTitle')}
                aria-label={t('settings.species.customConfiguration.list.editTitle')}
                disabled={store.isLoading || store.isSaving}
              >
                <SquarePen class="size-4" />
              </button>

              <button
                class="btn btn-ghost btn-xs text-error"
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
        <div class="text-center py-8 text-base-content opacity-60">
          <p class="text-sm">{t('settings.species.customConfiguration.emptyState.title')}</p>
          <p class="text-xs mt-1">
            {t('settings.species.customConfiguration.emptyState.description')}
          </p>
        </div>
      {/if}
    </div>
  </SettingsSection>
{/snippet}

<main class="settings-page-content" aria-label="Species settings configuration">
  <SettingsTabs {tabs} bind:activeTab />
</main>
