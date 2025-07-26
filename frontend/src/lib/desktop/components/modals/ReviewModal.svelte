<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import type { Detection } from '$lib/types/detection.types';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { alertIcons, navigationIcons } from '$lib/utils/icons';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import { t } from '$lib/i18n';

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
  let showCommentSection = $state(false);

  // Reset form when detection changes
  $effect(() => {
    if (detection) {
      const verified = detection.review?.verified;
      reviewStatus = verified === 'correct' || verified === 'false_positive' ? verified : 'correct';
      lockDetection = detection.locked || false;
      ignoreSpecies = isExcluded;
      comment = detection.comments?.[0]?.entry || '';
      // Show comment section if there's already a comment
      showCommentSection = !!comment;
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
      errorMessage = error instanceof Error ? error.message : t('common.review.errors.saveFailed');
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
        return t('common.review.status.verifiedCorrect');
      case 'false_positive':
        return t('common.review.status.falsePositive');
      default:
        return t('common.review.status.notReviewed');
    }
  }
</script>

<Modal
  {isOpen}
  title="Review Detection"
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

      <div class="space-y-6">
        <!-- Species info with thumbnail -->
        <div class="bg-base-200/50 rounded-lg p-4">
          <div class="flex gap-4 items-center">
            <!-- Bird thumbnail -->
            <div class="flex-shrink-0">
              <div class="w-32 h-24 relative overflow-hidden rounded-lg bg-base-100 shadow-md">
                <img
                  src="/api/v2/media/species-image?name={encodeURIComponent(
                    detection.scientificName
                  )}"
                  alt={detection.commonName}
                  class="w-full h-full object-cover"
                  onerror={handleBirdImageError}
                  loading="lazy"
                />
              </div>
            </div>

            <!-- Species names and status -->
            <div class="flex-1 flex flex-col justify-between py-1">
              <div>
                <h3 class="text-2xl font-bold text-base-content mb-1">{detection.commonName}</h3>
                <p class="text-base text-base-content/60 italic mb-3">{detection.scientificName}</p>
              </div>
              <div class="flex items-center gap-3">
                <span
                  class={`badge badge-lg gap-2 ${getStatusBadgeClass(detection.review?.verified)}`}
                >
                  {#if detection.review?.verified === 'correct'}
                    <svg
                      xmlns="http://www.w3.org/2000/svg"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke-width="2"
                      stroke="currentColor"
                      class="w-4 h-4"
                    >
                      <path stroke-linecap="round" stroke-linejoin="round" d={alertIcons.success} />
                    </svg>
                  {:else if detection.review?.verified === 'false_positive'}
                    <div class="w-4 h-4">
                      {@html navigationIcons.close}
                    </div>
                  {/if}
                  {getStatusText(detection.review?.verified)}
                </span>
                {#if detection.locked}
                  <span class="badge badge-lg badge-warning gap-2">
                    <svg
                      xmlns="http://www.w3.org/2000/svg"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke-width="2"
                      stroke="currentColor"
                      class="w-4 h-4"
                    >
                      <path
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z"
                      />
                    </svg>
                    Locked
                  </span>
                {/if}
              </div>
            </div>

            <!-- Confidence circle on the right -->
            <div class="flex-shrink-0 mr-4">
              <ConfidenceCircle confidence={detection.confidence} size="lg" />
            </div>
          </div>
        </div>

        <!-- Audio and Spectrogram -->
        <div class="mb-4">
          <div class="relative bg-base-200 rounded-lg p-4">
            <AudioPlayer
              audioUrl="/api/v2/audio/{detection.id}"
              detectionId={detection.id.toString()}
              showSpectrogram={true}
              responsive={true}
              className="w-full mx-auto"
            />
          </div>
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
            <span class="label-text">{t('common.review.form.correctDetection')}</span>
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
            <span class="label-text">{t('common.review.form.falsePositiveLabel')}</span>
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

        <!-- Comment Section Toggle -->
        <div class="form-control">
          <button
            type="button"
            class="btn btn-ghost btn-sm justify-start gap-2 p-2"
            onclick={() => (showCommentSection = !showCommentSection)}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              fill="none"
              viewBox="0 0 24 24"
              stroke-width="2"
              stroke="currentColor"
              class="w-4 h-4 transition-transform duration-200"
              class:rotate-90={showCommentSection}
            >
              <path stroke-linecap="round" stroke-linejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" />
            </svg>
            <span class="text-sm">
              {showCommentSection ? 'Hide' : 'Add'} Comment
              {#if comment && !showCommentSection}
                <span class="text-xs text-base-content/60">({comment.length} chars)</span>
              {/if}
            </span>
          </button>
        </div>

        <!-- Collapsible Comment Input -->
        {#if showCommentSection}
          <div class="form-control">
            <label class="label" for="comment-textarea">
              <span class="label-text">Comment</span>
            </label>
            <textarea
              id="comment-textarea"
              bind:value={comment}
              class="textarea textarea-bordered h-24"
              placeholder="Add a comment about this detection..."
            ></textarea>
          </div>
        {/if}
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
