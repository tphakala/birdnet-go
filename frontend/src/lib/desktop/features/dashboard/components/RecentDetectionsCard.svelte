<script lang="ts">
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import ReviewModal from '$lib/desktop/components/modals/ReviewModal.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import type { Detection } from '$lib/types/detection.types';

  interface Props {
    data: Detection[];
    loading?: boolean;
    error?: string | null;
    onRowClick?: (_detection: Detection) => void;
    onRefresh: () => void;
    limit?: number;
    onLimitChange?: (limit: number) => void;
    connectionStatus?: 'connecting' | 'connected' | 'error' | 'polling';
  }

  let { data = [], loading = false, error = null, onRowClick, onRefresh, limit = 10, onLimitChange, connectionStatus = 'polling' }: Props = $props();

  // State for number of detections to show
  let selectedLimit = $state(limit);

  // Load saved limit from localStorage on component mount
  if (typeof window !== 'undefined') {
    const savedLimit = localStorage.getItem('recentDetectionLimit');
    if (savedLimit) {
      selectedLimit = parseInt(savedLimit, 10);
    }
  }

  // Update selectedLimit when prop changes
  $effect(() => {
    selectedLimit = limit;
  });

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

  // Modal state for expanded audio player
  let expandedDetection = $state<Detection | null>(null);
  let showExpandedPlayer = $state(false);

  function showExpandedAudioPlayer(detection: Detection, event?: Event) {
    if (event) {
      event.stopPropagation(); // Prevent row click
    }
    expandedDetection = detection;
    showExpandedPlayer = true;
  }

  function closeExpandedPlayer() {
    showExpandedPlayer = false;
    expandedDetection = null;
  }

  function handleRowClick(detection: Detection) {
    if (onRowClick) {
      onRowClick(detection);
    }
  }

  function getStatusBadge(verified: string, locked: boolean) {
    if (locked) {
      return { type: 'locked', text: 'Locked', class: 'status-badge-locked' };
    }

    switch (verified) {
      case 'correct':
        return { type: 'correct', text: 'Verified', class: 'status-badge-correct' };
      case 'false_positive':
        return { type: 'false', text: 'False', class: 'status-badge-false' };
      default:
        return { type: 'unverified', text: 'Unverified', class: 'status-badge-unverified' };
    }
  }

  // Helper function to handle image error
  function handleImageError(e: Event) {
    const target = e.currentTarget as globalThis.HTMLImageElement;
    target.src = '/assets/images/bird-placeholder.svg';
  }

  // Modal states
  let showReviewModal = $state(false);
  let showConfirmModal = $state(false);
  let selectedDetection = $state<Detection | null>(null);
  let confirmModalConfig = $state({
    title: '',
    message: '',
    confirmLabel: 'Confirm',
    onConfirm: () => {},
  });

  // Action handlers
  function handleReview(detection: Detection) {
    selectedDetection = detection;
    showReviewModal = true;
  }

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

  // Helper to get connection status display info
  function getConnectionStatusInfo(status: string) {
    switch (status) {
      case 'connected':
        return { 
          text: 'Live', 
          color: 'text-green-600', 
          icon: '●',
          title: 'Real-time updates active'
        };
      case 'connecting':
        return { 
          text: 'Connecting', 
          color: 'text-yellow-600', 
          icon: '●',
          title: 'Connecting to real-time updates'
        };
      case 'error':
        return { 
          text: 'Disconnected', 
          color: 'text-red-600', 
          icon: '●',
          title: 'Real-time connection failed'
        };
      case 'polling':
        return { 
          text: 'Polling', 
          color: 'text-blue-600', 
          icon: '⟳',
          title: 'Using 30-second refresh polling'
        };
      default:
        return { 
          text: 'Unknown', 
          color: 'text-gray-600', 
          icon: '?',
          title: 'Connection status unknown'
        };
    }
  }
</script>

