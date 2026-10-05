<script lang="ts">
  import { onDestroy, onMount, untrack } from 'svelte';
  import { t, type TranslationKey } from '$lib/i18n';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { settingsActions, settingsStore } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import { ShieldCheck, Cloud, HeartHandshake } from '@lucide/svelte';
  import type { WizardStepProps } from '../types';
  import type { SettingsSectionPayloads } from '$lib/utils/settingsApi';
  import { generateId } from '$lib/utils/uuid';
  import { isBirdweatherToken } from '$lib/utils/birdweather';

  let { onValidChange, registerLeaveHandler }: WizardStepProps = $props();

  const TOKEN_FIELD_ID = generateId('wizard-birdweather-token');
  const TOKEN_ERROR_ID = generateId('wizard-birdweather-token-error');

  // Read synchronously so the first validity report already reflects the saved settings
  const store = get(settingsStore);
  const initial = {
    privacyEnabled: store?.formData?.realtime?.privacyFilter?.enabled ?? true,
    birdweatherEnabled: store?.formData?.realtime?.birdweather?.enabled ?? false,
    birdweatherId: store?.formData?.realtime?.birdweather?.id ?? '',
    sentryEnabled: store?.formData?.sentry?.enabled ?? false,
  };

  let privacyEnabled = $state(initial.privacyEnabled);
  let birdweatherEnabled = $state(initial.birdweatherEnabled);
  let birdweatherId = $state(initial.birdweatherId);
  let sentryEnabled = $state(initial.sentryEnabled);

  /** Sections this step saves, in save order: BirdWeather first because its
   * token is the value most likely to be rejected, so a failure there writes
   * nothing. */
  const SECTIONS = ['birdweather', 'privacyfilter', 'sentry'] as const;
  type IntegrationSection = (typeof SECTIONS)[number];

  // The token is sent trimmed: the server does not trim it, so this is the string
  // the validation below accepts.
  function currentPayloads(): { [S in IntegrationSection]: SettingsSectionPayloads[S] } {
    return {
      birdweather: { enabled: birdweatherEnabled, id: birdweatherId.trim() },
      privacyfilter: { enabled: privacyEnabled },
      sentry: { enabled: sentryEnabled },
    };
  }

  // JSON of what each section last held on the server (as far as this step
  // knows): the values read on open, then the values of each successful save.
  // The commit sends only sections that differ, so a retry after a partial
  // failure repeats only the sections that did not succeed.
  const loaded = currentPayloads();
  let savedJson: Record<IntegrationSection, string> = {
    birdweather: JSON.stringify(loaded.birdweather),
    privacyfilter: JSON.stringify(loaded.privacyfilter),
    sentry: JSON.stringify(loaded.sentry),
  };

  let blockedReason = $derived.by((): TranslationKey | undefined => {
    if (!birdweatherEnabled) return undefined;
    if (birdweatherId.trim() === '') return 'wizard.steps.integration.reasons.enterToken';
    return isBirdweatherToken(birdweatherId)
      ? undefined
      : 'wizard.steps.integration.reasons.tokenFormat';
  });
  let isValid = $derived(blockedReason === undefined);

  // Set when the user leaves the token field and on open for a saved token; cleared
  // by every edit. Display only: Next's reason does not depend on it.
  let tokenLeft = $state(initial.birdweatherId.trim() !== '');
  let showTokenError = $derived(tokenLeft && blockedReason !== undefined);

  function onTokenInput() {
    tokenLeft = false;
  }

  function onTokenBlur() {
    tokenLeft = true;
  }

  $effect(() => {
    // Primitives, so the effect runs only when one of them changes
    const valid = isValid;
    const why = blockedReason;
    untrack(() => onValidChange?.(valid, why));
  });

  function togglePrivacy() {
    privacyEnabled = !privacyEnabled;
  }

  function toggleBirdweather() {
    birdweatherEnabled = !birdweatherEnabled;
  }

  function toggleSentry() {
    sentryEnabled = !sentryEnabled;
  }

  // Next, Back and Done await the commit; it never runs on Skip or Leave setup.
  onMount(() => registerLeaveHandler?.(commit));

  // Set when the step unmounts, so a commit still in flight sends no further sections.
  let left = false;
  onDestroy(() => {
    left = true;
  });

  // Save the step's edits when the wizard leaves it with Next, Back or Done.
  // Each changed section is its own request, in SECTIONS order. If Skip closes
  // the wizard while one section is saving, that request completes but the
  // remaining sections are not sent.
  async function commit(): Promise<void> {
    const next = currentPayloads();
    for (const section of SECTIONS) {
      if (left) return;
      // Back runs this on an invalid step too: the invalid BirdWeather section
      // stays unsent and unsaved, the valid sections are saved.
      if (section === 'birdweather' && !isValid) continue;
      // eslint-disable-next-line security/detect-object-injection -- section is a member of SECTIONS
      const body = next[section];
      const json = JSON.stringify(body);
      // eslint-disable-next-line security/detect-object-injection -- section is a member of SECTIONS
      if (json === savedJson[section]) continue;
      await settingsActions.saveSection(section, body);
      // eslint-disable-next-line security/detect-object-injection -- section is a member of SECTIONS
      savedJson[section] = json;
    }
  }
