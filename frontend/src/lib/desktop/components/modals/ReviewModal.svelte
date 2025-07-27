<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import WeatherDetails from '$lib/desktop/components/data/WeatherDetails.svelte';
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
      // Initialize lockDetection to false - user intent to lock, not current status
      lockDetection = false;
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
        // Calculate the desired lock state: if locked, checkbox means "unlock"; if unlocked, checkbox means "lock"
        const desiredLockState = detection.locked ? !lockDetection : lockDetection;

        // Default save implementation
        await fetchWithCSRF('/api/v2/detections/review', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({
            id: detection.id,
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
  title={t('common.review.modalTitle', { species: detection?.commonName || '' })}
  size="7xl"
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

      <!-- Horizontal layout: Media content left, Controls right -->
      <div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <!-- Left Column: Species info and Audio/Spectrogram -->
        <div class="lg:col-span-2 space-y-4">
          <!-- Species info with thumbnail - 4 column layout -->
          <div class="bg-base-200/50 rounded-lg p-4">
            <div class="grid grid-cols-12 gap-4 items-start">
              <!-- Column 1: Thumbnail + Species Names (4 columns) -->
              <div class="col-span-4 flex gap-4 items-center">
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
                <div class="flex-1 min-w-0">
                  <h3 class="text-2xl font-semibold text-base-content mb-1 truncate">{detection.commonName}</h3>
                  <p class="text-base text-base-content/60 italic truncate">{detection.scientificName}</p>
                  <div class="flex items-center gap-2 mt-2 flex-wrap">
                    <span
                      class={`badge badge-md gap-2 ${getStatusBadgeClass(detection.review?.verified)}`}
                    >
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
                    {#if detection.locked}
                      <span class="badge badge-md badge-warning gap-1">
                        <svg
                          xmlns="http://www.w3.org/2000/svg"
                          fill="none"
                          viewBox="0 0 24 24"
                          stroke-width="2"
                          stroke="currentColor"
                          class="w-3 h-3"
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
              </div>

              <!-- Column 2: Date & Time (2 columns) -->
              <div class="col-span-2 text-center">
                <div class="text-sm text-base-content/60 mb-2">{t('detections.headers.dateTime')}</div>
                <div class="text-base text-base-content">{detection.date}</div>
                <div class="text-base text-base-content">{detection.time}</div>
                {#if detection.timeOfDay}
                  <div class="text-sm text-base-content/60 mt-1 capitalize">{detection.timeOfDay}</div>
                {/if}
              </div>

              <!-- Column 3: Weather Conditions (4 columns = more space) -->
              <div class="col-span-4 text-center">
                <div class="text-sm text-base-content/60 mb-2">{t('detections.headers.weather')}</div>
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
                  <div class="text-sm text-base-content/40 italic">{t('detections.weather.noData')}</div>
                {/if}
              </div>

              <!-- Column 4: Confidence (2 columns) -->
              <div class="col-span-2 flex flex-col items-center">
                <div class="text-sm text-base-content/60 mb-2">{t('search.tableHeaders.confidence')}</div>
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
            <h4 class="text-lg font-semibold mb-4">Review Detection</h4>
            
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
                This detection is currently locked.
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
                <span class="label-text">Lock this detection after saving</span>
              </label>
              <div id="lock-detection-help" class="text-sm text-base-content/70 ml-8">
                Locking this detection will prevent it from being deleted during regular cleanup.
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
                <span class="label-text">Unlock this detection after saving</span>
              </label>
              <div id="unlock-detection-help" class="text-sm text-base-content/70 ml-8">
                Unlocking will allow this detection to be deleted during regular cleanup.
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
                <span class="label-text">Ignore this species</span>
              </label>
              <div class="text-sm text-base-content/70 ml-8">
                Ignoring this species will prevent future detections of this species. This will not
                remove existing detections.
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

            <!-- Collapsible Comment Input -->
            {#if showCommentSection}
              <div class="mt-3">
                <label class="label" for="comment-textarea">
                  <span class="label-text">Comment</span>
                </label>
                <textarea
                  id="comment-textarea"
                  bind:value={comment}
                  class="textarea textarea-bordered h-24 w-full"
                  placeholder="Add a comment about this detection..."
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
