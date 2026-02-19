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
  import { fetchWithCSRF } from '$lib/utils/api';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { SvelteSet } from 'svelte/reactivity';

  const logger = loggers.ui;

  interface Props {
    detections: Detection[];
    onRefresh?: () => void;
  }

  let { detections, onRefresh }: Props = $props();

  // Track excluded species locally (session state)
  let excludedSpecies = new SvelteSet<string>();

  function isSpeciesExcluded(commonName: string): boolean {
    return excludedSpecies.has(commonName);
  }

  // Modal state
  let showConfirmModal = $state(false);
  let selectedDetection = $state<Detection | null>(null);
  let confirmModalConfig = $state({
    title: '',
    message: '',
    confirmLabel: 'Confirm',
    onConfirm: async () => {},
  });

  function handleReview(detection: Detection) {
    navigation.navigate(`/ui/detections/${detection.id}?tab=review`);
  }

  function handleToggleSpecies(detection: Detection) {
    const isExcluded = isSpeciesExcluded(detection.commonName);
    confirmModalConfig = {
      title: isExcluded
        ? t('dashboard.recentDetections.modals.showSpecies', { species: detection.commonName })
        : t('dashboard.recentDetections.modals.ignoreSpecies', { species: detection.commonName }),
      message: isExcluded
        ? t('dashboard.recentDetections.modals.showSpeciesConfirm', {
            species: detection.commonName,
          })
        : t('dashboard.recentDetections.modals.ignoreSpeciesConfirm', {
            species: detection.commonName,
          }),
      confirmLabel: t('common.buttons.confirm'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF('/api/v2/detections/ignore', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ common_name: detection.commonName }),
          });
          if (isExcluded) {
            excludedSpecies.delete(detection.commonName);
          } else {
            excludedSpecies.add(detection.commonName);
          }
          onRefresh?.();
        } catch (err) {
          logger.error('Error toggling species exclusion:', err);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }

  function handleToggleLock(detection: Detection) {
    confirmModalConfig = {
      title: detection.locked
        ? t('dashboard.recentDetections.modals.unlockDetection')
        : t('dashboard.recentDetections.modals.lockDetection'),
      message: detection.locked
        ? t('dashboard.recentDetections.modals.unlockDetectionConfirm', {
            species: detection.commonName,
          })
        : t('dashboard.recentDetections.modals.lockDetectionConfirm', {
            species: detection.commonName,
          }),
      confirmLabel: t('common.buttons.confirm'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detection.id}/lock`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ locked: !detection.locked }),
          });
          onRefresh?.();
        } catch (err) {
          logger.error('Error toggling lock status:', err);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }

  function handleDelete(detection: Detection) {
    confirmModalConfig = {
      title: t('dashboard.recentDetections.modals.deleteDetection', {
        species: detection.commonName,
      }),
      message: t('dashboard.recentDetections.modals.deleteDetectionConfirm', {
        species: detection.commonName,
      }),
      confirmLabel: t('common.buttons.delete'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detection.id}`, {
            method: 'DELETE',
          });
          onRefresh?.();
        } catch (err) {
          logger.error('Error deleting detection:', err);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }
</script>

<div class="grid grid-cols-1 lg:grid-cols-2 gap-4 p-2 sm:p-4">
  {#each detections as detection (detection.id)}
    <DetectionCard
      {detection}
      isExcluded={isSpeciesExcluded(detection.commonName)}
      onReview={() => handleReview(detection)}
      onToggleSpecies={() => handleToggleSpecies(detection)}
      onToggleLock={() => handleToggleLock(detection)}
      onDelete={() => handleDelete(detection)}
    />
  {/each}
</div>

{#if selectedDetection}
  <ConfirmModal
    isOpen={showConfirmModal}
    title={confirmModalConfig.title}
    message={confirmModalConfig.message}
    confirmLabel={confirmModalConfig.confirmLabel}
    onClose={() => {
      showConfirmModal = false;
      selectedDetection = null;
    }}
    onConfirm={async () => {
      try {
        await confirmModalConfig.onConfirm();
      } finally {
        showConfirmModal = false;
        selectedDetection = null;
      }
    }}
  />
{/if}
