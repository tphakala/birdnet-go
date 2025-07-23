<script lang="ts">
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import SpeciesInput from '$lib/desktop/components/forms/SpeciesInput.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import {
    settingsStore,
    settingsActions,
    privacyFilterSettings,
    dogBarkFilterSettings,
    realtimeSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import { api, ApiError } from '$lib/utils/api';
  import { toastActions } from '$lib/stores/toast';

  // API response interfaces
  interface SpeciesListResponse {
    species?: Array<{ label: string }>;
  }
  import { navigationIcons, actionIcons, alertIconsSvg } from '$lib/utils/icons'; // Centralized icons - see icons.ts

  let settings = $derived({
    privacy: $privacyFilterSettings || {
      enabled: false,
      confidence: 0.5,
      debug: false,
    },
    dogBark: $dogBarkFilterSettings || {
      enabled: false,
      confidence: 0.5,
      remember: 30,
      debug: false,
      species: [],
    },
  });

  let store = $derived($settingsStore);

  // Track changes for each section separately
  let privacyFilterHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.realtime?.privacyFilter,
      (store.formData as any)?.realtime?.privacyFilter
    )
  );

  let dogBarkFilterHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.realtime?.dogBarkFilter,
      (store.formData as any)?.realtime?.dogBarkFilter
    )
  );

  // Species management state
  let newSpecies = $state('');
  let allowedSpecies = $state<string[]>([]);
  let editIndex = $state<number | null>(null);
  let editSpecies = $state('');

  // Fetch allowed species on mount
  $effect(() => {
    const loadSpeciesList = async () => {
      try {
        const data = await api.get<SpeciesListResponse>('/api/v2/range/species/list');
        if (data?.species && Array.isArray(data.species)) {
          allowedSpecies = data.species.map((species: any) => species.label);
        }
      } catch (error) {
        // Species list loading failure affects form functionality but isn't critical
        // Show minimal feedback rather than intrusive error
        if (error instanceof ApiError && process.env.NODE_ENV === 'development') {
          console.warn('Failed to load species list for filtering:', error.message);
        }
        // Set empty array so form still works, just without suggestions
        allowedSpecies = [];
      }
    };

    loadSpeciesList();
  });

  // Privacy filter update handlers
  function updatePrivacyEnabled(enabled: boolean) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      privacyFilter: { ...(settings.privacy as any), enabled },
    });
  }

  function updatePrivacyConfidence(confidence: number) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      privacyFilter: { ...(settings.privacy as any), confidence },
    });
  }

  // Dog bark filter update handlers
  function updateDogBarkEnabled(enabled: boolean) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...(settings.dogBark as any), enabled },
    });
  }

  function updateDogBarkConfidence(confidence: number) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...(settings.dogBark as any), confidence },
    });
  }

  function updateDogBarkRemember(remember: number) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...(settings.dogBark as any), remember },
    });
  }

  // Species management functions
  function handleSpeciesInput(value: string) {
    newSpecies = value;
  }

  function addSpecies(species: string) {
    if (!species.trim()) return;

    const trimmedSpecies = species.trim();
    if ((settings.dogBark as any).species.includes(trimmedSpecies)) return; // Already exists

    const updatedSpecies = [...(settings.dogBark as any).species, trimmedSpecies];
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...(settings.dogBark as any), species: updatedSpecies },
    });
  }

  function removeSpecies(index: number) {
    const updatedSpecies = (settings.dogBark as any).species.filter(
      (_: string, i: number) => i !== index
    );
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...(settings.dogBark as any), species: updatedSpecies },
    });
  }

  function startEdit(index: number) {
    editIndex = index;
    editSpecies = (settings.dogBark as any).species[index];
  }

  function saveEdit() {
    if (editIndex === null || !editSpecies.trim()) return;

    const updatedSpecies = [...(settings.dogBark as any).species];
    updatedSpecies[editIndex] = editSpecies.trim();

    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...(settings.dogBark as any), species: updatedSpecies },
    });

    cancelEdit();
  }

  function cancelEdit() {
    editIndex = null;
    editSpecies = '';
  }

  function handleEditKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      saveEdit();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      cancelEdit();
    }
  }
</script>

