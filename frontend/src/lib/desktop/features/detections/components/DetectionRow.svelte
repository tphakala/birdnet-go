<!--
  DetectionRow.svelte
  
  A comprehensive row component for displaying individual bird detection records with interactive features.
  Includes confidence indicators, status badges, weather information, and action controls.
  
  Usage:
  - Detection listings and tables
  - Search results display
  - Administrative detection management
  - Any context requiring detailed detection information
  
  Features:
  - Confidence circle visualization
  - Status badges (verified, false positive, etc.)
  - Weather condition display
  - Action menu for review/delete operations
  - Thumbnail image support
  - Modal dialogs for review and confirmation
  - Responsive design
  
  Props:
  - detection: Detection - The detection data object
  - showThumbnails?: boolean - Whether to display species thumbnails
  - isExcluded?: boolean - Whether this detection is excluded
  - onDetailsClick?: (id: number) => void - Handler for detail view
  - onRefresh?: () => void - Handler for data refresh
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Detection } from '$lib/types/detection.types';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import StatusBadges from '$lib/desktop/components/data/StatusBadges.svelte';
  import WeatherMetrics from '$lib/desktop/components/data/WeatherMetrics.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import ReviewModal from '$lib/desktop/components/modals/ReviewModal.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { t } from '$lib/i18n';

  interface Props {
    detection: Detection;
    showThumbnails?: boolean;
    isExcluded?: boolean;
    onDetailsClick?: (id: number) => void;
    onRefresh?: () => void;
    className?: string;
  }

  let {
    detection,
    showThumbnails = false,
    isExcluded = false,
    onDetailsClick,
    onRefresh,
    className = '',
  }: Props = $props();

  // Modal states
  let showReviewModal = $state(false);
  let showConfirmModal = $state(false);
  let confirmModalConfig = $state({
    title: '',
    message: '',
    confirmLabel: 'Confirm',
    onConfirm: () => {},
  });

  function handleDetailsClick(e: Event) {
    e.preventDefault();
    onDetailsClick?.(detection.id);
  }

  // Action handlers
  function handleReview() {
    showReviewModal = true;
  }

  function handleToggleSpecies() {
    confirmModalConfig = {
      title: isExcluded
        ? `Show Species ${detection.commonName}`
        : `Ignore Species ${detection.commonName}`,
      message: isExcluded
        ? `Are you sure you want to show future detections of ${detection.commonName}?`
        : `Are you sure you want to ignore future detections of ${detection.commonName}? This will only affect new detections - existing detections will remain in the database.`,
      confirmLabel: 'Confirm',
      onConfirm: async () => {
        try {
          await fetchWithCSRF('/api/v2/detections/ignore', {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
            },
            body: JSON.stringify({
              common_name: detection.commonName,
            }),
          });
          onRefresh?.();
        } catch (error) {
          console.error('Error toggling species exclusion:', error);
        }
      },
    };
    showConfirmModal = true;
  }

  function handleToggleLock() {
    confirmModalConfig = {
      title: detection.locked ? 'Unlock Detection' : 'Lock Detection',
      message: detection.locked
        ? `Are you sure you want to unlock this detection of ${detection.commonName}? This will allow it to be deleted during regular cleanup.`
        : `Are you sure you want to lock this detection of ${detection.commonName}? This will prevent it from being deleted during regular cleanup.`,
      confirmLabel: 'Confirm',
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detection.id}/lock`, {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
            },
            body: JSON.stringify({
              locked: !detection.locked,
            }),
          });
          onRefresh?.();
        } catch (error) {
          console.error('Error toggling lock status:', error);
        }
      },
    };
    showConfirmModal = true;
  }

  function handleDelete() {
    confirmModalConfig = {
      title: `Delete Detection of ${detection.commonName}`,
      message: `Are you sure you want to delete detection of ${detection.commonName}? This action cannot be undone.`,
      confirmLabel: 'Delete',
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detection.id}`, {
            method: 'DELETE',
          });
          onRefresh?.();
        } catch (error) {
          console.error('Error deleting detection:', error);
        }
      },
    };
    showConfirmModal = true;
  }

  // Placeholder function for thumbnail URL
  function getThumbnailUrl(scientificName: string): string {
    // TODO: Replace with actual thumbnail API endpoint
    return `/api/v2/media/species-image?name=${encodeURIComponent(scientificName)}`;
  }
