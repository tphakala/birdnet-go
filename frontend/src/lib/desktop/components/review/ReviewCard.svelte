<!--
  ReviewCard.svelte - Authentication-protected Review Controls
  
  Purpose: Isolated review functionality only served to authenticated users
  
  Features:
  - Review status selection (correct/false positive)
  - Lock/unlock detection controls
  - Species ignore functionality
  - Comment system
  - Error handling and loading states
  
  Security: This component is only served to authenticated users
-->
<script lang="ts">
  import { fetchWithCSRF } from '$lib/utils/api';
  import { t } from '$lib/i18n';
  import { XCircle, TriangleAlert, ChevronRight } from '@lucide/svelte';
  import type { Detection } from '$lib/types/detection.types';

  interface Props {
    detection: Detection;
    onSaveComplete?: () => void;
  }

  let { detection, onSaveComplete }: Props = $props();

  // Review state
  let reviewStatus = $state<'correct' | 'false_positive'>('correct');
  let lockDetection = $state(false);
  let ignoreSpecies = $state(false);
  let comment = $state('');
  let isLoadingReview = $state(false);
  let reviewErrorMessage = $state<string | null>(null);
  let showCommentSection = $state(false);

  // Initialize review form when detection changes
  $effect(() => {
    if (detection) {
      // Use detection.verified directly (from API response), not detection.review?.verified
      const verified = detection.verified;
      reviewStatus = verified === 'correct' || verified === 'false_positive' ? verified : 'correct';
      lockDetection = false;
      ignoreSpecies = false;
      const firstComment =
        detection.comments && detection.comments.length > 0
          ? detection.comments[0]?.entry || ''
          : '';
      comment = firstComment;
      // Use firstComment (local variable) instead of comment ($state) to avoid
      // creating a reactive dependency that would reset the section when typing
      showCommentSection = !!firstComment;
      reviewErrorMessage = null;
    }
  });

  // Handle review save
  async function handleReviewSave(): Promise<void> {
    if (!detection || isLoadingReview) return;

    isLoadingReview = true;
    reviewErrorMessage = null;

    try {
      const desiredLockState = detection.locked ? !lockDetection : lockDetection;

      await fetchWithCSRF(`/api/v2/detections/${detection.id}/review`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          verified: reviewStatus,
          lock_detection: desiredLockState,
          ignore_species: ignoreSpecies ? detection.commonName : null,
          comment: comment,
        }),
      });

      onSaveComplete?.();
    } catch (error) {
      if (error instanceof Error) {
        if (error.message.includes('lock status')) {
          reviewErrorMessage = t('common.review.errors.lockStatusFailed');
        } else if (error.message.includes('verification')) {
          reviewErrorMessage = t('common.review.errors.verificationFailed');
        } else if (error.message.includes('comment')) {
          reviewErrorMessage = t('common.review.errors.commentFailed');
        } else {
          reviewErrorMessage = error.message;
        }
      } else {
        reviewErrorMessage = t('common.review.errors.saveFailed');
      }
    } finally {
      isLoadingReview = false;
    }
  }
</script>

