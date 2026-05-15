<!--
  SourceBadge.svelte

  A compact badge displaying the audio source (microphone/RTSP stream) name.
  Designed for overlay on spectrogram cards, matching the style of
  ConfidenceBadge and WeatherBadge.

  Props:
  - detection: Detection - The detection data (uses detection.source)
  - variant?: 'overlay' | 'inline' - Visual style (overlay for cards, inline for text)
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import type { Detection } from '$lib/types/detection.types';
  import { settingsStore } from '$lib/stores/settings';
  import { getFriendlyAudioSourceName } from '$lib/utils/audioSourceLabel';
  import { cn } from '$lib/utils/cn';
  import { Mic } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  interface Props {
    detection: Detection;
    variant?: 'overlay' | 'inline';
    className?: string;
  }

  let { detection, variant = 'overlay', className = '' }: Props = $props();

  let sourceLabel = $derived(
    getFriendlyAudioSourceName(
      detection.source,
      $settingsStore.formData.realtime?.audio?.sources,
      $settingsStore.formData.realtime?.rtsp?.streams
    )
  );
</script>

{#if sourceLabel}
  <div
    class={cn(variant === 'overlay' ? 'source-badge-overlay' : 'source-badge-inline', className)}
    title={sourceLabel}
    role="img"
    aria-label="{t('analytics.recentDetections.headers.source')}: {sourceLabel}"
  >
    <Mic class="size-3" />
    <span class="source-label">{sourceLabel}</span>
  </div>
{/if}

<style>
  /* Overlay variant — for spectrogram card overlays (dark background) */
  .source-badge-overlay {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    padding: 0.25rem 0.5rem;
    border-radius: 9999px;
    background-color: rgb(0 0 0 / 0.5);
    backdrop-filter: blur(4px);
    box-shadow: 0 2px 4px rgb(0 0 0 / 0.2);
    max-width: 10rem;
    color: white;
  }

  /* Inline variant — for text contexts (light/themed background) */
  .source-badge-inline {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    padding: 0.125rem 0.5rem;
    border-radius: 9999px;
    background-color: var(--color-primary);
    color: var(--color-primary-content);
    font-size: 0.75rem;
    font-weight: 500;
  }

  .source-label {
    font-size: 0.75rem;
    font-weight: 500;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
