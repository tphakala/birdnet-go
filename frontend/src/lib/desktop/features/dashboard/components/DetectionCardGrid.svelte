<!--
  DetectionCardGrid.svelte

  A card-based grid layout for displaying recent bird detections.
  Features prominent spectrogram visualization with overlaid metadata.

  Usage:
  - Dashboard view for recent detections
  - Visual browsing of detection recordings

  Props:
  - data: Detection[] - Array of detection objects
  - loading?: boolean - Loading state
  - error?: string | null - Error message
  - onRefresh: () => void - Callback to refresh data
  - limit?: number - Number of detections to show
  - onLimitChange?: (limit: number) => void - Callback when limit changes
  - newDetectionIds?: Set<number> - IDs of newly arrived detections
  - onFreezeStart?: () => void - Callback when interaction starts (freeze updates)
  - onFreezeEnd?: () => void - Callback when interaction ends (resume updates)
  - updatesAreFrozen?: boolean - Whether updates are currently frozen
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import DetectionCard from './DetectionCard.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import type { Detection } from '$lib/types/detection.types';
  import { RefreshCw, XCircle } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { cn } from '$lib/utils/cn';

  const logger = loggers.ui;

  interface Props {
    data: Detection[];
    loading?: boolean;
    error?: string | null;
    onRefresh: () => void;
    limit?: number;
    // eslint-disable-next-line no-unused-vars
    onLimitChange?: (limit: number) => void;
    newDetectionIds?: Set<number>;
    onFreezeStart?: () => void;
    onFreezeEnd?: () => void;
    updatesAreFrozen?: boolean;
    className?: string;
  }

  let {
    data = [],
    loading = false,
    error = null,
    onRefresh,
    limit = 6,
    onLimitChange,
    newDetectionIds = new Set(),
    onFreezeStart,
    onFreezeEnd,
    updatesAreFrozen = false,
    className = '',
  }: Props = $props();

  // State for number of detections to show - captures initial prop value without creating dependency
  // Uses untrack() to explicitly capture initial value only (local state is independent after init)
  let selectedLimit = $state(untrack(() => limit));

  // Updates the number of detections to display and persists the preference
  function handleLimitChange(newLimit: number) {
    selectedLimit = newLimit;

    if (typeof window !== 'undefined') {
      try {
        localStorage.setItem('recentDetectionLimit', newLimit.toString());
      } catch (e) {
        logger.error('Failed to save detection limit:', e);
      }
    }

    if (onLimitChange) {
      onLimitChange(newLimit);
    }
  }

  // Modal states
  let showConfirmModal = $state(false);
  let selectedDetection = $state<Detection | null>(null);
  let confirmModalConfig = $state({
    title: '',
    message: '',
    confirmLabel: 'Confirm',
    onConfirm: async () => {},
  });

  // Action handlers
  function handleReview(detection: Detection) {
    window.location.href = `/ui/detections/${detection.id}?tab=review`;
  }

  function handleToggleSpecies(detection: Detection) {
    const isExcluded = false;
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
          onRefresh();
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
          onRefresh();
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
          onRefresh();
        } catch (err) {
          logger.error('Error deleting detection:', err);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }
</script>

<section
  class={cn(
    'card col-span-12 bg-base-100 shadow-sm rounded-2xl border border-border-100 overflow-hidden',
    className
  )}
>
  <!-- Card Header -->
  <div class="px-6 py-4 border-b border-base-200">
    <div class="flex items-center justify-between">
      <div class="flex flex-col">
        <h3 class="font-semibold">{t('dashboard.recentDetections.title')}</h3>
        <p class="text-sm text-base-content/60">{t('dashboard.recentDetections.subtitle')}</p>
      </div>
      <div class="flex items-center gap-2">
        <label for="numDetectionsGrid" class="label-text text-sm"
          >{t('dashboard.recentDetections.controls.show')}</label
        >
        <select
          id="numDetectionsGrid"
          bind:value={selectedLimit}
          onchange={e => handleLimitChange(parseInt(e.currentTarget.value, 10))}
          class="select select-sm focus-visible:outline-hidden"
        >
          <option value={6}>6</option>
          <option value={12}>12</option>
          <option value={24}>24</option>
          <option value={48}>48</option>
        </select>
        <button
          onclick={onRefresh}
          class="btn btn-sm btn-ghost"
          class:opacity-50={updatesAreFrozen}
          disabled={loading || updatesAreFrozen}
          title={updatesAreFrozen
            ? 'Refresh paused while interaction is active'
            : t('dashboard.recentDetections.controls.refresh')}
          aria-label={t('dashboard.recentDetections.controls.refresh')}
        >
          <RefreshCw class={loading ? 'size-4 animate-spin' : 'size-4'} />
        </button>
      </div>
    </div>
  </div>

  <!-- Content -->
  <div class="p-4">
    {#if error}
      <div class="alert alert-error">
        <XCircle class="size-6" />
        <span>{error}</span>
      </div>
    {:else}
      <div class="relative">
        <!-- Loading overlay -->
        {#if loading}
          <div
            class="absolute inset-0 bg-base-100/80 z-20 flex items-center justify-center rounded-lg pointer-events-none"
          >
            <span class="loading loading-spinner loading-md"></span>
          </div>
        {/if}

        <!-- Detection Cards Grid -->
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {#each data.slice(0, selectedLimit) as detection (detection.id)}
            <DetectionCard
              {detection}
              isNew={newDetectionIds.has(detection.id)}
              {onFreezeStart}
              {onFreezeEnd}
              onReview={() => handleReview(detection)}
              onToggleSpecies={() => handleToggleSpecies(detection)}
              onToggleLock={() => handleToggleLock(detection)}
              onDelete={() => handleDelete(detection)}
            />
          {/each}
        </div>

        {#if data.length === 0}
          <div class="text-center py-8 text-base-content/60">
            {t('dashboard.recentDetections.noDetections')}
          </div>
        {/if}
      </div>
    {/if}
  </div>
</section>

<!-- Modals -->
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
      await confirmModalConfig.onConfirm();
      showConfirmModal = false;
      selectedDetection = null;
    }}
  />
{/if}
