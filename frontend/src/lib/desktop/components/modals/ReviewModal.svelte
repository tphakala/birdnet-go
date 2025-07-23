<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import type { Detection } from '$lib/types/detection.types';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { alertIcons, navigationIcons } from '$lib/utils/icons';

  interface Props {
    isOpen: boolean;
    detection: Detection | null;
    isExcluded?: boolean;
    onClose: () => void;
    onSave?: (
      _verified: 'correct' | 'false_positive',
      _lockDetection: boolean,
      _ignoreSpecies: boolean,
      _comment: string
    ) => Promise<void>;
  }

  let { isOpen = false, detection = null, isExcluded = false, onClose, onSave }: Props = $props();

  let reviewStatus = $state<'correct' | 'false_positive'>('correct');
  let lockDetection = $state(false);
  let ignoreSpecies = $state(false);
  let comment = $state('');
  let isLoading = $state(false);
  let errorMessage = $state<string | null>(null);

  // Reset form when detection changes
  $effect(() => {
    if (detection) {
      const verified = detection.review?.verified;
      reviewStatus = verified === 'correct' || verified === 'false_positive' ? verified : 'correct';
      lockDetection = detection.locked || false;
      ignoreSpecies = isExcluded;
      comment = detection.comments?.[0]?.entry || '';
    }
  });

  async function handleSave() {
    if (!detection || isLoading) return;

    isLoading = true;
    errorMessage = null; // Clear any previous error
    try {
      if (onSave) {
        await onSave(reviewStatus, lockDetection, ignoreSpecies, comment);
      } else {
        // Default save implementation
        await fetchWithCSRF('/api/v2/detections/review', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({
            id: detection.id,
            verified: reviewStatus,
            lock_detection: lockDetection,
            ignore_species: ignoreSpecies ? detection.commonName : null,
            comment: comment,
          }),
        });
      }
      onClose();
    } catch (error) {
      console.error('Error saving review:', error);
      errorMessage =
        error instanceof Error ? error.message : 'Failed to save review. Please try again.';
    } finally {
      isLoading = false;
    }
  }

  function getStatusBadgeClass(verified?: string): string {
    switch (verified) {
      case 'correct':
        return 'badge-success';
      case 'false_positive':
        return 'badge-error';
      default:
        return 'badge-ghost';
    }
  }

  function getStatusText(verified?: string): string {
    switch (verified) {
      case 'correct':
        return 'Verified Correct';
      case 'false_positive':
        return 'False Positive';
      default:
        return 'Not Reviewed';
    }
  }
</script>

<Modal
  {isOpen}
  title={`Review Detection: ${detection?.commonName || ''}`}
  size="lg"
  showCloseButton={true}
  {onClose}
  className="modal-bottom sm:modal-middle"
>
  {#snippet children()}
    {#if detection}
      <!-- Error message display -->
      {#if errorMessage}
        <div class="alert alert-error mb-4">
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
              d={alertIcons.error}
            />
          </svg>
          <span>{errorMessage}</span>
        </div>
      {/if}

      <div class="space-y-4">
        <!-- Species info -->
        <div class="flex items-center gap-2">
          <p class="text-sm text-base-content/70">{detection.scientificName}</p>
          <span class={`badge gap-1 ${getStatusBadgeClass(detection.review?.verified)}`}>
            {#if detection.review?.verified === 'correct'}
              <svg
                xmlns="http://www.w3.org/2000/svg"
                fill="none"
                viewBox="0 0 24 24"
                stroke-width="2"
                stroke="currentColor"
                class="w-3 h-3"
              >
                <path stroke-linecap="round" stroke-linejoin="round" d={alertIcons.success} />
              </svg>
            {:else if detection.review?.verified === 'false_positive'}
              <div class="w-3 h-3">
                {@html navigationIcons.close}
              </div>
            {/if}
            {getStatusText(detection.review?.verified)}
          </span>
        </div>

        <!-- Audio and Spectrogram -->
        <div class="mb-4">
          {#if detection.clipName}
            <div class="relative">
              <img
                loading="lazy"
                src={`/api/v2/media/spectrogram?clip=${detection.clipName}`}
                alt="Spectrogram"
                class="w-full h-auto rounded-md shadow-sm"
              />
            </div>
          {:else}
            <div class="alert">
              <svg
                xmlns="http://www.w3.org/2000/svg"
                fill="none"
                viewBox="0 0 24 24"
                class="stroke-info shrink-0 w-6 h-6"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d={alertIcons.info}
                ></path>
              </svg>
              <span>No audio recording available for this detection.</span>
            </div>
          {/if}
        </div>

        <!-- Review Form -->
        <div class="form-control">
          <label class="label cursor-pointer justify-start gap-4">
            <input
              type="radio"
              name="verified"
              value="correct"
              bind:group={reviewStatus}
              class="radio radio-primary radio-xs"
              disabled={detection.locked}
            />
            <span class="label-text">Correct Detection</span>
          </label>
          <label class="label cursor-pointer justify-start gap-4">
            <input
              type="radio"
              name="verified"
              value="false_positive"
              bind:group={reviewStatus}
              class="radio radio-primary radio-xs"
              disabled={detection.locked}
            />
            <span class="label-text">False Positive</span>
          </label>

          {#if detection.locked}
            <div class="text-sm text-base-content mt-2">
              <svg
                xmlns="http://www.w3.org/2000/svg"
                class="inline-block w-4 h-4 mr-1"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d={alertIcons.warning}
                />
              </svg>
              This detection is locked. You must unlock it first to change its review status.
            </div>
          {/if}
        </div>

        <!-- Lock Detection (only show when correct) -->
        {#if reviewStatus === 'correct'}
          <div class="form-control">
            <label class="label cursor-pointer justify-start gap-4">
              <input
                type="checkbox"
                bind:checked={lockDetection}
                class="checkbox checkbox-primary checkbox-xs"
              />
              <span class="label-text">Lock this detection</span>
            </label>
            <div class="text-sm text-base-content/70 ml-8">
              Locking this detection will prevent it from being deleted during regular cleanup.
            </div>
          </div>
        {/if}

        <!-- Ignore Species (only show when false positive and not locked) -->
        {#if !detection.locked && reviewStatus === 'false_positive'}
          <div class="form-control">
            <label class="label cursor-pointer justify-start gap-4">
              <input
                type="checkbox"
                bind:checked={ignoreSpecies}
                class="checkbox checkbox-primary checkbox-xs"
              />
              <span class="label-text">Ignore this species</span>
            </label>
            <div class="text-sm text-base-content/70 ml-8">
              Ignoring this species will prevent future detections of this species. This will not
              remove existing detections.
            </div>
          </div>
        {/if}

        <!-- Comment -->
        <div class="form-control">
          <label class="label" for="comment-textarea">
            <span class="label-text">Comment</span>
          </label>
          <textarea
            id="comment-textarea"
            bind:value={comment}
            class="textarea textarea-bordered h-24"
            placeholder="Add a comment"
          ></textarea>
        </div>
      </div>
    {/if}
  {/snippet}

  {#snippet footer()}
    <button type="button" class="btn" onclick={onClose} disabled={isLoading}> Cancel </button>
    <button
      type="button"
      class="btn btn-primary"
      onclick={handleSave}
      disabled={isLoading || !detection}
    >
      {#if isLoading}
        <span class="loading loading-spinner loading-sm"></span>
      {/if}
      Save Review
    </button>
  {/snippet}
</Modal>
