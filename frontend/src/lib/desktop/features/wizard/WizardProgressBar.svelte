<script lang="ts">
  import { t } from '$lib/i18n';
  import type { WizardFlow } from './types';

  interface Props {
    currentStep: number; // 0-based index
    totalSteps: number;
    flow: WizardFlow;
  }

  let { currentStep, totalSteps, flow }: Props = $props();

  let visible = $derived(flow === 'onboarding' && totalSteps > 1);
</script>

{#if visible}
  <div
    class="flex items-center justify-center gap-2"
    role="group"
    aria-label={t('wizard.progressLabel')}
  >
    <span class="sr-only"
      >{t('wizard.progress', { current: String(currentStep + 1), total: String(totalSteps) })}</span
    >
    {#each Array.from({ length: totalSteps }) as _, i (i)}
      {#if i === currentStep}
        <span class="size-2.5 rounded-full bg-[var(--color-primary)]" aria-hidden="true"></span>
      {:else if i < currentStep}
        <span class="size-2.5 rounded-full bg-[var(--color-primary)] opacity-60" aria-hidden="true"
        ></span>
      {:else}
        <span
          class="size-2.5 rounded-full border-2 border-[var(--border-200)] bg-transparent"
          aria-hidden="true"
        ></span>
      {/if}
    {/each}
  </div>
{/if}
