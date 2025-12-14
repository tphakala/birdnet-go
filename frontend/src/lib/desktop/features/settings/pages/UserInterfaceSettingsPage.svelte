<!--
  User Interface Settings Page Component
  
  Purpose: Configure all user interface related settings including language,
  dashboard display options, thumbnails, and UI preferences.
  
  Features:
  - Language selection for the user interface
  - Dashboard configuration (summary limits, new UI toggle)
  - Thumbnail display settings for different views
  - Image provider selection and fallback policies
  
  Props: None - This is a page component that uses global settings stores
  
  Performance Optimizations:
  - Cached CSRF token with $derived to avoid repeated DOM queries
  - Reactive computed properties for change detection
  - Async API loading for non-critical data
-->
<script lang="ts">
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectField from '$lib/desktop/components/forms/SelectField.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import FlagIcon, { type FlagLocale } from '$lib/desktop/components/ui/FlagIcon.svelte';
  import {
    settingsStore,
    settingsActions,
    dashboardSettings,
    DEFAULT_SPECTROGRAM_SETTINGS,
  } from '$lib/stores/settings';
  import type { SpectrogramPreRender } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import { api, ApiError } from '$lib/utils/api';
  import { Languages, LayoutDashboard } from '@lucide/svelte';
  import { toastActions } from '$lib/stores/toast';
  import { t, getLocale } from '$lib/i18n';
  import { LOCALES } from '$lib/i18n/config';

  // PERFORMANCE OPTIMIZATION: Reactive settings with proper defaults
  let settings = $derived({
    dashboard: {
      ...($dashboardSettings ?? {
        thumbnails: {
          summary: true,
          recent: true,
          imageProvider: 'wikimedia',
          fallbackPolicy: 'all',
        },
        summaryLimit: 100,
        newUI: false,
      }),
      locale: $dashboardSettings?.locale ?? (getLocale() as string),
      newUI: $dashboardSettings?.newUI ?? false,
      spectrogram: $dashboardSettings?.spectrogram ?? DEFAULT_SPECTROGRAM_SETTINGS,
    },
  });

  let store = $derived($settingsStore);

  // API State Management
  interface ApiState<T> {
    loading: boolean;
    error: string | null;
    data: T;
  }

  // Extended option type for locale with typed locale code
  interface LocaleOption extends SelectOption {
    localeCode: FlagLocale;
  }

  // PERFORMANCE OPTIMIZATION: Static UI locales computed once
  const uiLocales: LocaleOption[] = Object.entries(LOCALES).map(([code, info]) => ({
    value: code,
    label: info.name,
    localeCode: code as FlagLocale,
  }));

  // Image provider options
  let providerOptions = $state<ApiState<Array<{ value: string; label: string }>>>({
    loading: true,
    error: null,
    data: [],
  });
  let multipleProvidersAvailable = $derived(providerOptions.data.length > 1);

  // PERFORMANCE OPTIMIZATION: Reactive change detection with $derived
  // Separate change tracking for each settings section
  let interfaceSettingsHasChanges = $derived(
    hasSettingsChanged(
      {
        locale: store.originalData.realtime?.dashboard?.locale,
        newUI: store.originalData.realtime?.dashboard?.newUI,
      },
      {
        locale: store.formData.realtime?.dashboard?.locale,
        newUI: store.formData.realtime?.dashboard?.newUI,
      }
    )
  );

  // Display settings change detection (summaryLimit)
  let displaySettingsHasChanges = $derived(
    hasSettingsChanged(
      { summaryLimit: store.originalData.realtime?.dashboard?.summaryLimit },
      { summaryLimit: store.formData.realtime?.dashboard?.summaryLimit }
    )
  );

  // Bird images change detection (thumbnails)
  let birdImagesHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.realtime?.dashboard?.thumbnails,
      store.formData.realtime?.dashboard?.thumbnails
    )
  );

  // Spectrogram change detection
  let spectrogramHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.realtime?.dashboard?.spectrogram,
      store.formData.realtime?.dashboard?.spectrogram
    )
  );

  // Combined dashboard tab changes
  let dashboardDisplayHasChanges = $derived(
    displaySettingsHasChanges || birdImagesHasChanges || spectrogramHasChanges
  );

  // Load API data on component mount
  $effect(() => {
    loadImageProviders();
  });

  async function loadImageProviders() {
    providerOptions.loading = true;
    providerOptions.error = null;

    try {
      const providersData = await api.get<{
        providers?: Array<{ value: string; display: string }>;
      }>('/api/v2/settings/imageproviders');

      // Map v2 API response format to client format
      providerOptions.data = (providersData?.providers || []).map(
        (provider: { value: string; display: string }) => ({
          value: provider.value,
          label: provider.display,
        })
      );
    } catch (error) {
      if (error instanceof ApiError) {
        toastActions.warning(t('settings.main.errors.providersLoadFailed'));
      }
      providerOptions.error = t('settings.main.errors.providersLoadFailed');
      // Fallback to basic provider so form still works
      providerOptions.data = [{ value: 'wikipedia', label: 'Wikipedia' }];
    } finally {
      providerOptions.loading = false;
    }
  }

  // Update handlers
  function updateDashboardSetting(key: string, value: string | number | boolean) {
    settingsActions.updateSection('realtime', {
      dashboard: { ...settings.dashboard, [key]: value },
    });
  }

  function updateThumbnailSetting(key: string, value: string | boolean) {
    settingsActions.updateSection('realtime', {
      dashboard: {
        ...settings.dashboard,
        thumbnails: { ...settings.dashboard.thumbnails, [key]: value },
      },
    });
  }

  function updateSpectrogramSetting(key: keyof SpectrogramPreRender, value: boolean | string) {
    settingsActions.updateSection('realtime', {
      dashboard: {
        ...settings.dashboard,
        spectrogram: { ...settings.dashboard.spectrogram, [key]: value },
      },
    });
  }

  function updateUILocale(locale: string) {
    settingsActions.updateSection('realtime', {
      dashboard: { ...settings.dashboard, locale },
    });
  }

  // Storage key for remembering last active tab
  const STORAGE_KEY = 'birdnet-ui-settings-active-tab';

  // Tab state with persistence
  let activeTab = $state(localStorage.getItem(STORAGE_KEY) || 'interface');

  // Persist tab selection
  $effect(() => {
    localStorage.setItem(STORAGE_KEY, activeTab);
  });

  // Tab definitions
  let tabs = $derived<TabDefinition[]>([
    {
      id: 'interface',
      label: t('settings.main.sections.userInterface.interface.title'),
      icon: Languages,
      content: interfaceTabContent,
      hasChanges: interfaceSettingsHasChanges,
    },
    {
      id: 'dashboard',
      label: t('settings.main.sections.userInterface.dashboard.title'),
      icon: LayoutDashboard,
      content: dashboardTabContent,
      hasChanges: dashboardDisplayHasChanges,
    },
  ]);
