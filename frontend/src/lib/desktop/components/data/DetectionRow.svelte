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
  import ConfidenceCircle from './ConfidenceCircle.svelte';
  import StatusBadges from './StatusBadges.svelte';
  import WeatherIcon from './WeatherIcon.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import ReviewModal from '$lib/desktop/components/modals/ReviewModal.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';

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
    return `/api/v1/thumbnails/${encodeURIComponent(scientificName)}`;
  }

  // Format spectrogram URL - using detection ID for v2 API
  function getSpectrogramUrl(detection: Detection): string {
    if (detection.clipName) {
      return `/api/v1/media/spectrogram?clip=${encodeURIComponent(detection.clipName)}`;
    }
    // Fallback to using detection ID
    return `/api/v1/media/spectrogram?id=${detection.id}`;
  }

  // Format audio URL - using detection ID for v2 API
  function getAudioUrl(detection: Detection): string {
    if (detection.clipName) {
      return `/api/v1/media/audio?clip=${encodeURIComponent(detection.clipName)}`;
    }
    // Fallback to using detection ID
    return `/api/v1/media/audio?id=${detection.id}`;
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
  <div class="col-span-1 text-sm">
    {#if detection.weather}
      <div class="flex items-center gap-2">
        <WeatherIcon
          weatherIcon={detection.weather.weatherIcon}
          timeOfDay={detection.timeOfDay}
          size="sm"
        />
        <span class="text-xs text-base-content/70">
          {detection.weather.weatherMain || detection.weather.description || ''}
        </span>
      </div>
    {:else}
      <div class="text-base-content/50 text-xs">No weather data</div>
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
          aria-label="View details for {detection.commonName}"
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
  <div class="col-span-2">
    <StatusBadges {detection} />
  </div>

  <!-- Recording/Spectrogram -->
  <div class={showThumbnails ? 'col-span-2' : 'col-span-3'}>
    <div class="audio-player-container relative min-w-[50px] max-w-[200px]">
      <!-- Spectrogram Image -->
      <img
        loading="lazy"
        width="400"
        src={getSpectrogramUrl(detection)}
        alt="Spectrogram"
        class="w-full h-auto rounded-md object-contain"
        onerror={e => {
          const img = e.currentTarget as HTMLImageElement;
          img.onerror = null;
          img.src = '/assets/images/spectrogram-placeholder.svg';
        }}
      />

      <!-- Audio player placeholder overlay -->
      <div
        class="absolute bottom-0 left-0 right-0 bg-black bg-opacity-25 p-1 rounded-b-md transition-opacity duration-300 opacity-0 hover:opacity-100 hidden md:block"
      >
        <div class="flex items-center justify-center">
          <button
            class="text-white p-1 rounded-full hover:bg-white hover:bg-opacity-20"
            title="Audio player coming soon"
            aria-label="Play audio (coming soon)"
            disabled
          >
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"
              />
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
              />
            </svg>
          </button>
        </div>
      </div>
    </div>
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
