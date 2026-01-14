<!--
  AudioPlayer Component
  
  Advanced audio player with Web Audio API integration, featuring:
  - Gain control (volume boost up to +24dB)
  - High-pass filter with configurable frequency
  - Dynamic range compression
  - Visual spectrogram display
  - Play/pause controls with progress tracking
  - Download functionality
  - SSE update freezing during playback to prevent UI disruption
  
  Props:
  - audioUrl: URL of the audio file to play
  - detectionId: Unique ID for the detection
  - width: Player width in pixels (default: 200)
  - height: Player height in pixels (default: 80)
  - showSpectrogram: Show spectrogram image (default: true)
  - showDownload: Show download button (default: true)
  - className: Additional CSS classes
  - responsive: Enable responsive sizing (default: false)
    When true, the component adapts to its container width and maintains aspect ratio.
    Use for flexible layouts like cards and tables. When false, uses fixed dimensions.
  - spectrogramSize: Spectrogram display size - sm/md/lg/xl (default: md)
  - spectrogramRaw: Display raw spectrogram without axes (default: false)
  - onPlayStart: Callback when audio starts playing (freezes SSE updates)
  - onPlayEnd: Callback when audio stops playing after 3s delay (resumes SSE updates)
-->

<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { cn } from '$lib/utils/cn.js';
  import { Play, Pause, Download, XCircle } from '@lucide/svelte';
  import AudioSettingsButton from '$lib/desktop/features/dashboard/components/AudioSettingsButton.svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { useDelayedLoading } from '$lib/utils/delayedLoading.svelte.js';
  import { applyPlaybackRate, dbToGain } from '$lib/utils/audio';

  const logger = loggers.audio;

  // Debug logging helper - bypasses linter warnings when debug flag is enabled
  const debugLog = (message: string, data?: unknown) => {
    if (debug) {
      // eslint-disable-next-line no-console
      console.log(`[AudioPlayer:${detectionId}] ${message}`, data || '');
    }
  };

  // Web Audio API types - these are built-in browser types
  /* global AudioContext, MediaElementAudioSourceNode, GainNode, DynamicsCompressorNode, BiquadFilterNode, EventListener, ResizeObserver */

  // Size type for spectrogram
  type SpectrogramSize = 'sm' | 'md' | 'lg' | 'xl';

  interface Props {
    audioUrl: string;
    detectionId: string;
    width?: number | string;
    height?: number | string;
    showSpectrogram?: boolean;
    showDownload?: boolean;
    className?: string;
    responsive?: boolean;
    /** Spectrogram display size: sm (400px), md (800px), lg (1000px), xl (1200px) */
    spectrogramSize?: SpectrogramSize;
    /** Display raw spectrogram without axes and legends */
    spectrogramRaw?: boolean;
    /** Fired when audio playback starts - freezes detection updates */
    onPlayStart?: () => void;
    /** Fired 3 seconds after audio stops - resumes detection updates */
    onPlayEnd?: () => void;
    /** Enable debug logging for troubleshooting multi-session issues */
    debug?: boolean;
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
    spectrogramSize = 'md',
    spectrogramRaw = false,
    onPlayStart,
    onPlayEnd,
    debug = false,
  }: Props = $props();

  // Audio and UI elements
  let audioElement: HTMLAudioElement;
  let playerContainer: HTMLDivElement;
  let playPauseButton!: HTMLButtonElement; // Template-only binding
  let progressBar: HTMLDivElement;
  // svelte-ignore non_reactive_update
  let spectrogramImage: HTMLImageElement; // Template-only binding

  // Audio state
  let isPlaying = $state(false);
  let currentTime = $state(0);
  let duration = $state(0);
  let audioContextAvailable = $state(true);
  let progress = $state(0);
  let isLoading = $state(false);
  let error = $state<string | null>(null);
  let updateInterval: ReturnType<typeof setInterval> | undefined;

  // Spectrogram loading with delayed spinner
  const spectrogramLoader = useDelayedLoading({
    delayMs: 150,
    timeoutMs: 60000, // Increased to 60 seconds for large queues
    onTimeout: () => {
      logger.warn('Spectrogram loading timeout', { detectionId });
    },
  });

  // Spectrogram retry configuration
  const MAX_SPECTROGRAM_RETRIES = 4;
  const SPECTROGRAM_RETRY_DELAYS = [500, 1000, 2000, 4000]; // Exponential backoff in ms
  const SPECTROGRAM_POLL_INTERVAL = 2000; // Poll every 2 seconds
  const MAX_POLL_DURATION = 5 * 60 * 1000; // Maximum 5 minutes of polling

  // Spectrogram retry state
  let spectrogramRetryCount = $state(0);
  let spectrogramRetryTimer: ReturnType<typeof setTimeout> | undefined;

  // Spectrogram generation status
  let spectrogramStatus = $state<{
    status: string;
    queuePosition: number;
    message: string;
  } | null>(null);
  let statusPollTimer: ReturnType<typeof setTimeout> | undefined;
  let statusPollStartTime: number | undefined;
  let statusPollAbortController: AbortController | undefined;

  // User-requested spectrogram generation state
  // TODO: Wire up UI components for generate button when spectrogramNeedsGeneration is true
  // eslint-disable-next-line no-unused-vars
  let spectrogramNeedsGeneration = $state(false);
  let isGeneratingSpectrogram = $state(false);
  // eslint-disable-next-line no-unused-vars
  let generationError = $state<string | null>(null);
  // Default to "auto" mode if backend doesn't specify
  let spectrogramMode = $state<string>('auto');

  // Audio processing state
  let audioContext: AudioContext | null = null;
  let isInitializingContext = $state(false);
  let audioNodes: {
    source: MediaElementAudioSourceNode;
    gain: GainNode;
    compressor: DynamicsCompressorNode;
    filters: { highPass: BiquadFilterNode };
  } | null = null;

  // Cleanup tracking for memory leak prevention
  let resizeObserver: ResizeObserver | null = null;
  let playEndTimeout: ReturnType<typeof setTimeout> | undefined;
  let eventListeners: Array<{
    element: HTMLElement | Document | Window;
    event: string;
    handler: EventListener;
  }> = [];

  // Control state
  let gainValue = $state(0); // dB
  let filterFreq = $state(20); // Hz
  let playbackSpeed = $state(1.0); // Playback rate multiplier
  let showControls = $state(true); // Will be set based on width
  let isMobile = $state(false);

  // Constants
  const GAIN_MAX_DB = 24;
  const FILTER_HP_MIN_FREQ = 20;
  const FILTER_HP_MAX_FREQ = 10000;
  const FILTER_HP_DEFAULT_FREQ = 20;
  const PLAY_END_DELAY_MS = 3000; // 3 second delay after audio stops before resuming updates
  // Spinner delay is now handled by useDelayedLoading utility

  // Computed values
  const spectrogramUrl = $derived(
    showSpectrogram
      ? `/api/v2/spectrogram/${detectionId}?size=${spectrogramSize}${spectrogramRaw ? '&raw=true' : ''}`
      : null
  );

  const playPauseId = $derived(`playPause-${detectionId}`);
  const audioId = $derived(`audio-${detectionId}`);
  const progressId = $derived(`progress-${detectionId}`);

  // Utility functions
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

  const clearPlayEndTimeout = () => {
    if (playEndTimeout) {
      clearTimeout(playEndTimeout);
      playEndTimeout = undefined;
    }
  };

  // Audio context setup
  const initializeAudioContext = async () => {
    try {
      // Check if AudioContext is available (webkitAudioContext for Safari)
      type WebkitWindow = Window & { webkitAudioContext?: typeof AudioContext };
      const AudioContextClass = window.AudioContext || (window as WebkitWindow).webkitAudioContext;
      if (!AudioContextClass) {
        throw new Error('AudioContext not supported');
      }

      audioContext = new AudioContextClass();

      if (audioContext.state === 'suspended') {
        await audioContext.resume();
      }

      audioContextAvailable = true;
      return audioContext;
      // eslint-disable-next-line no-unused-vars
    } catch (_e) {
      logger.warn('Web Audio API is not supported in this browser');
      audioContextAvailable = false;
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

        await audioElement.play();
      }
    } catch (err) {
      logger.error('Error playing audio:', err);
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

  // Filter control
  const updateFilter = (newFreq: number) => {
    filterFreq = Math.max(FILTER_HP_MIN_FREQ, Math.min(FILTER_HP_MAX_FREQ, newFreq));
    if (audioNodes) {
      audioNodes.filters.highPass.frequency.value = filterFreq;
    }
  };

  // Speed control
  const handleSpeedChange = (newSpeed: number) => {
    playbackSpeed = newSpeed;
    if (audioElement) {
      applyPlaybackRate(audioElement, newSpeed);
    }
  };

  // Check spectrogram mode on mount/URL change to avoid double-request pattern
  // NOTE: Current implementation requires an initial network request to detect mode.
  //
  // Future optimization options:
  // 1. Read from global settings store (if spectrogram mode is exposed frontend-wide)
  // 2. Call dedicated /api/v2/spectrogram/:id/info endpoint for lightweight metadata
  // 3. Use format query parameter (e.g., ?format=json) for explicit JSON responses
  //
  // The current approach is acceptable as it eliminates the previous double-request
  // pattern where BOTH the <img> load AND a subsequent fetch() would fail before
  // showing the generate button.
  // eslint-disable-next-line no-unused-vars
  const checkSpectrogramMode = async () => {
    if (!spectrogramUrl) {
      spectrogramMode = 'auto';
      debugLog('checkSpectrogramMode: no spectrogramUrl');
      return;
    }

    debugLog('checkSpectrogramMode: fetching', { url: spectrogramUrl });
    try {
      const response = await fetch(spectrogramUrl);
      debugLog('checkSpectrogramMode: response', {
        status: response.status,
        contentType: response.headers.get('Content-Type'),
      });

      if (response.status === 404) {
        const contentType = response.headers.get('Content-Type');
        if (contentType?.includes('application/json')) {
          const responseData = await response.json();
          // Extract mode from data field in API v2 envelope, default to "auto" if not specified
          spectrogramMode = responseData.data?.mode ?? 'auto';

          debugLog('checkSpectrogramMode: mode detected', { mode: spectrogramMode });

          if (spectrogramMode === 'user-requested') {
            logger.debug('Spectrogram in user-requested mode', { detectionId });
            spectrogramNeedsGeneration = true;
            spectrogramLoader.setLoading(false);
          }
        }
      } else if (response.ok) {
        // Spectrogram exists - mode is no longer relevant
        spectrogramMode = 'auto';
        spectrogramNeedsGeneration = false;
        debugLog('checkSpectrogramMode: spectrogram exists');
      }
    } catch (err) {
      logger.debug('Failed to check spectrogram mode', { detectionId, error: err });
      debugLog('checkSpectrogramMode: error', err);
      // Default to auto mode on error
      spectrogramMode = 'auto';
    }
  };

  // Poll spectrogram generation status
  const pollSpectrogramStatus = async () => {
    if (!showSpectrogram || !detectionId) {
      debugLog('pollSpectrogramStatus: skipped', {
        showSpectrogram,
        detectionId,
      });
      return;
    }

    // Check if we've exceeded max polling duration
    if (statusPollStartTime && Date.now() - statusPollStartTime > MAX_POLL_DURATION) {
      logger.warn('Spectrogram polling timeout exceeded', {
        detectionId,
        pollDurationMs: Date.now() - statusPollStartTime,
      });
      debugLog('pollSpectrogramStatus: timeout exceeded', {
        pollDurationMs: Date.now() - statusPollStartTime,
      });
      clearStatusPollTimer();
      spectrogramLoader.setError();
      return;
    }

    // Create new AbortController for this poll request
    statusPollAbortController = new AbortController();

    const statusUrl = `/api/v2/spectrogram/${detectionId}/status?size=${spectrogramSize}${spectrogramRaw ? '&raw=true' : ''}`;
    debugLog('pollSpectrogramStatus: fetching', { url: statusUrl });

    try {
      const response = await fetch(statusUrl, { signal: statusPollAbortController.signal });

      debugLog('pollSpectrogramStatus: response', {
        ok: response.ok,
        status: response.status,
      });

      if (response.ok) {
        const responseData = await response.json();
        // Extract status from v2 envelope format
        const status = responseData.data ?? responseData;
        spectrogramStatus = status;

        debugLog('pollSpectrogramStatus: status data', {
          status: status.status,
          queuePosition: status.queuePosition,
          message: status.message,
        });

        // Check status and decide what to do
        if (
          status.status === 'exists' ||
          status.status === 'completed' ||
          status.status === 'generated'
        ) {
          // Spectrogram is ready, stop polling and try to load it
          debugLog('pollSpectrogramStatus: spectrogram ready, reloading image');
          clearStatusPollTimer();
          spectrogramStatus = null;
          // Force reload the image using Svelte binding
          if (spectrogramImage && spectrogramUrl) {
            const url = new URL(spectrogramUrl, window.location.origin);
            url.searchParams.set('t', Date.now().toString());
            spectrogramImage.src = url.toString();
            debugLog('pollSpectrogramStatus: image src updated', { src: url.toString() });
          }
        } else if (status.status === 'queued' || status.status === 'generating') {
          // Still processing, continue polling
          debugLog('pollSpectrogramStatus: still processing, scheduling next poll');
          clearStatusPollTimer();
          statusPollTimer = setTimeout(pollSpectrogramStatus, SPECTROGRAM_POLL_INTERVAL);
        } else if (status.status === 'failed') {
          // Generation failed
          debugLog('pollSpectrogramStatus: generation failed');
          clearStatusPollTimer();
          spectrogramLoader.setError();
        }
      }
    } catch (err) {
      // Ignore abort errors
      if (err instanceof Error && err.name === 'AbortError') {
        logger.debug('Spectrogram status poll aborted', { detectionId });
        debugLog('pollSpectrogramStatus: aborted');
        return;
      }
      logger.error('Failed to poll spectrogram status', { detectionId, error: err });
      debugLog('pollSpectrogramStatus: error', err);
    }
  };

  const clearStatusPollTimer = () => {
    if (statusPollTimer) {
      clearTimeout(statusPollTimer);
      statusPollTimer = undefined;
    }
    if (statusPollAbortController) {
      statusPollAbortController.abort();
      statusPollAbortController = undefined;
    }
    statusPollStartTime = undefined;
  };

  // Spectrogram loading handlers
  const handleSpectrogramLoad = () => {
    debugLog('handleSpectrogramLoad: success');
    spectrogramLoader.setLoading(false);
    spectrogramRetryCount = 0; // Reset retry count on successful load
    clearSpectrogramRetryTimer();
    clearStatusPollTimer(); // Also clear status polling
  };

  const handleSpectrogramError = async (event: Event) => {
    const img = event.currentTarget as HTMLImageElement;

    debugLog('handleSpectrogramError: triggered', {
      retryCount: spectrogramRetryCount,
      mode: spectrogramMode,
      src: img.src,
    });

    // Check if this is user-requested mode (using cached mode)
    if (spectrogramRetryCount === 0 && spectrogramMode === 'user-requested') {
      // User-requested mode - show generate button instead of error
      logger.debug('Spectrogram not generated in user-requested mode', {
        detectionId,
      });
      debugLog('handleSpectrogramError: user-requested mode, showing generate button');
      spectrogramNeedsGeneration = true;
      spectrogramLoader.setLoading(false);
      clearSpectrogramRetryTimer();
      clearStatusPollTimer();
      return;
    }

    // Start polling for generation status on first error
    if (spectrogramRetryCount === 0) {
      debugLog('handleSpectrogramError: first error, starting status polling');
      statusPollStartTime = Date.now();
      pollSpectrogramStatus();
    }

    // Retry on any error (likely 503 or temporary failure) up to max retries
    if (spectrogramRetryCount < MAX_SPECTROGRAM_RETRIES) {
      // Use exponential backoff for retry
      // Use Math.min to safely select delay without direct array indexing
      const delayIndex = Math.min(spectrogramRetryCount, SPECTROGRAM_RETRY_DELAYS.length - 1);
      const retryDelay = SPECTROGRAM_RETRY_DELAYS.at(delayIndex) ?? 4000;

      logger.debug('Spectrogram load failed, checking status and retrying', {
        detectionId,
        retryCount: spectrogramRetryCount + 1,
        maxRetries: MAX_SPECTROGRAM_RETRIES,
        retryDelay,
        status: spectrogramStatus?.status,
      });

      debugLog('handleSpectrogramError: scheduling retry', {
        retryCount: spectrogramRetryCount + 1,
        retryDelay,
        currentStatus: spectrogramStatus?.status,
      });

      spectrogramRetryCount++;

      // Schedule retry
      clearSpectrogramRetryTimer();
      spectrogramRetryTimer = setTimeout(() => {
        // Force reload by modifying URL with timestamp
        const url = new URL(img.src);
        url.searchParams.set('retry', spectrogramRetryCount.toString());
        url.searchParams.set('t', Date.now().toString());
        img.src = url.toString();
        debugLog('handleSpectrogramError: retry attempted', { newSrc: url.toString() });
      }, retryDelay);

      return; // Don't set error state yet
    }

    // Max retries exceeded - but keep polling if generation is in progress
    if (spectrogramStatus?.status === 'queued' || spectrogramStatus?.status === 'generating') {
      logger.info('Spectrogram still generating, continuing to poll', {
        detectionId,
        status: spectrogramStatus.status,
        queuePosition: spectrogramStatus.queuePosition,
      });
      debugLog('handleSpectrogramError: max retries but still generating, keep polling', {
        status: spectrogramStatus.status,
        queuePosition: spectrogramStatus.queuePosition,
      });
      // Don't set error, keep polling
      return;
    }

    // Really failed - stop everything
    debugLog('handleSpectrogramError: giving up after max retries');
    spectrogramLoader.setError();
    clearSpectrogramRetryTimer();
    clearStatusPollTimer();

    logger.warn('Spectrogram loading failed after max retries', {
      detectionId,
      retryCount: spectrogramRetryCount,
    });
  };

  const clearSpectrogramRetryTimer = () => {
    if (spectrogramRetryTimer) {
      clearTimeout(spectrogramRetryTimer);
      spectrogramRetryTimer = undefined;
    }
  };

  // Handle user-requested spectrogram generation
  // TODO: Wire up to generate button in UI when spectrogramNeedsGeneration is true
  // eslint-disable-next-line no-unused-vars
  const handleGenerateSpectrogram = async () => {
    if (isGeneratingSpectrogram) {
      debugLog('handleGenerateSpectrogram: already generating, skipping');
      return; // Prevent double-click
    }

    isGeneratingSpectrogram = true;
    generationError = null;

    logger.info('User requested spectrogram generation', { detectionId });
    debugLog('handleGenerateSpectrogram: starting generation');

    try {
      // Build POST URL using URL and URLSearchParams
      const generateUrl = new URL(
        `/api/v2/spectrogram/${detectionId}/generate`,
        window.location.origin
      );
      const params = new URLSearchParams();
      params.set('size', spectrogramSize);
      if (spectrogramRaw) {
        params.set('raw', 'true');
      }
      generateUrl.search = params.toString();

      debugLog('handleGenerateSpectrogram: POST request', { url: generateUrl.toString() });

      const response = await fetch(generateUrl.toString(), {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
      });

      debugLog('handleGenerateSpectrogram: response', { status: response.status });

      // Handle both 200 (immediate generation) and 202 (async generation)
      if (response.status === 202) {
        // Async generation - API returns 202 Accepted
        logger.info('Spectrogram generation queued', { detectionId });
        debugLog('handleGenerateSpectrogram: queued (202), starting polling');

        // Reset state and start polling
        spectrogramNeedsGeneration = false;
        spectrogramRetryCount = 0;
        spectrogramLoader.setLoading(true);
        statusPollStartTime = Date.now();

        // Start polling for status
        pollSpectrogramStatus();
      } else if (response.ok) {
        // Immediate generation (backward compatibility for old API)
        const responseData = await response.json();
        const data = responseData.data ?? responseData;
        logger.info('Spectrogram generated successfully', {
          detectionId,
          path: data.path,
        });
        debugLog('handleGenerateSpectrogram: immediate success', { path: data.path });

        // Reset state and reload the spectrogram
        spectrogramNeedsGeneration = false;
        spectrogramRetryCount = 0;
        spectrogramLoader.setLoading(true);

        // Reload the image with cache-busting parameter
        if (spectrogramImage && spectrogramUrl) {
          const url = new URL(spectrogramUrl, window.location.origin);
          url.searchParams.set('t', Date.now().toString());
          spectrogramImage.src = url.toString();
          debugLog('handleGenerateSpectrogram: reloading image', { src: url.toString() });
        }
      } else {
        // Error response
        let errorMessage = `Generation failed with status ${response.status}`;
        try {
          const errorData = await response.json();
          errorMessage = errorData.message ?? errorMessage;
        } catch (parseErr) {
          logger.warn('Failed to parse error response', { parseErr });
        }
        debugLog('handleGenerateSpectrogram: error response', { errorMessage });
        throw new Error(errorMessage);
      }
    } catch (err) {
      const errorMessage = err instanceof Error ? err.message : 'Failed to generate spectrogram';
      generationError = errorMessage;
      logger.error('Spectrogram generation failed', {
        detectionId,
        error: err,
      });
      debugLog('handleGenerateSpectrogram: exception', err);
      spectrogramLoader.setError();
    } finally {
      isGeneratingSpectrogram = false;
      debugLog('handleGenerateSpectrogram: completed');
    }
  };

  // Track previous URL to avoid unnecessary resets
  let previousSpectrogramUrl = $state<string | null>(null);

  // Handle spectrogram URL changes with proper loading state
  $effect(() => {
    // Only reset loading state if URL actually changed
    if (spectrogramUrl && spectrogramUrl !== previousSpectrogramUrl && !spectrogramLoader.error) {
      debugLog('spectrogramUrl changed', {
        from: previousSpectrogramUrl,
        to: spectrogramUrl,
      });

      previousSpectrogramUrl = spectrogramUrl;

      // Reset retry count and clear any pending retry timer for new spectrogram
      spectrogramRetryCount = 0;
      clearSpectrogramRetryTimer();
      // Abort any in-flight status polling when URL changes
      clearStatusPollTimer();
    }
  });

  // Lifecycle
  onMount(() => {
    debugLog('onMount: component mounted', {
      audioUrl,
      spectrogramUrl,
      showSpectrogram,
    });

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
        // Clear any pending delay timeout and immediately signal play start
        clearPlayEndTimeout();
        // Apply playback rate when starting
        applyPlaybackRate(audioElement, playbackSpeed);
        if (onPlayStart) {
          onPlayStart();
        }
      });

      addTrackedEventListener(audioElement, 'pause', () => {
        isPlaying = false;
        stopInterval();
        // Set delay before signaling play end to avoid disrupting UI during brief pauses
        clearPlayEndTimeout();
        playEndTimeout = setTimeout(() => {
          if (onPlayEnd) {
            onPlayEnd();
          }
        }, PLAY_END_DELAY_MS);
      });

      addTrackedEventListener(audioElement, 'ended', () => {
        isPlaying = false;
        stopInterval();
        // Set delay before signaling play end
        clearPlayEndTimeout();
        playEndTimeout = setTimeout(() => {
          if (onPlayEnd) {
            onPlayEnd();
          }
        }, PLAY_END_DELAY_MS);
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

    // Check if spectrogram image is already loaded from cache on mount
    // This handles the race condition where image loads before effects run
    debugLog('onMount: checking spectrogram state', {
      spectrogramUrl,
      spectrogramImageExists: !!spectrogramImage,
      imageComplete: spectrogramImage?.complete,
      imageNaturalHeight: spectrogramImage?.naturalHeight,
    });

    if (spectrogramImage?.complete && spectrogramImage?.naturalHeight !== 0) {
      debugLog('onMount: spectrogram already loaded from cache');
      spectrogramLoader.setLoading(false);
    }
  });

  // Debug effect to track spectrogram loader state changes
  $effect(() => {
    debugLog('loader state changed', {
      loading: spectrogramLoader.loading,
      showSpinner: spectrogramLoader.showSpinner,
      error: spectrogramLoader.error,
    });
  });

  // Debug effect to track spectrogram status changes
  $effect(() => {
    if (spectrogramStatus) {
      debugLog('spectrogram status changed', {
        status: spectrogramStatus.status,
        queuePosition: spectrogramStatus.queuePosition,
        message: spectrogramStatus.message,
      });
    }
  });

  onDestroy(() => {
    debugLog('onDestroy: component destroying', {
      isPlaying,
      spectrogramRetryCount,
      hasStatusPollTimer: !!statusPollTimer,
    });

    // Stop any running intervals
    stopInterval();

    // Clear any pending timeouts
    clearPlayEndTimeout();
    clearSpectrogramRetryTimer();
    clearStatusPollTimer();
    spectrogramLoader.cleanup();

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
        // eslint-disable-next-line no-unused-vars
      } catch (_e) {
        // Nodes may already be disconnected, ignore errors

        logger.warn('Error disconnecting audio nodes during cleanup');
      }
      audioNodes = null;
    }

    // Close audio context
    if (audioContext) {
      try {
        audioContext.close();
        // eslint-disable-next-line no-unused-vars
      } catch (_e) {
        // Context may already be closed, ignore errors

        logger.warn('Error closing audio context during cleanup');
      }
      audioContext = null;
    }
  });
