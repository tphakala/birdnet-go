<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { settingsActions, settingsStore, type RealtimeSettings } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import { getLogger } from '$lib/utils/logger';
  import type { WizardStepProps } from '../types';

  const logger = getLogger('IntegrationStep');

  let { onValidChange }: WizardStepProps = $props();

  let privacyEnabled = $state(true);
  let birdweatherEnabled = $state(false);
  let birdweatherId = $state('');
  let sentryEnabled = $state(false);
  let dirty = $state(false);

  $effect(() => {
    untrack(() => onValidChange?.(true));
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
  });

  function markDirty() {
    dirty = true;
  }

  // Save on unmount — only if user made changes
  $effect(() => {
    return () => {
      if (!dirty) return;
      settingsActions.updateSection('realtime', {
        privacyFilter: { enabled: privacyEnabled } as RealtimeSettings['privacyFilter'],
        birdweather: {
          enabled: birdweatherEnabled,
          id: birdweatherId,
        } as RealtimeSettings['birdweather'],
      });
      settingsActions.updateSection('sentry', {
        enabled: sentryEnabled,
      });
      settingsActions.saveSettings().catch(err => {
        logger.error('Failed to save integration settings', err);
      });
    };
  });
</script>

<div class="space-y-5">
  <label class="flex cursor-pointer items-start gap-3">
    <input
      type="checkbox"
      bind:checked={privacyEnabled}
      class="mt-0.5 size-4 shrink-0 accent-[var(--color-primary)]"
      onchange={markDirty}
      aria-describedby="privacy-filter-help"
    />
    <div>
      <span class="text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.integration.privacyFilterLabel')}
      </span>
      <p
        id="privacy-filter-help"
        class="mt-0.5 text-xs text-[var(--color-base-content)] opacity-60"
      >
        {t('wizard.steps.integration.privacyFilterHelp')}
      </p>
    </div>
  </label>

  <hr class="border-[var(--border-200)]" />

  <div>
    <label class="flex cursor-pointer items-start gap-3">
      <input
        type="checkbox"
        bind:checked={birdweatherEnabled}
        class="mt-0.5 size-4 shrink-0 accent-[var(--color-primary)]"
        onchange={markDirty}
        aria-describedby="birdweather-help"
      />
      <div>
        <span class="text-sm font-medium text-[var(--color-base-content)]">
          {t('wizard.steps.integration.birdweatherLabel')}
        </span>
        <p id="birdweather-help" class="mt-0.5 text-xs text-[var(--color-base-content)] opacity-60">
          {t('wizard.steps.integration.birdweatherHelp')}
        </p>
      </div>
    </label>
    {#if birdweatherEnabled}
      <div class="ml-7 mt-2">
        <label
          for="birdweather-id"
          class="mb-1 block text-xs text-[var(--color-base-content)] opacity-70"
        >
          {t('wizard.steps.integration.birdweatherIdLabel')}
        </label>
        <TextInput
          bind:value={birdweatherId}
          placeholder={t('wizard.steps.integration.birdweatherIdPlaceholder')}
          oninput={markDirty}
        />
      </div>
    {/if}
  </div>

  <hr class="border-[var(--border-200)]" />

  <label class="flex cursor-pointer items-start gap-3">
    <input
      type="checkbox"
      bind:checked={sentryEnabled}
      class="mt-0.5 size-4 shrink-0 accent-[var(--color-primary)]"
      onchange={markDirty}
      aria-describedby="error-reporting-help"
    />
    <div>
      <span class="text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.integration.errorReportingLabel')}
      </span>
      <p
        id="error-reporting-help"
        class="mt-0.5 text-xs text-[var(--color-base-content)] opacity-60"
      >
        {t('wizard.steps.integration.errorReportingHelp')}
      </p>
    </div>
  </label>
</div>