{#if store.isLoading}
  <div class="flex items-center justify-center py-12">
    <div class="loading loading-spinner loading-lg"></div>
  </div>
{:else}
  <div class="space-y-4">
    <!-- Privacy Filter Section -->
    <SettingsSection
      title="Privacy Filtering"
      description="Privacy filtering avoids saving audio clips when human vocals are detected"
      defaultOpen={true}
      hasChanges={privacyFilterHasChanges}
    >
      <div class="space-y-4">
        <!-- Enable Privacy Filtering -->
        <Checkbox
          bind:checked={settings.privacy.enabled}
          label="Enable Privacy Filtering"
          disabled={store.isLoading || store.isSaving}
          onchange={() => updatePrivacyEnabled(settings.privacy.enabled)}
        />

        {#if settings.privacy.enabled}
          <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
            <!-- Confidence Threshold -->
            <NumberField
              label="Confidence Threshold for Human Detection"
              value={(settings.privacy as any).confidence}
              onUpdate={updatePrivacyConfidence}
              min={0}
              max={1}
              step={0.01}
              disabled={store.isLoading || store.isSaving}
              helpText="Set the confidence level for human voice detection, lower value makes filter more sensitive"
            />
          </div>
        {/if}
      </div>
    </SettingsSection>

    <!-- Dog Bark Filter Section -->
    <SettingsSection
      title="False Positive Prevention"
      description="Configure false detection filters"
      defaultOpen={true}
      hasChanges={dogBarkFilterHasChanges}
    >
      <div class="space-y-4">
        <!-- Enable Dog Bark Filter -->
        <Checkbox
          bind:checked={settings.dogBark.enabled}
          label="Enable Dog Bark Filter"
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateDogBarkEnabled(settings.dogBark.enabled)}
        />

        {#if settings.dogBark.enabled}
          <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
            <!-- Confidence Threshold -->
            <NumberField
              label="Confidence Threshold"
              value={(settings.dogBark as any).confidence}
              onUpdate={updateDogBarkConfidence}
              min={0}
              max={1}
              step={0.01}
              disabled={store.isLoading || store.isSaving}
              helpText="Set the confidence level for dog bark detection, lower value makes filter more sensitive"
            />

            <!-- Dog Bark Expire Time -->
            <NumberField
              label="Dog Bark Expire Time (Minutes)"
              value={(settings.dogBark as any).remember}
              onUpdate={updateDogBarkRemember}
              min={0}
              step={1}
              disabled={store.isLoading || store.isSaving}
              helpText="Set how long to remember a detected dog bark"
            />
          </div>

          <!-- Dog Bark Species List -->
          <div class="form-control mt-6">
            <div class="label justify-start">
              <span class="label-text">Dog Bark Species List</span>
            </div>

            <!-- Species List -->
            {#if (settings.dogBark as any).species.length > 0}
              <div class="space-y-2 mb-4">
                {#each (settings.dogBark as any).species as species, index}
                  <div class="flex items-center gap-2 p-3 bg-base-200 rounded-lg">
                    {#if editIndex === index}
                      <input
                        type="text"
                        bind:value={editSpecies}
                        class="input input-sm input-bordered flex-1"
                        onkeydown={handleEditKeydown}
                        placeholder="Species name"
                      />
                      <button
                        type="button"
                        class="btn btn-sm btn-success"
                        onclick={saveEdit}
                        aria-label="Save changes"
                      >
                        {@html actionIcons.check}
                      </button>
                      <button
                        type="button"
                        class="btn btn-sm btn-ghost"
                        onclick={cancelEdit}
                        aria-label="Cancel edit"
                      >
                        {@html navigationIcons.close}
                      </button>
                    {:else}
                      <span class="flex-1 text-sm">{species}</span>
                      <button
                        type="button"
                        class="btn btn-sm btn-ghost"
                        onclick={() => startEdit(index)}
                        disabled={store.isLoading || store.isSaving}
                        aria-label="Edit species"
                      >
                        {@html actionIcons.edit}
                      </button>
                      <button
                        type="button"
                        class="btn btn-sm btn-error"
                        onclick={() => removeSpecies(index)}
                        disabled={store.isLoading || store.isSaving}
                        aria-label="Remove species"
                      >
                        {@html actionIcons.delete}
                      </button>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}

            <!-- Add New Species -->
            <SpeciesInput
              bind:value={newSpecies}
              label="Add Dog Bark Species"
              placeholder="Type species name..."
              helpText="Search and add species that might be confused with dog barks"
              disabled={store.isLoading || store.isSaving}
              predictions={allowedSpecies}
              size="sm"
              buttonText="Add"
              buttonIcon={true}
              onInput={handleSpeciesInput}
              onAdd={addSpecies}
            />

            <!-- Unsaved Changes Indicator -->
            {#if dogBarkFilterHasChanges}
              <div class="mt-2 text-xs text-info flex items-center gap-1">
                <div class="h-4 w-4">
                  {@html alertIconsSvg.info}
                </div>
                <span>Unsaved changes</span>
              </div>
            {/if}
          </div>
        {/if}
      </div>
    </SettingsSection>
  </div>
{/if}
