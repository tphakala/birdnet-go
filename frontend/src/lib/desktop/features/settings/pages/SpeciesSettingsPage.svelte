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
  import SpeciesInput from '$lib/desktop/components/forms/SpeciesInput.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectField from '$lib/desktop/components/forms/SelectField.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import {
    settingsStore,
    settingsActions,
    speciesSettings,
    realtimeSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import type { SpeciesConfig, Action, SpeciesSettings } from '$lib/stores/settings';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { safeGet } from '$lib/utils/security';

  const logger = loggers.settings;

  // PERFORMANCE OPTIMIZATION: Cache CSRF token with $derived
  let csrfToken = $derived(
    (document.querySelector('meta[name="csrf-token"]') as HTMLElement)?.getAttribute('content') ||
      ''
  );

  // PERFORMANCE OPTIMIZATION: Reactive settings with proper defaults
  let settings = $derived(
    $speciesSettings ||
      ({
        include: [] as string[],
        exclude: [] as string[],
        config: {} as Record<string, SpeciesConfig>,
      } as SpeciesSettings)
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

  // Actions modal state
  let showActionsModal = $state(false);
  let currentSpecies = $state('');
  let currentAction = $state<{
    type: 'ExecuteCommand';
    command: string;
    parameters: string;
    executeDefaults: boolean;
  }>({
    type: 'ExecuteCommand',
    command: '',
    parameters: '',
    executeDefaults: true,
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

  // Focus trapping effect for actions modal
  $effect(() => {
    if (showActionsModal) {
      // Store previously focused element
      previouslyFocusedElement = document.activeElement as HTMLElement;

      // Set focus to the modal after a microtask to ensure it's in the DOM
      setTimeout(() => {
        const modal = document.querySelector(
          '[role="dialog"][aria-labelledby="actions-modal-title"]'
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

  // PERFORMANCE OPTIMIZATION: Load species data with proper state management
  $effect(() => {
    loadSpeciesData();
  });

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

  // Prediction functions
  function updateIncludePredictions(input: string) {
    if (!input || input.length < 2) {
      includePredictions = [];
      return;
    }

    const inputLower = input.toLowerCase();
    includePredictions = allSpecies
      .filter(
        species => species.toLowerCase().includes(inputLower) && !settings.include.includes(species)
      )
      .slice(0, 10);
  }

  function updateExcludePredictions(input: string) {
    if (!input || input.length < 2) {
      excludePredictions = [];
      return;
    }

    const inputLower = input.toLowerCase();
    excludePredictions = filteredSpecies
      .filter(
        species => species.toLowerCase().includes(inputLower) && !settings.exclude.includes(species)
      )
      .slice(0, 10);
  }

  function updateConfigPredictions(input: string) {
    if (!input || input.length < 2) {
      configPredictions = [];
      return;
    }

    const inputLower = input.toLowerCase();
    const existingConfigs = Object.keys(settings.config).map(s => s.toLowerCase());
    configPredictions = allSpecies
      .filter(species => {
        const speciesLower = species.toLowerCase();
        return speciesLower.includes(inputLower) && !existingConfigs.includes(speciesLower);
      })
      .slice(0, 10);
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
  }

  function saveConfig() {
    const species = configInputValue.trim();
    if (!species) return;

    const threshold = Number(newThreshold);
    if (threshold < 0 || threshold > 1) return;

    const interval = Number(newInterval) || 0;

    let updatedConfig = { ...settings.config };

    if (editingSpecies && editingSpecies !== species) {
      // Rename: copy actions from old species
      const oldConfig = safeGet(settings.config, editingSpecies, {
        threshold: 0.5,
        interval: 0,
        actions: [],
      });
      // eslint-disable-next-line security/detect-object-injection
      delete updatedConfig[editingSpecies];
      // eslint-disable-next-line security/detect-object-injection
      updatedConfig[species] = {
        threshold,
        interval,
        actions: oldConfig.actions || [],
      };
    } else {
      // Add new or update existing
      const existingConfig = editingSpecies
        ? safeGet(settings.config, editingSpecies, { threshold: 0.5, interval: 0, actions: [] })
        : null;
      // eslint-disable-next-line security/detect-object-injection
      updatedConfig[species] = {
        threshold,
        interval,
        actions: existingConfig?.actions || [],
      };
    }

    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      species: {
        ...settings,
        config: updatedConfig,
      },
    });

    // Reset form
    cancelEdit();
  }

  function cancelEdit() {
    configInputValue = '';
    newThreshold = 0.5;
    newInterval = 0;
    editingSpecies = null;
    showAddForm = false;
  }

  // Actions modal functions
  function openActionsModal(species: string) {
    currentSpecies = species;

    const speciesConfig = safeGet(settings.config, species, {
      threshold: 0.5,
      interval: 0,
      actions: [],
    });
    const existingAction = speciesConfig.actions?.[0];
    if (existingAction) {
      currentAction = {
        type: existingAction.type,
        command: existingAction.command,
        parameters: Array.isArray(existingAction.parameters)
          ? existingAction.parameters.join(',')
          : '',
        executeDefaults: existingAction.executeDefaults !== false,
      };
    } else {
      currentAction = {
        type: 'ExecuteCommand',
        command: '',
        parameters: '',
        executeDefaults: true,
      };
    }

    showActionsModal = true;
  }

  function saveAction() {
    if (!currentSpecies) return;

    const newAction: Action = {
      type: currentAction.type,
      command: currentAction.command,
      parameters: currentAction.parameters
        .split(',')
        .map(p => p.trim())
        .filter(p => p),
      executeDefaults: currentAction.executeDefaults,
    };

    const updatedConfig = { ...settings.config };
    const currentSpeciesConfig = safeGet(settings.config, currentSpecies, {
      threshold: 0.5,
      interval: 0,
      actions: [],
    });
    Object.assign(updatedConfig, {
      [currentSpecies]: {
        ...currentSpeciesConfig,
        actions: [newAction],
      },
    });

    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      species: {
        ...settings,
        config: updatedConfig,
      },
    });
    closeActionsModal();
  }

  function closeActionsModal() {
    showActionsModal = false;
    currentSpecies = '';
  }

  // Parameter helper functions
  function addParameter(param: string) {
    if (currentAction.parameters) {
      currentAction.parameters += ',' + param;
    } else {
      currentAction.parameters = param;
    }
  }

  function clearParameters() {
    currentAction.parameters = '';
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
              class="btn btn-ghost btn-xs"
              onclick={() => removeIncludeSpecies(species)}
              disabled={store.isLoading || store.isSaving}
              aria-label="Remove {species}"
            >
              ✕
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
              class="btn btn-ghost btn-xs"
              onclick={() => removeExcludeSpecies(species)}
              disabled={store.isLoading || store.isSaving}
              aria-label="Remove {species}"
            >
              ✕
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
            class="btn btn-sm btn-primary gap-2"
            onclick={() => (showAddForm = true)}
            disabled={store.isLoading || store.isSaving}
          >
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M12 4v16m8-8H4"
              />
            </svg>
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
        <div class="border border-base-300 rounded-lg p-3 bg-base-100">
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
                onAdd={() => {}}
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
                onclick={saveConfig}
                disabled={!configInputValue.trim() || newThreshold < 0 || newThreshold > 1}
              >
                {editingSpecies ? 'Save' : 'Add'}
              </button>
              <button class="btn btn-xs btn-ghost flex-1" onclick={cancelEdit}> Cancel </button>
            </div>
          </div>
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
              <span class="font-mono text-xs font-medium">{config.threshold.toFixed(2)}</span>
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
              >
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"
                  />
                </svg>
              </button>

              <button
                class="btn btn-ghost btn-xs"
                onclick={() => openActionsModal(species)}
                title="Configure actions"
                aria-label="Configure actions for {species}"
              >
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"
                  />
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
                  />
                </svg>
              </button>

              <button
                class="btn btn-ghost btn-xs text-error"
                onclick={() => removeConfig(species)}
                title="Remove configuration"
                aria-label="Remove {species} configuration"
              >
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"
                  />
                </svg>
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

<!-- Actions Modal -->
{#if showActionsModal}
  <div
    class="modal modal-open"
    role="dialog"
    aria-modal="true"
    aria-labelledby="actions-modal-title"
  >
    <div class="modal-box bg-base-100 max-h-[90vh] overflow-y-auto">
      <h3 id="actions-modal-title" class="text-lg font-bold mb-4">
        {t('settings.species.actionsModal.title', { species: currentSpecies })}
      </h3>

      <div class="space-y-4">
        <SelectField
          label={t('settings.species.actionsModal.actionType.label')}
          bind:value={currentAction.type}
          options={[
            {
              value: 'ExecuteCommand',
              label: t('settings.species.actionsModal.actionType.executeCommand'),
            },
          ]}
          disabled={true}
          helpText={t('settings.species.actionsModal.actionType.onlySupported')}
        />

        <TextInput
          label={t('settings.species.actionsModal.command.label')}
          bind:value={currentAction.command}
          placeholder={t('settings.species.commandPathPlaceholder')}
          helpText={t('settings.species.actionsModal.command.helpText')}
        />

        <div class="form-control">
          <label class="label" for="action-parameters">
            <span class="label-text">{t('settings.species.actionsModal.parameters.label')}</span>
          </label>
          <TextInput
            id="action-parameters"
            bind:value={currentAction.parameters}
            placeholder={t('settings.species.parametersPlaceholder')}
            readonly={true}
            helpText={t('settings.species.actionsModal.parameters.helpText')}
          />
        </div>

        <div>
          <div class="font-medium text-sm mb-2">
            {t('settings.species.actionsModal.parameters.availableTitle')}
          </div>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-xs" onclick={() => addParameter('CommonName')}
              >{t('settings.species.actionsModal.parameters.buttons.commonName')}</button
            >
            <button type="button" class="btn btn-xs" onclick={() => addParameter('ScientificName')}
              >{t('settings.species.actionsModal.parameters.buttons.scientificName')}</button
            >
            <button type="button" class="btn btn-xs" onclick={() => addParameter('Confidence')}
              >{t('settings.species.actionsModal.parameters.buttons.confidence')}</button
            >
            <button type="button" class="btn btn-xs" onclick={() => addParameter('Time')}
              >{t('settings.species.actionsModal.parameters.buttons.time')}</button
            >
            <button type="button" class="btn btn-xs" onclick={() => addParameter('Source')}
              >{t('settings.species.actionsModal.parameters.buttons.source')}</button
            >
          </div>
          <div class="mt-2">
            <button type="button" class="btn btn-xs btn-warning" onclick={clearParameters}
              >{t('settings.species.actionsModal.parameters.buttons.clearParameters')}</button
            >
          </div>
        </div>

        <Checkbox
          bind:checked={currentAction.executeDefaults}
          label={t('settings.species.actionsModal.executeDefaults.label')}
          helpText={t('settings.species.actionsModal.executeDefaults.helpText')}
        />
      </div>

      <div class="modal-action mt-6">
        <button type="button" class="btn btn-primary" onclick={saveAction}>
          {t('common.buttons.save')}
        </button>
        <button type="button" class="btn btn-ghost" onclick={closeActionsModal}>
          {t('common.buttons.cancel')}
        </button>
      </div>
    </div>
    <div
      class="modal-backdrop bg-black/50"
      role="button"
      tabindex="0"
      onclick={closeActionsModal}
      onkeydown={e => (e.key === 'Escape' ? closeActionsModal() : null)}
      aria-label="Close actions modal"
    ></div>
  </div>
{/if}
