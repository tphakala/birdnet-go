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
  import { onMount } from 'svelte';
  import { cn } from '$lib/utils/cn.js';
  import { Play, Pause, Download, XCircle } from '@lucide/svelte';
  import AudioSettingsButton from '$lib/desktop/features/dashboard/components/AudioSettingsButton.svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { useDelayedLoading } from '$lib/utils/delayedLoading.svelte.js';
  import {
    applyPlaybackRate,
    dbToGain,
    PLAY_END_DELAY_MS,
    CANPLAY_TIMEOUT_MS,
    PROGRESS_UPDATE_INTERVAL_MS,
    MIN_CONTROLS_WIDTH_PX,
  } from '$lib/utils/audio';
  import {
    getAudioContext,
    isAudioContextSupported,
    releaseAudioContext,
  } from '$lib/utils/audioContextManager';
  import {
    createAudioNodeChain,
    disconnectAudioNodes,
    type AudioNodeChain,
  } from '$lib/utils/audioNodes';
  import { buildAppUrl } from '$lib/utils/urlHelpers';

  const logger = loggers.audio;

  // Debug logging helper - bypasses linter warnings when debug flag is enabled
  const debugLog = (message: string, data?: unknown) => {
    if (debug) {
      // eslint-disable-next-line no-console
      console.log(`[AudioPlayer:${detectionId}] ${message}`, data || '');
    }
  };

  // Web Audio API types - these are built-in browser types
  /* global Audio, AudioContext, EventListener, ResizeObserver */

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
  // Cache key for forcing spectrogram reload via Svelte reactivity (instead of direct DOM manipulation)
  let spectrogramCacheKey = $state(0);

  // Spectrogram generation status
  let spectrogramStatus = $state<{
    status: string;
    queuePosition: number;
    message: string;
  } | null>(null);
  let statusPollTimer: ReturnType<typeof setTimeout> | undefined;
  let statusPollStartTime: number | undefined;
  let statusPollAbortController: AbortController | undefined;

  // ==========================================================================
  // User-requested spectrogram generation (INCOMPLETE FEATURE)
  // TODO: This feature allows users to manually trigger spectrogram generation
  // when spectrogramMode is 'user-requested'. Currently the backend supports this
  // but the UI (generate button) is not wired up. To complete:
  // 1. Add a "Generate Spectrogram" button in the error state when spectrogramNeedsGeneration is true
  // 2. Wire the button to call handleGenerateSpectrogram()
  // 3. Show generationError if generation fails
  // Related functions: checkSpectrogramMode(), handleGenerateSpectrogram()
  // ==========================================================================
  /* eslint-disable no-unused-vars */
  let spectrogramNeedsGeneration = $state(false);
  let isGeneratingSpectrogram = $state(false);
  let generationError = $state<string | null>(null);
  let spectrogramMode = $state<string>('auto');
  /* eslint-enable no-unused-vars */

  // Audio processing state
  let audioContext: AudioContext | null = null;
  let isInitializingContext = $state(false);
  let audioNodes: AudioNodeChain | null = null;

  // Cleanup tracking for memory leak prevention
  let resizeObserver: ResizeObserver | null = null;
  let playEndTimeout: ReturnType<typeof setTimeout> | undefined;
  let canplayTimeoutId: ReturnType<typeof setTimeout> | undefined;
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
  // PLAY_END_DELAY_MS imported from $lib/utils/audio
  // Spinner delay is now handled by useDelayedLoading utility

  // Computed values
  // Base URL without cache-busting parameters
  const spectrogramBaseUrl = $derived(
    showSpectrogram
      ? buildAppUrl(
          `/api/v2/spectrogram/${encodeURIComponent(detectionId)}?size=${spectrogramSize}${spectrogramRaw ? '&raw=true' : ''}`
        )
      : null
  );

  // Active URL with cache-busting for reactive reloads (retry count + cache key)
  const spectrogramUrl = $derived(
    spectrogramBaseUrl
      ? `${spectrogramBaseUrl}&cache=${spectrogramCacheKey}${spectrogramRetryCount > 0 ? `&retry=${spectrogramRetryCount}` : ''}`
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

  // Audio context setup - uses shared singleton manager
  const initializeAudioContext = async () => {
    try {
      if (!isAudioContextSupported()) {
        throw new Error('AudioContext not supported');
      }
      audioContext = await getAudioContext();
      audioContextAvailable = true;
      return audioContext;
    } catch {
      logger.warn('Web Audio API is not supported in this browser');
      audioContextAvailable = false;
      return null;
    }
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
              audioNodes = createAudioNodeChain(audioContext, audioElement, {
                gainDb: gainValue,
                highPassFreq: filterFreq,
                includeCompressor: true,
              });
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
      error = t('media.audio.playError');
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
    updateInterval = setInterval(updateProgress, PROGRESS_UPDATE_INTERVAL_MS);
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
      audioNodes.highPass.frequency.value = filterFreq;
    }
  };

  // Speed control
  const handleSpeedChange = (newSpeed: number) => {
    playbackSpeed = newSpeed;
    if (audioElement) {
      applyPlaybackRate(audioElement, newSpeed);
    }
  };

  // Part of user-requested spectrogram generation feature (see TODO above)
  // Check spectrogram mode on mount/URL change to avoid double-request pattern
  /* eslint-disable no-unused-vars */
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

    const statusUrl = buildAppUrl(
      `/api/v2/spectrogram/${encodeURIComponent(detectionId)}/status?size=${spectrogramSize}${spectrogramRaw ? '&raw=true' : ''}`
    );
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
          // Spectrogram is ready, stop polling and reload via reactive cache key
          debugLog('pollSpectrogramStatus: spectrogram ready, reloading image');
          clearStatusPollTimer();
          spectrogramStatus = null;
          // Force reload by incrementing cache key (triggers Svelte reactivity)
          spectrogramCacheKey++;
          debugLog('pollSpectrogramStatus: cache key updated', { cacheKey: spectrogramCacheKey });
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

      // Schedule retry by incrementing retry count after delay
      // (spectrogramUrl is derived from spectrogramRetryCount, so this triggers reload)
      clearSpectrogramRetryTimer();
      spectrogramRetryTimer = setTimeout(() => {
        spectrogramRetryCount++;
        debugLog('handleSpectrogramError: retry attempted', { retryCount: spectrogramRetryCount });
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

  // Part of user-requested spectrogram generation feature (see TODO in state declarations)
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
        buildAppUrl(`/api/v2/spectrogram/${encodeURIComponent(detectionId)}/generate`),
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

        // Reset state and reload the spectrogram via reactive cache key
        spectrogramNeedsGeneration = false;
        spectrogramRetryCount = 0;
        spectrogramLoader.setLoading(true);

        // Reload by incrementing cache key (triggers Svelte reactivity)
        spectrogramCacheKey++;
        debugLog('handleGenerateSpectrogram: cache key updated', { cacheKey: spectrogramCacheKey });
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
  /* eslint-enable no-unused-vars */

  // Track previous base URL to detect when detection changes (not just cache key updates)
  let previousSpectrogramBaseUrl = $state<string | null>(null);

  // Handle spectrogram base URL changes (different detection) with proper loading state
  $effect(() => {
    // Only reset loading state if base URL actually changed (new detection)
    if (
      spectrogramBaseUrl &&
      spectrogramBaseUrl !== previousSpectrogramBaseUrl &&
      !spectrogramLoader.error
    ) {
      debugLog('spectrogramBaseUrl changed', {
        from: previousSpectrogramBaseUrl,
        to: spectrogramBaseUrl,
      });

      previousSpectrogramBaseUrl = spectrogramBaseUrl;

      // Reset retry/cache state for new spectrogram
      spectrogramRetryCount = 0;
      spectrogramCacheKey = 0;
      clearSpectrogramRetryTimer();
      // Abort any in-flight status polling when URL changes
      clearStatusPollTimer();
    }
  });

  // Sync audio source when URL changes (replaces template binding for iOS Safari compatibility)
  $effect(() => {
    if (audioElement && audioUrl) {
      // Compare resolved URLs to avoid unnecessary reloads
      const absoluteUrl = new URL(audioUrl, window.location.origin).href;
      if (audioElement.src !== absoluteUrl) {
        debugLog('audioUrl changed, updating src', { from: audioElement.src, to: audioUrl });
        audioElement.src = audioUrl;
        // Reset playback state for new audio
        isPlaying = false;
        currentTime = 0;
        progress = 0;
        duration = 0;
        error = null;
      }
    }
  });

  // Lifecycle
  onMount(() => {
    debugLog('onMount: component mounted', {
      audioUrl,
      spectrogramUrl,
      showSpectrogram,
    });

    // Create audio element dynamically to avoid iOS Safari issues
    // where DOM-bound audio elements don't fire canplay events
    audioElement = new Audio();
    audioElement.preload = 'metadata';
    audioElement.src = audioUrl;
    audioElement.id = audioId;

    // Check if mobile
    isMobile = 'ontouchstart' in window || navigator.maxTouchPoints > 0;

    // Check width for control visibility
    if (playerContainer) {
      const checkWidth = () => {
        showControls = playerContainer.offsetWidth >= MIN_CONTROLS_WIDTH_PX;
      };
      checkWidth();

      // Create and track ResizeObserver properly
      resizeObserver = new ResizeObserver(checkWidth);
      resizeObserver.observe(playerContainer);
    }

    // audioElement is now guaranteed to exist (created above)
    {
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

      // Safety timeout for canplay event (iOS Safari fallback)
      // If canplay doesn't fire within 3 seconds of loadstart, assume ready
      addTrackedEventListener(audioElement, 'loadstart', () => {
        isLoading = true;
        // Clear any existing timeout
        if (canplayTimeoutId) clearTimeout(canplayTimeoutId);
        // Set new timeout as fallback for iOS Safari
        canplayTimeoutId = setTimeout(() => {
          if (isLoading) {
            logger.warn('canplay timeout - assuming audio ready', { detectionId });
            isLoading = false;
          }
        }, CANPLAY_TIMEOUT_MS);
      });

      addTrackedEventListener(audioElement, 'canplay', () => {
        // Clear timeout since canplay fired normally
        if (canplayTimeoutId) {
          clearTimeout(canplayTimeoutId);
          canplayTimeoutId = undefined;
        }
        isLoading = false;
      });

      addTrackedEventListener(audioElement, 'error', () => {
        // Clear timeout on error
        if (canplayTimeoutId) {
          clearTimeout(canplayTimeoutId);
          canplayTimeoutId = undefined;
        }
        error = t('media.audio.error');
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

    // Cleanup function (Svelte 5 pattern)
    return () => {
      debugLog('cleanup: component destroying', {
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

      // Clear canplay safety timeout
      if (canplayTimeoutId) {
        clearTimeout(canplayTimeoutId);
        canplayTimeoutId = undefined;
      }

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

      // Stop audio playback to prevent resource leaks
      if (audioElement) {
        audioElement.pause();
        audioElement.src = '';
      }

      // Clean up Web Audio API resources using shared utilities
      disconnectAudioNodes(audioNodes);
      audioNodes = null;

      // Release reference to shared audio context (doesn't close it - it's reused)
      releaseAudioContext();
      audioContext = null;
    };
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
      {spectrogramLoader.loading
        ? t('components.audio.spectrogramLoading')
        : t('components.audio.spectrogramLoaded')}
    </div>

    <!-- Loading spinner overlay -->
    {#if spectrogramLoader.showSpinner}
      <div
        class="absolute inset-0 flex items-center justify-center bg-base-200/75 rounded-md border border-base-300"
      >
        <div
          class="loading loading-spinner loading-sm md:loading-md text-primary"
          role="status"
          aria-label={t('components.audio.spectrogramLoadingAria')}
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
          <span class="text-xs sm:text-sm text-base-content/50"
            >{t('components.audio.spectrogramUnavailable')}</span
          >
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
          aria-label={t('components.audio.spectrogramGeneratingAria')}
        ></div>
        <div class="text-xs sm:text-sm text-base-content mt-1" role="status" aria-live="polite">
          {#if spectrogramStatus.status === 'queued'}
            <span
              >{t('components.audio.queuePosition', {
                position: spectrogramStatus.queuePosition,
              })}</span
            >
          {:else}
            <span>{t('components.audio.generating')}</span>
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

  <!-- Audio element is created dynamically in onMount for iOS Safari compatibility -->

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
