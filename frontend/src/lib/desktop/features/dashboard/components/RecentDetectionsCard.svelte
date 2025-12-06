<script lang="ts">
  import { untrack } from 'svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import StatusBadges from '$lib/desktop/components/data/StatusBadges.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import type { Detection } from '$lib/types/detection.types';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import { RefreshCw, XCircle } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { cn } from '$lib/utils/cn';

  const logger = loggers.ui;

  // Animation control - set to false to disable all animations
  const ENABLE_NEW_DETECTION_ANIMATIONS = false;

  interface Props {
    data: Detection[];
    loading?: boolean;
    error?: string | null;
    onRowClick?: (_detection: Detection) => void;
    onRefresh: () => void;
    limit?: number;
    // eslint-disable-next-line no-unused-vars
    onLimitChange?: (limit: number) => void;
    newDetectionIds?: Set<number>;
    detectionArrivalTimes?: Map<number, number>;
    onFreezeStart?: () => void;
    onFreezeEnd?: () => void;
    updatesAreFrozen?: boolean;
    className?: string;
  }

  let {
    data = [],
    loading = false,
    error = null,
    onRowClick,
    onRefresh,
    limit = 5,
    onLimitChange,
    newDetectionIds = new Set(),
    // eslint-disable-next-line no-unused-vars
    detectionArrivalTimes: _detectionArrivalTimes = new Map(), // Reserved for future staggered animations
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

    // Save to localStorage
    if (typeof window !== 'undefined') {
      try {
        localStorage.setItem('recentDetectionLimit', newLimit.toString());
      } catch (e) {
        logger.error('Failed to save detection limit:', e);
      }
    }

    // Call parent callback
    if (onLimitChange) {
      onLimitChange(newLimit);
    }
  }

  // Modal state for expanded audio player (removed - not currently used)

  // Handles clicking on a detection row to trigger parent callback
  function handleRowClick(detection: Detection) {
    if (onRowClick) {
      onRowClick(detection);
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
  // Navigate to detection detail page for review
  function handleReview(detection: Detection) {
    window.location.href = `/ui/detections/${detection.id}?tab=review`;
  }

  // Toggles whether a species should be ignored in future detections
  function handleToggleSpecies(detection: Detection) {
    const isExcluded = false; // TODO: determine if species is excluded
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
            headers: {
              'Content-Type': 'application/json',
            },
            body: JSON.stringify({
              common_name: detection.commonName,
            }),
          });
          onRefresh();
        } catch (error) {
          logger.error('Error toggling species exclusion:', error);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }

  // Toggles the lock status of a detection to prevent/allow automatic cleanup
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
            headers: {
              'Content-Type': 'application/json',
            },
            body: JSON.stringify({
              locked: !detection.locked,
            }),
          });
          onRefresh();
        } catch (error) {
          logger.error('Error toggling lock status:', error);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }

  // Permanently deletes a detection after user confirmation
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
        } catch (error) {
          logger.error('Error deleting detection:', error);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }
</script>

