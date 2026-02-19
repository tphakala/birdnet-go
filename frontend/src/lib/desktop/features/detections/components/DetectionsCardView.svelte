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
  import { SvelteSet } from 'svelte/reactivity';
  import { useDetectionActions } from '../composables/useDetectionActions.svelte';

  interface Props {
    detections: Detection[];
    onRefresh?: () => void;
  }

  let { detections, onRefresh }: Props = $props();

  // Track excluded species locally (session state)
  let excludedSpecies = new SvelteSet<string>();

  const actions = useDetectionActions({
    onRefresh: () => onRefresh?.(),
    isSpeciesExcluded: name => excludedSpecies.has(name),
    onToggleExclusion: (name, exclude) => {
      if (exclude) {
        excludedSpecies.add(name);
      } else {
        excludedSpecies.delete(name);
      }
    },
  });
</script>

<div class="grid grid-cols-1 lg:grid-cols-2 gap-4 p-2 sm:p-4">
  {#each detections as detection (detection.id)}
    <DetectionCard
      {detection}
      isExcluded={excludedSpecies.has(detection.commonName)}
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
