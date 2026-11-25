<!--
  Sample Rate Badge Component

  Purpose: Tells whether a source's capture rate suits a model. Below the
  model's minimum rate it shows an error badge with that minimum; below its
  recommended rate a warning badge with the recommendation; otherwise nothing.

  Props:
  - sampleRate: Capture rate of the source, in Hz
  - minRate: The model's minimum rate in Hz; 0 or unset means none
  - recommendedRate: The model's recommended rate in Hz; 0 or unset means none

  @component
-->
<script lang="ts">
  import { AlertTriangle } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';

  interface Props {
    sampleRate: number;
    minRate?: number;
    recommendedRate?: number;
  }

  let { sampleRate, minRate = 0, recommendedRate = 0 }: Props = $props();

  const KHZ = 1000;
  const BADGE_CLASS = 'inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-medium';

  let belowMin = $derived(minRate > 0 && sampleRate < minRate);
  let belowRecommended = $derived(!belowMin && recommendedRate > 0 && sampleRate < recommendedRate);
</script>

{#if belowMin}
  <span class={cn(BADGE_CLASS, 'bg-[var(--color-error)]/15 text-[var(--text-error)]')}>
    <AlertTriangle class="size-3" aria-hidden="true" />
    {t('settings.audio.soundCards.compatibility.minSampleRate', {
      rate: String(minRate / KHZ),
    })}
  </span>
{:else if belowRecommended}
  <span class={cn(BADGE_CLASS, 'bg-[var(--color-warning)]/15 text-[var(--text-warning)]')}>
    <AlertTriangle class="size-3" aria-hidden="true" />
    {t('settings.audio.soundCards.compatibility.recommendedSampleRate', {
      rate: String(recommendedRate / KHZ),
    })}
  </span>
{/if}
