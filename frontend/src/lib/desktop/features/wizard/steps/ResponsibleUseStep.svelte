<script lang="ts">
  import { untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import type { WizardStepProps } from '../types';

  let { onValidChange }: WizardStepProps = $props();

  let acknowledged = $state(false);

  $effect(() => {
    const valid = acknowledged;
    untrack(() => onValidChange?.(valid));
  });
</script>

<div class="space-y-4">
  <p class="text-sm leading-relaxed text-[var(--color-base-content)]">
    {t('wizard.steps.responsibleUse.intro')}
  </p>

  <ul class="list-disc space-y-2 pl-5 text-sm text-[var(--color-base-content)] opacity-80">
    <li>{t('wizard.steps.responsibleUse.point1')}</li>
    <li>{t('wizard.steps.responsibleUse.point2')}</li>
    <li>{t('wizard.steps.responsibleUse.point3')}</li>
    <li>{t('wizard.steps.responsibleUse.point4')}</li>
  </ul>

  <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
    {t('wizard.steps.responsibleUse.citizenScienceHeading')}
  </h4>

  <ul class="list-disc space-y-2 pl-5 text-sm text-[var(--color-base-content)] opacity-80">
    <li>{t('wizard.steps.responsibleUse.citizenPoint1')}</li>
    <li>{t('wizard.steps.responsibleUse.citizenPoint2')}</li>
    <li>{t('wizard.steps.responsibleUse.citizenPoint3')}</li>
  </ul>

  <p class="text-sm leading-relaxed text-[var(--color-base-content)] opacity-70">
    {t('wizard.steps.responsibleUse.outro')}
  </p>

  <label
    class="mt-4 flex cursor-pointer items-center gap-3 rounded-lg border border-[var(--border-200)] p-3"
  >
    <input
      type="checkbox"
      bind:checked={acknowledged}
      class="size-4 shrink-0 accent-[var(--color-primary)]"
    />
    <span class="text-sm font-medium text-[var(--color-base-content)]">
      {t('wizard.steps.responsibleUse.acknowledge')}
    </span>
  </label>
</div>
