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

  interface Props {
    audioUrl: string;
    detectionId: string;
    width?: number;
    height?: number;
    showSpectrogram?: boolean;
    showDownload?: boolean;
    className?: string;
    controlsClassName?: string;
  }

  let {
    audioUrl,
    detectionId,
    width = 200,
    height = 80,
    showSpectrogram = true,
    showDownload = true,
    className = '',
    controlsClassName = ''
  }: Props = $props();

  // Audio and UI elements
  let audioElement: HTMLAudioElement;
  let playerContainer: HTMLDivElement;
  let playPauseButton: HTMLButtonElement;
  let progressBar: HTMLDivElement;
  let volumeControl: HTMLDivElement;
  let filterControl: HTMLDivElement;
  let volumeSlider: HTMLDivElement;
  let filterSlider: HTMLDivElement;

  // Audio state
  let isPlaying = $state(false);
  let currentTime = $state(0);
  let duration = $state(0);
  let progress = $state(0);
  let isLoading = $state(false);
  let error = $state<string | null>(null);
  let updateInterval: number | undefined;

  // Audio processing state
  let audioContext: AudioContext | null = null;
  let audioNodes: {
    source: MediaElementAudioSourceNode;
    gain: GainNode;
    compressor: DynamicsCompressorNode;
    filters: { highPass: BiquadFilterNode };
  } | null = null;

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
  const spectrogramUrl = $derived(
    showSpectrogram ? `/api/v2/spectrogram/${detectionId}` : null
  );

  const playPauseId = $derived(`playPause-${detectionId}`);
  const audioId = $derived(`audio-${detectionId}`);
  const progressId = $derived(`progress-${detectionId}`);

  // Utility functions
  const dbToGain = (db: number): number => Math.pow(10, db / 20);
  const gainToDb = (gain: number): number => 20 * Math.log10(gain);
  const formatTime = (seconds: number): string => {
    const mins = Math.floor(seconds / 60);
    const secs = Math.floor(seconds % 60);
    return `${mins}:${secs.toString().padStart(2, '0')}`;
  };

  // Audio context setup
  const initializeAudioContext = async () => {
    try {
      audioContext = new (window.AudioContext || (window as any).webkitAudioContext)();
      
      if (audioContext.state === 'suspended') {
        await audioContext.resume();
      }
      
      return audioContext;
    } catch (e) {
      console.warn('Web Audio API is not supported in this browser');
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
        highPass: highPassFilter
      }
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
    const clickPercent = 1 - (clickY / rect.height);
    const newGainDb = (clickPercent * (GAIN_MAX_DB + 60)) - 60;
    
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
    const clickPercent = 1 - (clickY / rect.height);
    const newFreq = FILTER_HP_MIN_FREQ + (clickPercent * (FILTER_HP_MAX_FREQ - FILTER_HP_MIN_FREQ));
    
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
      
      const resizeObserver = new ResizeObserver(checkWidth);
      resizeObserver.observe(playerContainer);
      
      onDestroy(() => resizeObserver.disconnect());
    }
    
    if (audioElement) {
      audioElement.addEventListener('play', () => { 
        isPlaying = true; 
        startInterval();
      });
      audioElement.addEventListener('pause', () => { 
        isPlaying = false; 
        stopInterval();
      });
      audioElement.addEventListener('ended', () => {
        isPlaying = false;
        stopInterval();
      });
      audioElement.addEventListener('timeupdate', handleTimeUpdate);
      audioElement.addEventListener('loadedmetadata', handleLoadedMetadata);
      audioElement.addEventListener('loadstart', () => { isLoading = true; });
      audioElement.addEventListener('error', () => { 
        error = 'Failed to load audio'; 
        isLoading = false; 
      });
      
      // If already playing when mounted, start interval
      if (!audioElement.paused) {
        startInterval();
      }
    }
    
    // Auto-hide sliders after 5 seconds
    let sliderTimeout: number | undefined;
    
    const resetSliderTimeout = () => {
      if (sliderTimeout) clearTimeout(sliderTimeout);
      sliderTimeout = setTimeout(() => {
        showVolumeSlider = false;
        showFilterSlider = false;
      }, 5000);
    };
    
    // Watch for slider visibility changes
    $effect(() => {
      if (showVolumeSlider || showFilterSlider) {
        resetSliderTimeout();
      }
    });
  });

  onDestroy(() => {
    stopInterval();
    if (audioNodes) {
      audioNodes.source.disconnect();
      audioNodes.gain.disconnect();
      audioNodes.compressor.disconnect();
      audioNodes.filters.highPass.disconnect();
    }
    if (audioContext) {
      audioContext.close();
    }
  });

  // SVG Icons
  const playIcon = `<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"></path>
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"></path>
  </svg>`;

  const pauseIcon = `<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 9v6m4-6v6m7-3a9 9 0 11-18 0 9 9 0 0118 0z"></path>
  </svg>`;

  const downloadIcon = `<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"></path>
  </svg>`;

  const volumeIcon = `<svg width="16" height="16" viewBox="0 0 24 24" style="color: white;" aria-hidden="true">
    <path d="M12 5v14l-7-7h-3v-4h3l7-7z" fill="currentColor"/>
    <path d="M16 8a4 4 0 0 1 0 8" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
    <path d="M19 5a8 8 0 0 1 0 14" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
  </svg>`;

  const filterIcon = `<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 4a1 1 0 011-1h16a1 1 0 011 1v2.586a1 1 0 01-.293.707l-6.414 6.414a1 1 0 00-.293.707V17l-4 4v-6.586a1 1 0 00-.293-.707L3.293 7.293A1 1 0 013 6.586V4z"></path>
  </svg>`;