</script>

<div
  bind:this={playerContainer}
  class={cn('relative group', className)}
  style={responsive
    ? ''
    : `width: ${typeof width === 'number' ? width + 'px' : width}; height: ${typeof height === 'number' ? height + 'px' : height};`}
>
  {#if spectrogramUrl}
    <!-- Screen reader announcement for loading state -->
    <div class="sr-only" role="status" aria-live="polite">
      {spectrogramLoader.loading ? 'Loading spectrogram...' : 'Spectrogram loaded'}
    </div>

    <!-- Loading spinner overlay -->
    {#if spectrogramLoader.showSpinner}
      <div
        class="absolute inset-0 flex items-center justify-center bg-base-200/75 rounded-md border border-base-300"
      >
        <div
          class="loading loading-spinner loading-sm md:loading-md text-primary"
          role="status"
          aria-label="Loading spectrogram"
        ></div>
      </div>
    {/if}

    {#if spectrogramLoader.error}
      <!-- Error placeholder for failed spectrogram -->
      <div
        class="flex items-center justify-center bg-base-200 rounded-md border border-base-300"
        style={responsive
          ? 'height: 80px;'
          : `width: ${typeof width === 'number' ? width + 'px' : width}; height: ${typeof height === 'number' ? height + 'px' : height};`}
      >
        <div class="text-center p-2">
          <XCircle class="size-6 sm:size-8 mx-auto mb-1 text-base-content/30" aria-hidden="true" />
          <span class="text-xs sm:text-sm text-base-content/50">Spectrogram unavailable</span>
        </div>
      </div>
    {:else if spectrogramStatus?.status === 'queued' || spectrogramStatus?.status === 'generating'}
      <!-- Show generation status -->
      <div
        class="absolute inset-0 flex flex-col items-center justify-center bg-base-200/90 rounded-md border border-base-300 p-2"
      >
        <div
          class="loading loading-spinner loading-xs sm:loading-sm md:loading-md"
          role="status"
          aria-label="Generating spectrogram"
        ></div>
        <div class="text-xs sm:text-sm text-base-content mt-1" role="status" aria-live="polite">
          {#if spectrogramStatus.status === 'queued'}
            <span>Queue: {spectrogramStatus.queuePosition}</span>
          {:else}
            <span>Generating...</span>
          {/if}
        </div>
        {#if spectrogramStatus.message}
          <div class="text-xs sm:text-sm text-base-content/70 mt-0.5">
            {spectrogramStatus.message}
          </div>
        {/if}
      </div>
    {:else}
      <img
        bind:this={spectrogramImage}
        id={`spectrogram-${detectionId}`}
        src={spectrogramUrl}
        alt="Audio spectrogram"
        loading="lazy"
        decoding="async"
        fetchpriority="low"
        class={responsive
          ? 'w-full h-auto object-contain rounded-md border border-base-300'
          : 'w-full h-full object-cover rounded-md border border-base-300'}
        class:opacity-0={spectrogramLoader.loading}
        style={responsive
          ? ''
          : `width: ${typeof width === 'number' ? width + 'px' : width}; height: ${typeof height === 'number' ? height + 'px' : height};`}
        width={responsive ? 400 : undefined}
        onload={handleSpectrogramLoad}
        onerror={handleSpectrogramError}
      />
    {/if}
  {/if}

  <!-- Audio element -->
  <audio bind:this={audioElement} id={audioId} src={audioUrl} preload="metadata" class="hidden">
    <track kind="captions" />
  </audio>

  <!-- Audio settings button (top-right) -->
  {#if showControls}
    <div
      class="absolute top-2 right-2 transition-opacity duration-200"
      class:opacity-0={!isMobile}
      class:group-hover:opacity-100={!isMobile}
    >
      <AudioSettingsButton
        {gainValue}
        {filterFreq}
        {playbackSpeed}
        onGainChange={updateGain}
        onFilterChange={updateFilter}
        onSpeedChange={handleSpeedChange}
        disabled={!audioContextAvailable}
      />
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
    class="absolute bottom-0 left-0 right-0 bg-black/25 p-1 rounded-b-md transition-opacity duration-300"
    class:opacity-0={!isMobile}
    class:group-hover:opacity-100={!isMobile}
    class:opacity-100={isMobile}
  >
    <div class="flex items-center justify-between">
      <!-- Play/Pause button -->
      <button
        bind:this={playPauseButton}
        id={playPauseId}
        class="text-white p-1 rounded-full hover:bg-white/20 shrink-0"
        onclick={handlePlayPause}
        disabled={isLoading}
        aria-label={isPlaying ? t('media.audio.pause') : t('media.audio.play')}
      >
        {#if isLoading}
          <div
            class="w-4 h-4 border-2 border-white border-t-transparent rounded-full animate-spin"
          ></div>
        {:else if isPlaying}
          <Pause class="size-4" />
        {:else}
          <Play class="size-4" />
        {/if}
      </button>

      <!-- Progress bar -->
      <div
        bind:this={progressBar}
        id={progressId}
        class="grow bg-gray-200 rounded-full h-1.5 mx-2 cursor-pointer"
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
      <span class="text-xs font-medium text-white shrink-0">{formatTime(currentTime)}</span>

      <!-- Download button -->
      {#if showDownload}
        <a
          href={audioUrl}
          download
          class="text-white p-1 rounded-full hover:bg-white/20 ml-2 shrink-0"
          aria-label={t('media.audio.download')}
        >
          <Download class="size-4" />
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
