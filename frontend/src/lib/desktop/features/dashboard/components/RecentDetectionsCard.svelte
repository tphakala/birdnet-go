<script lang="ts">
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import type { Detection } from '$lib/types/detection.types';

  interface Props {
    data: Detection[];
    loading?: boolean;
    error?: string | null;
    onRowClick?: (_detection: Detection) => void;
    onRefresh: () => void;
  }

  let { data = [], loading = false, error = null, onRowClick, onRefresh }: Props = $props();

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
</script>

<section class="card col-span-12 bg-base-100 shadow-sm">
  <!-- Card Header -->
  <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
    <div class="flex items-center justify-between mb-4">
      <span class="card-title grow text-base sm:text-xl">Recent Detections</span>
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
          class="grid grid-cols-12 gap-2 text-xs font-medium text-base-content/70 pb-2 border-b border-base-300"
        >
          <div class="col-span-2">Date & Time</div>
          <div class="col-span-2">Common Name</div>
          <div class="col-span-1">Confidence</div>
          <div class="col-span-2">Thumbnail</div>
          <div class="col-span-2">Status</div>
          <div class="col-span-2">Recording</div>
        </div>

        <!-- Detection Rows -->
        <div class="divide-y divide-base-200">
          {#each data as detection}
            <div
              class="grid grid-cols-12 gap-2 py-3 hover:bg-base-50 dark:hover:bg-base-200/50 transition-colors"
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

              <!-- Common Name -->
              <div class="col-span-2 text-sm font-medium">
                {detection.commonName}
                <div class="text-xs text-base-content/60">{detection.scientificName}</div>
              </div>

              <!-- Confidence -->
              <div class="col-span-1">
                <ConfidenceCircle confidence={detection.confidence} size="sm" />
              </div>

              <!-- Thumbnail -->
              <div class="col-span-2">
                <div class="thumbnail-container">
                  <img
                    src="/api/v2/species/{detection.speciesCode}/thumbnail"
                    alt={detection.commonName}
                    class="w-12 h-12 rounded object-cover"
                    onerror={handleImageError}
                  />
                </div>
              </div>

              <!-- Status -->
              <div class="col-span-2">
                {#if true}
                  {@const badge = getStatusBadge(detection.verified, detection.locked)}
                  <span class="status-badge {badge.class}">
                    {badge.text}
                  </span>
                {/if}
              </div>

              <!-- Recording -->
              <div class="col-span-2">
                <div class="flex items-center gap-2">
                  <AudioPlayer
                    audioUrl="/api/v2/audio/{detection.id}"
                    detectionId={detection.id.toString()}
                    width={140}
                    height={50}
                    showSpectrogram={true}
                    className="flex-1 max-w-[140px]"
                  />
                </div>
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
    display: inline-block;
  }

  /* Hover Effects */
  .grid:hover {
    transition: background-color 0.15s ease-in-out;
  }
</style>
