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
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import StatusBadges from '$lib/desktop/components/data/StatusBadges.svelte';
  import WeatherMetrics from '$lib/desktop/components/data/WeatherMetrics.svelte';
  import { Volume2 } from '@lucide/svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import { t } from '$lib/i18n';
  import type { Detection } from '$lib/types/detection.types';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { useImageDelayedLoading } from '$lib/utils/delayedLoading.svelte.js';
  import { loggers } from '$lib/utils/logger';
  import { navigation } from '$lib/stores/navigation.svelte';

  const logger = loggers.ui;

  interface Props {
    detection: Detection;
    isExcluded?: boolean;
    onDetailsClick?: (_id: number) => void;
    onRefresh?: () => void;
    onPlayMobileAudio?: (_payload: {
      audioUrl: string;
      speciesName: string;
      detectionId: number;
    }) => void;
  }

  let {
    detection,
    isExcluded = false,
    onDetailsClick,
    onRefresh,
    onPlayMobileAudio,
  }: Props = $props();

  // Modal states
  let showConfirmModal = $state(false);
  let confirmModalConfig = $state({
    title: '',
    message: '',
    confirmLabel: 'Confirm',
    onConfirm: () => {},
  });

  // Thumbnail loading with delayed spinner and URL failure tracking
  const thumbnailLoader = useImageDelayedLoading({
    delayMs: 150,
    timeoutMs: 10000,
    onTimeout: () => {
      logger.warn('Thumbnail loading timeout', {
        scientificName: detection.scientificName,
        detectionId: detection.id,
      });
    },
  });

  function handleDetailsClick(e: Event) {
    e.preventDefault();
    if (onDetailsClick) {
      onDetailsClick(detection.id);
    } else {
      // Default navigation to detection detail page
      navigation.navigate(`/ui/detections/${detection.id}`);
    }
  }

  // Action handlers
  function handleReview() {
    navigation.navigate(`/ui/detections/${detection.id}?tab=review`);
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

  // Thumbnail loading handlers
  function handleThumbnailLoad() {
    thumbnailLoader.setLoading(false);
  }

  function handleThumbnailError() {
    const currentUrl = getThumbnailUrl(detection.scientificName);
    thumbnailLoader.markUrlFailed(currentUrl);
  }

  // Track previous URL to avoid unnecessary resets
  let previousThumbnailUrl = $state<string | null>(null);

  // Handle thumbnail loading state when detection changes
  $effect(() => {
    const currentUrl = getThumbnailUrl(detection.scientificName);
    // Only reset loading state if URL actually changed
    if (detection.scientificName && currentUrl !== previousThumbnailUrl) {
      previousThumbnailUrl = currentUrl;

      // Check if this URL has previously failed to prevent retry loops
      if (thumbnailLoader.hasUrlFailed(currentUrl)) {
        thumbnailLoader.setError();
        return; // Don't try to load known failed URLs
      }

      // Start loading when URL changes
      thumbnailLoader.setLoading(true);
    }
  });

  // Cleanup is handled automatically by useImageDelayedLoading
  function playMobileAudio() {
    const audioUrl = `/api/v2/audio/${detection.id}`;
    onPlayMobileAudio?.({ audioUrl, speciesName: detection.commonName, detectionId: detection.id });
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
    <div class="text-base-content opacity-50 text-xs">
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
        <!-- Screen reader announcement for loading state -->
        <span class="sr-only" role="status" aria-live="polite">
          {thumbnailLoader.loading
            ? `Loading ${detection.commonName} thumbnail...`
            : `${detection.commonName} thumbnail loaded`}
        </span>

        <!-- Loading spinner overlay -->
        {#if thumbnailLoader.showSpinner}
          <div class="absolute inset-0 flex items-center justify-center bg-base-200/75 rounded-md">
            <div class="loading loading-spinner loading-sm text-primary"></div>
          </div>
        {/if}

        {#if thumbnailLoader.error}
          <!-- Error placeholder -->
          <div class="absolute inset-0 flex items-center justify-center bg-base-200 rounded-md">
            <svg
              class="w-8 h-8 text-base-content opacity-30"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
              xmlns="http://www.w3.org/2000/svg"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z"
              />
            </svg>
            <span class="sr-only">Image failed to load</span>
          </div>
        {:else if !thumbnailLoader.hasUrlFailed(getThumbnailUrl(detection.scientificName))}
          <!-- Only render img element if URL hasn't failed before -->
          <img
            loading="lazy"
            decoding="async"
            fetchpriority="low"
            src={getThumbnailUrl(detection.scientificName)}
            alt={detection.commonName}
            class="sp-thumbnail-image"
            class:opacity-0={thumbnailLoader.loading}
            onload={handleThumbnailLoad}
            onerror={e => {
              handleThumbnailError();
              handleBirdImageError(e);
            }}
          />
        {/if}
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
      <!-- Mobile-only quick play button -->
      <div class="mt-2 md:hidden">
        <button class="btn btn-primary btn-xs" aria-label="Play audio" onclick={playMobileAudio}>
          <Volume2 class="h-4 w-4" />
          Play
        </button>
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
  <div class="dr-audio-player-container">
    <AudioPlayer
      audioUrl={`/api/v2/audio/${detection.id}`}
      detectionId={detection.id.toString()}
      showSpectrogram={true}
      showDownload={true}
      spectrogramSize="sm"
      spectrogramRaw={true}
      responsive={true}
      className="w-full"
    />
  </div>
</td>

<!-- Action Menu -->
<td onclick={e => e.stopPropagation()}>
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

  /* DR Audio Player Container - 2:1 aspect ratio matching spectrogram dimensions */
  .dr-audio-player-container {
    position: relative;
    width: 100%;
    max-width: 200px; /* Constrain maximum width in table */
    min-height: var(--spectrogram-min-height, 60px); /* Fallback to 60px if var not defined */
    aspect-ratio: var(--spectrogram-aspect-ratio, 2 / 1); /* Fallback to 2:1 if var not defined */
    background: linear-gradient(to bottom, rgb(128 128 128 / 0.1), rgb(128 128 128 / 0.05));
    border-radius: 0.5rem;
    overflow: hidden; /* Contain the AudioPlayer content */
  }

  /* Ensure AudioPlayer fills container - using more specific selectors to avoid !important */
  .dr-audio-player-container :global(.group) {
    width: 100%;
    height: 100%;
    position: absolute;
    top: 0;
    left: 0;
  }

  /* Override any conflicting styles with higher specificity */
  .dr-audio-player-container > :global(div > .group) {
    width: 100%;
    height: 100%;
    position: absolute;
    top: 0;
    left: 0;
  }

  /* Responsive spectrogram sizing */
  .dr-audio-player-container :global(img) {
    object-fit: cover;
    height: 100%;
    width: 100%;
  }

  /* Higher specificity for image styles if needed */
  .dr-audio-player-container :global(.group img),
  .dr-audio-player-container :global(div img) {
    object-fit: cover;
    height: 100%;
    width: 100%;
  }
</style>