<section class={cn('card col-span-12 bg-base-100 shadow-sm', className)}>
  <!-- Card Header -->
  <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
    <div class="flex items-center justify-between mb-4">
      <div class="flex items-center gap-3">
        <span class="card-title grow text-base sm:text-xl"
          >{t('dashboard.recentDetections.title')}</span
        >
      </div>
      <div class="flex items-center gap-2">
        <label for="numDetections" class="label-text text-sm"
          >{t('dashboard.recentDetections.controls.show')}</label
        >
        <select
          id="numDetections"
          bind:value={selectedLimit}
          onchange={e => handleLimitChange(parseInt(e.currentTarget.value, 10))}
          class="select select-sm focus-visible:outline-none"
        >
          <option value={5}>5</option>
          <option value={10}>10</option>
          <option value={25}>25</option>
          <option value={50}>50</option>
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

    <!-- Content -->
    {#if error}
      <div class="alert alert-error">
        <XCircle class="size-6" />
        <span>{error}</span>
      </div>
    {:else}
      <!-- Desktop Layout -->
      <div class="relative">
        <!-- Loading overlay -->
        {#if loading}
          <div
            class="absolute inset-0 bg-base-100/80 z-20 flex items-center justify-center rounded-lg pointer-events-none"
          >
            <span class="loading loading-spinner loading-md"></span>
          </div>
          <!-- Disable pointer events on content during loading -->
          <div class="absolute inset-0 z-10" style:pointer-events="auto" style:cursor="wait"></div>
        {/if}

        <!-- Header Row -->
        <div class="detection-header-dashboard">
          <div>{t('dashboard.recentDetections.headers.dateTime')}</div>
          <div>{t('dashboard.recentDetections.headers.species')}</div>
          <div>{t('dashboard.recentDetections.headers.confidence')}</div>
          <div>{t('dashboard.recentDetections.headers.status')}</div>
          <div>{t('dashboard.recentDetections.headers.recording')}</div>
          <div>{t('dashboard.recentDetections.headers.actions')}</div>
        </div>

        <!-- Detection Rows -->
        <div class="divide-y divide-base-200">
          {#each data.slice(0, selectedLimit) as detection (detection.id)}
            {@const isNew = ENABLE_NEW_DETECTION_ANIMATIONS && newDetectionIds.has(detection.id)}
            <div
              class="detection-grid-dashboard detection-row"
              class:cursor-pointer={onRowClick}
              class:new-detection={isNew}
              role="button"
              tabindex="0"
              onclick={() => handleRowClick(detection)}
              onkeydown={e =>
                e.key === 'Enter' || e.key === ' ' ? handleRowClick(detection) : null}
            >
              <!-- Date & Time -->
              <div class="text-sm">
                <div class="text-xs">{detection.date} {detection.time}</div>
              </div>

              <!-- Combined Species Column with thumbnail -->
              <div class="sp-species-container sp-layout-dashboard">
                <!-- Thumbnail -->
                <div class="rd-thumbnail-wrapper">
                  <button
                    class="rd-thumbnail-button"
                    onclick={() => handleRowClick(detection)}
                    tabindex="-1"
                  >
                    <!-- Placeholder background that maintains aspect ratio -->
                    <div class="rd-thumbnail-placeholder"></div>
                    <img
                      src="/api/v2/media/species-image?name={encodeURIComponent(
                        detection.scientificName
                      )}"
                      alt={detection.commonName}
                      class="rd-thumbnail-image"
                      onerror={handleBirdImageError}
                      loading="lazy"
                    />
                  </button>
                </div>

                <!-- Species Names -->
                <div class="sp-species-info-wrapper">
                  <div class="sp-species-names">
                    <div class="sp-species-common-name">{detection.commonName}</div>
                    <div class="sp-species-scientific-name">{detection.scientificName}</div>
                  </div>
                </div>
              </div>

              <!-- Confidence -->
              <div>
                <ConfidenceCircle confidence={detection.confidence} size="md" />
              </div>

              <!-- Status -->
              <div>
                <StatusBadges {detection} size="sm" />
              </div>

              <!-- Recording -->
              <div onclick={e => e.stopPropagation()} role="presentation">
                <div class="rd-audio-player-container relative min-w-[50px]">
                  <AudioPlayer
                    audioUrl="/api/v2/audio/{detection.id}"
                    detectionId={detection.id.toString()}
                    showSpectrogram={true}
                    spectrogramSize="sm"
                    spectrogramRaw={true}
                    responsive={true}
                    className="w-full"
                    onPlayStart={onFreezeStart}
                    onPlayEnd={onFreezeEnd}
                    debug={false}
                  />
                </div>
              </div>

              <!-- Action Menu -->
              <div onclick={e => e.stopPropagation()} role="presentation">
                <ActionMenu
                  {detection}
                  isExcluded={false}
                  onReview={() => handleReview(detection)}
                  onToggleSpecies={() => handleToggleSpecies(detection)}
                  onToggleLock={() => handleToggleLock(detection)}
                  onDelete={() => handleDelete(detection)}
                  onMenuOpen={onFreezeStart}
                  onMenuClose={onFreezeEnd}
                />
              </div>
            </div>
          {/each}
        </div>
      </div>

      {#if data.length === 0}
        <div class="text-center py-8 text-base-content/60">
          {t('dashboard.recentDetections.noDetections')}
        </div>
      {/if}
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

<style>
  /* Use existing confidence circle styles from custom.css - no additional styles needed */

  /* RD prefix to avoid conflicts with global CSS */

  /* Dashboard-specific species container adjustments handled by shared CSS */

  /* Thumbnail wrapper - responsive width */
  .rd-thumbnail-wrapper {
    flex: 0 0 45%; /* Reduced to give more space to names */
    min-width: 40px; /* Minimum size on very small screens */
    max-width: 120px; /* Maximum size on large screens */
  }

  /* Thumbnail button - maintains aspect ratio */
  .rd-thumbnail-button {
    display: block;
    width: 100%;
    aspect-ratio: 4/3; /* Consistent aspect ratio */
    position: relative;
    overflow: hidden;
    border-radius: 0.375rem;
    background-color: oklch(var(--b2) / 0.3);
  }

  /* Thumbnail placeholder - animated skeleton */
  .rd-thumbnail-placeholder {
    position: absolute;
    top: 0;
    left: 0;
    width: 100%;
    height: 100%;
    background: linear-gradient(
      90deg,
      oklch(var(--b2) / 0.5) 0%,
      oklch(var(--b2) / 0.3) 50%,
      oklch(var(--b2) / 0.5) 100%
    );
    background-size: 200% 100%;
    animation: shimmer 1.5s infinite;
  }

  @keyframes shimmer {
    0% {
      background-position: 200% 0;
    }

    100% {
      background-position: -200% 0;
    }
  }

  /* Thumbnail image */
  .rd-thumbnail-image {
    position: absolute;
    top: 0;
    left: 0;
    width: 100%;
    height: 100%;
    object-fit: contain;

    /* Hide placeholder when image loads */
    z-index: 1;
    background-color: oklch(var(--b1));
  }

  /* Species display styles now handled by shared CSS (/lib/styles/species-display.css) */
  /* Dashboard-specific overrides below */

  /* RD Audio Player Container */
  .rd-audio-player-container {
    position: relative;
    width: 100%;
    min-height: var(--spectrogram-min-height, 60px); /* Fallback to 60px if var not defined */
    aspect-ratio: var(--spectrogram-aspect-ratio, 2 / 1); /* Fallback to 2:1 if var not defined */
    background: linear-gradient(to bottom, rgb(128 128 128 / 0.1), rgb(128 128 128 / 0.05));
    border-radius: 0.5rem;
    overflow: hidden; /* Contain the AudioPlayer content */
  }

  /* Ensure AudioPlayer fills container - using more specific selectors to avoid !important */
  .rd-audio-player-container :global(.group) {
    width: 100%;
    height: 100%;
    position: absolute;
    top: 0;
    left: 0;
  }

  /* Override any conflicting styles with higher specificity */
  .rd-audio-player-container > :global(div > .group) {
    width: 100%;
    height: 100%;
    position: absolute;
    top: 0;
    left: 0;
  }

  /* Responsive spectrogram sizing */
  .rd-audio-player-container :global(img) {
    object-fit: cover;
    height: 100%;
    width: 100%;

    /* Smooth fade-in for spectrogram to prevent flash */
    animation: fadeIn 0.3s ease-out;
  }

  @keyframes fadeIn {
    from {
      opacity: 0;
    }

    to {
      opacity: 1;
    }
  }

  /* Grid alignment - items-center is handled by Tailwind class */

  /* Detection row theme-aware styling with hover effects */
  .detection-row {
    border-bottom: 1px solid oklch(var(--bc) / 0.1);
    transition: background-color 0.15s ease-in-out;
  }

  /* New detection animations - very subtle highlight */
  .new-detection {
    animation: subtleHighlight 2s ease-out both;
    position: relative;
    z-index: 1; /* Lower than action menu */
  }

  @keyframes subtleHighlight {
    0% {
      opacity: 0.95;
      background-color: oklch(var(--p) / 0.03);
      border-left: 2px solid oklch(var(--p) / 0.15);
    }

    100% {
      opacity: 1;
      background-color: transparent;
      border-left: 2px solid transparent;
    }
  }

  /* Smooth transitions handled above in .detection-row */

  /* Respect reduced motion for placeholder animations */
  @media (prefers-reduced-motion: reduce) {
    .rd-thumbnail-placeholder,
    .rd-audio-player-container:not(:has(img)) {
      animation: none;
      background: oklch(var(--b2) / 0.4);
    }
  }
</style>
