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
  import { api, ApiError } from '$lib/utils/api';
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

  // PERFORMANCE OPTIMIZATION: Static UI locales computed once
  const uiLocales = Object.entries(LOCALES).map(([code, info]) => ({
    value: code,
    label: `${info.flag} ${info.name}`,
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
        locale: (store.originalData as any)?.realtime?.dashboard?.locale,
        newUI: (store.originalData as any)?.realtime?.dashboard?.newUI,
      },
      {
        locale: (store.formData as any)?.realtime?.dashboard?.locale,
        newUI: (store.formData as any)?.realtime?.dashboard?.newUI,
      }
    )
  );

  let dashboardDisplayHasChanges = $derived(
    hasSettingsChanged(
      {
        summaryLimit: (store.originalData as any)?.realtime?.dashboard?.summaryLimit,
        thumbnails: (store.originalData as any)?.realtime?.dashboard?.thumbnails,
      },
      {
        summaryLimit: (store.formData as any)?.realtime?.dashboard?.summaryLimit,
        thumbnails: (store.formData as any)?.realtime?.dashboard?.thumbnails,
      }
    )
  );

  // PERFORMANCE OPTIMIZATION: Cache CSRF token with $derived
  // Even though api utility handles CSRF internally, we cache it for:
  // 1. Consistency with other settings pages
  // 2. Future direct API calls that might be needed
  // 3. Avoiding repeated DOM queries
  // eslint-disable-next-line no-unused-vars
  let csrfToken = $derived(
    (document.querySelector('meta[name="csrf-token"]') as HTMLElement)?.getAttribute('content') ||
      ''
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
      providerOptions.data = (providersData?.providers || []).map((provider: any) => ({
        value: provider.value,
        label: provider.display,
      }));
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
  function updateDashboardSetting(key: string, value: any) {
    settingsActions.updateSection('realtime', {
      dashboard: { ...settings.dashboard, [key]: value },
    });
  }

  function updateThumbnailSetting(key: string, value: any) {
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
</script>

<main class="space-y-4 settings-page-content" aria-label="User interface settings configuration">
  <!-- Interface Settings Section -->
  <SettingsSection
    title={t('settings.main.sections.userInterface.interface.title')}
    description={t('settings.main.sections.userInterface.interface.description')}
    defaultOpen={true}
    hasChanges={interfaceSettingsHasChanges}
  >
    <div class="space-y-4">
      <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
        <SelectField
          id="ui-locale"
          value={settings.dashboard.locale}
          label={t('settings.main.sections.userInterface.interface.locale.label')}
          options={uiLocales}
          helpText={t('settings.main.sections.userInterface.interface.locale.helpText')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateUILocale}
        />
      </div>

      <Checkbox
        checked={settings.dashboard.newUI}
        label={t('settings.main.sections.userInterface.interface.newUI.label')}
        helpText={t('settings.main.sections.userInterface.interface.newUI.helpText')}
        disabled={store.isLoading || store.isSaving}
        onchange={value => updateDashboardSetting('newUI', value)}
      />
    </div>
  </SettingsSection>

  <!-- Dashboard Display Settings Section -->
  <SettingsSection
    title={t('settings.main.sections.userInterface.dashboard.title')}
    description={t('settings.main.sections.userInterface.dashboard.description')}
    defaultOpen={true}
    hasChanges={dashboardDisplayHasChanges}
  >
    <div class="space-y-6">
      <!-- Summary Settings -->
      <div>
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
      </div>

      <!-- Thumbnail Settings -->
      <div>
        <h4 class="text-lg font-medium pb-2 mt-6">
          {t('settings.main.sections.userInterface.dashboard.thumbnails.title')}
        </h4>

        <div class="space-y-4">
          <Checkbox
            checked={settings.dashboard.thumbnails.summary}
            label={t('settings.main.sections.userInterface.dashboard.thumbnails.summary.label')}
            helpText={t(
              'settings.main.sections.userInterface.dashboard.thumbnails.summary.helpText'
            )}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateThumbnailSetting('summary', value)}
          />

          <Checkbox
            checked={settings.dashboard.thumbnails.recent}
            label={t('settings.main.sections.userInterface.dashboard.thumbnails.recent.label')}
            helpText={t(
              'settings.main.sections.userInterface.dashboard.thumbnails.recent.helpText'
            )}
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
      </div>

      <!-- Spectrogram Generation Settings -->
      <div>
        <h4 class="text-lg font-medium pb-2 mt-6">
          {t('settings.main.sections.userInterface.dashboard.spectrogram.title')}
        </h4>

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
      </div>
    </div>
  </SettingsSection>
</main>
