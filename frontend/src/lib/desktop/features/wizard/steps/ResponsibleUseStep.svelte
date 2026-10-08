<script lang="ts">
  import { untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import type { WizardStepProps } from '../types';

  let { onValidChange }: WizardStepProps = $props();

  let acknowledged = $state(false);

  $effect(() => {
    const valid = acknowledged;
    untrack(() => onValidChange?.(valid));
  });
</script>

<div class="space-y-5">
  <p class="text-sm leading-relaxed text-[var(--color-base-content)]">
    {t('wizard.steps.responsibleUse.intro')}
  </p>

  <ul class="list-disc space-y-2 pl-5 text-sm text-[var(--color-base-content)] opacity-90">
    <li>{t('wizard.steps.responsibleUse.point1')}</li>
    <li>{t('wizard.steps.responsibleUse.point2')}</li>
    <li>{t('wizard.steps.responsibleUse.point3')}</li>
    <li>{t('wizard.steps.responsibleUse.point4')}</li>
  </ul>

  <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
    {t('wizard.steps.responsibleUse.citizenScienceHeading')}
  </h4>

  <ul class="list-disc space-y-2 pl-5 text-sm text-[var(--color-base-content)] opacity-90">
    <li>{t('wizard.steps.responsibleUse.citizenPoint1')}</li>
    <li>{t('wizard.steps.responsibleUse.citizenPoint2')}</li>
    <li>{t('wizard.steps.responsibleUse.citizenPoint3')}</li>
  </ul>

  <p class="text-sm leading-relaxed text-[var(--color-base-content)]">
    {t('wizard.steps.responsibleUse.outro')}
  </p>

  <Checkbox
    bind:checked={acknowledged}
    size="sm"
    className="mt-6"
    labelClassName="w-full rounded-lg border-2 p-4 transition-colors {acknowledged
      ? 'border-[var(--color-primary)] bg-[var(--color-primary)]/5'
      : 'border-[var(--border-200)] hover:border-[var(--border-300)]'}"
  >
    <span class="text-sm font-medium text-[var(--color-base-content)]">
      {t('wizard.steps.responsibleUse.acknowledge')}
    </span>
  </Checkbox>
</div>
