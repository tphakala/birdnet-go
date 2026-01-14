<!--
  PlayOverlay.svelte

  A centered play button overlay with audio playback functionality.
  Appears on card hover and handles audio playback for detection recordings.
  Features a playhead position indicator and click-to-seek on the spectrogram.

  Props:
  - detectionId: number - Detection ID for audio URL
  - onFreezeStart?: () => void - Callback when playback starts
  - onFreezeEnd?: () => void - Callback when playback ends
  - gainValue?: number - Audio gain in dB (controlled by parent)
  - filterFreq?: number - High-pass filter frequency in Hz (controlled by parent)
  - playbackSpeed?: number - Playback speed multiplier (controlled by parent)
  - onAudioContextAvailable?: (available: boolean) => void - Callback for audio context status
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { Play, Pause, Loader2 } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn';
  import { loggers } from '$lib/utils/logger';
  import { t } from '$lib/i18n';
  import { applyPlaybackRate, dbToGain } from '$lib/utils/audio';

  const logger = loggers.audio;

  /* global AudioContext, MediaElementAudioSourceNode, GainNode, BiquadFilterNode */

  interface Props {
    detectionId: number;
    onFreezeStart?: () => void;
    onFreezeEnd?: () => void;
    gainValue?: number;
    filterFreq?: number;
    playbackSpeed?: number;
    onAudioContextAvailable?: (_isAvailable: boolean) => void;
  }

  let {
    detectionId,
    onFreezeStart,
    onFreezeEnd,
    gainValue = 0,
    filterFreq = 20,
    playbackSpeed = 1.0,
    onAudioContextAvailable,
  }: Props = $props();

  let audioElement: HTMLAudioElement;
  let overlayElement: HTMLDivElement;
  let isPlaying = $state(false);
  let isLoading = $state(false);
  let error = $state<string | null>(null);
  let progress = $state(0);
  let duration = $state(0);
  let currentTime = $state(0);
  let isDragging = $state(false);
  let playEndTimeout: ReturnType<typeof setTimeout> | undefined;
  let updateInterval: ReturnType<typeof setInterval> | undefined;

  // Web Audio API
  let audioContext: AudioContext | null = null;
  let isInitializingContext = $state(false);
  let audioNodes = $state<{
    source: MediaElementAudioSourceNode;
    gain: GainNode;
    highPass: BiquadFilterNode;
  } | null>(null);

  const PLAY_END_DELAY_MS = 3000;

  const audioUrl = $derived(`/api/v2/audio/${detectionId}`);

  // Update audio nodes when gain/filter props change
  // Read values unconditionally to ensure they're tracked as dependencies
  $effect(() => {
    const currentGain = gainValue;
    const nodes = audioNodes;
    if (nodes) {
      nodes.gain.gain.value = dbToGain(currentGain);
    }
  });

  $effect(() => {
    const currentFilterFreq = filterFreq;
    const nodes = audioNodes;
    if (nodes) {
      nodes.highPass.frequency.value = currentFilterFreq;
    }
  });

  // Update playback speed when prop changes (during playback)
  $effect(() => {
    const speed = playbackSpeed;
    if (audioElement && isPlaying) {
      applyPlaybackRate(audioElement, speed);
    }
  });

  async function handlePlayPause(event: MouseEvent) {
    event.stopPropagation();

    if (!audioElement) return;

    // Clear any previous error when retrying
    error = null;

    try {
      if (isPlaying) {
        audioElement.pause();
      } else {
        // Initialize audio context on first play (for gain/filter controls)
        // Guard against rapid clicks that could create multiple AudioContexts
        if (!audioContext && !isInitializingContext) {
          isInitializingContext = true;
          try {
            audioContext = await initializeAudioContext();
            if (audioContext && !audioNodes) {
              audioNodes = createAudioNodes(audioContext, audioElement);
            }
          } finally {
            isInitializingContext = false;
          }
        }

        // Safety check: component may have unmounted during async initialization
        if (!audioElement) return;

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
    startProgressInterval();
    clearPlayEndTimeout();

    // Apply playback rate when starting
    if (audioElement) {
      applyPlaybackRate(audioElement, playbackSpeed);
    }

    onFreezeStart?.();
  }

  function handlePause() {
    isPlaying = false;
    stopProgressInterval();
    clearPlayEndTimeout();
    playEndTimeout = setTimeout(() => {
      onFreezeEnd?.();
    }, PLAY_END_DELAY_MS);
  }

  function handleEnded() {
    isPlaying = false;
    stopProgressInterval();
    progress = 0;
    currentTime = 0;
    clearPlayEndTimeout();
    playEndTimeout = setTimeout(() => {
      onFreezeEnd?.();
    }, PLAY_END_DELAY_MS);
  }

  // Smooth progress update using interval (like old AudioPlayer)
  function updateProgress() {
    if (!audioElement) return;
    currentTime = audioElement.currentTime;
    if (duration > 0) {
      progress = (currentTime / duration) * 100;
    }
  }

  function startProgressInterval() {
    if (updateInterval) clearInterval(updateInterval);
    updateInterval = setInterval(updateProgress, 50); // 50ms for smooth updates
  }

  function stopProgressInterval() {
    if (updateInterval) {
      clearInterval(updateInterval);
      updateInterval = undefined;
    }
  }

  function handleTimeUpdate() {
    // Fallback for when interval isn't running
    updateProgress();
  }

  function handleLoadedMetadata() {
    if (audioElement) {
      duration = audioElement.duration;
    }
  }

  // Seek to position when clicking on the overlay area
  function handleSeek(event: MouseEvent) {
    if (!audioElement || !overlayElement || duration === 0) return;

    const rect = overlayElement.getBoundingClientRect();
    const clickX = event.clientX - rect.left;
    const clickPercent = clickX / rect.width;
    const newTime = clickPercent * duration;

    audioElement.currentTime = Math.max(0, Math.min(newTime, duration));
    progress = clickPercent * 100;
  }

  // Handle mouse down for drag seeking
  function handleMouseDown(event: MouseEvent) {
    // Only handle left click
    if (event.button !== 0) return;

    isDragging = true;
    handleSeek(event);

    // Add document-level listeners for drag
    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);
  }

  function handleMouseMove(event: MouseEvent) {
    if (isDragging) {
      handleSeek(event);
    }
  }

  function handleMouseUp() {
    isDragging = false;
    document.removeEventListener('mousemove', handleMouseMove);
    document.removeEventListener('mouseup', handleMouseUp);
  }

  // Format time helper
  function formatTime(seconds: number): string {
    if (!isFinite(seconds) || seconds < 0) return '0:00';
    const mins = Math.floor(seconds / 60);
    const secs = Math.floor(seconds % 60);
    return `${mins}:${secs.toString().padStart(2, '0')}`;
  }

  async function initializeAudioContext(): Promise<AudioContext | null> {
    try {
      const AudioContextClass =
        window.AudioContext ||
        (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
      if (!AudioContextClass) {
        throw new Error('AudioContext not supported');
      }

      const ctx = new AudioContextClass();
      if (ctx.state === 'suspended') {
        await ctx.resume();
      }

      onAudioContextAvailable?.(true);
      return ctx;
    } catch (err) {
      logger.warn('Web Audio API not supported:', err);
      onAudioContextAvailable?.(false);
      return null;
    }
  }

  function createAudioNodes(ctx: AudioContext, audio: HTMLAudioElement) {
    const source = ctx.createMediaElementSource(audio);
    const gain = ctx.createGain();
    gain.gain.value = dbToGain(gainValue);

    const highPass = ctx.createBiquadFilter();
    highPass.type = 'highpass';
    highPass.frequency.value = filterFreq;
    highPass.Q.value = 1;

    // Connect: source -> highpass -> gain -> destination
    source.connect(highPass).connect(gain).connect(ctx.destination);

    return { source, gain, highPass };
  }

  function handleLoadStart() {
    // With preload="metadata", browser loads metadata only
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
      audioElement.addEventListener('loadedmetadata', handleLoadedMetadata);
      audioElement.addEventListener('loadstart', handleLoadStart);
      audioElement.addEventListener('canplay', handleCanPlay);
      audioElement.addEventListener('error', handleError);
    }
  });

  onDestroy(() => {
    clearPlayEndTimeout();
    stopProgressInterval();
    // Clean up drag listeners if component unmounts while dragging
    document.removeEventListener('mousemove', handleMouseMove);
    document.removeEventListener('mouseup', handleMouseUp);

    if (audioElement) {
      audioElement.removeEventListener('play', handlePlay);
      audioElement.removeEventListener('pause', handlePause);
      audioElement.removeEventListener('ended', handleEnded);
      audioElement.removeEventListener('timeupdate', handleTimeUpdate);
      audioElement.removeEventListener('loadedmetadata', handleLoadedMetadata);
      audioElement.removeEventListener('loadstart', handleLoadStart);
      audioElement.removeEventListener('canplay', handleCanPlay);
      audioElement.removeEventListener('error', handleError);
    }

    // Clean up Web Audio API
    if (audioNodes) {
      try {
        audioNodes.source.disconnect();
        audioNodes.gain.disconnect();
        audioNodes.highPass.disconnect();
      } catch {
        // Nodes may already be disconnected
      }
      audioNodes = null;
    }

    if (audioContext) {
      try {
        audioContext.close();
      } catch {
        // Context may already be closed
      }
      audioContext = null;
    }
  });