</script>

<div class="space-y-3">
  <button
    type="button"
    class="flex w-full cursor-pointer items-start gap-3 rounded-lg border-2 p-4 text-left transition-colors {privacyEnabled
      ? 'border-[var(--color-primary)] bg-[var(--color-primary)]/5'
      : 'border-[var(--border-200)] hover:border-[var(--border-300)]'}"
    onclick={togglePrivacy}
    aria-pressed={privacyEnabled}
  >
    <ShieldCheck class="mt-0.5 size-5 shrink-0 text-[var(--color-base-content)]" />
    <div class="flex-1">
      <span class="text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.integration.privacyFilterLabel')}
      </span>
      <p class="mt-0.5 text-sm text-[var(--color-base-content)] opacity-80">
        {t('wizard.steps.integration.privacyFilterHelp')}
      </p>
    </div>
    <span
      class="mt-0.5 inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors {privacyEnabled
        ? 'bg-[var(--color-primary)]'
        : 'bg-[var(--color-base-300)]'}"
      aria-hidden="true"
    >
      <span
        class="inline-block size-3.5 rounded-full bg-white shadow transition-transform {privacyEnabled
          ? 'translate-x-5'
          : 'translate-x-0.5'}"
      ></span>
    </span>
  </button>

  <button
    type="button"
    class="flex w-full cursor-pointer items-start gap-3 rounded-lg border-2 p-4 text-left transition-colors {birdweatherEnabled
      ? 'border-[var(--color-primary)] bg-[var(--color-primary)]/5'
      : 'border-[var(--border-200)] hover:border-[var(--border-300)]'}"
    onclick={toggleBirdweather}
    aria-pressed={birdweatherEnabled}
  >
    <Cloud
      class="mt-0.5 size-5 shrink-0 {birdweatherEnabled
        ? 'text-[var(--color-primary)]'
        : 'text-[var(--color-base-content)] opacity-70'}"
    />
    <div class="flex-1">
      <span class="text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.integration.birdweatherLabel')}
      </span>
      <p class="mt-0.5 text-sm text-[var(--color-base-content)] opacity-80">
        {t('wizard.steps.integration.birdweatherHelp')}
      </p>
    </div>
    <span
      class="mt-0.5 inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors {birdweatherEnabled
        ? 'bg-[var(--color-primary)]'
        : 'bg-[var(--color-base-300)]'}"
      aria-hidden="true"
    >
      <span
        class="inline-block size-3.5 rounded-full bg-white shadow transition-transform {birdweatherEnabled
          ? 'translate-x-5'
          : 'translate-x-0.5'}"
      ></span>
    </span>
  </button>
  {#if birdweatherEnabled}
    <div class="ml-12 mt-[-0.25rem]">
      <label
        for={TOKEN_FIELD_ID}
        class="mb-1 block text-sm text-[var(--color-base-content)] opacity-80"
      >
        {t('settings.integration.birdweather.token.label')}
      </label>
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div onclick={(e: MouseEvent) => e.stopPropagation()}>
        <TextInput
          id={TOKEN_FIELD_ID}
          bind:value={birdweatherId}
          aria-invalid={showTokenError ? 'true' : undefined}
          aria-describedby={showTokenError ? TOKEN_ERROR_ID : undefined}
          oninput={onTokenInput}
          onblur={onTokenBlur}
        />
      </div>
      <!-- Always rendered with two lines reserved: the alert is announced when it fills,
           and showing it does not move the controls below -->
      <p id={TOKEN_ERROR_ID} role="alert" class="mt-1 min-h-10 text-sm text-[var(--text-error)]">
        {showTokenError && blockedReason ? t(blockedReason) : ''}
      </p>
    </div>
  {/if}

  <button
    type="button"
    class="flex w-full cursor-pointer items-start gap-3 rounded-lg border-2 p-4 text-left transition-colors {sentryEnabled
      ? 'border-[var(--color-primary)] bg-[var(--color-primary)]/5'
      : 'border-[var(--border-200)] hover:border-[var(--border-300)]'}"
    onclick={toggleSentry}
    aria-pressed={sentryEnabled}
  >
    <HeartHandshake class="mt-0.5 size-5 shrink-0 text-[var(--color-base-content)]" />
    <div class="flex-1">
      <span class="text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.integration.errorReportingLabel')}
      </span>
      <p class="mt-0.5 text-sm text-[var(--color-base-content)] opacity-80">
        {t('wizard.steps.integration.errorReportingHelp')}
      </p>
    </div>
    <span
      class="mt-0.5 inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors {sentryEnabled
        ? 'bg-[var(--color-primary)]'
        : 'bg-[var(--color-base-300)]'}"
      aria-hidden="true"
    >
      <span
        class="inline-block size-3.5 rounded-full bg-white shadow transition-transform {sentryEnabled
          ? 'translate-x-5'
          : 'translate-x-0.5'}"
      ></span>
    </span>
  </button>
</div>
