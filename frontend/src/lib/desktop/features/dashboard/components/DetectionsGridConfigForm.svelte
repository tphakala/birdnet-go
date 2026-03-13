<!--
  Configuration form for the Detections Grid dashboard element.
  Placeholder for future options.
  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import type { DetectionsGridConfig } from '$lib/stores/settings';

  interface Props {
    config: DetectionsGridConfig;
    onUpdate: (_config: DetectionsGridConfig) => void;
  }

  // eslint-disable-next-line no-unused-vars
  let { config, onUpdate }: Props = $props();
</script>

<div class="text-sm text-[var(--color-base-content)]/60">
  {t('dashboard.editMode.noConfig')}
</div>
