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
  import { CheckCircle, XCircle, TriangleAlert, ChevronRight } from '@lucide/svelte';
  import StatusPill from '$lib/desktop/components/ui/StatusPill.svelte';
  import ConfidenceBadge from '$lib/desktop/components/data/ConfidenceBadge.svelte';
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
  let showAllAlternativePredictions = $state(false);

  const alternativePredictionPreviewLimit = 2;
  const alternativePredictions = $derived(detection.alternativePredictions ?? []);
  const hiddenAlternativePredictionCount = $derived(
    Math.max(0, alternativePredictions.length - alternativePredictionPreviewLimit)
  );
  const visibleAlternativePredictions = $derived(
    showAllAlternativePredictions
      ? alternativePredictions
      : alternativePredictions.slice(0, alternativePredictionPreviewLimit)
  );

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
      showAllAlternativePredictions = false;
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

<div class="card bg-[var(--color-base-100)] shadow-xs border border-[var(--color-base-300)]">
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

    {#if alternativePredictions.length > 0}
      <section
        class="bg-[var(--color-base-200)] rounded-lg p-4 mb-6"
        aria-labelledby="alternative-predictions-title"
      >
        <div class="flex flex-col gap-1 sm:flex-row sm:items-baseline sm:justify-between">
          <h4 id="alternative-predictions-title" class="font-medium">
            {t('common.review.form.alternativePredictionsTitle')}
          </h4>
          <p class="text-xs text-[var(--color-base-content)]/60">
            {t('common.review.form.alternativePredictionsHelp')}
          </p>
        </div>
        <ol class="mt-3 space-y-2">
          <li
            class="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 rounded-md border border-[var(--color-primary)]/25 bg-[var(--color-base-100)] px-3 py-2 text-sm"
          >
            <span
              class="flex size-7 items-center justify-center rounded-full bg-[var(--color-primary)]/15 text-[var(--color-primary)]"
              aria-label={t('common.review.form.detectedPredictionLabel')}
            >
              <CheckCircle class="size-4" />
            </span>
            <span class="min-w-0">
              <span class="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span class="truncate font-medium">{detection.commonName}</span>
                <span
                  class="rounded-full bg-[var(--color-primary)]/10 px-2 py-0.5 text-[0.6875rem] font-semibold text-[var(--color-primary)]"
                >
                  {t('common.review.form.detectedPredictionLabel')}
                </span>
              </span>
              {#if detection.scientificName !== detection.commonName}
                <span class="block truncate text-xs italic text-[var(--color-base-content)]/60">
                  {detection.scientificName}
                </span>
              {/if}
            </span>
            <ConfidenceBadge confidence={detection.confidence} className="shadow-none" />
          </li>
          {#each visibleAlternativePredictions as prediction (prediction.scientificName)}
            <li
              class="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 rounded-md bg-[var(--color-base-100)] px-3 py-2 text-sm"
            >
              <span
                class="flex size-7 items-center justify-center rounded-full bg-[var(--color-base-300)] text-xs font-semibold"
              >
                {prediction.rank}
              </span>
              <span class="min-w-0">
                <span class="block truncate font-medium">{prediction.commonName}</span>
                {#if prediction.scientificName !== prediction.commonName}
                  <span class="block truncate text-xs italic text-[var(--color-base-content)]/60">
                    {prediction.scientificName}
                  </span>
                {/if}
              </span>
              <ConfidenceBadge confidence={prediction.confidence} className="shadow-none" />
            </li>
          {/each}
        </ol>
        {#if hiddenAlternativePredictionCount > 0}
          <button
            type="button"
            class="mt-2 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-[var(--color-primary)] transition-colors hover:bg-[var(--color-primary)]/10 focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]/30"
            aria-expanded={showAllAlternativePredictions}
            onclick={() => (showAllAlternativePredictions = !showAllAlternativePredictions)}
          >
            <span class="transition-transform" class:rotate-90={showAllAlternativePredictions}>
              <ChevronRight class="size-3" aria-hidden="true" />
            </span>
            {#if showAllAlternativePredictions}
              {t('common.ui.showLess')}
            {:else}
              {t('common.ui.showMore')} ({hiddenAlternativePredictionCount})
            {/if}
          </button>
        {/if}
      </section>
    {/if}

    <!-- Review Controls Container -->
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
      <!-- Left Column: Review Form -->
      <div class="space-y-4">
        <!-- Review Status Form -->
        <div class="bg-[var(--color-base-200)] rounded-lg p-4">
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
            <div class="text-sm text-[var(--color-base-content)]/70 mt-2">
              <TriangleAlert class="inline-block size-4 mr-1" />
              {t('common.review.form.detectionLocked')}
            </div>
          {/if}
        </div>

        <!-- Lock/Unlock Controls -->
        {#if reviewStatus === 'correct' && !detection.locked}
          <div class="bg-[var(--color-base-200)] rounded-lg p-4">
            <label class="label cursor-pointer justify-start gap-4">
              <input
                type="checkbox"
                bind:checked={lockDetection}
                class="checkbox checkbox-primary checkbox-xs"
              />
              <span class="label-text">{t('common.review.form.lockDetection')}</span>
            </label>
            <div class="text-sm text-[var(--color-base-content)]/70 ml-8">
              {t('common.review.form.lockDetectionHelp')}
            </div>
          </div>
        {/if}

        {#if detection.locked}
          <div class="bg-[var(--color-base-200)] rounded-lg p-4">
            <label class="label cursor-pointer justify-start gap-4">
              <input
                type="checkbox"
                bind:checked={lockDetection}
                class="checkbox checkbox-primary checkbox-xs"
              />
              <span class="label-text">{t('common.review.form.unlockDetection')}</span>
            </label>
            <div class="text-sm text-[var(--color-base-content)]/70 ml-8">
              {t('common.review.form.unlockDetectionHelp')}
            </div>
          </div>
        {/if}

        <!-- Ignore Species -->
        {#if !detection.locked && reviewStatus === 'false_positive'}
          <div class="bg-[var(--color-base-200)] rounded-lg p-4">
            <label class="label cursor-pointer justify-start gap-4">
              <input
                type="checkbox"
                bind:checked={ignoreSpecies}
                class="checkbox checkbox-primary checkbox-xs"
              />
              <span class="label-text">{t('common.review.form.ignoreSpecies')}</span>
            </label>
            <div class="text-sm text-[var(--color-base-content)]/70 ml-8">
              {t('common.review.form.ignoreSpeciesHelp')}
            </div>
          </div>
        {/if}
      </div>

      <!-- Right Column: Comment & Status -->
      <div class="space-y-4">
        <!-- Comment Section -->
        <div class="bg-[var(--color-base-200)] rounded-lg p-4">
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
                <span class="text-xs text-[var(--color-base-content)]/60">
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
        <div class="bg-[var(--color-base-300)] rounded-lg p-4">
          <h4 class="font-medium mb-3">{t('common.review.form.currentStatusTitle')}</h4>
          <div class="space-y-2 text-sm">
            <div class="flex justify-between">
              <span class="text-[var(--color-base-content)]/70"
                >{t('common.review.form.reviewLabel')}:</span
              >
              <span>
                {#if detection.verified === 'correct'}
                  <StatusPill
                    variant="success"
                    label={t('common.review.status.verifiedCorrect')}
                    size="xs"
                    showDot={false}
                  />
                {:else if detection.verified === 'false_positive'}
                  <StatusPill
                    variant="error"
                    label={t('common.review.status.falsePositive')}
                    size="xs"
                    showDot={false}
                  />
                {:else}
                  <StatusPill
                    variant="neutral"
                    label={t('common.review.status.notReviewed')}
                    size="xs"
                    showDot={false}
                  />
                {/if}
              </span>
            </div>
            <div class="flex justify-between">
              <span class="text-[var(--color-base-content)]/70"
                >{t('common.review.form.lockLabel')}:</span
              >
              <span>
                {#if detection.locked}
                  <StatusPill
                    variant="warning"
                    label={t('search.statusBadges.locked')}
                    size="xs"
                    showDot={false}
                  />
                {:else}
                  <StatusPill
                    variant="neutral"
                    label={t('search.statusBadges.unlocked')}
                    size="xs"
                    showDot={false}
                  />
                {/if}
              </span>
            </div>
            {#if detection.comments && detection.comments.length > 0}
              <div class="flex justify-between">
                <span class="text-[var(--color-base-content)]/70"
                  >{t('common.review.form.commentsLabel')}:</span
                >
                <span>{detection.comments.length}</span>
              </div>
            {/if}
          </div>
        </div>
      </div>
    </div>

    <!-- Action Buttons -->
    <div class="card-actions justify-end mt-6 pt-4 border-t border-[var(--color-base-300)]">
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
