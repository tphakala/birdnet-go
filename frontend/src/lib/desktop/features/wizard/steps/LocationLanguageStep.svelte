<script lang="ts">
  import { onDestroy, onMount, untrack } from 'svelte';
  import { t, getLocale, setLocale } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import LanguageSelector from '$lib/desktop/components/ui/LanguageSelector.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import LocationMap from '$lib/desktop/components/forms/LocationMap.svelte';
  import { settingsActions, settingsStore } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import FlagIcon, { type FlagLocale } from '$lib/desktop/components/ui/FlagIcon.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import CurrentLocationButton from '$lib/desktop/features/settings/components/CurrentLocationButton.svelte';
  import type { WizardStepProps } from '../types';
  import { generateId } from '$lib/utils/uuid';

  const UI_LANGUAGE_HELP_ID = generateId('wizard-ui-language-help');
  const SPECIES_LANGUAGE_HELP_ID = generateId('wizard-species-language-help');

  let { onValidChange, registerLeaveHandler }: WizardStepProps = $props();

  // Read at init (not in onMount) so the map is created with the stored
  // coordinates and never shows a 0,0 first frame.
  const storedBirdnet = get(settingsStore).formData?.birdnet;
  let latitude = $state(storedBirdnet?.latitude ?? 0);
  let longitude = $state(storedBirdnet?.longitude ?? 0);
  let speciesLocale = $state(storedBirdnet?.locale ?? 'en');
  // A stored location counts as set when it was explicitly configured (even 0,0)
  const storedLocationConfigured = storedBirdnet?.locationConfigured === true;
  let localesLoading = $state(true);
  let localesFailed = $state(false);
  let localeOptions = $state<Array<{ value: string; label: string }>>([]);
  let dirty = $state(false);
  // Bumped by map and place picks and by typing, never by a browser result, so
  // a browser location that arrives after newer user intent is dropped.
  let coordinateIntentVersion = $state(0);

  // Baseline the UI locale against the PERSISTED backend value (not the
  // runtime locale). This has three effects:
  //   1) If the user picks a different UI language in the wizard, the
  //      leave handler detects the change and writes it to the backend.
  //   2) If runtime/localStorage has already drifted from the backend
  //      before the wizard opens (e.g. a previous wizard run set
  //      localStorage but failed to persist to config.yaml), the leave
  //      handler still writes the runtime value so the drift is healed
  //      rather than silently preserved.
  //   3) On a fresh install the backend's Dashboard.Locale field is an
  //      empty string (Go zero value), which the API serializes as
  //      `undefined` thanks to the `omitempty` JSON tag. Leaving the
  //      baseline as `undefined` makes `getLocale() !== initialUILocale`
  //      true in the leave handler, so the runtime/browser-detected locale
  //      gets persisted to the backend on first save, no user interaction
  //      required. A save before the settings load finished is refused by
  //      the settings store (saveSection throws until dataLoaded), so the
  //      baseline can never be written over unloaded data.
  let initialUILocale = $state<string | undefined>(
    get(settingsStore).formData?.realtime?.dashboard?.locale
  );

  $effect(() => {
    untrack(() => onValidChange?.(true));
  });

  onMount(() => {
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

  // A map click, pin drag or chosen place
  function handleLocationChange(lat: number, lon: number) {
    coordinateIntentVersion += 1;
    latitude = lat;
    longitude = lon;
    dirty = true;
  }

  function handleBrowserLocation(lat: number, lon: number) {
    latitude = lat;
    longitude = lon;
    dirty = true;
  }

  // The UI language applies, and is cached in localStorage, as soon as it is
  // picked. When the wizard leaves this step without saving it (Skip, Leave
  // setup), restore the language from the last save.
  // If a save is still in flight, wait for it to settle first: a saved language
  // stays so the UI matches the backend, otherwise the old one comes back.
  let uiLocaleAtLastSave = getLocale();
  // Reactive so the browser location button is disabled while a save runs.
  let pendingSave = $state.raw<Promise<void> | null>(null);
  // Set when the step unmounts, so a commit still in flight sends no further parts.
  let left = false;
  onDestroy(() => {
    left = true;
    const restore = () => {
      if (getLocale() !== uiLocaleAtLastSave) setLocale(uiLocaleAtLastSave);
    };
    if (pendingSave) {
      pendingSave.then(restore, restore);
    } else {
      restore();
    }
  });

  // Next, Back and Done await the commit; it never runs on Skip or Leave setup.
  onMount(() => registerLeaveHandler?.(commit));

  // Save the step's edits when the wizard leaves it with Next, Back or Done.
  // Only runs if the user made changes (or the UI locale needs healing). If Skip
  // closes the wizard while one part is saving, that request completes but the
  // next part is not sent.
  async function commit(): Promise<void> {
    const uiLocaleChanged = getLocale() !== initialUILocale;
    if (!dirty && !uiLocaleChanged) return;

    // Each part saves on its own and is marked saved as soon as it succeeds, so
    // a retry after a failure sends only the part that did not go through.
    const save = (async () => {
      if (dirty) {
        await settingsActions.saveSection('birdnet', {
          latitude,
          longitude,
          locale: speciesLocale,
          // Mirrors the server, which sets locationConfigured when either
          // coordinate is non-zero, so the store matches config.yaml.
          ...(latitude !== 0 || longitude !== 0 ? { locationConfigured: true } : {}),
        });
        dirty = false;
      }

      if (uiLocaleChanged && !left) {
        const savedLocale = getLocale();
        await settingsActions.saveSection('dashboard', { locale: savedLocale });
        initialUILocale = savedLocale;
        uiLocaleAtLastSave = savedLocale;
      }
    })();
    pendingSave = save;
    try {
      await save;
    } finally {
      pendingSave = null;
    }
  }
</script>

<!-- Two columns once the dialog is wide (the container is the step box): the fields on
     the left, the map on the right. Narrower, the map stacks below the fields. -->
<div class="grid gap-x-6 gap-y-5 @2xl:grid-cols-2">
  <div class="min-w-0 space-y-5">
    <div>
      <label
        for="wizard-ui-language"
        class="mb-1 block text-sm font-medium text-[var(--color-base-content)]"
      >
        {t('wizard.steps.locationLanguage.uiLanguageLabel')}
      </label>
      <p id={UI_LANGUAGE_HELP_ID} class="mb-2 text-sm text-[var(--color-base-content)] opacity-80">
        {t('wizard.steps.locationLanguage.uiLanguageHelp')}
      </p>
      <LanguageSelector id="wizard-ui-language" aria-describedby={UI_LANGUAGE_HELP_ID} />
    </div>

    <div>
      <!-- The dropdown (the label's control) is absent while the locales load -->
      <label
        for={localesLoading ? undefined : 'wizard-species-locale'}
        class="mb-1 block text-sm font-medium text-[var(--color-base-content)]"
      >
        {t('wizard.steps.locationLanguage.speciesLanguageLabel')}
      </label>
      <p
        id={SPECIES_LANGUAGE_HELP_ID}
        class="mb-2 text-sm text-[var(--color-base-content)] opacity-80"
      >
        {t('wizard.steps.locationLanguage.speciesLanguageHelp')}
      </p>
      {#if localesLoading}
        <div
          class="flex items-center gap-3 rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] px-4 py-3"
        >
          <LoadingSpinner size="sm" aria-hidden="true" />
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
          aria-describedby={SPECIES_LANGUAGE_HELP_ID}
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
        <span class="block text-sm font-medium text-[var(--color-base-content)]">
          {t('wizard.steps.locationLanguage.locationLabel')}
        </span>
        <p class="mt-1 text-sm text-[var(--color-base-content)] opacity-80">
          {t('wizard.steps.locationLanguage.locationHelp')}
        </p>
      </div>

      <!-- Typing is newer intent than a browser location still on its way -->
      <div class="grid grid-cols-2 gap-3" oninput={() => (coordinateIntentVersion += 1)}>
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

      <div class="mt-3">
        <!-- Disabled while the step saves, so a location that arrives then is dropped,
             not shown as detected after the coordinates were already sent -->
        <CurrentLocationButton
          compact
          disabled={pendingSave !== null}
          {latitude}
          {longitude}
          {coordinateIntentVersion}
          onLocation={handleBrowserLocation}
        />
      </div>
    </div>
  </div>

  <!-- Place search above a map of fixed height, in both layouts -->
  <div class="min-w-0">
    <LocationMap
      {latitude}
      {longitude}
      locationSet={storedLocationConfigured || latitude !== 0 || longitude !== 0}
      title={t('wizard.steps.locationLanguage.locationLabel')}
      onLocationChange={handleLocationChange}
      placeSearch
      pinchZoom
      doubleTapZoomKeepsPin
      startView="world"
      controls="overlay"
      mapClass="h-[300px]"
    />
  </div>
</div>
