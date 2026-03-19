<script lang="ts">
  import { t } from '$lib/i18n';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { settingsActions, type RealtimeSettings } from '$lib/stores/settings';
  import type { WizardStepProps } from '../types';

  let { onValidChange }: WizardStepProps = $props();

  let privacyEnabled = $state(true);
  let birdweatherEnabled = $state(false);
  let birdweatherId = $state('');
  let sentryEnabled = $state(false);

  $effect(() => {
    onValidChange?.(true);
  });

  $effect(() => {
    return () => {
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
      settingsActions.saveSettings().catch(() => {});
    };
  });
</script>

<div class="space-y-5">
  <label class="flex cursor-pointer items-start gap-3">
    <input
      type="checkbox"
      bind:checked={privacyEnabled}
      class="mt-0.5 size-4 shrink-0 accent-[var(--color-primary)]"
    />
    <div>
      <span class="text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.integration.privacyFilterLabel')}
      </span>
      <p class="mt-0.5 text-xs text-[var(--color-base-content)] opacity-60">
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
      />
      <div>
        <span class="text-sm font-medium text-[var(--color-base-content)]">
          {t('wizard.steps.integration.birdweatherLabel')}
        </span>
        <p class="mt-0.5 text-xs text-[var(--color-base-content)] opacity-60">
          {t('wizard.steps.integration.birdweatherHelp')}
        </p>
      </div>
    </label>
    {#if birdweatherEnabled}
      <div class="ml-7 mt-2">
        <TextInput
          bind:value={birdweatherId}
          placeholder={t('wizard.steps.integration.birdweatherIdPlaceholder')}
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
    />
    <div>
      <span class="text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.integration.errorReportingLabel')}
      </span>
      <p class="mt-0.5 text-xs text-[var(--color-base-content)] opacity-60">
        {t('wizard.steps.integration.errorReportingHelp')}
      </p>
    </div>
  </label>
</div>
