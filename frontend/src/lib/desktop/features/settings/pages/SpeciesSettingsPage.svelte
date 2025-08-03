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
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
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

  // Edit config state
  let editingConfig = $state<string | null>(null);
  let editConfigNewName = $state('');
  let editConfigThreshold = $state(0.5);
  let editConfigInterval = $state(0);

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

  // Configuration management
  function addConfig() {
    const species = configInputValue.trim();
    if (!species || safeGet(settings.config, species)) return;

    const threshold = Number(newThreshold);
    if (threshold < 0 || threshold > 1) return;

    const interval = Number(newInterval) || 0;

    const newConfig: SpeciesConfig = {
      threshold,
      interval,
      actions: [],
    };

    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      species: {
        ...settings,
        config: { ...settings.config, [species]: newConfig },
      },
    });

    // Clear form
    configInputValue = '';
    newThreshold = 0.5;
    newInterval = 0;
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

  function startEditConfig(species: string) {
    editingConfig = species;
    editConfigNewName = species;
    const config = safeGet(settings.config, species, { threshold: 0.5, interval: 0, actions: [] });
    editConfigThreshold = config.threshold;
    editConfigInterval = config.interval || 0;
  }

  function saveEditConfig() {
    if (!editingConfig || !editConfigNewName) return;

    const originalSpecies = editingConfig;
    const newSpecies = editConfigNewName;
    const threshold = editConfigThreshold;
    const interval = editConfigInterval || 0;

    let updatedConfig: typeof settings.config;

    if (originalSpecies !== newSpecies) {
      // Rename: create new entry and delete old one
      const originalConfig = safeGet(settings.config, originalSpecies, {
        threshold: 0.5,
        interval: 0,
        actions: [],
      });
      const baseConfig = Object.fromEntries(
        Object.entries(settings.config).filter(([key]) => key !== originalSpecies)
      );
      updatedConfig = {
        ...baseConfig,
        [newSpecies]: {
          threshold,
          interval,
          actions: originalConfig.actions || [],
        },
      };
    } else {
      // Just update values
      const existingConfig = safeGet(settings.config, originalSpecies, {
        threshold: 0.5,
        interval: 0,
        actions: [],
      });
      updatedConfig = {
        ...settings.config,
        [originalSpecies]: {
          ...existingConfig,
          threshold,
          interval,
        },
      };
    }

    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      species: {
        ...settings,
        config: updatedConfig,
      },
    });
    cancelEditConfig();
  }

  function cancelEditConfig() {
    editingConfig = null;
    editConfigNewName = '';
    editConfigThreshold = 0.5;
    editConfigInterval = 0;
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

<!-- Remove page-level loading spinner to prevent flickering -->
<div class="space-y-4">
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
      <!-- Help text -->
      <div class="text-sm text-base-content mb-4">
        <p>{t('settings.species.customConfiguration.helpText.intro')}</p>
        <ul class="list-disc list-inside pl-4 text-xs">
          <li><b>Threshold</b>: {t('settings.species.customConfiguration.helpText.threshold')}</li>
          <li>
            <b>Interval</b>: {t('settings.species.customConfiguration.helpText.interval')}
          </li>
          <li><b>Actions</b>: {t('settings.species.customConfiguration.helpText.actions')}</li>
        </ul>
      </div>

      <!-- Configuration list -->
      <div class="space-y-2">
        <!-- Column headers -->
        {#if Object.keys(settings.config).length > 0}
          <div class="grid grid-cols-12 gap-2 mb-1 text-xs font-medium text-base-content/70">
            <div class="col-span-5 px-2">
              {t('settings.species.customConfiguration.columnHeaders.species')}
            </div>
            <div class="col-span-6 px-2">
              {t('settings.species.customConfiguration.columnHeaders.settings')}
            </div>
            <div class="col-span-1 px-2 text-right">
              {t('settings.species.customConfiguration.columnHeaders.actions')}
            </div>
          </div>
        {/if}

        <!-- Edit mode -->
        {#if editingConfig}
          <div class="flex items-center justify-between p-2 rounded-md bg-base-300">
            <div class="flex-grow grid grid-cols-12 gap-2">
              <div class="col-span-6">
                <TextInput
                  bind:value={editConfigNewName}
                  placeholder={t('forms.placeholders.speciesName')}
                  size="xs"
                />
              </div>
              <div class="col-span-2">
                <NumberField
                  label={t('settings.species.customConfiguration.labels.threshold')}
                  value={editConfigThreshold}
                  onUpdate={value => (editConfigThreshold = value)}
                  min={0}
                  max={1}
                  step={0.01}
                  placeholder={t(
                    'settings.species.customConfiguration.addForm.thresholdPlaceholder'
                  )}
                />
              </div>
              <div class="col-span-2">
                <NumberField
                  label={t('settings.species.customConfiguration.labels.interval')}
                  value={editConfigInterval}
                  onUpdate={value => (editConfigInterval = value)}
                  min={0}
                  max={3600}
                  step={1}
                  placeholder={t(
                    'settings.species.customConfiguration.addForm.intervalPlaceholder'
                  )}
                />
              </div>
              <div class="col-span-2 flex space-x-1">
                <button
                  type="button"
                  class="btn btn-primary btn-xs flex-1"
                  onclick={saveEditConfig}
                >
                  {t('common.buttons.save')}
                </button>
                <button
                  type="button"
                  class="btn btn-ghost btn-xs flex-1"
                  onclick={cancelEditConfig}
                >
                  {t('common.buttons.cancel')}
                </button>
              </div>
            </div>
          </div>
        {/if}

        <!-- List items -->
        {#each Object.entries(settings.config) as [species, config]}
          {#if editingConfig !== species}
            <div class="flex items-center justify-between p-2 rounded-md bg-base-200">
              <div class="flex-grow grid grid-cols-12 gap-2 items-center">
                <!-- Species name -->
                <div class="col-span-5 text-sm pl-2">{species}</div>

                <!-- Settings badges -->
                <div class="col-span-6 flex flex-wrap gap-1">
                  <span class="badge badge-sm badge-neutral">
                    {t('settings.species.customConfiguration.badges.threshold', {
                      value: config.threshold.toFixed(2),
                    })}
                  </span>
                  {#if config.interval > 0}
                    <span class="badge badge-sm badge-secondary">
                      {t('settings.species.customConfiguration.badges.interval', {
                        value: config.interval,
                      })}
                    </span>
                  {/if}
                  {#if config.actions?.length > 0}
                    <span class="badge badge-sm badge-accent"
                      >{t('settings.species.customConfiguration.badges.customAction')}</span
                    >
                  {/if}
                  {#if config.actions?.[0]?.executeDefaults}
                    <span class="badge badge-sm badge-info"
                      >{t('settings.species.customConfiguration.badges.executeDefaults')}</span
                    >
                  {/if}
                </div>

                <!-- Actions dropdown -->
                <div class="col-span-1 text-right">
                  <div class="dropdown dropdown-end">
                    <div tabindex="0" role="button" class="btn btn-ghost btn-xs">⋮</div>
                    <ul class="dropdown-content menu bg-base-100 rounded-box z-[1] w-40 p-2 shadow">
                      <li>
                        <button onclick={() => startEditConfig(species)}
                          >{t('settings.species.customConfiguration.dropdown.editConfig')}</button
                        >
                      </li>
                      <li>
                        <button onclick={() => openActionsModal(species)}
                          >{t('settings.species.customConfiguration.dropdown.addAction')}</button
                        >
                      </li>
                      <li>
                        <button onclick={() => removeConfig(species)} class="text-error">
                          {t('settings.species.customConfiguration.dropdown.remove')}
                        </button>
                      </li>
                    </ul>
                  </div>
                </div>
              </div>
            </div>
          {/if}
        {/each}

        {#if Object.keys(settings.config).length === 0}
          <div class="text-sm text-base-content/60 italic p-2 text-center">
            {t('settings.species.customConfiguration.noConfigurationsMessage')}
          </div>
        {/if}
      </div>

      <!-- Add configuration form -->
      <div class="mt-4 space-y-4">
        <!-- Input fields -->
        <div class="grid grid-cols-12 gap-4">
          <div class="col-span-6">
            <SpeciesInput
              bind:value={configInputValue}
              placeholder={t('settings.species.customConfiguration.addForm.speciesPlaceholder')}
              predictions={configPredictions}
              size="sm"
              onInput={updateConfigPredictions}
              onAdd={() => {}}
              buttonText=""
              buttonIcon={false}
              disabled={store.isLoading || store.isSaving}
            />
          </div>

          <div class="col-span-2">
            <NumberField
              label={t('settings.species.customConfiguration.labels.threshold')}
              value={newThreshold}
              onUpdate={value => (newThreshold = value)}
              min={0}
              max={1}
              step={0.01}
              placeholder={t('settings.species.customConfiguration.addForm.thresholdPlaceholder')}
            />
          </div>

          <div class="col-span-2">
            <NumberField
              label={t('settings.species.customConfiguration.labels.intervalSeconds')}
              value={newInterval}
              onUpdate={value => (newInterval = value)}
              min={0}
              max={3600}
              step={1}
              placeholder={t('settings.species.customConfiguration.addForm.intervalPlaceholder')}
            />
          </div>

          <div class="col-span-2">
            <button
              type="button"
              class="btn btn-primary btn-sm w-full"
              onclick={addConfig}
              disabled={!configInputValue.trim() || newThreshold < 0 || newThreshold > 1}
            >
              {t('settings.species.customConfiguration.labels.addButton')}
            </button>
          </div>
        </div>
      </div>
    </div>
  </SettingsSection>
</div>

<!-- Actions Modal -->
{#if showActionsModal}
  <div class="modal modal-open">
    <div class="modal-box bg-base-100 max-h-[90vh] overflow-y-auto">
      <h3 class="text-lg font-bold mb-4">
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