<div class="card bg-base-100 shadow-xs border border-base-300">
  <div class="card-body">
    <h3 class="card-title text-lg font-semibold mb-4">
      {t('common.review.form.reviewDetectionTitle')}
    </h3>

    <!-- Error message display -->
    {#if reviewErrorMessage}
      <div class="alert alert-error mb-4">
        <XCircle class="size-6" />
        <span>{reviewErrorMessage}</span>
      </div>
    {/if}

    <!-- Review Controls Container -->
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
      <!-- Left Column: Review Form -->
      <div class="space-y-4">
        <!-- Review Status Form -->
        <div class="bg-base-200 rounded-lg p-4">
          <h4 class="font-medium mb-3">{t('common.review.form.detectionStatusTitle')}</h4>

          <label class="label cursor-pointer justify-start gap-4">
            <input
              type="radio"
              name="verified"
              value="correct"
              bind:group={reviewStatus}
              class="radio radio-primary radio-xs"
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
            />
            <span class="label-text">{t('common.review.form.falsePositiveLabel')}</span>
          </label>

          {#if detection.locked}
            <div class="text-sm text-base-content/70 mt-2">
              <TriangleAlert class="inline-block size-4 mr-1" />
              {t('common.review.form.detectionLocked')}
            </div>
          {/if}
        </div>

        <!-- Lock/Unlock Controls -->
        {#if reviewStatus === 'correct' && !detection.locked}
          <div class="bg-base-200 rounded-lg p-4">
            <label class="label cursor-pointer justify-start gap-4">
              <input
                type="checkbox"
                bind:checked={lockDetection}
                class="checkbox checkbox-primary checkbox-xs"
              />
              <span class="label-text">{t('common.review.form.lockDetection')}</span>
            </label>
            <div class="text-sm text-base-content/70 ml-8">
              {t('common.review.form.lockDetectionHelp')}
            </div>
          </div>
        {/if}

        {#if detection.locked}
          <div class="bg-base-200 rounded-lg p-4">
            <label class="label cursor-pointer justify-start gap-4">
              <input
                type="checkbox"
                bind:checked={lockDetection}
                class="checkbox checkbox-primary checkbox-xs"
              />
              <span class="label-text">{t('common.review.form.unlockDetection')}</span>
            </label>
            <div class="text-sm text-base-content/70 ml-8">
              {t('common.review.form.unlockDetectionHelp')}
            </div>
          </div>
        {/if}

        <!-- Ignore Species -->
        {#if !detection.locked && reviewStatus === 'false_positive'}
          <div class="bg-base-200 rounded-lg p-4">
            <label class="label cursor-pointer justify-start gap-4">
              <input
                type="checkbox"
                bind:checked={ignoreSpecies}
                class="checkbox checkbox-primary checkbox-xs"
              />
              <span class="label-text">{t('common.review.form.ignoreSpecies')}</span>
            </label>
            <div class="text-sm text-base-content/70 ml-8">
              {t('common.review.form.ignoreSpeciesHelp')}
            </div>
          </div>
        {/if}
      </div>

      <!-- Right Column: Comment & Status -->
      <div class="space-y-4">
        <!-- Comment Section -->
        <div class="bg-base-200 rounded-lg p-4">
          <button
            type="button"
            class="btn btn-ghost btn-sm justify-start gap-2 p-2"
            onclick={() => (showCommentSection = !showCommentSection)}
          >
            <div class="transition-transform duration-200" class:rotate-90={showCommentSection}>
              <ChevronRight class="size-4" />
            </div>
            <span class="text-sm">
              {showCommentSection
                ? t('common.review.form.hideComment')
                : t('common.review.form.addComment')}
              {#if comment && !showCommentSection}
                <span class="text-xs text-base-content/60">
                  ({comment.length}
                  {t('common.review.form.chars')})
                </span>
              {/if}
            </span>
          </button>

          {#if showCommentSection}
            <div class="mt-3">
              <label class="label" for="comment-textarea">
                <span class="label-text">{t('common.review.form.comment')}</span>
              </label>
              <textarea
                id="comment-textarea"
                bind:value={comment}
                class="textarea h-24 w-full"
                placeholder={t('common.review.form.commentPlaceholder')}
              ></textarea>
            </div>
          {/if}
        </div>

        <!-- Current Status Summary -->
        <div class="bg-base-300 rounded-lg p-4">
          <h4 class="font-medium mb-3">{t('common.review.form.currentStatusTitle')}</h4>
          <div class="space-y-2 text-sm">
            <div class="flex justify-between">
              <span class="text-base-content/70">{t('common.review.form.reviewLabel')}:</span>
              <span>
                {#if detection.verified && detection.verified !== 'unverified'}
                  <span
                    class="badge badge-xs"
                    class:badge-success={detection.verified === 'correct'}
                    class:badge-error={detection.verified === 'false_positive'}
                  >
                    {detection.verified === 'correct'
                      ? t('common.review.status.verifiedCorrect')
                      : t('common.review.status.falsePositive')}
                  </span>
                {:else}
                  <span class="badge badge-neutral badge-xs"
                    >{t('common.review.status.notReviewed')}</span
                  >
                {/if}
              </span>
            </div>
            <div class="flex justify-between">
              <span class="text-base-content/70">{t('common.review.form.lockLabel')}:</span>
              <span>
                {#if detection.locked}
                  <span class="badge badge-warning badge-xs">{t('search.statusBadges.locked')}</span
                  >
                {:else}
                  <span class="badge badge-ghost badge-xs">{t('search.statusBadges.unlocked')}</span
                  >
                {/if}
              </span>
            </div>
            {#if detection.comments && detection.comments.length > 0}
              <div class="flex justify-between">
                <span class="text-base-content/70">{t('common.review.form.commentsLabel')}:</span>
                <span>{detection.comments.length}</span>
              </div>
            {/if}
          </div>
        </div>
      </div>
    </div>

    <!-- Action Buttons -->
    <div class="card-actions justify-end mt-6 pt-4 border-t border-base-300">
      <button
        type="button"
        class="btn btn-primary"
        onclick={handleReviewSave}
        disabled={isLoadingReview || !detection}
      >
        {#if isLoadingReview}
          <span class="loading loading-spinner loading-sm"></span>
        {/if}
        {t('common.review.form.saveReview')}
      </button>
    </div>
  </div>
</div>
