<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import WeatherDetails from '$lib/desktop/components/data/WeatherDetails.svelte';
  import SpeciesBadges from './SpeciesBadges.svelte';
  import SpeciesThumbnail from './SpeciesThumbnail.svelte';
  import type { Detection } from '$lib/types/detection.types';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { XCircle, TriangleAlert, ChevronRight } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { safeArrayAccess } from '$lib/utils/security';

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
      // Use detection.verified directly (from API response)
      const verified = detection.verified;
      reviewStatus = verified === 'correct' || verified === 'false_positive' ? verified : 'correct';
      // Initialize lockDetection to false - user intent to lock, not current status
      lockDetection = false;
      ignoreSpecies = isExcluded;
      const firstComment = safeArrayAccess(detection.comments || [], 0);
      const firstCommentValue = firstComment?.entry || '';
      comment = firstCommentValue;
      // Use firstCommentValue (local variable) instead of comment ($state) to avoid
      // creating a reactive dependency that would reset the section when typing
      showCommentSection = !!firstCommentValue;
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
        // Calculate the desired lock state: if locked, checkbox means "unlock"; if unlocked, checkbox means "lock"
        const desiredLockState = detection.locked ? !lockDetection : lockDetection;

        // Default save implementation
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
      }
      onClose();
    } catch (error) {
      // Error handled with specific error messages below

      // Provide more specific error messages based on the error
      if (error instanceof Error) {
        if (error.message.includes('lock status')) {
          errorMessage = 'Failed to update lock status. Please try again.';
        } else if (error.message.includes('verification')) {
          errorMessage = 'Failed to update verification status. Please try again.';
        } else if (error.message.includes('comment')) {
          errorMessage = 'Failed to save comment. Please try again.';
        } else {
          errorMessage = error.message;
        }
      } else {
        errorMessage = t('common.review.errors.saveFailed');
      }
    } finally {
      isLoading = false;
    }
  }
</script>

<Modal
  {isOpen}
  title={t('common.review.modalTitle', { species: detection?.commonName || '' })}
  size="7xl"
  showCloseButton={true}
  {onClose}
  className="modal-bottom sm:modal-middle"
>
  {#snippet children()}
    {#if detection && isOpen}
      <!-- Error message display -->
      {#if errorMessage}
        <div class="alert alert-error mb-4">
          <XCircle class="size-6" />
          <span>{errorMessage}</span>
        </div>
      {/if}

      <!-- Horizontal layout: Media content left, Controls right -->
      <div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <!-- Left Column: Species info and Audio/Spectrogram -->
        <div class="lg:col-span-2 space-y-4">
          <!-- Species info with thumbnail - 4 column layout -->
          <div class="bg-base-200/50 rounded-lg p-4">
            <!-- Single Row Layout: All 4 segments in one row using flex -->
            <div class="flex gap-4 items-start">
              <!-- Section 1: Thumbnail + Species Names (flex-grow for more space) -->
              <div class="flex gap-4 items-center flex-1 min-w-0">
                <SpeciesThumbnail
                  scientificName={detection.scientificName}
                  commonName={detection.commonName}
                  size="lg"
                />
                <div class="flex-1 min-w-0">
                  <h3 class="text-2xl font-semibold text-base-content mb-1 truncate">
                    {detection.commonName}
                  </h3>
                  <p class="text-base text-base-content/60 italic truncate">
                    {detection.scientificName}
                  </p>
                  <div class="mt-2">
                    <SpeciesBadges {detection} size="md" />
                  </div>
                </div>
              </div>

              <!-- Section 2: Date & Time (fixed width) -->
              <div class="shrink-0 text-center" style:min-width="120px">
                <div class="text-sm text-base-content/60 mb-2">
                  {t('detections.headers.dateTime')}
                </div>
                <div class="text-base text-base-content">{detection.date}</div>
                <div class="text-base text-base-content">{detection.time}</div>
                {#if detection.timeOfDay}
                  <div class="text-sm text-base-content/60 mt-1 capitalize">
                    {detection.timeOfDay}
                  </div>
                {/if}
              </div>

              <!-- Section 3: Weather Conditions (fixed width) -->
              <div class="shrink-0 text-center" style:min-width="180px">
                <div class="text-sm text-base-content/60 mb-2">
                  {t('detections.headers.weather')}
                </div>
                {#if detection.weather}
                  <div class="flex justify-center">
                    <WeatherDetails
                      weatherIcon={detection.weather.weatherIcon}
                      weatherDescription={detection.weather.description}
                      temperature={detection.weather.temperature}
                      windSpeed={detection.weather.windSpeed}
                      windGust={detection.weather.windGust}
                      units={detection.weather.units}
                      size="md"
                      className="text-center"
                    />
                  </div>
                {:else}
                  <div class="text-sm text-base-content/40 italic">
                    {t('detections.weather.noData')}
                  </div>
                {/if}
              </div>

              <!-- Section 4: Confidence (fixed width) -->
              <div class="shrink-0 flex flex-col items-center" style:min-width="100px">
                <div class="text-sm text-base-content/60 mb-2">
                  {t('search.tableHeaders.confidence')}
                </div>
                <ConfidenceCircle confidence={detection.confidence} size="lg" />
              </div>
            </div>
          </div>

          <!-- Audio and Spectrogram -->
          <div class="relative bg-base-200 rounded-lg p-4">
            <AudioPlayer
              audioUrl="/api/v2/audio/{detection.id}"
              detectionId={detection.id.toString()}
              showSpectrogram={true}
              spectrogramSize="lg"
              spectrogramRaw={false}
              responsive={true}
              className="w-full mx-auto"
            />
          </div>
        </div>

        <!-- Right Column: Review Controls -->
        <div class="space-y-4">
          <!-- Review Form -->
          <div class="form-control bg-base-100 rounded-lg p-4">
            <h4 class="text-lg font-semibold mb-4">
              {t('common.review.form.reviewDetectionTitle')}
            </h4>

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
            <div class="form-control bg-base-100 rounded-lg p-4">
              <label class="label cursor-pointer justify-start gap-4">
                <input
                  type="checkbox"
                  bind:checked={lockDetection}
                  class="checkbox checkbox-primary checkbox-xs"
                  aria-describedby="lock-detection-help"
                />
                <span class="label-text">{t('common.review.form.lockDetection')}</span>
              </label>
              <div id="lock-detection-help" class="text-sm text-base-content/70 ml-8">
                {t('common.review.form.lockDetectionHelp')}
              </div>
            </div>
          {/if}

          {#if detection.locked}
            <div class="form-control bg-base-100 rounded-lg p-4">
              <label class="label cursor-pointer justify-start gap-4">
                <input
                  type="checkbox"
                  bind:checked={lockDetection}
                  class="checkbox checkbox-primary checkbox-xs"
                  aria-describedby="unlock-detection-help"
                />
                <span class="label-text">{t('common.review.form.unlockDetection')}</span>
              </label>
              <div id="unlock-detection-help" class="text-sm text-base-content/70 ml-8">
                {t('common.review.form.unlockDetectionHelp')}
              </div>
            </div>
          {/if}

          <!-- Ignore Species -->
          {#if !detection.locked && reviewStatus === 'false_positive'}
            <div class="form-control bg-base-100 rounded-lg p-4">
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

          <!-- Comment Section -->
          <div class="form-control bg-base-100 rounded-lg p-4">
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
                  <span class="text-xs text-base-content/60"
                    >{t('common.review.form.commentCount', { chars: comment.length })}</span
                  >
                {/if}
              </span>
            </button>

            <!-- Collapsible Comment Input -->
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