</script>

<div
  class={cn(
    'grid grid-cols-12 gap-4 items-center px-4 py-1 hover:bg-gray-50 transition-colors',
    className
  )}
>
  <!-- Date & Time -->
  <div class="col-span-2 text-sm">
    <span>{detection.date} {detection.time}</span>
  </div>

  <!-- Weather Column -->
  <div class="col-span-2 text-sm">
    {#if detection.weather}
      <div class="flex flex-col gap-1">
        <WeatherMetrics
          weatherIcon={detection.weather.weatherIcon}
          weatherDescription={detection.weather.description}
          temperature={detection.weather.temperature}
          windSpeed={detection.weather.windSpeed}
          windGust={detection.weather.windGust}
          units={detection.weather.units}
          size="md"
          className="ml-1"
        />
      </div>
    {:else}
      <div class="text-base-content/50 text-xs">{t('detections.weather.noData')}</div>
    {/if}
  </div>

  <!-- Bird species with confidence -->
  <div class="col-span-3 text-sm">
    <div class="flex items-center gap-3">
      <ConfidenceCircle confidence={detection.confidence} size="sm" />
      <button
        onclick={handleDetailsClick}
        class="hover:text-blue-600 transition-colors cursor-pointer text-left"
      >
        {detection.commonName}
      </button>
    </div>
  </div>

  <!-- Bird thumbnail -->
  {#if showThumbnails}
    <div class="col-span-1">
      <div class="thumbnail-container w-full max-w-[80px]">
        <button
          onclick={handleDetailsClick}
          class="flex items-center justify-center cursor-pointer"
          aria-label={t('detections.row.viewDetails', { species: detection.commonName })}
        >
          <img
            loading="lazy"
            src={getThumbnailUrl(detection.scientificName)}
            alt={`${detection.commonName} thumbnail`}
            class="w-full h-auto rounded-md object-contain"
          />
        </button>
      </div>
    </div>
  {/if}

  <!-- Status -->
  <div class="col-span-1">
    <StatusBadges {detection} />
  </div>

  <!-- Recording/Spectrogram -->
  <div class={showThumbnails ? 'col-span-2' : 'col-span-3'}>
    <AudioPlayer
      audioUrl="/api/v2/audio/{detection.id}"
      detectionId={detection.id.toString()}
      width={200}
      height={80}
      showSpectrogram={true}
      showDownload={true}
      className="w-full max-w-[200px]"
    />
  </div>

  <!-- Action Menu -->
  <div class="col-span-1 flex justify-end">
    <ActionMenu
      {detection}
      {isExcluded}
      onReview={handleReview}
      onToggleSpecies={handleToggleSpecies}
      onToggleLock={handleToggleLock}
      onDelete={handleDelete}
    />
  </div>
</div>

<!-- Modals -->
<ReviewModal
  isOpen={showReviewModal}
  {detection}
  {isExcluded}
  onClose={() => (showReviewModal = false)}
  onSave={async (verified, lockDetection, ignoreSpecies, comment) => {
    await fetchWithCSRF(`/api/v2/detections/${detection.id}/review`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        verified,
        lock_detection: lockDetection,
        ignore_species: ignoreSpecies ? detection.commonName : null,
        comment,
      }),
    });
    onRefresh?.();
  }}
/>

<ConfirmModal
  isOpen={showConfirmModal}
  title={confirmModalConfig.title}
  message={confirmModalConfig.message}
  confirmLabel={confirmModalConfig.confirmLabel}
  onClose={() => (showConfirmModal = false)}
  onConfirm={async () => {
    await confirmModalConfig.onConfirm();
    showConfirmModal = false;
  }}
/>
