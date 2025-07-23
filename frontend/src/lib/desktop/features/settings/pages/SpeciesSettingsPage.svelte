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
  import type { SpeciesConfig, Action } from '$lib/stores/settings';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import { t } from '$lib/i18n';

  // Derived settings with fallbacks
  let settings = $state({
    include: [] as string[],
    exclude: [] as string[],
    config: {} as Record<string, SpeciesConfig>,
  });

  // Update settings when store changes
  $effect(() => {
    if ($speciesSettings) {
      settings = $speciesSettings;
    }
  });

  let store = $derived($settingsStore);

  // Species predictions state
  let allSpecies = $state<string[]>([]);
  let filteredSpecies = $state<string[]>([]);
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

  // Change detection
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

  // Load species data from API
  async function loadSpeciesData() {
    try {
      const response = await fetch('/api/v2/range/species/list');
      if (response.ok) {
        const data = await response.json();
        allSpecies = data.species?.map((s: any) => s.commonName || s.label) || [];
        filteredSpecies = [...allSpecies];
      }
    } catch (error) {
      console.error('Failed to load species data:', error);
      allSpecies = [];
      filteredSpecies = [];
    }
  }

  // Initialize species data
  $effect(() => {
    loadSpeciesData();
  });

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
    if (!species || settings.config[species]) return;

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
    const { [species]: _, ...remaining } = settings.config;
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      species: {
        ...settings,
        config: remaining,
      },
    });
    void _; // Explicitly indicate variable is intentionally unused
  }

  function startEditConfig(species: string) {
    editingConfig = species;
    editConfigNewName = species;
    editConfigThreshold = settings.config[species].threshold;
    editConfigInterval = settings.config[species].interval || 0;
  }

  function saveEditConfig() {
    if (!editingConfig || !editConfigNewName) return;

    const originalSpecies = editingConfig;
    const newSpecies = editConfigNewName;
    const threshold = editConfigThreshold;
    const interval = editConfigInterval || 0;

    const updatedConfig = { ...settings.config };

    if (originalSpecies !== newSpecies) {
      // Rename: create new entry and delete old one
      updatedConfig[newSpecies] = {
        threshold,
        interval,
        actions: settings.config[originalSpecies].actions || [],
      };
      delete updatedConfig[originalSpecies];
    } else {
      // Just update values
      updatedConfig[originalSpecies] = {
        ...updatedConfig[originalSpecies],
        threshold,
        interval,
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

    const existingAction = settings.config[species]?.actions?.[0];
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

    const updatedConfig = {
      ...settings.config,
      [currentSpecies]: {
        ...settings.config[currentSpecies],
        actions: [newAction],
      },
    };

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

{#if store.isLoading}
  <div class="flex items-center justify-center py-12">
    <div class="loading loading-spinner loading-lg"></div>
  </div>
{:else}
  <!-- Include Species Section -->
  <SettingsSection
    title="Always Include Species"
    description="Species in this list will always be included in range of detected species"
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
            No species added to include list
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
    title="Always Exclude Species"
    description="Species in this list will always be excluded from detection"
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
            No species added to exclude list
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
    title="Custom Species Configuration"
    description="Species specific threshold values, detection intervals, and actions"
    defaultOpen={true}
    hasChanges={configHasChanges}
  >
    <div class="space-y-4">
      <!-- Help text -->
      <div class="text-sm text-base-content mb-4">
        <p>Configure species-specific settings:</p>
        <ul class="list-disc list-inside pl-4 text-xs">
          <li><b>Threshold</b>: Minimum confidence score (0-1) required for detection</li>
          <li>
            <b>Interval</b>: Minimum time in seconds between detections of the same species (0 = use
            global default)
          </li>
          <li><b>Actions</b>: Custom commands to execute when this species is detected</li>
        </ul>
      </div>

      <!-- Configuration list -->
      <div class="space-y-2">
        <!-- Column headers -->
        {#if Object.keys(settings.config).length > 0}
          <div class="grid grid-cols-12 gap-2 mb-1 text-xs font-medium text-base-content/70">
            <div class="col-span-5 px-2">Species</div>
            <div class="col-span-6 px-2">Settings</div>
            <div class="col-span-1 px-2 text-right">Actions</div>
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
                  label="Threshold"
                  value={editConfigThreshold}
                  onUpdate={value => (editConfigThreshold = value)}
                  min={0}
                  max={1}
                  step={0.01}
                  placeholder="0.5"
                />
              </div>
              <div class="col-span-2">
                <NumberField
                  label="Interval"
                  value={editConfigInterval}
                  onUpdate={value => (editConfigInterval = value)}
                  min={0}
                  max={3600}
                  step={1}
                  placeholder="0"
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
                    Threshold: {config.threshold.toFixed(2)}
                  </span>
                  {#if config.interval > 0}
                    <span class="badge badge-sm badge-secondary">
                      Interval: {config.interval}s
                    </span>
                  {/if}
                  {#if config.actions?.length > 0}
                    <span class="badge badge-sm badge-accent">Custom Action</span>
                  {/if}
                  {#if config.actions?.[0]?.executeDefaults}
                    <span class="badge badge-sm badge-info">+Defaults</span>
                  {/if}
                </div>

                <!-- Actions dropdown -->
                <div class="col-span-1 text-right">
                  <div class="dropdown dropdown-end">
                    <div tabindex="0" role="button" class="btn btn-ghost btn-xs">⋮</div>
                    <ul class="dropdown-content menu bg-base-100 rounded-box z-[1] w-40 p-2 shadow">
                      <li>
                        <button onclick={() => startEditConfig(species)}
                          >{t('common.buttons.edit-config')}</button
                        >
                      </li>
                      <li>
                        <button onclick={() => openActionsModal(species)}
                          >{t('common.buttons.add-action')}</button
                        >
                      </li>
                      <li>
                        <button onclick={() => removeConfig(species)} class="text-error">
                          Remove
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
            No custom species configurations added
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
              placeholder="Species name"
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
              label="Threshold"
              value={newThreshold}
              onUpdate={value => (newThreshold = value)}
              min={0}
              max={1}
              step={0.01}
              placeholder="0.5"
            />
          </div>

          <div class="col-span-2">
            <NumberField
              label="Interval (sec)"
              value={newInterval}
              onUpdate={value => (newInterval = value)}
              min={0}
              max={3600}
              step={1}
              placeholder="0"
            />
          </div>

          <div class="col-span-2">
            <button
              type="button"
              class="btn btn-primary btn-sm w-full"
              onclick={addConfig}
              disabled={!configInputValue.trim() || newThreshold < 0 || newThreshold > 1}
            >
              Add
            </button>
          </div>
        </div>
      </div>
    </div>
  </SettingsSection>
{/if}

<!-- Actions Modal -->
{#if showActionsModal}
  <div class="modal modal-open">
    <div class="modal-box bg-base-100 max-h-[90vh] overflow-y-auto">
      <h3 class="text-lg font-bold mb-4">Actions for {currentSpecies}</h3>

      <div class="space-y-4">
        <SelectField
          label="Action Type"
          bind:value={currentAction.type}
          options={[{ value: 'ExecuteCommand', label: 'Execute Command' }]}
          disabled={true}
          helpText="Currently, only Execute Command actions are supported"
        />

        <TextInput
          label="Command"
          bind:value={currentAction.command}
          placeholder={t('settings.species.commandPathPlaceholder')}
          helpText="Provide the full path to the command or script you want to execute"
        />

        <div class="form-control">
          <label class="label" for="action-parameters">
            <span class="label-text">Parameters</span>
          </label>
          <TextInput
            id="action-parameters"
            bind:value={currentAction.parameters}
            placeholder={t('settings.species.parametersPlaceholder')}
            readonly={true}
            helpText="These values will be passed to your command in the order listed"
          />
        </div>

        <div>
          <div class="font-medium text-sm mb-2">Available Parameters</div>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-xs" onclick={() => addParameter('CommonName')}
              >CommonName</button
            >
            <button type="button" class="btn btn-xs" onclick={() => addParameter('ScientificName')}
              >ScientificName</button
            >
            <button type="button" class="btn btn-xs" onclick={() => addParameter('Confidence')}
              >Confidence</button
            >
            <button type="button" class="btn btn-xs" onclick={() => addParameter('Time')}
              >Time</button
            >
            <button type="button" class="btn btn-xs" onclick={() => addParameter('Source')}
              >Source</button
            >
          </div>
          <div class="mt-2">
            <button type="button" class="btn btn-xs btn-warning" onclick={clearParameters}
              >Clear Parameters</button
            >
          </div>
        </div>

        <Checkbox
          bind:checked={currentAction.executeDefaults}
          label="Also run default actions (database storage, notifications, etc.)"
          helpText="When enabled, both your custom action and the system's default actions will run. When disabled, only your custom action will execute."
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
