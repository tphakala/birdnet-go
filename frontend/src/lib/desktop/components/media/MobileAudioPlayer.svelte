<script lang="ts">
  import { t } from '$lib/i18n';
  import { getLogger } from '$lib/utils/logger';

  const logger = getLogger('mobileAudioPlayer');

  interface Props {
    audioUrl: string;
    speciesName?: string;
    detectionTime?: string;
    detectionId?: number | string;
    showSpectrogram?: boolean;
    onClose?: () => void;
  }

  let {
    audioUrl,
    speciesName = '',
    detectionTime = '',
    detectionId = undefined,
    showSpectrogram = true,
    onClose,
  }: Props = $props();

  let isPlaying = $state(false);
  let currentTime = $state(0);
  let duration = $state(0);
  let audioElement: HTMLAudioElement | null = null;
  let isLoading = $state(false);
  let spectrogramError = $state(false);
  let spectrogramUrl = $state<string>('');
  $effect(() => {
    spectrogramUrl =
      !showSpectrogram || !detectionId ? '' : `/api/v2/spectrogram/${detectionId}?size=md`;
  });

  function handlePlayPause() {
    if (!audioElement) return;

    if (isPlaying) {
      audioElement.pause();
      isPlaying = false;
    } else {
      isLoading = true;
      audioElement.play().catch(error => {
        logger.error('Error playing audio:', error);
        isLoading = false;
      });
      isPlaying = true;
    }
  }

  function handleTimeUpdate() {
    if (audioElement) {
      currentTime = audioElement.currentTime;
    }
  }

  function handleLoadedMetadata() {
    if (audioElement) {
      duration = audioElement.duration;
      isLoading = false;
    }
  }

  function handleEnded() {
    isPlaying = false;
    currentTime = 0;
  }

  function handleSliderChange(e: Event) {
    const target = e.target as HTMLInputElement;
    const newTime = parseFloat(target.value);
    if (audioElement) {
      audioElement.currentTime = newTime;
      currentTime = newTime;
    }
  }

  function formatTime(seconds: number): string {
    if (!seconds || !isFinite(seconds)) return '0:00';
    const mins = Math.floor(seconds / 60);
    const secs = Math.floor(seconds % 60);
    return `${mins}:${secs.toString().padStart(2, '0')}`;
  }

  function handleClose() {
    if (audioElement) {
      audioElement.pause();
      audioElement.currentTime = 0;
    }
    isPlaying = false;
    if (onClose) {
      onClose();
    }
  }
</script>

<!-- Mobile-optimized audio player -->
<div class="fixed inset-0 z-50 bg-black/50 flex items-end md:hidden">
  <div class="w-full rounded-t-3xl shadow-2xl relative overflow-hidden bg-base-100">
    {#if showSpectrogram && spectrogramUrl && !spectrogramError}
      <img
        src={spectrogramUrl}
        alt="Audio spectrogram"
        class="absolute inset-0 w-full h-full object-cover"
        onerror={() => (spectrogramError = true)}
      />
      <div class="absolute inset-0 bg-base-100/70"></div>
    {/if}
    <!-- Header -->
    <div class="flex items-center justify-between p-4 border-b border-base-300">
      <div class="flex-1">
        <h3 class="font-bold text-sm line-clamp-1">{speciesName}</h3>
        {#if detectionTime}
          <p class="text-xs text-base-content opacity-60">{detectionTime}</p>
        {/if}
      </div>
      <button
        onclick={handleClose}
        class="btn btn-ghost btn-sm btn-circle"
        aria-label={t('common.aria.close')}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-5 w-5"
          viewBox="0 0 20 20"
          fill="currentColor"
        >
          <path
            fill-rule="evenodd"
            d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z"
            clip-rule="evenodd"
          />
        </svg>
      </button>
    </div>

    <!-- Controls -->
    <div class="px-4 py-4 space-y-4">
      <!-- Progress Bar -->
      <div class="space-y-2">
        <input
          type="range"
          min="0"
          max={duration || 0}
          value={currentTime}
          onchange={handleSliderChange}
          class="range range-sm w-full"
          aria-label="Audio progress"
        />
        <div class="flex justify-between text-xs text-base-content opacity-60">
          <span>{formatTime(currentTime)}</span>
          <span>{formatTime(duration)}</span>
        </div>
      </div>

      <!-- Play/Pause Button -->
      <div class="flex justify-center">
        <button
          onclick={handlePlayPause}
          disabled={isLoading}
          class="btn btn-primary btn-circle btn-lg"
          aria-label={isPlaying ? t('common.actions.pause') : t('common.actions.play')}
        >
          {#if isLoading}
            <svg
              class="animate-spin h-6 w-6"
              xmlns="http://www.w3.org/2000/svg"
              fill="none"
              viewBox="0 0 24 24"
            >
              <circle
                class="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                stroke-width="4"
              ></circle>
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              ></path>
            </svg>
          {:else if isPlaying}
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-6 w-6"
              viewBox="0 0 20 20"
              fill="currentColor"
            >
              <path
                d="M5.75 1.5A1.75 1.75 0 004 3.25v13.5c0 .966.784 1.75 1.75 1.75h1a1.75 1.75 0 001.75-1.75V3.25A1.75 1.75 0 007.75 1.5h-2zm7.5 0A1.75 1.75 0 0011.5 3.25v13.5c0 .966.784 1.75 1.75 1.75h1a1.75 1.75 0 001.75-1.75V3.25A1.75 1.75 0 0014.25 1.5h-2z"
              />
            </svg>
          {:else}
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-6 w-6"
              viewBox="0 0 20 20"
              fill="currentColor"
            >
              <path
                d="M6.3 2.841A1.5 1.5 0 004 4.11V15.89a1.5 1.5 0 002.3 1.269l9.344-5.89a1.5 1.5 0 000-2.538L6.3 2.84z"
              />
            </svg>
          {/if}
        </button>
      </div>

      <!-- Secondary Actions -->
      <div class="flex gap-2">
        <button class="btn btn-outline btn-sm flex-1">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-4 w-4"
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              d="M5.5 13a3.5 3.5 0 01-.369-6.98 4 4 0 117.753-1.3A4.5 4.5 0 1113.5 13H11V9.413l1.293 1.293a1 1 0 001.414-1.414l-3-3a1 1 0 00-1.414 0l-3 3a1 1 0 001.414 1.414L9 9.414V13H5.5z"
            />
          </svg>
          {t('common.actions.download')}
        </button>
      </div>
    </div>

    <!-- Hidden Audio Element -->
    <audio
      bind:this={audioElement}
      src={audioUrl}
      ontimeupdate={handleTimeUpdate}
      onloadedmetadata={handleLoadedMetadata}
      onended={handleEnded}
      crossorigin="anonymous"
    ></audio>
  </div>
</div>

<style>
  :global(.mobile-audio-player-open) {
    overflow: hidden;
  }

  /* Ensure spectrogram looks good on small screens */
  img[alt='Audio spectrogram'] {
    image-rendering: auto;
  }
</style>
