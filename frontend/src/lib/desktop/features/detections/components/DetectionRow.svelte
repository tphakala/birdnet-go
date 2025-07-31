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
  - isExcluded?: boolean - Whether this detection is excluded
  - onDetailsClick?: (id: number) => void - Handler for detail view
  - onRefresh?: () => void - Handler for data refresh
-->
<script lang="ts">
  import type { Detection } from '$lib/types/detection.types';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import StatusBadges from '$lib/desktop/components/data/StatusBadges.svelte';
  import WeatherMetrics from '$lib/desktop/components/data/WeatherMetrics.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import ReviewModal from '$lib/desktop/components/modals/ReviewModal.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.ui;

  interface Props {
    detection: Detection;
    isExcluded?: boolean;
    onDetailsClick?: (_id: number) => void;
    onRefresh?: () => void;
  }

  let { detection, isExcluded = false, onDetailsClick, onRefresh }: Props = $props();

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
          logger.error('Error toggling species exclusion:', error);
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
          logger.error('Error toggling lock status:', error);
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
          logger.error('Error deleting detection:', error);
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

<!-- DetectionRow now returns table cells for proper table structure -->
<!-- Date & Time -->
<td class="text-sm">
  <span>{detection.date} {detection.time}</span>
</td>

<!-- Weather Column -->
<td class="text-sm hidden md:table-cell">
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
    <div class="text-base-content/50 text-xs">
      {t('detections.weather.noData')}
    </div>
  {/if}
</td>

<!-- Bird species (with thumbnail) -->
<td class="text-sm">
  <div class="sp-species-container sp-layout-detections">
    <!-- Thumbnail -->
    <div class="sp-thumbnail-wrapper">
      <button class="sp-thumbnail-button" onclick={handleDetailsClick} tabindex="0">
        <img
          loading="lazy"
          src={getThumbnailUrl(detection.scientificName)}
          alt={detection.commonName}
          class="sp-thumbnail-image"
          onerror={e => handleBirdImageError(e)}
        />
      </button>
    </div>

    <!-- Species Names -->
    <div class="sp-species-info-wrapper">
      <div class="sp-species-names">
        <button
          onclick={handleDetailsClick}
          class="sp-species-common-name hover:text-blue-600 transition-colors cursor-pointer text-left"
        >
          {detection.commonName}
        </button>
        <div class="sp-species-scientific-name">{detection.scientificName}</div>
      </div>
    </div>
  </div>
</td>

<!-- Confidence -->
<td class="text-sm">
  <ConfidenceCircle confidence={detection.confidence} size="md" />
</td>

<!-- Status -->
<td>
  <StatusBadges {detection} />
</td>

<!-- Recording/Spectrogram -->
<td class="hidden md:table-cell">
  <AudioPlayer
    audioUrl="/api/v2/audio/{detection.id}"
    detectionId={detection.id.toString()}
    width={200}
    height={80}
    showSpectrogram={true}
    showDownload={true}
    spectrogramSize="sm"
    spectrogramRaw={true}
    className="w-full max-w-[200px]"
  />
</td>

<!-- Action Menu -->
<td>
  <ActionMenu
    {detection}
    {isExcluded}
    onReview={handleReview}
    onToggleSpecies={handleToggleSpecies}
    onToggleLock={handleToggleLock}
    onDelete={handleDelete}
  />
</td>

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

<style>
  /* Thumbnail wrapper - responsive width */
  .sp-thumbnail-wrapper {
    flex: 0 0 30%; /* Reduced to give more space to names */
    min-width: 40px; /* Minimum size on very small screens */
    max-width: 80px; /* Maximum size on large screens */
  }

  /* Thumbnail button - maintains aspect ratio */
  .sp-thumbnail-button {
    display: block;
    width: 100%;
    aspect-ratio: 4/3; /* Consistent aspect ratio */
    position: relative;
    overflow: hidden;
    border-radius: 0.375rem;
    background-color: oklch(var(--b2) / 0.3);
  }

  /* Thumbnail image */
  .sp-thumbnail-image {
    position: absolute;
    top: 0;
    left: 0;
    width: 100%;
    height: 100%;
    object-fit: contain;
  }
</style>
