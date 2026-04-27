<!--
  Analysis Suspend Badge Component

  Purpose: Compact status badge indicating whether low-noise auto-suspend
  currently has analysis paused for a source.

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';

  interface Props {
    suspended: boolean;
  }

  let { suspended }: Props = $props();
</script>

<span
  class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium bg-[var(--color-base-300)] text-[var(--color-base-content)] opacity-70"
>
  {suspended
    ? t('settings.audio.lowNoiseAutoSuspend.status.suspended')
    : t('settings.audio.lowNoiseAutoSuspend.status.active')}
</span>
