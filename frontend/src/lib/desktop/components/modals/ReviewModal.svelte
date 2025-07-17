<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import type { Detection } from '$lib/types/detection.types';
  import { fetchWithCSRF } from '$lib/utils/api';

  interface Props {
    isOpen: boolean;
    detection: Detection | null;
    isExcluded?: boolean;
    onClose: () => void;
    onSave?: (
      verified: 'correct' | 'false_positive',
      lockDetection: boolean,
      ignoreSpecies: boolean,
      comment: string
    ) => Promise<void>;
  }

  let { isOpen = false, detection = null, isExcluded = false, onClose, onSave }: Props = $props();

  let reviewStatus = $state<'correct' | 'false_positive'>('correct');
  let lockDetection = $state(false);
  let ignoreSpecies = $state(false);
  let comment = $state('');
  let isLoading = $state(false);

  // Reset form when detection changes
  $effect(() => {
    if (detection) {
      const verified = detection.review?.verified;
      reviewStatus = (verified === 'correct' || verified === 'false_positive') ? verified : 'correct';
      lockDetection = detection.locked || false;
      ignoreSpecies = isExcluded;
      comment = (detection.comments?.[0] as any)?.entry || '';
    }
  });

  async function handleSave() {
    if (!detection || isLoading) return;

    isLoading = true;
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
                <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
              </svg>
            {:else if detection.review?.verified === 'false_positive'}
              <svg
                xmlns="http://www.w3.org/2000/svg"
                fill="none"
                viewBox="0 0 24 24"
                stroke-width="2"
                stroke="currentColor"
                class="w-3 h-3"
              >
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
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
                  d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
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
                viewBox="0 0 20 20"
                fill="currentColor"
              >
                <path
                  fill-rule="evenodd"
                  d="M8.257 3.099c.765-1.36 2.722-1.36 3.486 0l5.58 9.92c.75 1.334-.213 2.98-1.742 2.98H4.42c-1.53 0-2.493-1.646-1.743-2.98l5.58-9.92zM11 13a1 1 0 11-2 0 1 1 0 012 0zm-1-8a1 1 0 00-1 1v3a1 1 0 002 0V6a1 1 0 00-1-1z"
                  clip-rule="evenodd"
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
