<script lang="ts">
  import { untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import type { WizardStepProps } from '../types';

  let { onValidChange }: WizardStepProps = $props();

  $effect(() => {
    untrack(() => onValidChange?.(true));
  });
</script>

<div class="flex flex-col items-center gap-6 py-4 text-center">
  <img
    src="/assets/BirdNET-Go-logo.webp"
    alt={t('about.logoAlt')}
    class="h-24 w-24 rounded-2xl shadow-lg"
  />

  <div class="space-y-3">
    <h2 class="text-2xl font-bold text-[var(--color-base-content)]">
      {t('wizard.steps.welcome.heading')}
    </h2>

    <p class="mx-auto max-w-md text-sm leading-relaxed text-[var(--color-base-content)] opacity-70">
      {t('wizard.steps.welcome.description')}
    </p>
  </div>

  <p class="mx-auto max-w-md text-xs leading-relaxed text-[var(--color-base-content)] opacity-50">
    {t('wizard.steps.welcome.credit')}
  </p>

  <p class="text-sm font-medium text-[var(--color-base-content)]">
    {t('wizard.steps.welcome.cta')}
  </p>
</div>
