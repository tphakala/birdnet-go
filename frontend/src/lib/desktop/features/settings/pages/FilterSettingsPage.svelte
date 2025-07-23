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
  import { t } from '$lib/i18n';

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
      title={t('settings.filters.privacyFiltering.title')}
      description={t('settings.filters.privacyFiltering.description')}
      defaultOpen={true}
      hasChanges={privacyFilterHasChanges}
    >
      <div class="space-y-4">
        <!-- Enable Privacy Filtering -->
        <Checkbox
          bind:checked={settings.privacy.enabled}
          label={t('settings.filters.privacyFiltering.enable')}
          disabled={store.isLoading || store.isSaving}
          onchange={() => updatePrivacyEnabled(settings.privacy.enabled)}
        />

        {#if settings.privacy.enabled}
          <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
            <!-- Confidence Threshold -->
            <NumberField
              label={t('settings.filters.privacyFiltering.confidenceLabel')}
              value={(settings.privacy as any).confidence}
              onUpdate={updatePrivacyConfidence}
              min={0}
              max={1}
              step={0.01}
              disabled={store.isLoading || store.isSaving}
              helpText={t('settings.filters.privacyFiltering.confidenceHelp')}
            />
          </div>
        {/if}
      </div>
    </SettingsSection>

    <!-- Dog Bark Filter Section -->
    <SettingsSection
      title={t('settings.filters.falsePositivePrevention.title')}
      description={t('settings.filters.falsePositivePrevention.description')}
      defaultOpen={true}
      hasChanges={dogBarkFilterHasChanges}
    >
      <div class="space-y-4">
        <!-- Enable Dog Bark Filter -->
        <Checkbox
          bind:checked={settings.dogBark.enabled}
          label={t('settings.filters.falsePositivePrevention.enableDogBark')}
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateDogBarkEnabled(settings.dogBark.enabled)}
        />

        {#if settings.dogBark.enabled}
          <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
            <!-- Confidence Threshold -->
            <NumberField
              label={t('settings.filters.falsePositivePrevention.confidenceLabel')}
              value={(settings.dogBark as any).confidence}
              onUpdate={updateDogBarkConfidence}
              min={0}
              max={1}
              step={0.01}
              disabled={store.isLoading || store.isSaving}
              helpText={t('settings.filters.falsePositivePrevention.confidenceHelp')}
            />

            <!-- Dog Bark Expire Time -->
            <NumberField
              label={t('settings.filters.falsePositivePrevention.expireTimeLabel')}
              value={(settings.dogBark as any).remember}
              onUpdate={updateDogBarkRemember}
              min={0}
              step={1}
              disabled={store.isLoading || store.isSaving}
              helpText={t('settings.filters.falsePositivePrevention.expireTimeHelp')}
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
                        placeholder={t('settings.filters.speciesNamePlaceholder')}
                      />
                      <button
                        type="button"
                        class="btn btn-sm btn-success"
                        onclick={saveEdit}
                        aria-label={t('common.aria.saveChanges')}
                      >
                        {@html actionIcons.check}
                      </button>
                      <button
                        type="button"
                        class="btn btn-sm btn-ghost"
                        onclick={cancelEdit}
                        aria-label={t('common.aria.cancelEdit')}
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
                        aria-label={t('common.aria.editSpecies')}
                      >
                        {@html actionIcons.edit}
                      </button>
                      <button
                        type="button"
                        class="btn btn-sm btn-error"
                        onclick={() => removeSpecies(index)}
                        disabled={store.isLoading || store.isSaving}
                        aria-label={t('common.aria.removeSpecies')}
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
              label={t('settings.filters.falsePositivePrevention.addDogBarkSpeciesLabel')}
              placeholder={t('settings.filters.typeSpeciesName')}
              helpText={t('settings.filters.falsePositivePrevention.addDogBarkSpeciesHelp')}
              disabled={store.isLoading || store.isSaving}
              predictions={allowedSpecies}
              size="sm"
              buttonText={t('settings.filters.falsePositivePrevention.addSpeciesButton')}
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
                <span>{t('settings.actions.unsavedChanges')}</span>
              </div>
            {/if}
          </div>
        {/if}
      </div>
    </SettingsSection>
  </div>
{/if}
