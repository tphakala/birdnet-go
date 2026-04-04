<script lang="ts">
  import { untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import type { WizardStepProps } from '../types';

  let { onValidChange }: WizardStepProps = $props();

  $effect(() => {
    untrack(() => onValidChange?.(true));
  });
</script>

<div class="flex flex-col items-center gap-4 pt-6 text-center">
  <div class="flex h-32 w-32 items-center justify-center">
    <img
      src="/ui/assets/BirdNET-Go-logo.webp"
      alt={t('about.logoAlt')}
      class="h-full w-full object-contain"
    />
  </div>

  <div class="space-y-2">
    <h2 class="text-2xl font-bold text-[var(--color-base-content)]">
      {t('wizard.steps.welcome.heading')}
    </h2>

    <p class="text-sm leading-relaxed text-[var(--color-base-content)] opacity-80">
      {t('wizard.steps.welcome.description')}
    </p>
  </div>

  <div class="space-y-1.5">
    <p class="text-sm leading-relaxed text-[var(--color-base-content)] opacity-70">
      {t('wizard.steps.welcome.credit')}
    </p>
    <a
      href="https://github.com/birdnet-team/BirdNET-Analyzer"
      target="_blank"
      rel="noopener noreferrer"
      class="inline-flex items-center gap-1 text-sm text-[var(--color-primary)] hover:underline"
    >
      {t('wizard.steps.welcome.learnMore')}
      <span aria-hidden="true">&rarr;</span>
    </a>
  </div>

  <p class="text-sm font-medium text-[var(--color-primary)]">
    {t('wizard.steps.welcome.cta')}
  </p>
</div>
