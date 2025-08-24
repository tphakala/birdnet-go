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
  import { onMount, onDestroy } from 'svelte';
  import SpeciesInput from '$lib/desktop/components/forms/SpeciesInput.svelte';
  import {
    settingsStore,
    settingsActions,
    speciesSettings,
    realtimeSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import type { SpeciesConfig, SpeciesSettings } from '$lib/stores/settings';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { safeGet } from '$lib/utils/security';
  import { navigationIcons, actionIcons } from '$lib/utils/icons';
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
      (store.originalData as any)?.realtime?.species?.include,
      (store.formData as any)?.realtime?.species?.include
    )
  );

  let excludeHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.realtime?.species?.exclude,
      (store.formData as any)?.realtime?.species?.exclude
    )
  );

  let configHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.realtime?.species?.config,
      (store.formData as any)?.realtime?.species?.config
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
      speciesListState.data = data.species?.map((s: any) => s.commonName || s.label) || [];
    } catch (error) {
      logger.error('Failed to load species data:', error);
      speciesListState.error = t('settings.species.errors.speciesLoadFailed');
      speciesListState.data = [];
    } finally {
      speciesListState.loading = false;
    }
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
      // eslint-disable-next-line security/detect-object-injection
      delete updatedConfig[editingSpecies];
    }

    // Add/update species configuration
    // eslint-disable-next-line security/detect-object-injection
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
</script>

<main class="space-y-4 settings-page-content" aria-label="Species settings configuration">
  <!-- Include Species Section -->
  <SettingsSection
    title={t('settings.species.alwaysInclude.title')}
    description={t('settings.species.alwaysInclude.description')}
    defaultOpen={true}
    hasChanges={includeHasChanges}
  >
    <div class="space-y-4">
      <!-- Species list -->
      <div class="space-y-2">
        {#each settings.include as species}
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
              {@html actionIcons.delete}
            </button>
          </div>
        {/each}

        {#if settings.include.length === 0}
          <div class="text-sm text-base-content/60 italic p-2 text-center">
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

  <!-- Exclude Species Section -->
  <SettingsSection
    title={t('settings.species.alwaysExclude.title')}
    description={t('settings.species.alwaysExclude.description')}
    defaultOpen={true}
    hasChanges={excludeHasChanges}
  >
    <div class="space-y-4">
      <!-- Species list -->
      <div class="space-y-2">
        {#each settings.exclude as species}
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
              {@html actionIcons.delete}
            </button>
          </div>
        {/each}

        {#if settings.exclude.length === 0}
          <div class="text-sm text-base-content/60 italic p-2 text-center">
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

  <!-- Custom Configuration Section -->
  <SettingsSection
    title={t('settings.species.customConfiguration.title')}
    description={t('settings.species.customConfiguration.description')}
    defaultOpen={true}
    hasChanges={configHasChanges}
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
            {@html actionIcons.add}
            Add Configuration
          </button>
        {:else}
          <span class="text-sm font-medium">
            {editingSpecies ? `Editing: ${editingSpecies}` : 'New Configuration'}
          </span>
        {/if}

        {#if Object.keys(settings.config).length > 0}
          <span class="text-xs text-base-content/60">
            {Object.keys(settings.config).length} configured
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
                <span class="label-text text-xs">Species</span>
              </label>
              <SpeciesInput
                id="config-species"
                bind:value={configInputValue}
                placeholder="Type to search..."
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
                <span class="label-text text-xs">Threshold</span>
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
                <span class="label-text text-xs">Interval (s)</span>
              </label>
              <input
                id="config-interval"
                type="number"
                value={newInterval}
                onchange={e => (newInterval = Number(e.currentTarget.value))}
                min="0"
                max="3600"
                class="input input-bordered input-xs w-full"
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
                  Saving...
                {:else}
                  {editingSpecies ? 'Save' : 'Add'}
                {/if}
              </button>
              <button
                class="btn btn-xs btn-ghost flex-1"
                onclick={cancelEdit}
                disabled={store.isSaving}
              >
                Cancel
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
                {@html navigationIcons.chevronRight}
              </span>
              <span>Configure Actions</span>
              {#if actionCommand}
                <span class="badge badge-xs badge-accent">Configured</span>
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
                  class="input input-bordered input-xs w-full"
                />
                <div class="label">
                  <span class="label-text-alt text-xs"
                    >{t('settings.species.actionsModal.command.helpText')}</span
                  >
                </div>
              </div>

              <!-- Parameters -->
              <div>
                <label class="label py-1" for="action-parameters">
                  <span class="label-text text-xs flex items-center gap-1">
                    {t('settings.species.actionsModal.parameters.label')}
                    <span class="text-base-content/60" title="Use buttons below or type directly">
                      ⓘ
                    </span>
                  </span>
                </label>
                <input
                  id="action-parameters"
                  type="text"
                  bind:value={actionParameters}
                  placeholder="Click buttons below to add parameters or type manually"
                  class="input input-bordered input-xs w-full bg-base-200/50"
                  title="Add parameters using the buttons below or type directly (comma-separated)"
                />
                <div class="label">
                  <span class="label-text-alt text-xs"
                    >{t('settings.species.actionsModal.parameters.helpText')}</span
                  >
                </div>
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
                <div class="label">
                  <span class="label-text-alt text-xs"
                    >{t('settings.species.actionsModal.executeDefaults.helpText')}</span
                  >
                </div>
              </div>
            </div>
          {/if}
        </div>
      {/if}

      <!-- Compact Configuration List -->
      <div class="space-y-2">
        {#each Object.entries(settings.config) as [species, config]}
          <div
            class="flex items-center gap-3 p-2 rounded-lg bg-base-100 border border-base-300 hover:border-base-content/20 transition-colors"
          >
            <!-- Species Name -->
            <div class="flex-1 min-w-0">
              <span class="font-medium text-sm truncate block">{species}</span>
            </div>

            <!-- Threshold -->
            <div class="flex items-center gap-2">
              <span class="text-xs text-base-content/60">Threshold:</span>
              <span class="font-mono text-xs font-medium">{(config.threshold ?? 0).toFixed(2)}</span
              >
            </div>

            <!-- Interval -->
            <div class="flex items-center gap-2">
              <span class="text-xs text-base-content/60">Interval:</span>
              <span class="font-mono text-xs font-medium">
                {config.interval > 0 ? `${config.interval}s` : 'None'}
              </span>
            </div>

            <!-- Action Badge -->
            {#if config.actions?.length > 0}
              <span class="badge badge-xs badge-accent">Action</span>
            {/if}

            <!-- Actions -->
            <div class="flex items-center gap-1">
              <button
                class="btn btn-ghost btn-xs"
                onclick={() => startEdit(species)}
                title="Edit configuration"
                aria-label="Edit {species} configuration"
                disabled={store.isLoading || store.isSaving}
              >
                {@html actionIcons.edit}
              </button>

              <button
                class="btn btn-ghost btn-xs text-error"
                onclick={() => removeConfig(species)}
                title="Remove configuration"
                aria-label="Remove {species} configuration"
                disabled={store.isLoading || store.isSaving}
              >
                {@html actionIcons.delete}
              </button>
            </div>
          </div>
        {/each}
      </div>

      <!-- Empty State -->
      {#if Object.keys(settings.config).length === 0 && !showAddForm}
        <div class="text-center py-8 text-base-content/60">
          <p class="text-sm">No configurations yet.</p>
          <p class="text-xs mt-1">
            Click "Add Configuration" to customize species detection settings.
          </p>
        </div>
      {/if}
    </div>
  </SettingsSection>
</main>
