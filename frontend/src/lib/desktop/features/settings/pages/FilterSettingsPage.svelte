<!--
  Filter Settings Page Component
  
  Purpose: Configure filtering settings for BirdNET-Go including privacy filters and 
  false positive prevention (dog bark filter) with species-specific rules.
  
  Features:
  - Privacy filter configuration with confidence threshold
  - False positive prevention (dog bark filter) with species management
  - Dynamic species list with add/edit/delete functionality
  - Confidence threshold and retention time settings
  - Real-time validation and change detection
  
  Props: None - This is a page component that uses global settings stores
  
  Performance Optimizations:
  - Removed page-level loading spinner to prevent flickering
  - Async API loading for species list with proper state management
  - Reactive change detection with $derived
  - Error handling without disrupting UI functionality
  
  @component
-->
<script lang="ts">
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import SpeciesInput from '$lib/desktop/components/forms/SpeciesInput.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import {
    settingsStore,
    settingsActions,
    privacyFilterSettings,
    dogBarkFilterSettings,
    daylightFilterSettings,
    realtimeSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import { api, ApiError } from '$lib/utils/api';
  import { t } from '$lib/i18n';
  import { safeArrayAccess } from '$lib/utils/security';

  // API response interfaces
  interface SpeciesListResponse {
    species?: Array<{ label: string }>;
  }
  import { X, Check, SquarePen, Trash2, Info, Filter } from '@lucide/svelte';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.settings;

  // PERFORMANCE OPTIMIZATION: Reactive settings with proper defaults
  let settings = $derived(
    (() => {
      const privacyBase = $privacyFilterSettings || {
        enabled: false,
        confidence: 0.5,
        debug: false,
      };

      const dogBarkBase = $dogBarkFilterSettings || {
        enabled: false,
        confidence: 0.5,
        remember: 30,
        debug: false,
        species: [],
      };

      const daylightBase = $daylightFilterSettings || {
        enabled: false,
        debug: false,
        offset: 0,
        species: [],
      };

      // Ensure species is always an array even if dogBarkFilterSettings exists but has undefined/null species
      return {
        privacy: privacyBase,
        dogBark: {
          ...dogBarkBase,
          species: dogBarkBase.species ?? [], // Always ensures species is an array
        },
        daylight: {
          ...daylightBase,
          species: daylightBase.species ?? [],
        },
      };
    })()
  );

  let store = $derived($settingsStore);

  // PERFORMANCE OPTIMIZATION: Reactive change detection with $derived
  let privacyFilterHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.realtime?.privacyFilter,
      store.formData.realtime?.privacyFilter
    )
  );

  let dogBarkFilterHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.realtime?.dogBarkFilter,
      store.formData.realtime?.dogBarkFilter
    )
  );

  let daylightFilterHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.realtime?.daylightFilter,
      store.formData.realtime?.daylightFilter
    )
  );

  // Tab state
  let activeTab = $state('filters');

  // Tab definitions
  let tabs = $derived<TabDefinition[]>([
    {
      id: 'filters',
      label: t('settings.filters.title'),
      icon: Filter,
      content: filtersTabContent,
      hasChanges: privacyFilterHasChanges || dogBarkFilterHasChanges || daylightFilterHasChanges,
    },
  ]);

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

  // Species management state (Dog Bark)
  let newSpecies = $state('');
  let editIndex = $state<number | null>(null);
  let editSpecies = $state('');

  // Species management state (Daylight)
  let daylightNewSpecies = $state('');
  let daylightEditIndex = $state<number | null>(null);
  let daylightEditSpecies = $state('');

  // PERFORMANCE OPTIMIZATION: Load species list with proper state management
  $effect(() => {
    loadSpeciesList();
  });

  async function loadSpeciesList() {
    speciesListState.loading = true;
    speciesListState.error = null;

    try {
      const data = await api.get<SpeciesListResponse>('/api/v2/range/species/list');
      if (data?.species && Array.isArray(data.species)) {
        speciesListState.data = data.species.map(
          (species: { label: string; commonName?: string }) => species.commonName || species.label
        );
      } else {
        speciesListState.data = [];
      }
    } catch (error) {
      // Species list loading failure affects form functionality but isn't critical
      // Show minimal feedback rather than intrusive error
      if (error instanceof ApiError) {
        logger.warn('Failed to load species list for filtering', error, {
          component: 'FilterSettingsPage',
          action: 'loadSpeciesList',
        });
      }
      speciesListState.error = t('settings.filters.errors.speciesLoadFailed');
      // Set empty array so form still works, just without suggestions
      speciesListState.data = [];
    } finally {
      speciesListState.loading = false;
    }
  }

  // Privacy filter update handlers
  function updatePrivacyEnabled(enabled: boolean) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      privacyFilter: { ...settings.privacy, enabled },
    });
  }

  function updatePrivacyConfidence(confidence: number) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      privacyFilter: { ...settings.privacy, confidence },
    });
  }

  // Dog bark filter update handlers
  function updateDogBarkEnabled(enabled: boolean) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...settings.dogBark, enabled },
    });
  }

  function updateDogBarkConfidence(confidence: number) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...settings.dogBark, confidence },
    });
  }

  function updateDogBarkRemember(remember: number) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...settings.dogBark, remember },
    });
  }

  // Species management functions
  function handleSpeciesInput(value: string) {
    newSpecies = value;
  }

  function addSpecies(species: string) {
    if (!species.trim()) return;

    const trimmedSpecies = species.trim();
    if (settings.dogBark.species.includes(trimmedSpecies)) return; // Already exists

    const updatedSpecies = [...settings.dogBark.species, trimmedSpecies];
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...settings.dogBark, species: updatedSpecies },
    });
  }

  function removeSpecies(index: number) {
    const updatedSpecies = settings.dogBark.species.filter((_: string, i: number) => i !== index);
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...settings.dogBark, species: updatedSpecies },
    });
  }

  function startEdit(index: number) {
    editIndex = index;
    editSpecies = safeArrayAccess(settings.dogBark.species, index) || '';
  }

  function saveEdit() {
    if (editIndex === null || !editSpecies.trim()) return;

    const updatedSpecies = [...settings.dogBark.species];
    if (editIndex >= 0 && editIndex < updatedSpecies.length) {
      updatedSpecies.splice(editIndex, 1, editSpecies.trim());
    }

    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...settings.dogBark, species: updatedSpecies },
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

  // Daylight filter update handlers
  function updateDaylightEnabled(enabled: boolean) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      daylightFilter: { ...settings.daylight, enabled },
    });
  }

  function updateDaylightOffset(offset: number) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      daylightFilter: { ...settings.daylight, offset },
    });
  }

  // Daylight species management functions
  function handleDaylightSpeciesInput(value: string) {
    daylightNewSpecies = value;
  }

  function addDaylightSpecies(species: string) {
    if (!species.trim()) return;

    const trimmedSpecies = species.trim();
    if (
      settings.daylight.species.some(
        (s: string) => s.toLowerCase() === trimmedSpecies.toLowerCase()
      )
    )
      return;

    const updatedSpecies = [...settings.daylight.species, trimmedSpecies];
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      daylightFilter: { ...settings.daylight, species: updatedSpecies },
    });
  }

  function removeDaylightSpecies(index: number) {
    const updatedSpecies = settings.daylight.species.filter((_: string, i: number) => i !== index);
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      daylightFilter: { ...settings.daylight, species: updatedSpecies },
    });
  }

  function startDaylightEdit(index: number) {
    daylightEditIndex = index;
    daylightEditSpecies = safeArrayAccess(settings.daylight.species, index) || '';
  }

  function saveDaylightEdit() {
    if (daylightEditIndex === null || !daylightEditSpecies.trim()) return;

    const trimmedSpecies = daylightEditSpecies.trim();
    const isDuplicate = settings.daylight.species.some(
      (s: string, i: number) =>
        i !== daylightEditIndex && s.toLowerCase() === trimmedSpecies.toLowerCase()
    );
    if (isDuplicate) return;

    const updatedSpecies = [...settings.daylight.species];
    if (daylightEditIndex >= 0 && daylightEditIndex < updatedSpecies.length) {
      updatedSpecies.splice(daylightEditIndex, 1, trimmedSpecies);
    }

    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      daylightFilter: { ...settings.daylight, species: updatedSpecies },
    });

    cancelDaylightEdit();
  }

  function cancelDaylightEdit() {
    daylightEditIndex = null;
    daylightEditSpecies = '';
  }

  function handleDaylightEditKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      saveDaylightEdit();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      cancelDaylightEdit();
    }
  }
