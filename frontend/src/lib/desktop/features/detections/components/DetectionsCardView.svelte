<!--
  DetectionsCardView.svelte

  A card-based grid layout for the detections listing page.
  Reuses the dashboard DetectionCard component for visual consistency.

  Props:
  - detections: Detection[] - Array of detection objects to display
  - onRefresh?: () => void - Callback to refresh data after mutations
-->
<script lang="ts">
  import DetectionCard from '$lib/desktop/features/dashboard/components/DetectionCard.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import type { Detection } from '$lib/types/detection.types';
  import { isExcluded as isSpeciesExcluded, setExcluded } from '$lib/stores/excludedSpecies.svelte';
  import { useDetectionActions } from '../composables/useDetectionActions.svelte';
  import { appState } from '$lib/stores/appState.svelte';

  interface Props {
    detections: Detection[];
    onRefresh?: () => void;
  }

  let { detections, onRefresh }: Props = $props();

  // Hide spectrogram/audio on the cards when audio clip export is disabled.
  let audioEnabled = $derived(appState.audioExportEnabled);

  // Exclusion state is the shared, server-hydrated excludedSpecies store
  // (hydrated by the parent DetectionsList).
  const actions = useDetectionActions({
    onRefresh: () => onRefresh?.(),
    isSpeciesExcluded,
    onToggleExclusion: setExcluded,
  });
</script>

<div class="grid grid-cols-1 lg:grid-cols-2 gap-4 p-2 sm:p-4">
  {#each detections as detection (detection.id)}
    <DetectionCard
      {detection}
      {audioEnabled}
      isExcluded={isSpeciesExcluded(detection.commonName)}
      onMarkCorrect={() => actions.handleMarkCorrect(detection)}
      onMarkFalsePositive={() => actions.handleMarkFalsePositive(detection)}
      onReview={() => actions.handleReview(detection)}
      onToggleSpecies={() => actions.handleToggleSpecies(detection)}
      onToggleLock={() => actions.handleToggleLock(detection)}
      onDelete={() => actions.handleDelete(detection)}
    />
  {/each}
</div>

{#if actions.selectedDetection}
  <ConfirmModal
    isOpen={actions.showConfirmModal}
    title={actions.confirmModalConfig.title}
    message={actions.confirmModalConfig.message}
    confirmLabel={actions.confirmModalConfig.confirmLabel}
    onClose={actions.closeModal}
    onConfirm={actions.confirmModal}
  />
{/if}
