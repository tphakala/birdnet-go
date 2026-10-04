<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { settingsActions, settingsStore } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import { ShieldCheck, Cloud, HeartHandshake } from '@lucide/svelte';
  import type { WizardStepProps } from '../types';
  import { useStepSave } from '../stepSave';

  let { onValidChange, registerLeaveHandler }: WizardStepProps = $props();

  let privacyEnabled = $state(true);
  let birdweatherEnabled = $state(false);
  let birdweatherId = $state('');
  let sentryEnabled = $state(false);
  let dirty = $state(false);

  // What each section last held on the server (as far as this step knows): the
  // values read on mount, then the values of each successful save. The commit
  // sends only sections that differ, so a retry after a partial failure repeats
  // only the sections that did not succeed.
  let saved = {
    birdweather: { enabled: false, id: '' },
    privacyfilter: { enabled: true },
    sentry: { enabled: false },
  };

  let isValid = $derived(!birdweatherEnabled || birdweatherId.trim() !== '');

  $effect(() => {
    const valid = isValid;
    untrack(() => onValidChange?.(valid));
  });

  onMount(() => {
    // Load current settings
    const store = get(settingsStore);
    const realtime = store?.formData?.realtime;
    if (realtime?.privacyFilter) {
      privacyEnabled = realtime.privacyFilter.enabled ?? true;
    }
    if (realtime?.birdweather) {
      birdweatherEnabled = realtime.birdweather.enabled ?? false;
      birdweatherId = realtime.birdweather.id ?? '';
    }
    const sentry = store?.formData?.sentry;
    if (sentry) {
      sentryEnabled = sentry.enabled ?? false;
    }
    saved = {
      birdweather: { enabled: birdweatherEnabled, id: birdweatherId },
      privacyfilter: { enabled: privacyEnabled },
      sentry: { enabled: sentryEnabled },
    };
  });

  function togglePrivacy() {
    privacyEnabled = !privacyEnabled;
    dirty = true;
  }

  function toggleBirdweather() {
    birdweatherEnabled = !birdweatherEnabled;
    dirty = true;
  }

  function toggleSentry() {
    sentryEnabled = !sentryEnabled;
    dirty = true;
  }

  function markDirty() {
    dirty = true;
  }

  useStepSave(() => registerLeaveHandler, commit);

  // Save the step's edits when the wizard leaves it with Next, Back or Done.
  // Only runs if the user made changes. Each changed section is its own request,
  // BirdWeather first because its token is the value most likely to be rejected,
  // so a failure there writes nothing.
  async function commit(): Promise<void> {
    if (!dirty) return;

    const birdweather = { enabled: birdweatherEnabled, id: birdweatherId };
    if (JSON.stringify(birdweather) !== JSON.stringify(saved.birdweather)) {
      await settingsActions.saveSection('birdweather', birdweather);
      saved.birdweather = birdweather;
    }

    const privacyfilter = { enabled: privacyEnabled };
    if (JSON.stringify(privacyfilter) !== JSON.stringify(saved.privacyfilter)) {
      await settingsActions.saveSection('privacyfilter', privacyfilter);
      saved.privacyfilter = privacyfilter;
    }

    const sentry = { enabled: sentryEnabled };
    if (JSON.stringify(sentry) !== JSON.stringify(saved.sentry)) {
      await settingsActions.saveSection('sentry', sentry);
      saved.sentry = sentry;
    }

    dirty = false;
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
        for="birdweather-id"
        class="mb-1 block text-sm text-[var(--color-base-content)] opacity-80"
      >
        {t('wizard.steps.integration.birdweatherIdLabel')}
      </label>
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div onclick={(e: MouseEvent) => e.stopPropagation()}>
        <TextInput
          id="birdweather-id"
          bind:value={birdweatherId}
          placeholder={t('wizard.steps.integration.birdweatherIdPlaceholder')}
          oninput={markDirty}
        />
      </div>
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
