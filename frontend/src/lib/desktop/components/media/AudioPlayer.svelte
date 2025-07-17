<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';

  interface Props {
    audioUrl: string;
    spectrogramUrl?: string;
    detectionId?: string;
    width?: number;
    height?: number;
    showDownload?: boolean;
    showSpectrogram?: boolean;
    autoPlay?: boolean;
    className?: string;
    spectrogramClassName?: string;
    controlsClassName?: string;
    onPlay?: () => void;
    onPause?: () => void;
    onEnded?: () => void;
    // eslint-disable-next-line no-unused-vars
    onTimeUpdate?: (currentTime: number, duration: number) => void;
    children?: Snippet;
  }

  let {
    audioUrl,
    spectrogramUrl,
    detectionId,
    width = 400,
    height = 200,
    showDownload = true,
    showSpectrogram = true,
    autoPlay = false,
    className = '',
    spectrogramClassName = '',
    controlsClassName = '',
    onPlay,
    onPause,
    onEnded,
    onTimeUpdate,
    children,
  }: Props = $props();

  let audioElement = $state<HTMLAudioElement>();
  let spectrogramContainer = $state<HTMLDivElement>();
  let progressBar = $state<HTMLDivElement>();
  let positionIndicator = $state<HTMLDivElement>();

  let isPlaying = $state(false);
  let currentTime = $state(0);
  let duration = $state(0);
  let isLoading = $state(true);
  let hasError = $state(false);
  let updateInterval: number | null = null;

  // Calculate progress percentage
  const progressPercent = $derived(duration > 0 ? (currentTime / duration) * 100 : 0);

  // Format time display
  const formatTime = (seconds: number): string => {
    const minutes = Math.floor(seconds / 60);
    const remainingSeconds = Math.floor(seconds % 60);
    return `${minutes}:${remainingSeconds.toString().padStart(2, '0')}`;
  };

  const currentTimeFormatted = $derived(formatTime(currentTime));
  const durationFormatted = $derived(formatTime(duration));

  // Play/pause toggle
  function togglePlayPause() {
    if (!audioElement) return;

    if (audioElement.paused) {
      audioElement.play().catch(_err => {
        // Error playing audio
        hasError = true;
      });
    } else {
      audioElement.pause();
    }
  }

  // Update progress
  function updateProgress() {
    if (!audioElement) return;
    currentTime = audioElement.currentTime;
    duration = audioElement.duration || 0;

    if (onTimeUpdate) {
      onTimeUpdate(currentTime, duration);
    }
  }

  // Seek to position
  function seekToPosition(event: MouseEvent) {
    if (!audioElement || !duration) return;

    const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
    const pos = (event.clientX - rect.left) / rect.width;
    audioElement.currentTime = pos * duration;
    updateProgress();
  }

  // Handle keyboard controls on progress bar
  function handleKeyDown(event: KeyboardEvent) {
    if (!audioElement || !duration) return;

    const step = 5; // 5 seconds step

    switch (event.key) {
      case 'ArrowLeft':
        event.preventDefault();
        audioElement.currentTime = Math.max(0, audioElement.currentTime - step);
        updateProgress();
        break;
      case 'ArrowRight':
        event.preventDefault();
        audioElement.currentTime = Math.min(duration, audioElement.currentTime + step);
        updateProgress();
        break;
      case ' ':
      case 'Enter':
        event.preventDefault();
        togglePlayPause();
        break;
    }
  }

  // Audio event handlers
  function handlePlay() {
    isPlaying = true;
    updateInterval = window.setInterval(updateProgress, 100);
    onPlay?.();
  }

  function handlePause() {
    isPlaying = false;
    if (updateInterval) {
      window.clearInterval(updateInterval);
      updateInterval = null;
    }
    onPause?.();
  }

  function handleEnded() {
    isPlaying = false;
    if (updateInterval) {
      window.clearInterval(updateInterval);
      updateInterval = null;
    }
    currentTime = 0;
    onEnded?.();
  }

  function handleLoadedMetadata() {
    isLoading = false;
    duration = audioElement?.duration || 0;
    updateProgress();
  }

  function handleError() {
    isLoading = false;
    hasError = true;
    // Error loading audio
  }

  onMount(() => {
    if (audioElement && autoPlay) {
      audioElement.play().catch(_err => {
        // Autoplay failed
      });
    }
  });

  onDestroy(() => {
    if (updateInterval) {
      window.clearInterval(updateInterval);
    }
  });

  // Generate spectrogram URL if not provided
  const computedSpectrogramUrl = $derived(
    spectrogramUrl ?? (detectionId ? `/api/v2/spectrogram/${detectionId}?width=${width}` : '')
  );
