<!--
  AudioPlayer Component
  
  Advanced audio player with Web Audio API integration, featuring:
  - Gain control (volume boost up to +24dB)
  - High-pass filter with configurable frequency
  - Dynamic range compression
  - Visual spectrogram display
  - Play/pause controls with progress tracking
  - Download functionality
  
  Props:
  - audioUrl: URL of the audio file to play
  - detectionId: Unique ID for the detection
  - width: Player width in pixels (default: 200)
  - height: Player height in pixels (default: 80)
  - showSpectrogram: Show spectrogram image (default: true)
  - showDownload: Show download button (default: true)
  - className: Additional CSS classes
  - controlsClassName: CSS classes for controls container
-->

<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { cn } from '$lib/utils/cn.js';
  import { mediaIcons } from '$lib/utils/icons.js';
  import { t } from '$lib/i18n';

  // Web Audio API types - these are built-in browser types
  /* global AudioContext, MediaElementAudioSourceNode, GainNode, DynamicsCompressorNode, BiquadFilterNode, EventListener, ResizeObserver */

  interface Props {
    audioUrl: string;
    detectionId: string;
    width?: number | string;
    height?: number | string;
    showSpectrogram?: boolean;
    showDownload?: boolean;
    className?: string;
    responsive?: boolean;
  }

  let {
    audioUrl,
    detectionId,
    width = 200,
    height = 80,
    showSpectrogram = true,
    showDownload = true,
    className = '',
    responsive = false,
  }: Props = $props();

  // Audio and UI elements
  let audioElement: HTMLAudioElement;
  let playerContainer: HTMLDivElement;
  let playPauseButton: HTMLButtonElement;
  let progressBar: HTMLDivElement;
  // svelte-ignore non_reactive_update
  let volumeControl: HTMLDivElement;
  // svelte-ignore non_reactive_update
  let filterControl: HTMLDivElement;
  // svelte-ignore non_reactive_update
  let volumeSlider: HTMLDivElement;
  // svelte-ignore non_reactive_update
  let filterSlider: HTMLDivElement;

  // Audio state
  let isPlaying = $state(false);
  let currentTime = $state(0);
  let duration = $state(0);
  let audioContextAvailable = $state(true);
  let audioContextError = $state<string | null>(null);
  let progress = $state(0);
  let isLoading = $state(false);
  let error = $state<string | null>(null);
  let updateInterval: ReturnType<typeof setInterval> | undefined;

  // Audio processing state
  let audioContext: AudioContext | null = null;
  let audioNodes: {
    source: MediaElementAudioSourceNode;
    gain: GainNode;
    compressor: DynamicsCompressorNode;
    filters: { highPass: BiquadFilterNode };
  } | null = null;

  // Cleanup tracking for memory leak prevention
  let resizeObserver: ResizeObserver | null = null;
  let sliderTimeout: ReturnType<typeof setTimeout> | undefined;
  let eventListeners: Array<{
    element: HTMLElement | Document | Window;
    event: string;
    handler: EventListener;
  }> = [];

  // Control state
  let gainValue = $state(0); // dB
  let filterFreq = $state(20); // Hz
  let showVolumeSlider = $state(false);
  let showFilterSlider = $state(false);
  let showControls = $state(true); // Will be set based on width
  let isMobile = $state(false);

  // Constants
  const GAIN_MAX_DB = 24;
  const FILTER_HP_MIN_FREQ = 20;
  const FILTER_HP_MAX_FREQ = 10000;
  const FILTER_HP_DEFAULT_FREQ = 20;

  // Computed values
  const spectrogramUrl = $derived(showSpectrogram ? `/api/v2/spectrogram/${detectionId}` : null);

  const playPauseId = $derived(`playPause-${detectionId}`);
  const audioId = $derived(`audio-${detectionId}`);
  const progressId = $derived(`progress-${detectionId}`);

  // Utility functions
  const dbToGain = (db: number): number => Math.pow(10, db / 20);
  const formatTime = (seconds: number): string => {
    const mins = Math.floor(seconds / 60);
    const secs = Math.floor(seconds % 60);
    return `${mins}:${secs.toString().padStart(2, '0')}`;
  };

  // Memory leak prevention helpers
  const addTrackedEventListener = (
    element: HTMLElement | Document | Window,
    event: string,
    handler: EventListener,
    options?: boolean | AddEventListenerOptions
  ) => {
    element.addEventListener(event, handler, options);
    eventListeners.push({ element, event, handler });
  };

  const clearSliderTimeout = () => {
    if (sliderTimeout) {
      clearTimeout(sliderTimeout);
      sliderTimeout = undefined;
    }
  };

  const resetSliderTimeout = () => {
    clearSliderTimeout();
    sliderTimeout = setTimeout(() => {
      showVolumeSlider = false;
      showFilterSlider = false;
    }, 5000);
  };

  // Audio context setup
  const initializeAudioContext = async () => {
    try {
      // Check if AudioContext is available
      const AudioContextClass = window.AudioContext || (window as any).webkitAudioContext;
      if (!AudioContextClass) {
        throw new Error('AudioContext not supported');
      }

      audioContext = new AudioContextClass();

      if (audioContext.state === 'suspended') {
        await audioContext.resume();
      }

      audioContextAvailable = true;
      audioContextError = null;
      return audioContext;
    } catch (_e) {
      // eslint-disable-next-line no-console
      console.warn('Web Audio API is not supported in this browser');
      audioContextAvailable = false;
      audioContextError =
        'Advanced audio features (volume control, filtering) are not available in this browser.';
      return null;
    }
  };

  // Create audio processing nodes
  const createAudioNodes = (audioContext: AudioContext, audio: HTMLAudioElement) => {
    const audioSource = audioContext.createMediaElementSource(audio);
    const gainNode = audioContext.createGain();
    gainNode.gain.value = 1;

    const highPassFilter = audioContext.createBiquadFilter();
    highPassFilter.type = 'highpass';
    highPassFilter.frequency.value = FILTER_HP_DEFAULT_FREQ;
    highPassFilter.Q.value = 1;

    const compressor = audioContext.createDynamicsCompressor();
    compressor.threshold.value = -24;
    compressor.knee.value = 30;
    compressor.ratio.value = 12;
    compressor.attack.value = 0.003;
    compressor.release.value = 0.25;

    audioSource
      .connect(highPassFilter)
      .connect(gainNode)
      .connect(compressor)
      .connect(audioContext.destination);

    return {
      source: audioSource,
      gain: gainNode,
      compressor,
      filters: {
        highPass: highPassFilter,
      },
    };
  };

  // Audio event handlers
  const handlePlayPause = async () => {
    if (!audioElement) return;

    try {
      if (isPlaying) {
        audioElement.pause();
      } else {
        // Initialize audio context on first play
        if (!audioContext) {
          audioContext = await initializeAudioContext();
          if (audioContext && !audioNodes) {
            audioNodes = createAudioNodes(audioContext, audioElement);
          }
        }

        await audioElement.play();
      }
    } catch (err) {
      console.error('Error playing audio:', err);
      error = 'Failed to play audio';
    }
  };

  const updateProgress = () => {
    if (!audioElement) return;
    currentTime = audioElement.currentTime;
    if (duration > 0) {
      progress = (currentTime / duration) * 100;
    }
  };

  const startInterval = () => {
    if (updateInterval) clearInterval(updateInterval);
    updateInterval = setInterval(updateProgress, 100);
  };

  const stopInterval = () => {
    if (updateInterval) {
      clearInterval(updateInterval);
      updateInterval = undefined;
    }
  };

  const handleTimeUpdate = () => {
    // Keep as fallback but primary updates come from interval
    updateProgress();
  };

  const handleLoadedMetadata = () => {
    if (!audioElement) return;
    duration = audioElement.duration;
    isLoading = false;
    error = null;
  };

  const handleProgressClick = (event: MouseEvent) => {
    if (!audioElement || !progressBar) return;

    const rect = progressBar.getBoundingClientRect();
    const clickX = event.clientX - rect.left;
    const clickPercent = clickX / rect.width;
    const newTime = clickPercent * duration;

    audioElement.currentTime = Math.max(0, Math.min(newTime, duration));
  };

  // Volume control
  const updateGain = (newGainDb: number) => {
    gainValue = Math.max(-60, Math.min(GAIN_MAX_DB, newGainDb));
    if (audioNodes) {
      audioNodes.gain.gain.value = dbToGain(gainValue);
    }
  };

  const handleVolumeSlider = (event: MouseEvent) => {
    if (!volumeSlider) return;

    const rect = volumeSlider.getBoundingClientRect();
    const clickY = event.clientY - rect.top;
    const clickPercent = 1 - clickY / rect.height;
    const newGainDb = clickPercent * (GAIN_MAX_DB + 60) - 60;

    updateGain(newGainDb);
  };

  // Filter control
  const updateFilter = (newFreq: number) => {
    filterFreq = Math.max(FILTER_HP_MIN_FREQ, Math.min(FILTER_HP_MAX_FREQ, newFreq));
    if (audioNodes) {
      audioNodes.filters.highPass.frequency.value = filterFreq;
    }
  };

  const handleFilterSlider = (event: MouseEvent) => {
    if (!filterSlider) return;

    const rect = filterSlider.getBoundingClientRect();
    const clickY = event.clientY - rect.top;
    const clickPercent = 1 - clickY / rect.height;
    const newFreq = FILTER_HP_MIN_FREQ + clickPercent * (FILTER_HP_MAX_FREQ - FILTER_HP_MIN_FREQ);

    updateFilter(newFreq);
  };

  // Lifecycle
  onMount(() => {
    // Check if mobile
    isMobile = 'ontouchstart' in window || navigator.maxTouchPoints > 0;

    // Check width for control visibility
    if (playerContainer) {
      const checkWidth = () => {
        showControls = playerContainer.offsetWidth >= 175;
      };
      checkWidth();

      // Create and track ResizeObserver properly
      resizeObserver = new ResizeObserver(checkWidth);
      resizeObserver.observe(playerContainer);
    }

    if (audioElement) {
      // Add all audio event listeners with proper tracking
      addTrackedEventListener(audioElement, 'play', () => {
        isPlaying = true;
        startInterval();
      });

      addTrackedEventListener(audioElement, 'pause', () => {
        isPlaying = false;
        stopInterval();
      });

      addTrackedEventListener(audioElement, 'ended', () => {
        isPlaying = false;
        stopInterval();
      });

      addTrackedEventListener(audioElement, 'timeupdate', handleTimeUpdate);
      addTrackedEventListener(audioElement, 'loadedmetadata', handleLoadedMetadata);

      addTrackedEventListener(audioElement, 'loadstart', () => {
        isLoading = true;
      });

      addTrackedEventListener(audioElement, 'error', () => {
        error = 'Failed to load audio';
        isLoading = false;
      });

      // If already playing when mounted, start interval
      if (!audioElement.paused) {
        startInterval();
      }
    }
  });

  // Watch for slider visibility changes with proper cleanup
  $effect(() => {
    if (showVolumeSlider || showFilterSlider) {
      resetSliderTimeout();
    }

    // Cleanup function for the effect
    return () => {
      clearSliderTimeout();
    };
  });

  onDestroy(() => {
    // Stop any running intervals
    stopInterval();

    // Clear any pending timeouts
    clearSliderTimeout();

    // Remove all tracked event listeners
    eventListeners.forEach(({ element, event, handler }) => {
      element.removeEventListener(event, handler);
    });
    eventListeners = [];

    // Disconnect ResizeObserver
    if (resizeObserver) {
      resizeObserver.disconnect();
      resizeObserver = null;
    }

    // Clean up Web Audio API resources
    if (audioNodes) {
      try {
        audioNodes.source.disconnect();
        audioNodes.gain.disconnect();
        audioNodes.compressor.disconnect();
        audioNodes.filters.highPass.disconnect();
      } catch (_e) {
        // Nodes may already be disconnected, ignore errors
        // eslint-disable-next-line no-console
        console.warn('Error disconnecting audio nodes during cleanup');
      }
      audioNodes = null;
    }

    // Close audio context
    if (audioContext) {
      try {
        audioContext.close();
      } catch (_e) {
        // Context may already be closed, ignore errors
        // eslint-disable-next-line no-console
        console.warn('Error closing audio context during cleanup');
      }
      audioContext = null;
    }
  });

  // Icons imported from centralized icon constants