</script>

<!-- Audio element (hidden) -->
<audio bind:this={audioElement} src={audioUrl} preload="metadata" class="hidden">
  <track kind="captions" />
</audio>

<!-- Seek area - covers the full card for click-to-seek -->
<div
  bind:this={overlayElement}
  class="seek-overlay"
  role="slider"
  tabindex="0"
  aria-label={t('media.audio.seekProgress', {
    current: Math.floor(currentTime),
    total: Math.floor(duration),
  })}
  aria-valuemin={0}
  aria-valuemax={100}
  aria-valuenow={Math.round(progress)}
  aria-valuetext={`${formatTime(currentTime)} of ${formatTime(duration)}`}
  onmousedown={handleMouseDown}
  onkeydown={e => {
    if (!audioElement || duration === 0) return;
    const SMALL_SKIP = 5;
    const LARGE_SKIP = 10;

    switch (e.key) {
      case 'ArrowRight':
        e.preventDefault();
        audioElement.currentTime = Math.min(duration, currentTime + SMALL_SKIP);
        break;
      case 'ArrowLeft':
        e.preventDefault();
        audioElement.currentTime = Math.max(0, currentTime - SMALL_SKIP);
        break;
      case 'PageUp':
        e.preventDefault();
        audioElement.currentTime = Math.min(duration, currentTime + LARGE_SKIP);
        break;
      case 'PageDown':
        e.preventDefault();
        audioElement.currentTime = Math.max(0, currentTime - LARGE_SKIP);
        break;
      case 'Home':
        e.preventDefault();
        audioElement.currentTime = 0;
        break;
      case 'End':
        e.preventDefault();
        audioElement.currentTime = duration;
        break;
    }
  }}
