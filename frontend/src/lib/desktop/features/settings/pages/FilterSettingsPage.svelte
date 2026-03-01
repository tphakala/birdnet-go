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
  import SpeciesListEditor from '$lib/desktop/components/forms/SpeciesListEditor.svelte';
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

  // API response interfaces
  interface SpeciesListResponse {
    species?: Array<{ label: string }>;
  }
  import { Filter } from '@lucide/svelte';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.settings;

  // Daylight filter offset slider bounds (hours)
  const DAYLIGHT_OFFSET_MIN = -12;
  const DAYLIGHT_OFFSET_MAX = 12;
  const DAYLIGHT_OFFSET_STEP = 1;

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

  // Species change handlers
  function handleDogBarkSpeciesChange(updatedSpecies: string[]) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      dogBarkFilter: { ...settings.dogBark, species: updatedSpecies },
    });
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

  function handleDaylightSpeciesChange(updatedSpecies: string[]) {
    settingsActions.updateSection('realtime', {
      ...$realtimeSettings,
      daylightFilter: { ...settings.daylight, species: updatedSpecies },
    });
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
            <SpeciesListEditor
              species={settings.dogBark.species}
              disabled={!settings.dogBark.enabled || store.isLoading || store.isSaving}
              predictions={speciesListState.data}
              predictionsLoading={speciesListState.loading}
              listLabel={t('settings.filters.dogBarkSpeciesList')}
              addLabel={t('settings.filters.falsePositivePrevention.addDogBarkSpeciesLabel')}
              addPlaceholder={t('settings.filters.typeSpeciesName')}
              addHelpText={t('settings.filters.falsePositivePrevention.addDogBarkSpeciesHelp')}
              addButtonText={t('settings.filters.falsePositivePrevention.addSpeciesButton')}
              hasChanges={dogBarkFilterHasChanges}
              caseInsensitive={false}
              onSpeciesChange={handleDogBarkSpeciesChange}
            />
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
                min={DAYLIGHT_OFFSET_MIN}
                max={DAYLIGHT_OFFSET_MAX}
                step={DAYLIGHT_OFFSET_STEP}
                disabled={!settings.daylight.enabled || store.isLoading || store.isSaving}
                helpText={t('settings.filters.daylightFilter.offsetHelp')}
              />
            </div>

            <!-- Nocturnal Species List -->
            <SpeciesListEditor
              species={settings.daylight.species}
              disabled={!settings.daylight.enabled || store.isLoading || store.isSaving}
              predictions={speciesListState.data}
              predictionsLoading={speciesListState.loading}
              listLabel={t('settings.filters.daylightFilter.speciesListLabel')}
              addLabel={t('settings.filters.daylightFilter.addSpeciesLabel')}
              addPlaceholder={t('settings.filters.typeSpeciesName')}
              addHelpText={t('settings.filters.daylightFilter.addSpeciesHelp')}
              addButtonText={t('settings.filters.falsePositivePrevention.addSpeciesButton')}
              hasChanges={daylightFilterHasChanges}
              onSpeciesChange={handleDaylightSpeciesChange}
            />
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