</script>

<div
  bind:this={playerContainer}
  class={cn('relative group', className)}
  style={responsive
    ? ''
    : `width: ${typeof width === 'number' ? width + 'px' : width}; height: ${typeof height === 'number' ? height + 'px' : height};`}
>
  {#if spectrogramUrl}
    <img
      src={spectrogramUrl}
      alt="Audio spectrogram"
      loading="lazy"
      class={responsive
        ? 'w-full h-auto object-contain rounded-md border border-base-300'
        : 'w-full h-full object-cover rounded-md border border-base-300'}
      style={responsive
        ? ''
        : `width: ${typeof width === 'number' ? width + 'px' : width}; height: ${typeof height === 'number' ? height + 'px' : height};`}
      width={responsive ? 400 : undefined}
    />
  {/if}

  <!-- Audio element -->
  <audio bind:this={audioElement} id={audioId} src={audioUrl} preload="metadata" class="hidden">
    <track kind="captions" />
  </audio>

  <!-- Volume control (top controls) -->
  {#if showControls}
    <div
      bind:this={volumeControl}
      class="absolute top-2 right-2 volume-control transition-opacity duration-200"
      class:opacity-0={!isMobile && !showVolumeSlider}
      class:group-hover:opacity-100={!isMobile}
    >
      <button
        class="flex items-center justify-center gap-1 text-white px-2 py-1 rounded-full bg-black bg-opacity-50 hover:bg-opacity-75 transition-all duration-200"
        class:cursor-not-allowed={!audioContextAvailable}
        class:opacity-50={!audioContextAvailable}
        disabled={!audioContextAvailable}
        onclick={() => {
          if (audioContextAvailable) {
            showVolumeSlider = !showVolumeSlider;
            if (showVolumeSlider) showFilterSlider = false;
          }
        }}
        aria-label={t('media.audio.volume')}
        title={!audioContextAvailable
          ? audioContextError || 'Volume control unavailable'
          : 'Volume control'}
      >
        {@html mediaIcons.volume}
        <span class="text-xs text-white">{gainValue > 0 ? '+' : ''}{gainValue} dB</span>
      </button>

      {#if showVolumeSlider}
        <div
          bind:this={volumeSlider}
          class="absolute top-0 w-8 bg-black bg-opacity-20 backdrop-blur-sm rounded p-2 volume-slider z-50"
          style:left="calc(100% + 4px)"
          style:height="{height}px"
          role="button"
          tabindex="0"
          aria-label={t('media.audio.volumeGain', { value: gainValue })}
          onclick={handleVolumeSlider}
          onkeydown={e => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault();
              const rect = volumeSlider.getBoundingClientRect();
              const centerY = rect.top + rect.height / 2;
              const mockEvent = { clientY: centerY } as MouseEvent;
              handleVolumeSlider(mockEvent);
            }
          }}
        >
          <div class="relative h-full w-2 bg-white bg-opacity-50 rounded-full mx-auto">
            <div
              class="absolute bottom-0 w-full bg-blue-500 rounded-full transition-all duration-100"
              style:height="{(gainValue / GAIN_MAX_DB) * 100}%"
            ></div>
          </div>
        </div>
      {/if}
    </div>
  {/if}

  <!-- Filter control (top controls) -->
  {#if showControls}
    <div
      bind:this={filterControl}
      class="absolute top-2 left-2 filter-control transition-opacity duration-200"
      class:opacity-0={!isMobile && !showFilterSlider}
      class:group-hover:opacity-100={!isMobile}
    >
      <button
        class="flex items-center justify-center gap-1 text-white px-2 py-1 rounded-full bg-black bg-opacity-50 hover:bg-opacity-75 transition-all duration-200"
        class:cursor-not-allowed={!audioContextAvailable}
        class:opacity-50={!audioContextAvailable}
        disabled={!audioContextAvailable}
        onclick={() => {
          if (audioContextAvailable) {
            showFilterSlider = !showFilterSlider;
            if (showFilterSlider) showVolumeSlider = false;
          }
        }}
        aria-label={t('media.audio.filterControl')}
        title={!audioContextAvailable
          ? audioContextError || 'Filter control unavailable'
          : 'Filter control'}
      >
        <span class="text-xs text-white">HP: {Math.round(filterFreq)} Hz</span>
      </button>

      {#if showFilterSlider}
        <div
          bind:this={filterSlider}
          class="absolute top-0 w-8 bg-black bg-opacity-20 backdrop-blur-sm rounded p-2 filter-slider z-50"
          style:right="calc(100% + 4px)"
          style:height="{height}px"
          role="button"
          tabindex="0"
          aria-label={t('media.audio.highPassFilter', { freq: Math.round(filterFreq) })}
          onclick={handleFilterSlider}
          onkeydown={e => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault();
              const rect = filterSlider.getBoundingClientRect();
              const centerY = rect.top + rect.height / 2;
              const mockEvent = { clientY: centerY } as MouseEvent;
              handleFilterSlider(mockEvent);
            }
          }}
        >
          <div class="relative h-full w-2 bg-white bg-opacity-50 rounded-full mx-auto">
            <div
              class="absolute bottom-0 w-full bg-blue-500 rounded-full transition-all duration-100"
              style:height="{(Math.log(filterFreq / FILTER_HP_MIN_FREQ) /
                Math.log(FILTER_HP_MAX_FREQ / FILTER_HP_MIN_FREQ)) *
                100}%"
            ></div>
          </div>
        </div>
      {/if}
    </div>
  {/if}

  <!-- Play position indicator -->
  <div
    class="absolute top-0 bottom-0 w-0.5 bg-gray-100 pointer-events-none"
    style:left="{progress}%"
    style:transition="left 0.1s linear"
    style:opacity={progress > 0 && progress < 100 ? '0.7' : '0'}
  ></div>

  <!-- Bottom overlay controls -->
  <div
    class="absolute bottom-0 left-0 right-0 bg-black bg-opacity-25 p-1 rounded-b-md transition-opacity duration-300"
    class:opacity-0={!isMobile}
    class:group-hover:opacity-100={!isMobile}
    class:opacity-100={isMobile}
  >
    <div class="flex items-center justify-between">
      <!-- Play/Pause button -->
      <button
        bind:this={playPauseButton}
        id={playPauseId}
        class="text-white p-1 rounded-full hover:bg-white hover:bg-opacity-20 flex-shrink-0"
        onclick={handlePlayPause}
        disabled={isLoading}
        aria-label={isPlaying ? t('media.audio.pause') : t('media.audio.play')}
      >
        {#if isLoading}
          <div
            class="w-4 h-4 border-2 border-white border-t-transparent rounded-full animate-spin"
          ></div>
        {:else}
          {@html isPlaying ? mediaIcons.pause : mediaIcons.play}
        {/if}
      </button>

      <!-- Progress bar -->
      <div
        bind:this={progressBar}
        id={progressId}
        class="flex-grow bg-gray-200 rounded-full h-1.5 mx-2 cursor-pointer"
        role="button"
        tabindex="0"
        aria-label={t('media.audio.seekProgress', {
          current: Math.floor(currentTime),
          total: Math.floor(duration),
        })}
        onclick={handleProgressClick}
        onkeydown={e => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            const rect = progressBar.getBoundingClientRect();
            const centerX = rect.left + rect.width / 2;
            const mockEvent = { clientX: centerX } as MouseEvent;
            handleProgressClick(mockEvent);
          }
        }}
      >
        <div
          class="bg-blue-600 h-1.5 rounded-full transition-all duration-100"
          style:width="{progress}%"
        ></div>
      </div>

      <!-- Current time -->
      <span class="text-xs font-medium text-white flex-shrink-0">{formatTime(currentTime)}</span>

      <!-- Download button -->
      {#if showDownload}
        <a
          href={audioUrl}
          download
          class="text-white p-1 rounded-full hover:bg-white hover:bg-opacity-20 ml-2 flex-shrink-0"
          aria-label={t('media.audio.download')}
        >
          {@html mediaIcons.download}
        </a>
      {/if}
    </div>
  </div>

  <!-- Error message -->
  {#if error}
    <div
      class="absolute inset-0 flex items-center justify-center bg-red-100 dark:bg-red-900 rounded-md"
    >
      <span class="text-red-600 dark:text-red-300 text-sm">{error}</span>
    </div>
  {/if}
</div>

<style>
  .volume-slider,
  .filter-slider {
    z-index: 1000;
  }

  .volume-control button,
  .filter-control button {
    backdrop-filter: blur(4px);
  }
</style>
