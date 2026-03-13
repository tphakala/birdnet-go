<!--
  Configuration form for the Daily Summary dashboard element.
  @component
-->
<script lang="ts">
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import { t } from '$lib/i18n';
  import type { DailySummaryConfig } from '$lib/stores/settings';

  interface Props {
    config: DailySummaryConfig;
    onUpdate: (_config: DailySummaryConfig) => void;
  }

  let { config, onUpdate }: Props = $props();
</script>

<div class="space-y-4">
  <NumberField
    label={t('settings.main.sections.userInterface.dashboard.summaryLimit.label')}
    value={config.summaryLimit}
    onUpdate={value => onUpdate({ ...config, summaryLimit: value })}
    min={10}
    max={1000}
    helpText={t('settings.main.sections.userInterface.dashboard.summaryLimit.helpText')}
  />
</div>