>
  <!-- Playhead position indicator -->
  {#if progress > 0 || isPlaying}
    <div
      class="playhead"
      style:left={`${progress}%`}
      style:opacity={progress > 0 && progress < 100 ? 1 : 0}
    ></div>
  {/if}
</div>

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

<!-- Bottom controls bar - time display only -->
<div class="controls-bar">
  <div class="time-display">
    {#if duration > 0}
      <span>{formatTime(currentTime)}</span>
      <span class="time-separator">/</span>
      <span>{formatTime(duration)}</span>
    {/if}
  </div>
</div>

<style>
  /* Seek overlay - covers card for click-to-seek */
  /* z-index 5: behind badges (z-10) but above spectrogram */
  .seek-overlay {
    position: absolute;
    inset: 0;
    z-index: 5;
    cursor: pointer;
  }

  /* Playhead position indicator - white like old player */
  .playhead {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 2px;
    background-color: rgb(255 255 255 / 0.85);
    box-shadow: 0 0 3px rgb(0 0 0 / 0.4);
    pointer-events: none;
    transition: left 0.05s linear;
    transform: translateX(-50%);
  }

  .play-overlay {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 20;
    pointer-events: none;
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
    pointer-events: auto;
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

  /* Bottom controls bar */
  .controls-bar {
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    display: flex;
    align-items: center;
    padding: 0.375rem 0.5rem;
    background: linear-gradient(to top, rgb(0 0 0 / 0.6) 0%, transparent 100%);
    z-index: 25;
    opacity: 0;
    transition: opacity 0.2s ease;
  }

  :global(.group:hover) .controls-bar {
    opacity: 1;
  }

  /* Time display */
  .time-display {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    font-size: 0.75rem;
    font-weight: 500;
    color: white;
    text-shadow: 0 1px 2px rgb(0 0 0 / 0.5);
    font-variant-numeric: tabular-nums;
  }

  .time-separator {
    opacity: 0.7;
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
    pointer-events: auto;
  }

  :global(.dark) .error-indicator {
    background-color: rgb(127 29 29 / 0.9);
    color: rgb(254 202 202);
  }
</style>