</script>

{#snippet filtersTabContent()}
  <div class="space-y-6">
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
          checked={settings.privacy.enabled}
          label={t('settings.filters.privacyFiltering.enable')}
          disabled={store.isLoading || store.isSaving}
          onchange={enabled => updatePrivacyEnabled(enabled)}
        />

        <!-- Fieldset for accessible disabled state - all inputs greyed out when feature disabled -->
        <fieldset
          disabled={!settings.privacy.enabled || store.isLoading || store.isSaving}
          class="contents"
          aria-describedby="privacy-filter-status"
        >
          <span id="privacy-filter-status" class="sr-only">
            {settings.privacy.enabled
              ? t('settings.filters.privacyFiltering.enable')
              : t('settings.filters.privacyFiltering.disabled')}
          </span>
          <div class="transition-opacity duration-200" class:opacity-50={!settings.privacy.enabled}>
            <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
              <!-- Confidence Threshold -->
              <NumberField
                label={t('settings.filters.privacyFiltering.confidenceLabel')}
                value={settings.privacy.confidence}
                onUpdate={updatePrivacyConfidence}
                min={0}
                max={1}
                step={0.01}
                disabled={!settings.privacy.enabled || store.isLoading || store.isSaving}
                helpText={t('settings.filters.privacyFiltering.confidenceHelp')}
              />
            </div>
          </div>
        </fieldset>
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
          checked={settings.dogBark.enabled}
          label={t('settings.filters.falsePositivePrevention.enableDogBark')}
          disabled={store.isLoading || store.isSaving}
          onchange={enabled => updateDogBarkEnabled(enabled)}
        />

        <!-- Fieldset for accessible disabled state - all inputs greyed out when feature disabled -->
        <fieldset
          disabled={!settings.dogBark.enabled || store.isLoading || store.isSaving}
          class="contents"
          aria-describedby="dogbark-filter-status"
        >
          <span id="dogbark-filter-status" class="sr-only">
            {settings.dogBark.enabled
              ? t('settings.filters.falsePositivePrevention.enableDogBark')
              : t('settings.filters.falsePositivePrevention.disabled')}
          </span>
          <div
            class="space-y-4 transition-opacity duration-200"
            class:opacity-50={!settings.dogBark.enabled}
          >
            <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
              <!-- Confidence Threshold -->
              <NumberField
                label={t('settings.filters.falsePositivePrevention.confidenceLabel')}
                value={settings.dogBark.confidence}
                onUpdate={updateDogBarkConfidence}
                min={0}
                max={1}
                step={0.01}
                disabled={!settings.dogBark.enabled || store.isLoading || store.isSaving}
                helpText={t('settings.filters.falsePositivePrevention.confidenceHelp')}
              />

              <!-- Dog Bark Expire Time -->
              <NumberField
                label={t('settings.filters.falsePositivePrevention.expireTimeLabel')}
                value={settings.dogBark.remember}
                onUpdate={updateDogBarkRemember}
                min={0}
                step={1}
                disabled={!settings.dogBark.enabled || store.isLoading || store.isSaving}
                helpText={t('settings.filters.falsePositivePrevention.expireTimeHelp')}
              />
            </div>

            <!-- Dog Bark Species List -->
            <div class="mt-6">
              <div class="flex justify-start mb-1">
                <span class="text-sm text-[var(--color-base-content)]"
                  >{t('settings.filters.dogBarkSpeciesList')}</span
                >
              </div>

              <!-- Species List -->
              {#if settings.dogBark.species.length > 0}
                <div class="space-y-2 mb-4">
                  {#each settings.dogBark.species as species, index (species)}
                    <div class="flex items-center gap-2 p-3 bg-[var(--color-base-200)] rounded-lg">
                      {#if editIndex === index}
                        <input
                          type="text"
                          bind:value={editSpecies}
                          class="flex-1 h-8 px-3 text-sm bg-[var(--color-base-100)] border border-[var(--border-200)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
                          onkeydown={handleEditKeydown}
                          placeholder={t('settings.filters.speciesNamePlaceholder')}
                        />
                        <button
                          type="button"
                          class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-success)] text-[var(--color-success-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-success)] focus-visible:ring-offset-2 transition-colors"
                          onclick={saveEdit}
                          aria-label={t('common.aria.saveChanges')}
                        >
                          <Check class="size-4" />
                        </button>
                        <button
                          type="button"
                          class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-black/5 dark:hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-base-content)] focus-visible:ring-offset-2 transition-colors"
                          onclick={cancelEdit}
                          aria-label={t('common.aria.cancelEdit')}
                        >
                          <X class="size-4" />
                        </button>
                      {:else}
                        <span class="flex-1 text-sm">{species}</span>
                        <button
                          type="button"
                          class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-black/5 dark:hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-base-content)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                          onclick={() => startEdit(index)}
                          disabled={!settings.dogBark.enabled || store.isLoading || store.isSaving}
                          aria-label={t('common.aria.editSpecies')}
                        >
                          <SquarePen class="size-4" />
                        </button>
                        <button
                          type="button"
                          class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-error)] text-[var(--color-error-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-error)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                          onclick={() => removeSpecies(index)}
                          disabled={!settings.dogBark.enabled || store.isLoading || store.isSaving}
                          aria-label={t('common.aria.removeSpecies')}
                        >
                          <Trash2 class="size-4" />
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
                disabled={!settings.dogBark.enabled ||
                  store.isLoading ||
                  store.isSaving ||
                  speciesListState.loading}
                predictions={speciesListState.data}
                size="sm"
                buttonText={t('settings.filters.falsePositivePrevention.addSpeciesButton')}
                buttonIcon={true}
                onInput={handleSpeciesInput}
                onAdd={addSpecies}
              />

              <!-- Unsaved Changes Indicator -->
              {#if dogBarkFilterHasChanges}
                <div class="mt-2 text-xs text-[var(--color-info)] flex items-center gap-1">
                  <Info class="size-4" />
                  <span>{t('settings.actions.unsavedChanges')}</span>
                </div>
              {/if}
            </div>
          </div>
        </fieldset>
      </div>
    </SettingsSection>

    <!-- Daylight Filter Section -->
    <SettingsSection
      title={t('settings.filters.daylightFilter.title')}
      description={t('settings.filters.daylightFilter.description')}
      defaultOpen={true}
      hasChanges={daylightFilterHasChanges}
    >
      <div class="space-y-4">
        <!-- Enable Daylight Filter -->
        <Checkbox
          checked={settings.daylight.enabled}
          label={t('settings.filters.daylightFilter.enable')}
          disabled={store.isLoading || store.isSaving}
          onchange={enabled => updateDaylightEnabled(enabled)}
        />

        <!-- Fieldset for accessible disabled state - all inputs greyed out when feature disabled -->
        <fieldset
          disabled={!settings.daylight.enabled || store.isLoading || store.isSaving}
          class="contents"
          aria-describedby="daylight-filter-status"
        >
          <span id="daylight-filter-status" class="sr-only">
            {settings.daylight.enabled
              ? t('settings.filters.daylightFilter.enable')
              : t('settings.filters.daylightFilter.disabled')}
          </span>
          <div
            class="space-y-4 transition-opacity duration-200"
            class:opacity-50={!settings.daylight.enabled}
          >
            <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
              <!-- Daylight Window Offset -->
              <NumberField
                label={t('settings.filters.daylightFilter.offsetLabel')}
                value={settings.daylight.offset}
                onUpdate={updateDaylightOffset}
                min={-12}
                max={12}
                step={1}
                disabled={!settings.daylight.enabled || store.isLoading || store.isSaving}
                helpText={t('settings.filters.daylightFilter.offsetHelp')}
              />
            </div>

            <!-- Nocturnal Species List -->
            <div class="mt-6">
              <div class="flex justify-start mb-1">
                <span class="text-sm text-[var(--color-base-content)]"
                  >{t('settings.filters.daylightFilter.speciesListLabel')}</span
                >
              </div>

              <!-- Species List -->
              {#if settings.daylight.species.length > 0}
                <div class="space-y-2 mb-4">
                  {#each settings.daylight.species as species, index (species)}
                    <div class="flex items-center gap-2 p-3 bg-[var(--color-base-200)] rounded-lg">
                      {#if daylightEditIndex === index}
                        <input
                          type="text"
                          bind:value={daylightEditSpecies}
                          class="flex-1 h-8 px-3 text-sm bg-[var(--color-base-100)] border border-[var(--border-200)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
                          onkeydown={handleDaylightEditKeydown}
                          placeholder={t('settings.filters.speciesNamePlaceholder')}
                        />
                        <button
                          type="button"
                          class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-success)] text-[var(--color-success-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-success)] focus-visible:ring-offset-2 transition-colors"
                          onclick={saveDaylightEdit}
                          aria-label={t('common.aria.saveChanges')}
                        >
                          <Check class="size-4" />
                        </button>
                        <button
                          type="button"
                          class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-black/5 dark:hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-base-content)] focus-visible:ring-offset-2 transition-colors"
                          onclick={cancelDaylightEdit}
                          aria-label={t('common.aria.cancelEdit')}
                        >
                          <X class="size-4" />
                        </button>
                      {:else}
                        <span class="flex-1 text-sm">{species}</span>
                        <button
                          type="button"
                          class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-black/5 dark:hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-base-content)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                          onclick={() => startDaylightEdit(index)}
                          disabled={!settings.daylight.enabled || store.isLoading || store.isSaving}
                          aria-label={t('common.aria.editSpecies')}
                        >
                          <SquarePen class="size-4" />
                        </button>
                        <button
                          type="button"
                          class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-error)] text-[var(--color-error-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-error)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                          onclick={() => removeDaylightSpecies(index)}
                          disabled={!settings.daylight.enabled || store.isLoading || store.isSaving}
                          aria-label={t('common.aria.removeSpecies')}
                        >
                          <Trash2 class="size-4" />
                        </button>
                      {/if}
                    </div>
                  {/each}
                </div>
              {/if}

              <!-- Add New Species -->
              <SpeciesInput
                bind:value={daylightNewSpecies}
                label={t('settings.filters.daylightFilter.addSpeciesLabel')}
                placeholder={t('settings.filters.typeSpeciesName')}
                helpText={t('settings.filters.daylightFilter.addSpeciesHelp')}
                disabled={!settings.daylight.enabled ||
                  store.isLoading ||
                  store.isSaving ||
                  speciesListState.loading}
                predictions={speciesListState.data}
                size="sm"
                buttonText={t('settings.filters.falsePositivePrevention.addSpeciesButton')}
                buttonIcon={true}
                onInput={handleDaylightSpeciesInput}
                onAdd={addDaylightSpecies}
              />

              <!-- Unsaved Changes Indicator -->
              {#if daylightFilterHasChanges}
                <div class="mt-2 text-xs text-[var(--color-info)] flex items-center gap-1">
                  <Info class="size-4" />
                  <span>{t('settings.actions.unsavedChanges')}</span>
                </div>
              {/if}
            </div>
          </div>
        </fieldset>
      </div>
    </SettingsSection>
  </div>
{/snippet}

<!-- Main Content -->
<main class="settings-page-content" aria-label="Filter settings configuration">
  <SettingsTabs {tabs} bind:activeTab />
</main>
