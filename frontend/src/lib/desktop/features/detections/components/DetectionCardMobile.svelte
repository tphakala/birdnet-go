<script lang="ts">
  // Use prop callback instead of legacy event dispatcher
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import StatusBadges from '$lib/desktop/components/data/StatusBadges.svelte';
  import { Volume2 } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { Detection } from '$lib/types/detection.types';
  import { buildAppUrl } from '$lib/utils/urlHelpers';

  interface Props {
    detection: Detection;
    onDetailsClick?: (_id: number) => void;
    onPlayMobileAudio?: (_payload: {
      audioUrl: string;
      speciesName: string;
      detectionId: number;
    }) => void;
    className?: string;
  }

  let { detection, onDetailsClick, onPlayMobileAudio, className = '' }: Props = $props();

  // Legacy dispatcher removed

  let spectrogramError = $state(false);
  let spectrogramUrl = $derived(`/api/v2/spectrogram/${detection.id}?size=sm`);

  function handlePlay() {
    const audioUrl = `/api/v2/audio/${detection.id}`;
    if (onPlayMobileAudio) {
      onPlayMobileAudio({ audioUrl, speciesName: detection.commonName, detectionId: detection.id });
    }
  }

  function goToDetails() {
    if (onDetailsClick) {
      onDetailsClick(detection.id);
    } else {
      window.location.href = buildAppUrl(`/ui/detections/${detection.id}`);
    }
  }
</script>

<section class={`card bg-base-100 shadow-xs relative overflow-hidden ${className}`}>
  {#if spectrogramUrl && !spectrogramError}
    <img
      src={spectrogramUrl}
      alt="Audio spectrogram"
      class="absolute inset-0 w-full h-full object-cover opacity-20"
      onerror={() => (spectrogramError = true)}
    />
    <div class="absolute inset-0 bg-base-100/60"></div>
  {/if}
  <div class="card-body p-3 space-y-3 relative">
    <!-- Header: Names and confidence -->
    <div class="flex items-start gap-3">
      <div class="flex-1 min-w-0">
        <div class="text-base font-semibold leading-tight truncate">
          {detection.commonName}
        </div>
        <div class="text-xs opacity-70 truncate">
          {detection.scientificName}
        </div>
        <div class="mt-1 text-xs opacity-70">
          {detection.date}
          {detection.time}
        </div>
      </div>
      <div class="shrink-0">
        <ConfidenceCircle confidence={detection.confidence} size="sm" />
      </div>
    </div>

    <!-- Status badges -->
    <div class="flex flex-wrap gap-2">
      <StatusBadges {detection} />
    </div>

    <!-- Actions -->
    <div class="flex items-center gap-2">
      <button
        class="btn btn-primary btn-sm"
        onclick={handlePlay}
        aria-label={t('search.detailsPanel.playAudio', { species: detection.commonName })}
      >
        <Volume2 class="h-4 w-4" />
        {t('common.actions.play')}
      </button>
      <button
        class="btn btn-outline btn-sm"
        onclick={goToDetails}
        aria-label={t('search.detailsPanel.viewDetails', { species: detection.commonName })}
      >
        {t('common.actions.view')}
      </button>
    </div>
  </div>
</section>

<style>
  /* Keep styles minimal and mobile-focused */
  .card :global(.badge) {
    font-size: 0.7rem;
  }
</style>
