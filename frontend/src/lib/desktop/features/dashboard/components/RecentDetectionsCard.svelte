<script lang="ts">
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import ReviewModal from '$lib/desktop/components/modals/ReviewModal.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import type { Detection } from '$lib/types/detection.types';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';

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
    detectionArrivalTimes = new Map(), // Reserved for future staggered animations
  }: Props = $props();

  // State for number of detections to show
  let selectedLimit = $state(limit);

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

  // Returns appropriate badge configuration based on detection verification and lock status
  function getStatusBadge(verified: string, locked: boolean) {
    if (locked) {
      return { type: 'locked', text: 'Locked', class: 'status-badge locked' };
    }

    switch (verified) {
      case 'correct':
        return { type: 'correct', text: 'Verified', class: 'status-badge correct' };
      case 'false_positive':
        return { type: 'false', text: 'False', class: 'status-badge false' };
      default:
        return { type: 'unverified', text: 'Unverified', class: 'status-badge unverified' };
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
      title: `Delete Detection of ${detection.commonName}`,
      message: `Are you sure you want to delete detection of ${detection.commonName}? This action cannot be undone.`,
      confirmLabel: 'Delete',
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
        <span class="card-title grow text-base sm:text-xl">Recent Detections</span>
      </div>
      <div class="flex items-center gap-2">
        <label for="numDetections" class="label-text text-sm">Show:</label>
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
          disabled={loading}
          aria-label="Refresh detections"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-4 w-4"
            class:animate-spin={loading}
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
            />
          </svg>
        </button>
      </div>
    </div>

    <!-- Content -->
    {#if loading}
      <div class="flex justify-center py-8">
        <span class="loading loading-spinner loading-md"></span>
      </div>
    {:else if error}
      <div class="alert alert-error">
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="stroke-current shrink-0 h-6 w-6"
          fill="none"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z"
          />
        </svg>
        <span>{error}</span>
      </div>
    {:else}
      <!-- Desktop Layout -->
      <div>
        <!-- Header Row -->
        <div
          class="grid grid-cols-12 gap-4 text-xs font-medium text-base-content/70 pb-2 border-b border-base-300 px-4"
        >
          <div class="col-span-2">Date & Time</div>
          <div class="col-span-2">Common Name</div>
          <div class="col-span-2">Thumbnail</div>
          <div class="col-span-2">Status</div>
          <div class="col-span-3">Recording</div>
          <div class="col-span-1">Actions</div>
        </div>

        <!-- Detection Rows -->
        <div class="divide-y divide-base-200">
          {#each data.slice(0, selectedLimit) as detection}
            {@const badge = getStatusBadge(detection.verified, detection.locked)}
            {@const isNew = newDetectionIds.has(detection.id)}
            <div
              class="grid grid-cols-12 gap-4 items-center px-4 py-1 hover:bg-base-200/30 transition-colors detection-row"
              class:cursor-pointer={onRowClick}
              class:new-detection={isNew}
              style=""
              role="button"
              tabindex="0"
              onclick={() => handleRowClick(detection)}
              onkeydown={e =>
                e.key === 'Enter' || e.key === ' ' ? handleRowClick(detection) : null}
            >
              <!-- Date & Time -->
              <div class="col-span-2 text-sm">
                <div class="text-xs">{detection.date} {detection.time}</div>
              </div>

              <!-- Common Name with Confidence -->
              <div class="col-span-2 text-sm">
                <div class="flex flex-col items-center gap-2">
                  <ConfidenceCircle confidence={detection.confidence} size="sm" />
                  <div class="text-center">
                    <div class="font-medium hover:text-blue-600 cursor-pointer">
                      {detection.commonName}
                    </div>
                    <div class="text-xs text-base-content/60">{detection.scientificName}</div>
                  </div>
                </div>
              </div>

              <!-- Thumbnail -->
              <div class="col-span-2 relative flex items-center">
                <div class="thumbnail-container w-full">
                  <button
                    class="flex items-center justify-center"
                    onclick={() => handleRowClick(detection)}
                    tabindex="-1"
                  >
                    <img
                      loading="lazy"
                      src="/api/v2/media/species-image?name={encodeURIComponent(detection.scientificName)}"
                      alt={detection.commonName}
                      class="w-full h-auto rounded-md object-contain"
                      onerror={handleBirdImageError}
                    />
                  </button>
                  <div class="thumbnail-tooltip hidden">
                    <!-- TODO: Add thumbnail attribution when available -->
                  </div>
                </div>
              </div>

              <!-- Status -->
              <div class="col-span-2">
                <div class="flex flex-wrap gap-1">
                  <span class="status-badge {badge.class}">
                    {badge.text}
                  </span>
                </div>
              </div>

              <!-- Recording -->
              <div class="col-span-3">
                <div class="audio-player-container relative min-w-[50px]">
                  <AudioPlayer
                    audioUrl="/api/v2/audio/{detection.id}"
                    detectionId={detection.id.toString()}
                    showSpectrogram={true}
                    className="w-full h-auto"
                  />
                </div>
              </div>

              <!-- Action Menu -->
              <div class="col-span-1 flex justify-end">
                <ActionMenu
                  {detection}
                  isExcluded={false}
                  onReview={() => handleReview(detection)}
                  onToggleSpecies={() => handleToggleSpecies(detection)}
                  onToggleLock={() => handleToggleLock(detection)}
                  onDelete={() => handleDelete(detection)}
                />
              </div>
            </div>
          {/each}
        </div>
      </div>

      {#if data.length === 0}
        <div class="text-center py-8 text-base-content/60">No recent detections</div>
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

  /* Thumbnail Container - matches original template styling */
  .thumbnail-container {
    position: relative;
    display: inline-block;
  }

  /* Audio Player Container */
  .audio-player-container {
    position: relative;
    width: 100%;
  }

  /* Ensure AudioPlayer fills container width */
  .audio-player-container :global(.group) {
    width: 100% !important;
    height: auto !important;
  }

  /* Responsive spectrogram sizing - let it maintain natural aspect ratio */
  .audio-player-container :global(img) {
    object-fit: contain !important;
    height: auto !important;
    width: 100% !important;
    max-width: 400px;
  }

  /* Grid alignment - items-center is handled by Tailwind class */

  /* Detection row theme-aware styling with hover effects */
  .detection-row {
    border-bottom: 1px solid hsl(var(--bc) / 0.1);
    transition:
      transform 0.3s ease-out,
      background-color 0.15s ease-in-out;
  }

  /* New detection animations - theme-aware fade-in */
  .new-detection {
    animation: slideInFade 0.8s cubic-bezier(0.25, 0.46, 0.45, 0.94) both;
  }

  @keyframes slideInFade {
    0% {
      transform: translateY(-30px);
      opacity: 0;
      background-color: hsl(var(--p) / 0.2);
      border-left: 4px solid hsl(var(--p));
    }
    50% {
      background-color: hsl(var(--p) / 0.15);
      border-left: 4px solid hsl(var(--p));
    }
    100% {
      transform: translateY(0);
      opacity: 1;
      background-color: transparent;
      border-left: none;
    }
  }

  /* Smooth transitions handled above in .detection-row */
</style>
