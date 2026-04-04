<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import LanguageSelector from '$lib/desktop/components/ui/LanguageSelector.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import LocationPickerMap from '../components/LocationPickerMap.svelte';
  import { settingsActions, settingsStore } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import { MapPin } from '@lucide/svelte';
  import FlagIcon, { type FlagLocale } from '$lib/desktop/components/ui/FlagIcon.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import type { WizardStepProps } from '../types';
  import { getLogger } from '$lib/utils/logger';
  import { toastActions } from '$lib/stores/toast';

  const logger = getLogger('LocationLanguageStep');

  let { onValidChange }: WizardStepProps = $props();

  let latitude = $state(0);
  let longitude = $state(0);
  let speciesLocale = $state('en');
  let localesLoading = $state(true);
  let localesFailed = $state(false);
  let localeOptions = $state<Array<{ value: string; label: string }>>([]);
  let geolocating = $state(false);
  let hasGeolocation = $state(false);
  let dirty = $state(false);

  $effect(() => {
    untrack(() => onValidChange?.(true));
  });

  onMount(() => {
    hasGeolocation = typeof navigator !== 'undefined' && !!navigator.geolocation;

    const store = get(settingsStore);
    if (store?.formData?.birdnet) {
      latitude = store.formData.birdnet.latitude ?? 0;
      longitude = store.formData.birdnet.longitude ?? 0;
      speciesLocale = store.formData.birdnet.locale ?? 'en';
    }

    api
      .get<Record<string, string>>('/api/v2/settings/locales')
      .then(data => {
        localeOptions = Object.entries(data ?? {}).map(([value, label]) => ({
          value,
          label: label as string,
        }));
      })
      .catch(() => {
        localeOptions = [{ value: 'en', label: 'English' }];
        localesFailed = true;
      })
      .finally(() => {
        localesLoading = false;
      });
  });

  function handleLocationChange(lat: number, lon: number) {
    latitude = lat;
    longitude = lon;
    dirty = true;
  }

  function handleGeolocation() {
    if (!hasGeolocation) return;

    if (!window.isSecureContext) {
      toastActions.warning(t('wizard.steps.locationLanguage.geolocationRequiresHttps'));
      return;
    }

    geolocating = true;
    navigator.geolocation.getCurrentPosition(
      position => {
        latitude = Math.round(position.coords.latitude * 1000) / 1000;
        longitude = Math.round(position.coords.longitude * 1000) / 1000;
        geolocating = false;
        dirty = true;
      },
      error => {
        logger.error('Geolocation failed', error);
        geolocating = false;
        if (error.code === error.PERMISSION_DENIED) {
          toastActions.warning(t('wizard.steps.locationLanguage.geolocationDenied'));
        } else {
          toastActions.error(t('wizard.steps.locationLanguage.geolocationFailed'));
        }
      },
      { enableHighAccuracy: true, timeout: 10000 }
    );
  }

  // Save on unmount — only if user made changes
  $effect(() => {
    return () => {
      if (!dirty) return;
      settingsActions.updateSection('birdnet', {
        latitude,
        longitude,
        locale: speciesLocale,
      });
      settingsActions.saveSettings().catch(err => {
        logger.error('Failed to save location/language settings', err);
      });
    };
  });
</script>

<div class="space-y-5">
  <div>
    <label
      for="wizard-ui-language"
      class="mb-1 block text-sm font-medium text-[var(--color-base-content)]"
    >
      {t('wizard.steps.locationLanguage.uiLanguageLabel')}
    </label>
    <p class="mb-2 text-sm text-[var(--color-base-content)] opacity-80">
      {t('wizard.steps.locationLanguage.uiLanguageHelp')}
    </p>
    <LanguageSelector id="wizard-ui-language" />
  </div>

  <div>
    <label
      for="wizard-species-locale"
      class="mb-1 block text-sm font-medium text-[var(--color-base-content)]"
    >
      {t('wizard.steps.locationLanguage.speciesLanguageLabel')}
    </label>
    <p class="mb-2 text-sm text-[var(--color-base-content)] opacity-80">
      {t('wizard.steps.locationLanguage.speciesLanguageHelp')}
    </p>
    {#if localesLoading}
      <div
        class="flex items-center gap-3 rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] px-4 py-3"
      >
        <span
          class="inline-block size-4 animate-spin rounded-full border-2 border-[var(--border-300)] border-t-[var(--color-primary)]"
        ></span>
        <span class="text-sm font-medium text-[var(--color-base-content)] opacity-80"
          >{t('wizard.steps.locationLanguage.localesLoading')}</span
        >
      </div>
    {:else}
      {#if localesFailed}
        <SettingsNote className="mt-0 mb-2">
          <p>{t('wizard.steps.locationLanguage.localesLoadFailed')}</p>
        </SettingsNote>
      {/if}
      <SelectDropdown
        id="wizard-species-locale"
        options={localeOptions}
        value={speciesLocale}
        searchable={true}
        onChange={value => {
          if (typeof value === 'string') {
            speciesLocale = value;
            dirty = true;
          }
        }}
      >
        {#snippet renderOption(option)}
          <div class="flex items-center gap-2">
            <FlagIcon locale={option.value as FlagLocale} className="size-4" />
            <span>{option.label}</span>
          </div>
        {/snippet}
        {#snippet renderSelected(options)}
          {#if options.length > 0}
            <span class="flex items-center gap-2">
              <FlagIcon locale={options[0].value as FlagLocale} className="size-4" />
              <span>{options[0].label}</span>
            </span>
          {/if}
        {/snippet}
      </SelectDropdown>
    {/if}
  </div>

  <div>
    <div class="mb-2">
      <div class="flex items-start justify-between gap-4">
        <span class="block text-sm font-medium text-[var(--color-base-content)]">
          {t('wizard.steps.locationLanguage.locationLabel')}
        </span>
        {#if hasGeolocation}
          <button
            type="button"
            class="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-[var(--radius-field)] border border-[var(--border-200)] bg-transparent px-3 py-1.5 text-xs font-medium text-[var(--color-base-content)] transition-colors hover:bg-[var(--hover-overlay)] disabled:opacity-50"
            onclick={handleGeolocation}
            disabled={geolocating}
          >
            <MapPin class="size-3.5" />
            {t('wizard.steps.locationLanguage.useMyLocation')}
          </button>
        {/if}
      </div>
      <p class="mt-1 text-sm text-[var(--color-base-content)] opacity-80">
        {t('wizard.steps.locationLanguage.locationHelp')}
      </p>
    </div>

    <div class="mb-3 grid grid-cols-2 gap-3">
      <NumberField
        label={t('wizard.steps.locationLanguage.latitudeLabel')}
        value={latitude}
        min={-90}
        max={90}
        step={0.001}
        onUpdate={value => {
          latitude = value;
          dirty = true;
        }}
      />
      <NumberField
        label={t('wizard.steps.locationLanguage.longitudeLabel')}
        value={longitude}
        min={-180}
        max={180}
        step={0.001}
        onUpdate={value => {
          longitude = value;
          dirty = true;
        }}
      />
    </div>

    <LocationPickerMap {latitude} {longitude} onLocationChange={handleLocationChange} />
  </div>
</div>
