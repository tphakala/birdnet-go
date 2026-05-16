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
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import type { Detection } from '$lib/types/detection.types';
  import { cn } from '$lib/utils/cn';
  import { SvelteSet } from 'svelte/reactivity';
  import { useDetectionActions } from '../composables/useDetectionActions.svelte';

  interface Props {
    detections: Detection[];
    onRefresh?: () => void;
    selectionActive?: boolean;
    selectedIds?: (_id: string) => boolean;
    onToggleSelect?: (_id: string, _shiftKey: boolean) => void;
  }

  let {
    detections,
    onRefresh,
    selectionActive = false,
    selectedIds = () => false,
    onToggleSelect,
  }: Props = $props();

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
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class={cn('relative', selectionActive && 'cursor-pointer')}
      onclick={e => {
        if (selectionActive) {
          if ((e.target as HTMLElement).closest('button, a, input, label')) return;
          onToggleSelect?.(String(detection.id), e.shiftKey);
        }
      }}
      role={selectionActive ? 'option' : undefined}
      aria-selected={selectionActive ? selectedIds(String(detection.id)) : undefined}
    >
      {#if selectionActive}
        <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
        <div class="absolute top-2 left-2 z-10" onclick={e => e.stopPropagation()}>
          <Checkbox
            checked={selectedIds(String(detection.id))}
            size="sm"
            variant="primary"
            onchange={() => onToggleSelect?.(String(detection.id), false)}
          />
        </div>
        {#if selectedIds(String(detection.id))}
          <div
            class="absolute inset-0 rounded-lg ring-2 ring-[var(--color-primary)]/30 pointer-events-none z-[5]"
          ></div>
        {/if}
      {/if}
      <DetectionCard
        {detection}
        isExcluded={excludedSpecies.has(detection.commonName)}
        onMarkCorrect={() => actions.handleMarkCorrect(detection)}
        onMarkFalsePositive={() => actions.handleMarkFalsePositive(detection)}
        onReview={() => actions.handleReview(detection)}
        onToggleSpecies={() => actions.handleToggleSpecies(detection)}
        onToggleLock={() => actions.handleToggleLock(detection)}
        onDelete={() => actions.handleDelete(detection)}
      />
    </div>
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
