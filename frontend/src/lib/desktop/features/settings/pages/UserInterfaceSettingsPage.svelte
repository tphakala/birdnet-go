<!--
  User Interface Settings Page

  Purpose: Global appearance and display settings for BirdNET-Go.
  Contains settings moved from MainSettingsPage: color scheme, language,
  thumbnails, spectrograms, temperature unit.

  Sections:
  - Appearance: Color scheme picker, logo style
  - Language & Regional: UI locale, temperature unit
  - Visual Content: Thumbnails, spectrograms

  @component
-->
<script lang="ts">
  import {
    settingsStore,
    settingsActions,
    dashboardSettings,
    DEFAULT_SPECTROGRAM_SETTINGS,
    type SpectrogramPreRender,
    type SpectrogramStyle,
    type SpectrogramDynamicRange,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import ColorSchemePicker from '$lib/desktop/features/settings/components/ColorSchemePicker.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import FlagIcon, { type FlagLocale } from '$lib/desktop/components/ui/FlagIcon.svelte';
  import { t, getLocale } from '$lib/i18n';
  import { LOCALES } from '$lib/i18n/config';
  import { Palette, Globe, Image } from '@lucide/svelte';
  import { api, ApiError } from '$lib/utils/api';
  import { toastActions } from '$lib/stores/toast';

  let activeTab = $state('appearance');
  let store = $derived($settingsStore);

  // --- Locale options (same pattern as MainSettingsPage) ---
  interface LocaleOption extends SelectOption {
    localeCode: FlagLocale;
  }

  const uiLocales: LocaleOption[] = Object.entries(LOCALES).map(([code, info]) => ({
    value: code,
    label: info.name,
    localeCode: code as FlagLocale,
  }));

  // --- Spectrogram style definitions ---
  const SPECTROGRAM_STYLES: { value: SpectrogramStyle; labelKey: string }[] = [
    { value: 'default', labelKey: 'default' },
    { value: 'high_contrast_dark', labelKey: 'highContrastDark' },
    { value: 'scientific_dark', labelKey: 'scientificDark' },
    { value: 'scientific', labelKey: 'scientific' },
  ];

  let spectrogramStyleOptions = $derived.by(() => {
    getLocale();
    return SPECTROGRAM_STYLES.map(style => ({
      value: style.value,
      label: t(
        `settings.main.sections.userInterface.dashboard.spectrogram.style.options.${style.labelKey}`
      ),
    }));
  });

  function getStyleDescriptionKey(style: SpectrogramStyle): string {
    return SPECTROGRAM_STYLES.find(s => s.value === style)?.labelKey ?? 'default';
  }

  // --- Dynamic range presets ---
  const DYNAMIC_RANGE_PRESETS: { value: SpectrogramDynamicRange; labelKey: string }[] = [
    { value: '80', labelKey: 'highContrast' },
    { value: '100', labelKey: 'standard' },
    { value: '120', labelKey: 'extended' },
  ];

  let dynamicRangeOptions = $derived.by(() => {
    getLocale();
    return DYNAMIC_RANGE_PRESETS.map(preset => ({
      value: preset.value,
      label: t(
        `settings.main.sections.userInterface.dashboard.spectrogram.dynamicRange.options.${preset.labelKey}`
      ),
    }));
  });

  function getDynamicRangeDescriptionKey(value: SpectrogramDynamicRange): string {
    return DYNAMIC_RANGE_PRESETS.find(p => p.value === value)?.labelKey ?? 'standard';
  }

  // --- Image provider options (async loaded) ---
  interface ApiState<T> {
    loading: boolean;
    error: string | null;
    data: T;
  }

  let providerOptions = $state<ApiState<Array<{ value: string; label: string }>>>({
    loading: true,
    error: null,
    data: [],
  });
  let multipleProvidersAvailable = $derived(providerOptions.data.length > 1);

  async function loadImageProviders() {
    providerOptions.loading = true;
    providerOptions.error = null;

    try {
      const providersData = await api.get<{
        providers?: Array<{ value: string; display: string }>;
      }>('/api/v2/settings/imageproviders');

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
      providerOptions.data = [{ value: 'wikipedia', label: 'Wikipedia' }];
    } finally {
      providerOptions.loading = false;
    }
  }

  $effect(() => {
    loadImageProviders();
  });

  // --- Derived settings ---
  let settings = $derived({
    dashboard: {
      ...($dashboardSettings ?? {
        thumbnails: {
          summary: true,
          recent: true,
          imageProvider: 'avicommons',
          fallbackPolicy: 'none',
        },
        summaryLimit: 30,
      }),
      locale: $dashboardSettings?.locale ?? (getLocale() as string),
      spectrogram: $dashboardSettings?.spectrogram ?? DEFAULT_SPECTROGRAM_SETTINGS,
    },
  });

  let currentSpectrogramStyle = $derived<SpectrogramStyle>(
    (settings.dashboard.spectrogram?.style as SpectrogramStyle) ?? 'default'
  );

  let currentDynamicRange = $derived<SpectrogramDynamicRange>(
    (settings.dashboard.spectrogram?.dynamicRange as SpectrogramDynamicRange) ?? '100'
  );

  // --- Change detection ---
  let appearanceHasChanges = $derived(
    hasSettingsChanged(
      {
        colorScheme: store.originalData.realtime?.dashboard?.colorScheme,
        customColors: store.originalData.realtime?.dashboard?.customColors,
        logoStyle: store.originalData.realtime?.dashboard?.logoStyle,
      },
      {
        colorScheme: store.formData.realtime?.dashboard?.colorScheme,
        customColors: store.formData.realtime?.dashboard?.customColors,
        logoStyle: store.formData.realtime?.dashboard?.logoStyle,
      }
    )
  );

  let languageHasChanges = $derived(
    hasSettingsChanged(
      {
        locale: store.originalData.realtime?.dashboard?.locale,
        temperatureUnit: store.originalData.realtime?.dashboard?.temperatureUnit,
      },
      {
        locale: store.formData.realtime?.dashboard?.locale,
        temperatureUnit: store.formData.realtime?.dashboard?.temperatureUnit,
      }
    )
  );

  let visualContentHasChanges = $derived(
    hasSettingsChanged(
      {
        thumbnails: store.originalData.realtime?.dashboard?.thumbnails,
        spectrogram: store.originalData.realtime?.dashboard?.spectrogram,
      },
      {
        thumbnails: store.formData.realtime?.dashboard?.thumbnails,
        spectrogram: store.formData.realtime?.dashboard?.spectrogram,
      }
    )
  );

  // --- Update handlers ---
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

  // --- Tab definitions ---
  let tabs: TabDefinition[] = $derived([
    {
      id: 'appearance',
      label: t('settings.userInterface.tabs.appearance'),
      icon: Palette,
      hasChanges: appearanceHasChanges,
      content: appearanceTabContent,
    },
    {
      id: 'language',
      label: t('settings.userInterface.tabs.language'),
      icon: Globe,
      hasChanges: languageHasChanges,
      content: languageTabContent,
    },
    {
      id: 'visualContent',
      label: t('settings.userInterface.tabs.visualContent'),
      icon: Image,
      hasChanges: visualContentHasChanges,
      content: visualContentTabContent,
    },
  ]);
</script>

<!-- Tab Content Snippets -->

{#snippet appearanceTabContent()}
  <div class="space-y-6">
    <SettingsSection
      title={t('settings.userInterface.appearance.title')}
      description={t('settings.userInterface.appearance.description')}
      originalData={{
        colorScheme: store.originalData.realtime?.dashboard?.colorScheme,
        customColors: store.originalData.realtime?.dashboard?.customColors,
        logoStyle: store.originalData.realtime?.dashboard?.logoStyle,
      }}
      currentData={{
        colorScheme: store.formData.realtime?.dashboard?.colorScheme,
        customColors: store.formData.realtime?.dashboard?.customColors,
        logoStyle: store.formData.realtime?.dashboard?.logoStyle,
      }}
    >
      <ColorSchemePicker disabled={store.isLoading || store.isSaving} />
    </SettingsSection>
  </div>
{/snippet}

{#snippet languageTabContent()}
  <div class="space-y-6">
    <SettingsSection
      title={t('settings.userInterface.language.title')}
      description={t('settings.userInterface.language.description')}
      originalData={{
        locale: store.originalData.realtime?.dashboard?.locale,
        temperatureUnit: store.originalData.realtime?.dashboard?.temperatureUnit,
      }}
      currentData={{
        locale: store.formData.realtime?.dashboard?.locale,
        temperatureUnit: store.formData.realtime?.dashboard?.temperatureUnit,
      }}
    >
      <div class="space-y-6">
        <!-- Language selector -->
        <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
          <SelectDropdown
            options={uiLocales}
            value={settings.dashboard.locale}
            label={t('settings.main.sections.userInterface.interface.locale.label')}
            helpText={t('settings.main.sections.userInterface.interface.locale.helpText')}
            disabled={store.isLoading || store.isSaving}
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

        <!-- Temperature unit -->
        <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6">
          <SelectDropdown
            options={[
              {
                value: 'celsius',
                label: t('settings.integration.weather.temperatureUnit.options.celsius'),
              },
              {
                value: 'fahrenheit',
                label: t('settings.integration.weather.temperatureUnit.options.fahrenheit'),
              },
            ]}
            value={settings.dashboard.temperatureUnit || 'celsius'}
            label={t('settings.integration.weather.temperatureUnit.label')}
            helpText={t('settings.integration.weather.temperatureUnit.helpText')}
            disabled={store.isLoading || store.isSaving}
            variant="select"
            groupBy={false}
            onChange={value => updateDashboardSetting('temperatureUnit', value as string)}
          />
        </div>
      </div>
    </SettingsSection>
  </div>
{/snippet}

{#snippet visualContentTabContent()}
  <div class="space-y-6">
    <SettingsSection
      title={t('settings.userInterface.visualContent.title')}
      description={t('settings.userInterface.visualContent.description')}
      originalData={{
        thumbnails: store.originalData.realtime?.dashboard?.thumbnails,
        spectrogram: store.originalData.realtime?.dashboard?.spectrogram,
      }}
      currentData={{
        thumbnails: store.formData.realtime?.dashboard?.thumbnails,
        spectrogram: store.formData.realtime?.dashboard?.spectrogram,
      }}
    >
      <div class="space-y-6">
        <!-- Bird Images Section -->
        <div class="space-y-4">
          <h4 class="text-sm font-medium text-[var(--color-base-content)]/70">
            {t('settings.main.sections.userInterface.dashboard.birdImages.title')}
          </h4>

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
              <SelectDropdown
                options={providerOptions.data}
                value={settings.dashboard.thumbnails.imageProvider}
                label={t(
                  'settings.main.sections.userInterface.dashboard.thumbnails.imageProvider.label'
                )}
                helpText={t(
                  'settings.main.sections.userInterface.dashboard.thumbnails.imageProvider.helpText'
                )}
                disabled={store.isLoading ||
                  store.isSaving ||
                  !multipleProvidersAvailable ||
                  providerOptions.loading}
                variant="select"
                groupBy={false}
                menuSize="sm"
                onChange={value => updateThumbnailSetting('imageProvider', value as string)}
              />
            </div>

            {#if multipleProvidersAvailable}
              <SelectDropdown
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
                value={settings.dashboard.thumbnails.fallbackPolicy}
                label={t(
                  'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.label'
                )}
                helpText={t(
                  'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.helpText'
                )}
                disabled={store.isLoading || store.isSaving}
                variant="select"
                groupBy={false}
                menuSize="sm"
                onChange={value => updateThumbnailSetting('fallbackPolicy', value as string)}
              />
            {/if}
          </div>
        </div>

        <!-- Divider -->
        <div class="border-t border-[var(--color-base-200)]"></div>

        <!-- Spectrograms Section -->
        <div class="space-y-4">
          <h4 class="text-sm font-medium text-[var(--color-base-content)]/70">
            {t('settings.main.sections.userInterface.dashboard.spectrogram.title')}
          </h4>

          <!-- Generation Mode with contextual note -->
          <div class="space-y-3">
            <SelectDropdown
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
              value={settings.dashboard.spectrogram?.mode ?? 'auto'}
              label={t('settings.main.sections.userInterface.dashboard.spectrogram.mode.label')}
              disabled={store.isLoading || store.isSaving}
              variant="select"
              groupBy={false}
              menuSize="sm"
              onChange={value => updateSpectrogramSetting('mode', value as string)}
            />

            {#if (settings.dashboard.spectrogram?.mode ?? 'auto') === 'auto'}
              <SettingsNote>
                <span>
                  {t(
                    'settings.main.sections.userInterface.dashboard.spectrogram.mode.auto.helpText'
                  )}
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

          <!-- Style Selection as visual cards -->
          <div class="mt-6">
            <span class="text-sm font-medium">
              {t('settings.main.sections.userInterface.dashboard.spectrogram.style.label')}
            </span>
            <p class="text-xs text-[var(--color-base-content)]/60 mt-1">
              {t('settings.main.sections.userInterface.dashboard.spectrogram.style.helpText')}
            </p>

            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 mt-4">
              {#each spectrogramStyleOptions as style (style.value)}
                {@const isSelected = currentSpectrogramStyle === style.value}
                <button
                  type="button"
                  class="group relative flex flex-col items-center p-2 rounded-lg border transition-all focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]/50 {isSelected
                    ? 'border-[var(--color-primary)]/60 bg-[var(--color-primary)]/10 shadow-[0_0_0_1px_rgba(37,99,235,0.3)]'
                    : 'border-[var(--border-200)] bg-[var(--color-base-100)] hover:border-[var(--color-base-content)]/30'}"
                  disabled={store.isLoading || store.isSaving}
                  onclick={() => updateSpectrogramSetting('style', style.value)}
                >
                  <img
                    src={`/ui/assets/images/spectrogram-preview-${style.value}.png`}
                    alt={style.label}
                    class="w-full aspect-[4/3] object-cover rounded"
                  />
                  <span
                    class="mt-2 text-xs leading-tight text-center {isSelected
                      ? 'text-[var(--color-primary)] font-medium'
                      : 'text-[var(--color-base-content)]/70'}"
                  >
                    {style.label}
                  </span>
                </button>
              {/each}
            </div>

            <p class="text-sm text-[var(--color-base-content)]/60 italic mt-4">
              {t(
                `settings.main.sections.userInterface.dashboard.spectrogram.style.descriptions.${getStyleDescriptionKey(currentSpectrogramStyle)}`
              )}
            </p>
          </div>

          <!-- Dynamic Range Selection -->
          <div class="mt-6 space-y-3">
            <SelectDropdown
              options={dynamicRangeOptions}
              value={currentDynamicRange}
              label={t(
                'settings.main.sections.userInterface.dashboard.spectrogram.dynamicRange.label'
              )}
              disabled={store.isLoading || store.isSaving}
              variant="select"
              groupBy={false}
              menuSize="sm"
              onChange={value => updateSpectrogramSetting('dynamicRange', value as string)}
            />

            <SettingsNote>
              <span>
                {t(
                  `settings.main.sections.userInterface.dashboard.spectrogram.dynamicRange.descriptions.${getDynamicRangeDescriptionKey(currentDynamicRange)}`
                )}
              </span>
            </SettingsNote>
          </div>
        </div>
      </div>
    </SettingsSection>
  </div>
{/snippet}

<SettingsTabs {tabs} bind:activeTab />
