<!--
  PlayOverlay.svelte

  A centered play button overlay with audio playback functionality.
  Appears on card hover and handles audio playback for detection recordings.

  Props:
  - detectionId: number - Detection ID for audio URL
  - onFreezeStart?: () => void - Callback when playback starts
  - onFreezeEnd?: () => void - Callback when playback ends
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { Play, Pause, Loader2 } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn';
  import { loggers } from '$lib/utils/logger';
  import { t } from '$lib/i18n';

  const logger = loggers.audio;

  interface Props {
    detectionId: number;
    onFreezeStart?: () => void;
    onFreezeEnd?: () => void;
  }

  let { detectionId, onFreezeStart, onFreezeEnd }: Props = $props();

  let audioElement: HTMLAudioElement;
  let isPlaying = $state(false);
  let isLoading = $state(false);
  let error = $state<string | null>(null);
  let progress = $state(0);
  let playEndTimeout: ReturnType<typeof setTimeout> | undefined;

  const PLAY_END_DELAY_MS = 3000;

  const audioUrl = $derived(`/api/v2/audio/${detectionId}`);

  async function handlePlayPause(event: MouseEvent) {
    event.stopPropagation();

    if (!audioElement) return;

    // Clear any previous error when retrying
    error = null;

    try {
      if (isPlaying) {
        audioElement.pause();
      } else {
        isLoading = true;
        await audioElement.play();
        isLoading = false;
      }
    } catch (err) {
      logger.error('Error playing audio:', err);
      error = t('media.audio.playError');
      isLoading = false;
    }
  }

  function clearPlayEndTimeout() {
    if (playEndTimeout) {
      clearTimeout(playEndTimeout);
      playEndTimeout = undefined;
    }
  }

  function handlePlay() {
    isPlaying = true;
    clearPlayEndTimeout();
    onFreezeStart?.();
  }

  function handlePause() {
    isPlaying = false;
    clearPlayEndTimeout();
    playEndTimeout = setTimeout(() => {
      onFreezeEnd?.();
    }, PLAY_END_DELAY_MS);
  }

  function handleEnded() {
    isPlaying = false;
    progress = 0;
    clearPlayEndTimeout();
    playEndTimeout = setTimeout(() => {
      onFreezeEnd?.();
    }, PLAY_END_DELAY_MS);
  }

  function handleTimeUpdate() {
    if (audioElement && audioElement.duration) {
      progress = (audioElement.currentTime / audioElement.duration) * 100;
    }
  }

  function handleLoadStart() {
    // With preload="none", the browser shouldn't be loading, but some browsers (Edge)
    // fire this event anyway. Only set loading in handlePlayPause when user clicks play.
    error = null;
  }

  function handleCanPlay() {
    isLoading = false;
  }

  function handleError() {
    error = t('media.audio.error');
    isLoading = false;
  }

  onMount(() => {
    if (audioElement) {
      audioElement.addEventListener('play', handlePlay);
      audioElement.addEventListener('pause', handlePause);
      audioElement.addEventListener('ended', handleEnded);
      audioElement.addEventListener('timeupdate', handleTimeUpdate);
      audioElement.addEventListener('loadstart', handleLoadStart);
      audioElement.addEventListener('canplay', handleCanPlay);
      audioElement.addEventListener('error', handleError);
    }
  });

  onDestroy(() => {
    clearPlayEndTimeout();
    if (audioElement) {
      audioElement.removeEventListener('play', handlePlay);
      audioElement.removeEventListener('pause', handlePause);
      audioElement.removeEventListener('ended', handleEnded);
      audioElement.removeEventListener('timeupdate', handleTimeUpdate);
      audioElement.removeEventListener('loadstart', handleLoadStart);
      audioElement.removeEventListener('canplay', handleCanPlay);
      audioElement.removeEventListener('error', handleError);
    }
  });
</script>

<!-- Audio element (hidden) -->
<audio bind:this={audioElement} src={audioUrl} preload="none" class="hidden">
  <track kind="captions" />
</audio>

<!-- Play button container - centered, visible on hover -->
<div class="play-overlay pointer-events-none">
  <button
    class={cn('play-button pointer-events-auto', isPlaying && 'is-playing')}
    onclick={handlePlayPause}
    disabled={!!error}
    aria-label={isPlaying ? t('media.audio.pause') : t('media.audio.play')}
  >
    {#if isLoading}
      <Loader2 class="size-8 animate-spin" />
    {:else if isPlaying}
      <Pause class="size-8" />
    {:else}
      <Play class="size-8 ml-1" />
    {/if}
  </button>

  <!-- Progress ring when playing -->
  {#if isPlaying}
    <svg class="progress-ring" viewBox="0 0 56 56">
      <circle class="progress-ring-bg" cx="28" cy="28" r="26" fill="none" stroke-width="3" />
      <circle
        class="progress-ring-fill"
        cx="28"
        cy="28"
        r="26"
        fill="none"
        stroke-width="3"
        stroke-dasharray={2 * Math.PI * 26}
        stroke-dashoffset={2 * Math.PI * 26 * (1 - progress / 100)}
      />
    </svg>
  {/if}

  <!-- Error indicator -->
  {#if error}
    <span class="error-indicator pointer-events-auto" aria-live="polite" role="alert" title={error}>
      {t('media.audio.errorShort')}
    </span>
  {/if}
</div>

<style>
  .play-overlay {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 20;
  }

  .play-button {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 3.5rem;
    height: 3.5rem;
    border-radius: 9999px;
    background-color: rgb(255 255 255 / 0.9);
    color: rgb(15 23 42);
    opacity: 0;
    transform: scale(0.9);
    transition: all 0.2s ease;
    box-shadow: 0 4px 12px rgb(0 0 0 / 0.3);
  }

  .play-button:hover {
    transform: scale(1.1);
    background-color: white;
  }

  .play-button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  /* Show on card hover */
  :global(.group:hover) .play-button {
    opacity: 1;
    transform: scale(1);
  }

  /* Always show when playing */
  .play-button.is-playing {
    opacity: 1;
    transform: scale(1);
  }

  /* Progress ring */
  .progress-ring {
    position: absolute;
    width: 3.5rem;
    height: 3.5rem;
    transform: rotate(-90deg);
    pointer-events: none;
  }

  .progress-ring-bg {
    stroke: rgb(255 255 255 / 0.3);
  }

  .progress-ring-fill {
    stroke: rgb(59 130 246);
    stroke-linecap: round;
    transition: stroke-dashoffset 0.1s linear;
  }

  /* Error indicator */
  .error-indicator {
    position: absolute;
    bottom: -1.5rem;
    left: 50%;
    transform: translateX(-50%);
    font-size: 0.75rem;
    color: rgb(239 68 68);
    background-color: rgb(254 242 242 / 0.95);
    padding: 0.125rem 0.5rem;
    border-radius: 0.25rem;
    white-space: nowrap;
    box-shadow: 0 1px 3px rgb(0 0 0 / 0.1);
  }

  :global(.dark) .error-indicator {
    background-color: rgb(127 29 29 / 0.9);
    color: rgb(254 202 202);
  }
</style>