</script>

<div bind:this={playerContainer} class={cn('relative group', className)} style="width: {width}px; height: {height}px;">
  {#if spectrogramUrl}
    <img 
      src={spectrogramUrl}
      alt="Audio spectrogram"
      loading="lazy"
      class="w-full h-full object-cover rounded-md border border-base-300"
      style="width: {width}px; height: {height}px;"
    />
  {/if}

  <!-- Audio element -->
  <audio 
    bind:this={audioElement}
    id={audioId}
    src={audioUrl}
    preload="metadata"
    class="hidden"
  >
    <track kind="captions" />
  </audio>

  <!-- Volume control (top controls) -->
  {#if showControls}
  <div bind:this={volumeControl} 
       class="absolute top-2 right-2 volume-control transition-opacity duration-200"
       class:opacity-0={!isMobile && !showVolumeSlider}
       class:group-hover:opacity-100={!isMobile}>
    <button 
      class="flex items-center justify-center gap-1 text-white px-2 py-1 rounded-full bg-black bg-opacity-50 hover:bg-opacity-75 transition-all duration-200"
      onclick={() => {
        showVolumeSlider = !showVolumeSlider;
        if (showVolumeSlider) showFilterSlider = false;
      }}
      aria-label="Volume control"
    >
      {@html volumeIcon}
      <span class="text-xs text-white">{gainValue > 0 ? '+' : ''}{gainValue} dB</span>
    </button>
    
    {#if showVolumeSlider}
      <div 
        bind:this={volumeSlider}
        class="absolute top-0 w-8 bg-black bg-opacity-20 backdrop-blur-sm rounded p-2 volume-slider z-50"
        style="left: calc(100% + 4px); height: {height}px;"
        onclick={handleVolumeSlider}
      >
        <div class="relative h-full w-2 bg-white bg-opacity-50 rounded-full mx-auto">
          <div 
            class="absolute bottom-0 w-full bg-blue-500 rounded-full transition-all duration-100"
            style="height: {(gainValue / GAIN_MAX_DB) * 100}%"
          ></div>
        </div>
      </div>
    {/if}
  </div>
  {/if}

  <!-- Filter control (top controls) -->
  {#if showControls}
  <div bind:this={filterControl} 
       class="absolute top-2 left-2 filter-control transition-opacity duration-200"
       class:opacity-0={!isMobile && !showFilterSlider}
       class:group-hover:opacity-100={!isMobile}>
    <button 
      class="flex items-center justify-center gap-1 text-white px-2 py-1 rounded-full bg-black bg-opacity-50 hover:bg-opacity-75 transition-all duration-200"
      onclick={() => {
        showFilterSlider = !showFilterSlider;
        if (showFilterSlider) showVolumeSlider = false;
      }}
      aria-label="Filter control"
    >
      <span class="text-xs text-white">HP: {Math.round(filterFreq)} Hz</span>
    </button>
    
    {#if showFilterSlider}
      <div 
        bind:this={filterSlider}
        class="absolute top-0 w-8 bg-black bg-opacity-20 backdrop-blur-sm rounded p-2 filter-slider z-50"
        style="right: calc(100% + 4px); height: {height}px;"
        onclick={handleFilterSlider}
      >
        <div class="relative h-full w-2 bg-white bg-opacity-50 rounded-full mx-auto">
          <div 
            class="absolute bottom-0 w-full bg-blue-500 rounded-full transition-all duration-100"
            style="height: {Math.log(filterFreq/FILTER_HP_MIN_FREQ) / Math.log(FILTER_HP_MAX_FREQ/FILTER_HP_MIN_FREQ) * 100}%"
            ></div>
        </div>
      </div>
    {/if}
  </div>
  {/if}

  <!-- Play position indicator -->
  <div 
    class="absolute top-0 bottom-0 w-0.5 bg-gray-100 pointer-events-none"
    style="left: {progress}%; transition: left 0.1s linear; opacity: {(progress > 0 && progress < 100) ? '0.7' : '0'};"
  ></div>

  <!-- Bottom overlay controls -->
  <div class="absolute bottom-0 left-0 right-0 bg-black bg-opacity-25 p-1 rounded-b-md transition-opacity duration-300"
       class:opacity-0={!isMobile}
       class:group-hover:opacity-100={!isMobile}
       class:opacity-100={isMobile}>
    <div class="flex items-center justify-between">
      <!-- Play/Pause button -->
      <button 
        bind:this={playPauseButton}
        id={playPauseId}
        class="text-white p-1 rounded-full hover:bg-white hover:bg-opacity-20 flex-shrink-0"
        onclick={handlePlayPause}
        disabled={isLoading}
        aria-label={isPlaying ? 'Pause' : 'Play'}
      >
        {#if isLoading}
          <div class="w-4 h-4 border-2 border-white border-t-transparent rounded-full animate-spin"></div>
        {:else}
          {@html isPlaying ? pauseIcon : playIcon}
        {/if}
      </button>

      <!-- Progress bar -->
      <div 
        bind:this={progressBar}
        id={progressId}
        class="flex-grow bg-gray-200 rounded-full h-1.5 mx-2 cursor-pointer"
        onclick={handleProgressClick}
      >
        <div 
          class="bg-blue-600 h-1.5 rounded-full transition-all duration-100"
          style="width: {progress}%"
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
          aria-label="Download audio"
        >
          {@html downloadIcon}
        </a>
      {/if}
    </div>
  </div>


  <!-- Error message -->
  {#if error}
    <div class="absolute inset-0 flex items-center justify-center bg-red-100 dark:bg-red-900 rounded-md">
      <span class="text-red-600 dark:text-red-300 text-sm">{error}</span>
    </div>
  {/if}
</div>

<style>
  .volume-slider, .filter-slider {
    z-index: 1000;
  }
  
  .volume-control button, .filter-control button {
    backdrop-filter: blur(4px);
  }
</style>