<section class="card col-span-12 bg-base-100 shadow-sm">
  <!-- Card Header -->
  <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
    <div class="flex items-center justify-between mb-4">
      <div class="flex items-center gap-3">
        <span class="card-title grow text-base sm:text-xl">Recent Detections</span>
        {#snippet connectionIndicator()}
          {@const statusInfo = getConnectionStatusInfo(connectionStatus)}
          <div 
            class="flex items-center gap-1 text-xs {statusInfo.color}"
            title={statusInfo.title}
          >
            <span class="text-sm">{statusInfo.icon}</span>
            <span>{statusInfo.text}</span>
          </div>
        {/snippet}
        {@render connectionIndicator()}
      </div>
      <div class="flex items-center gap-2">
        <label for="numDetections" class="label-text text-sm">Show:</label>
        <select
          id="numDetections"
          bind:value={selectedLimit}
          onchange={(e) => handleLimitChange(parseInt(e.currentTarget.value, 10))}
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
            <div
              class="grid grid-cols-12 gap-4 items-center px-4 py-1 hover:bg-base-50 dark:hover:bg-base-200/50 transition-colors"
              class:cursor-pointer={onRowClick}
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
                  <img
                    src="/api/v2/species/{detection.speciesCode}/thumbnail"
                    alt={detection.commonName}
                    class="w-full h-auto rounded-md object-contain max-h-16"
                    onerror={handleImageError}
                  />
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
                    responsive={true}
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



<style>
  /* Status Badge Styles */
  .status-badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0.25rem 0.75rem;
    border-radius: 9999px;
    font-size: 0.75rem;
    font-weight: 500;
    white-space: nowrap;
    border: 1px solid;
  }

  .status-badge-unverified {
    color: #6b7280;
    background-color: #f3f4f6;
    border-color: #d1d5db;
  }

  .status-badge-correct {
    color: #059669;
    background-color: #ecfdf5;
    border-color: #a7f3d0;
  }

  .status-badge-false {
    color: #dc2626;
    background-color: #fef2f2;
    border-color: #fecaca;
  }

  .status-badge-locked {
    color: #d97706;
    background-color: #fffbeb;
    border-color: #fed7aa;
  }

  /* Dark theme status badges */
  :global([data-theme='dark']) .status-badge-unverified {
    color: #9ca3af;
    background-color: rgba(107, 114, 128, 0.1);
    border-color: rgba(107, 114, 128, 0.3);
  }

  :global([data-theme='dark']) .status-badge-correct {
    color: #10b981;
    background-color: rgba(5, 150, 105, 0.1);
    border-color: rgba(5, 150, 105, 0.3);
  }

  :global([data-theme='dark']) .status-badge-false {
    color: #ef4444;
    background-color: rgba(220, 38, 38, 0.1);
    border-color: rgba(220, 38, 38, 0.3);
  }

  :global([data-theme='dark']) .status-badge-locked {
    color: #f59e0b;
    background-color: rgba(217, 119, 6, 0.1);
    border-color: rgba(217, 119, 6, 0.3);
  }

  /* Confidence Circle */
  .confidence-circle {
    width: 42px;
    height: 42px;
    position: relative;
    border-radius: 50%;
    background: var(--lighter-color, #f3f4f6);
    display: flex;
    align-items: center;
    justify-content: center;
    border: 2px solid;
  }

  .confidence-circle.confidence-high {
    --lighter-color: #ecfdf5;
    border-color: #059669;
    color: #059669;
  }

  .confidence-circle.confidence-medium {
    --lighter-color: #fffbeb;
    border-color: #d97706;
    color: #d97706;
  }

  .confidence-circle.confidence-low {
    --lighter-color: #fef2f2;
    border-color: #dc2626;
    color: #dc2626;
  }

  .confidence-text {
    font-size: 0.75rem;
    font-weight: 600;
    line-height: 1;
  }

  /* Thumbnail Container */
  .thumbnail-container {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 60px;
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

  /* Row styling for better height management */
  .grid {
    align-items: center;
  }

  /* Hover Effects */
  .grid:hover {
    transition: background-color 0.15s ease-in-out;
  }

  /* Improved spacing */
  .detection-row {
    padding: 0.5rem 1rem;
    border-bottom: 1px solid var(--border-color, #e5e7eb);
  }

  .detection-row:hover {
    background-color: var(--hover-bg, #f9fafb);
  }
</style>

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
