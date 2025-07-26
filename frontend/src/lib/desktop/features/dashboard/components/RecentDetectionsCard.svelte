<script lang="ts">
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import StatusBadges from '$lib/desktop/components/data/StatusBadges.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import ReviewModal from '$lib/desktop/components/modals/ReviewModal.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import type { Detection } from '$lib/types/detection.types';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import { actionIcons, alertIconsSvg } from '$lib/utils/icons';
  import { t } from '$lib/i18n';

  interface Props {
    data: Detection[];
    loading?: boolean;
    error?: string | null;
    onRowClick?: (_detection: Detection) => void;
    onRefresh: () => void;
    limit?: number;
    onLimitChange?: (limit: number) => void;
    newDetectionIds?: Set<number>;
    detectionArrivalTimes?: Map<number, number>;
    isRefreshEnabled?: boolean;
    onActionMenuStatusChange?: (isOpen: boolean) => void;
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
    detectionArrivalTimes: _detectionArrivalTimes = new Map(), // Reserved for future staggered animations
    isRefreshEnabled = true,
    onActionMenuStatusChange,
  }: Props = $props();

  // State for number of detections to show
  let selectedLimit = $state(limit);

  // Track open action menus
  let openActionMenus = $state(new Set<number>());

  // Use derived state for menu open status
  let menuOpenStatus = $derived(openActionMenus.size > 0);

  // Notify parent when menus open/close
  $effect(() => {
    console.log('RecentDetectionsCard: Menu open status changed to', menuOpenStatus);
    onActionMenuStatusChange?.(menuOpenStatus);
  });

  // Update selectedLimit when prop changes
  $effect(() => {
    selectedLimit = limit;
  });

  // Updates the number of detections to display and persists the preference
  function handleLimitChange(newLimit: number) {
    selectedLimit = newLimit;

    // Save to localStorage
    if (typeof window !== 'undefined') {
      try {
        localStorage.setItem('recentDetectionLimit', newLimit.toString());
      } catch (e) {
        console.error('Failed to save detection limit:', e);
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
  let showReviewModal = $state(false);
  let showConfirmModal = $state(false);
  let selectedDetection = $state<Detection | null>(null);
  let confirmModalConfig = $state({
    title: '',
    message: '',
    confirmLabel: 'Confirm',
    onConfirm: async () => {},
  });

  // Action handlers
  // Opens the review modal for manual verification of a detection
  function handleReview(detection: Detection) {
    selectedDetection = detection;
    showReviewModal = true;
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
          console.error('Error toggling species exclusion:', error);
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
          console.error('Error toggling lock status:', error);
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
          console.error('Error deleting detection:', error);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }
</script>

<section class="card col-span-12 bg-base-100 shadow-sm">
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
          disabled={loading || menuOpenStatus}
          aria-label={t('dashboard.recentDetections.controls.refresh')}
          title={menuOpenStatus ? 'Refresh disabled while action menu is open' : ''}
        >
          <div class="h-4 w-4" class:animate-spin={loading}>
            {@html actionIcons.refresh}
          </div>
        </button>
      </div>
    </div>

    <!-- Content -->
    {#if error}
      <div class="alert alert-error">
        {@html alertIconsSvg.error}
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
          {#each data.slice(0, selectedLimit) as detection}
            {@const isNew = newDetectionIds.has(detection.id)}
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
              <div>
                <div class="rd-audio-player-container relative min-w-[50px]">
                  <AudioPlayer
                    audioUrl="/api/v2/audio/{detection.id}"
                    detectionId={detection.id.toString()}
                    showSpectrogram={true}
                    className="w-full"
                  />
                </div>
              </div>

              <!-- Action Menu -->
              <div>
                <ActionMenu
                  {detection}
                  isExcluded={false}
                  onReview={() => handleReview(detection)}
                  onToggleSpecies={() => handleToggleSpecies(detection)}
                  onToggleLock={() => handleToggleLock(detection)}
                  onDelete={() => handleDelete(detection)}
                  onOpenChange={isOpen => {
                    console.log(
                      `ActionMenu for detection ${detection.id} is now ${isOpen ? 'open' : 'closed'}`
                    );
                    if (isOpen) {
                      openActionMenus.add(detection.id);
                    } else {
                      openActionMenus.delete(detection.id);
                    }
                    // Force reactivity update
                    openActionMenus = openActionMenus;
                  }}
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
  <ReviewModal
    isOpen={showReviewModal}
    detection={selectedDetection}
    isExcluded={false}
    onClose={() => {
      showReviewModal = false;
      selectedDetection = null;
    }}
    onSave={async (verified, lockDetection, ignoreSpecies, comment) => {
      if (!selectedDetection) return;

      await fetchWithCSRF(`/api/v2/detections/${selectedDetection.id}/review`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          verified,
          lock_detection: lockDetection,
          ignore_species: ignoreSpecies ? selectedDetection.commonName : null,
          comment,
        }),
      });
      onRefresh();
      showReviewModal = false;
      selectedDetection = null;
    }}
  />

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
    background: linear-gradient(to bottom, rgb(128, 128, 128, 0.4), rgb(128, 128, 128, 0.1));
    border-radius: 0.5rem;
  }

  /* Audio player skeleton (before content loads) */
  .rd-audio-player-container::before {
    content: '';
    width: 1px;
    margin-left: -1px;
    float: left;
    height: 0;
    padding-top: 50%; /* Maintains a 2:1 ratio */
  }

  .rd-audio-player-container::after {
    content: '';
    display: table;
    clear: both;
  }

  /* Add shimmer effect to audio player container while loading */
  .rd-audio-player-container:not(:has(img)) {
    background: linear-gradient(
      90deg,
      oklch(var(--b2) / 0.4) 0%,
      oklch(var(--b2) / 0.2) 50%,
      oklch(var(--b2) / 0.4) 100%
    );
    background-size: 200% 100%;
    animation: shimmer 1.5s infinite;
  }

  /* Ensure AudioPlayer fills container width */
  .rd-audio-player-container :global(.group) {
    width: 100% !important;
    height: auto !important;
  }

  /* Responsive spectrogram sizing - let it maintain natural aspect ratio */
  .rd-audio-player-container :global(img) {
    object-fit: contain !important;
    height: auto !important;
    width: 100% !important;
    max-width: 400px;
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