</script>

<!-- Interface Settings Tab Content -->
{#snippet interfaceTabContent()}
  <SettingsSection
    title={t('settings.main.sections.userInterface.interface.title')}
    description={t('settings.main.sections.userInterface.interface.description')}
    defaultOpen={true}
    originalData={{
      locale: store.originalData.realtime?.dashboard?.locale,
      newUI: store.originalData.realtime?.dashboard?.newUI,
    }}
    currentData={{
      locale: store.formData.realtime?.dashboard?.locale,
      newUI: store.formData.realtime?.dashboard?.newUI,
    }}
  >
    <div class="space-y-4">
      <!-- Modern UI Toggle - Primary setting -->
      <Checkbox
        checked={settings.dashboard.newUI}
        label={t('settings.main.sections.userInterface.interface.newUI.label')}
        helpText={t('settings.main.sections.userInterface.interface.newUI.helpText')}
        disabled={store.isLoading || store.isSaving}
        onchange={value => updateDashboardSetting('newUI', value)}
      />

      <!-- Language Settings - Dependent on Modern UI -->
      <fieldset
        disabled={!settings.dashboard.newUI || store.isLoading || store.isSaving}
        class="contents"
        aria-describedby="locale-section-status"
      >
        <span id="locale-section-status" class="sr-only">
          {settings.dashboard.newUI
            ? t('settings.main.sections.userInterface.interface.locale.label')
            : t('settings.main.sections.userInterface.interface.locale.requiresModernUI')}
        </span>
        <div
          class="space-y-4 transition-opacity duration-200"
          class:opacity-50={!settings.dashboard.newUI}
        >
          <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
            <SelectDropdown
              options={uiLocales}
              value={settings.dashboard.locale}
              label={t('settings.main.sections.userInterface.interface.locale.label')}
              helpText={t('settings.main.sections.userInterface.interface.locale.helpText')}
              disabled={!settings.dashboard.newUI || store.isLoading || store.isSaving}
              variant="select"
              groupBy={false}
              onChange={value => updateUILocale(value as string)}
            >
              {#snippet renderOption(option)}
                {@const localeOption = option as LocaleOption}
                <div class="flex items-center gap-2">
                  <FlagIcon locale={localeOption.localeCode} className="size-4" />
                  <span>{localeOption.label}</span>
                </div>
              {/snippet}
              {#snippet renderSelected(options)}
                {@const localeOption = options[0] as LocaleOption}
                <span class="flex items-center gap-2">
                  <FlagIcon locale={localeOption.localeCode} className="size-4" />
                  <span>{localeOption.label}</span>
                </span>
              {/snippet}
            </SelectDropdown>
          </div>

          {#if !settings.dashboard.newUI}
            <SettingsNote>
              <span>
                {t('settings.main.sections.userInterface.interface.locale.requiresModernUI')}
              </span>
            </SettingsNote>
          {/if}
        </div>
      </fieldset>
    </div>
  </SettingsSection>
{/snippet}

<!-- Dashboard Display Settings Tab Content -->
{#snippet dashboardTabContent()}
  <div class="space-y-6">
    <!-- Card 1: Display Settings -->
    <SettingsSection
      title={t('settings.main.sections.userInterface.dashboard.displaySettings.title')}
      description={t('settings.main.sections.userInterface.dashboard.displaySettings.description')}
      originalData={{
        summaryLimit: store.originalData.realtime?.dashboard?.summaryLimit,
      }}
      currentData={{ summaryLimit: store.formData.realtime?.dashboard?.summaryLimit }}
    >
      <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
        <NumberField
          label={t('settings.main.sections.userInterface.dashboard.summaryLimit.label')}
          value={settings.dashboard.summaryLimit}
          onUpdate={value => updateDashboardSetting('summaryLimit', value)}
          min={10}
          max={1000}
          helpText={t('settings.main.sections.userInterface.dashboard.summaryLimit.helpText')}
          disabled={store.isLoading || store.isSaving}
        />
      </div>
    </SettingsSection>

    <!-- Card 2: Bird Images -->
    <SettingsSection
      title={t('settings.main.sections.userInterface.dashboard.birdImages.title')}
      description={t('settings.main.sections.userInterface.dashboard.birdImages.description')}
      originalData={store.originalData.realtime?.dashboard?.thumbnails}
      currentData={store.formData.realtime?.dashboard?.thumbnails}
    >
      <div class="space-y-4">
        <Checkbox
          checked={settings.dashboard.thumbnails.summary}
          label={t('settings.main.sections.userInterface.dashboard.thumbnails.summary.label')}
          helpText={t('settings.main.sections.userInterface.dashboard.thumbnails.summary.helpText')}
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateThumbnailSetting('summary', value)}
        />

        <Checkbox
          checked={settings.dashboard.thumbnails.recent}
          label={t('settings.main.sections.userInterface.dashboard.thumbnails.recent.label')}
          helpText={t('settings.main.sections.userInterface.dashboard.thumbnails.recent.helpText')}
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateThumbnailSetting('recent', value)}
        />

        <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
          <div class:opacity-50={!multipleProvidersAvailable}>
            <SelectField
              id="image-provider"
              value={settings.dashboard.thumbnails.imageProvider}
              label={t(
                'settings.main.sections.userInterface.dashboard.thumbnails.imageProvider.label'
              )}
              options={providerOptions.data}
              helpText={t(
                'settings.main.sections.userInterface.dashboard.thumbnails.imageProvider.helpText'
              )}
              disabled={store.isLoading ||
                store.isSaving ||
                !multipleProvidersAvailable ||
                providerOptions.loading}
              onchange={value => updateThumbnailSetting('imageProvider', value)}
            />
          </div>

          {#if multipleProvidersAvailable}
            <SelectField
              id="fallback-policy"
              value={settings.dashboard.thumbnails.fallbackPolicy}
              label={t(
                'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.label'
              )}
              options={[
                {
                  value: 'all',
                  label: t(
                    'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.options.all'
                  ),
                },
                {
                  value: 'none',
                  label: t(
                    'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.options.none'
                  ),
                },
              ]}
              helpText={t(
                'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.helpText'
              )}
              disabled={store.isLoading || store.isSaving}
              onchange={value => updateThumbnailSetting('fallbackPolicy', value)}
            />
          {/if}
        </div>
      </div>
    </SettingsSection>

    <!-- Card 3: Spectrogram Generation -->
    <SettingsSection
      title={t('settings.main.sections.userInterface.dashboard.spectrogramGeneration.title')}
      description={t(
        'settings.main.sections.userInterface.dashboard.spectrogramGeneration.description'
      )}
      originalData={store.originalData.realtime?.dashboard?.spectrogram}
      currentData={store.formData.realtime?.dashboard?.spectrogram}
    >
      <div class="space-y-4">
        <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
          <SelectField
            id="spectrogram-mode"
            value={settings.dashboard.spectrogram?.mode ?? 'auto'}
            label={t('settings.main.sections.userInterface.dashboard.spectrogram.mode.label')}
            options={[
              {
                value: 'auto',
                label: t(
                  'settings.main.sections.userInterface.dashboard.spectrogram.mode.auto.label'
                ),
              },
              {
                value: 'prerender',
                label: t(
                  'settings.main.sections.userInterface.dashboard.spectrogram.mode.prerender.label'
                ),
              },
              {
                value: 'user-requested',
                label: t(
                  'settings.main.sections.userInterface.dashboard.spectrogram.mode.userRequested.label'
                ),
              },
            ]}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateSpectrogramSetting('mode', value)}
          />
        </div>

        <!-- Mode-specific notes -->
        {#if (settings.dashboard.spectrogram?.mode ?? 'auto') === 'auto'}
          <SettingsNote>
            <span>
              {t('settings.main.sections.userInterface.dashboard.spectrogram.mode.auto.helpText')}
            </span>
          </SettingsNote>
        {:else if (settings.dashboard.spectrogram?.mode ?? 'auto') === 'prerender'}
          <SettingsNote>
            <span>
              {t(
                'settings.main.sections.userInterface.dashboard.spectrogram.mode.prerender.helpText'
              )}
            </span>
          </SettingsNote>
        {:else if (settings.dashboard.spectrogram?.mode ?? 'auto') === 'user-requested'}
          <SettingsNote>
            <span>
              {t(
                'settings.main.sections.userInterface.dashboard.spectrogram.mode.userRequested.helpText'
              )}
            </span>
          </SettingsNote>
        {/if}
      </div>
    </SettingsSection>
  </div>
{/snippet}

<main class="settings-page-content" aria-label="User interface settings configuration">
  <SettingsTabs {tabs} bind:activeTab />
</main>