</script>

<div class={cn('audio-player', className)}>
  <audio
    bind:this={audioElement}
    src={audioUrl}
    onplay={handlePlay}
    onpause={handlePause}
    onended={handleEnded}
    onloadedmetadata={handleLoadedMetadata}
    onerror={handleError}
    class="hidden"
  ></audio>

  {#if showSpectrogram && computedSpectrogramUrl}
    <div
      bind:this={spectrogramContainer}
      class={cn('relative cursor-pointer overflow-hidden rounded', spectrogramClassName)}
      style:width="{width}px"
      style:height="{height}px"
      onclick={seekToPosition}
      onkeydown={handleKeyDown}
      role="button"
      tabindex="0"
      aria-label="Seek in audio"
    >
      {#if isLoading}
        <div class="absolute inset-0 flex items-center justify-center bg-base-200">
          <span class="loading loading-spinner loading-md"></span>
        </div>
      {/if}

      {#if hasError}
        <div class="absolute inset-0 flex items-center justify-center bg-base-200">
          <div class="text-center">
            <svg
              class="mx-auto h-12 w-12 text-base-content/30"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
              />
            </svg>
            <p class="mt-2 text-sm text-base-content/50">Failed to load audio</p>
          </div>
        </div>
      {:else}
        <img
          src={computedSpectrogramUrl}
          alt="Audio spectrogram"
          class="h-full w-full object-cover"
          onerror={() => (hasError = true)}
        />

        <!-- Play position indicator -->
        <div
          bind:this={positionIndicator}
          class="absolute bottom-0 top-0 w-0.5 bg-primary pointer-events-none transition-all"
          style:left="{progressPercent}%"
          style:opacity={currentTime > 0 && currentTime < duration ? 0.7 : 0}
          aria-hidden="true"
        ></div>
      {/if}

      <!-- Custom content overlay -->
      {#if children}
        <div class="absolute inset-0 pointer-events-none">
          {@render children()}
        </div>
      {/if}
    </div>
  {/if}

  <!-- Audio controls -->
  <div
    class={cn(
      'flex items-center gap-2 rounded bg-base-200 p-2',
      showSpectrogram && computedSpectrogramUrl ? '-mt-1 rounded-t-none' : '',
      controlsClassName
    )}
  >
    <!-- Play/Pause button -->
    <button
      class="btn btn-sm btn-circle btn-ghost"
      onclick={togglePlayPause}
      disabled={hasError}
      aria-label={isPlaying ? 'Pause' : 'Play'}
      aria-pressed={isPlaying}
    >
      {#if isPlaying}
        <svg class="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M10 9v6m4-6v6m7-3a9 9 0 11-18 0 9 9 0 0118 0z"
          />
        </svg>
      {:else}
        <svg class="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"
          />
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
          />
        </svg>
      {/if}
    </button>

    <!-- Progress bar -->
    <div
      bind:this={progressBar}
      class="relative h-1.5 flex-1 cursor-pointer rounded-full bg-base-300"
      onclick={seekToPosition}
      onkeydown={handleKeyDown}
      role="slider"
      tabindex="0"
      aria-label="Audio playback position"
      aria-valuemin="0"
      aria-valuemax={duration}
      aria-valuenow={currentTime}
      aria-valuetext={currentTimeFormatted}
    >
      <div
        class="absolute left-0 top-0 h-full rounded-full bg-primary transition-all"
        style:width="{progressPercent}%"
        aria-hidden="true"
      ></div>
    </div>

    <!-- Time display -->
    <span class="text-xs font-medium tabular-nums">
      {currentTimeFormatted} / {durationFormatted}
    </span>

    <!-- Download button -->
    {#if showDownload}
      <a
        href={audioUrl}
        download
        class="btn btn-sm btn-circle btn-ghost"
        aria-label="Download audio file"
      >
        <svg class="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"
          />
        </svg>
      </a>
    {/if}
  </div>
</div>
