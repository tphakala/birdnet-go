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
  - spectrogramSize: Spectrogram display size - md/lg/xl (default: lg)
  - spectrogramRaw: Display raw spectrogram without axes (default: false)
  - onPlayStart: Callback when audio starts playing (freezes SSE updates)
  - onPlayEnd: Callback when audio stops playing after 3s delay (resumes SSE updates)
-->

<script lang="ts">
  import { onMount } from 'svelte';
  import { cn } from '$lib/utils/cn.js';
  import { Play, Pause, Download, XCircle, SkipBack, ChevronDown } from '@lucide/svelte';
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
    MAX_AUDIO_LOAD_RETRIES,
    AUDIO_RETRY_DELAY_MS,
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
  type SpectrogramSize = 'md' | 'lg' | 'xl';

  interface Props {
    audioUrl: string;
    detectionId: string;
    width?: number | string;
    height?: number | string;
    showSpectrogram?: boolean;
    showDownload?: boolean;
    className?: string;
    responsive?: boolean;
    /** Spectrogram display size: md (514px), lg (1026px), xl (2050px) */
    spectrogramSize?: SpectrogramSize;
    /** Display raw spectrogram without axes and legends */
    spectrogramRaw?: boolean;
    /** Fired when audio playback starts - freezes detection updates */
    onPlayStart?: () => void;
    /** Fired 3 seconds after audio stops - resumes detection updates */
    onPlayEnd?: () => void;
    /** Enable debug logging for troubleshooting multi-session issues */
    debug?: boolean;
    /** Enable clip extraction with range selection on spectrogram */
    enableClipExtraction?: boolean;
    /** Label for clip filenames (e.g. "Eurasian Blue Tit_2026-03-14_14-30-25") */
    clipLabel?: string;
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
    enableClipExtraction = false,
    clipLabel = '',
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

  // Audio load retry state — handles race condition where detection DB record
  // exists but audio file is still being encoded by FFmpeg (backend returns 503)
  let audioRetryCount = 0;
  let audioRetryTimer: ReturnType<typeof setTimeout> | undefined;

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

  // Clip extraction selection state
  let selectionStartSec = $state<number | null>(null);
  let selectionEndSec = $state<number | null>(null);
  let isDragSelecting = $state(false);
  let draggingHandle = $state<'start' | 'end' | null>(null);
  let dragOriginX = $state(0);

  // Clip extraction state
  let extractionFormat = $state('wav');
  let isExtracting = $state(false);
  let extractionError = $state<string | null>(null);
  let isPlayingSelection = $state(false);
  let selectionPlaybackTimeout: ReturnType<typeof setTimeout> | undefined;
  let showFormatMenu = $state(false);
  let formatMenuRef: HTMLDivElement | undefined = $state();

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

  // Convert mouse/touch X position to time in seconds
  const xToTime = (clientX: number): number => {
    if (!playerContainer || duration <= 0) return 0;
    const rect = playerContainer.getBoundingClientRect();
    const relativeX = Math.max(0, Math.min(clientX - rect.left, rect.width));
    return (relativeX / rect.width) * duration;
  };

  // Convert time in seconds to percentage of container width
  const timeToPercent = (timeSec: number): number => {
    if (duration <= 0) return 0;
    return (timeSec / duration) * 100;
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

  // Selection interaction constants
  const MIN_DRAG_DISTANCE = 5; // Pixels — distinguishes click from drag
  const MOUSE_HANDLE_THRESHOLD = 16; // Pixels — snap zone for mouse handle grab
  const TOUCH_HANDLE_THRESHOLD = 24; // Pixels — larger snap zone for touch
  const MIN_SELECTION_DURATION = 0.05; // Seconds — minimum useful selection
  const ARROW_KEY_STEP = 0.1; // Seconds — keyboard handle adjustment

  // Check if a pointer X position is near a selection handle, returning which handle
  const detectHandleGrab = (clientX: number, thresholdPx: number): 'start' | 'end' | null => {
    if (selectionStartSec === null || selectionEndSec === null) return null;
    const rect = playerContainer.getBoundingClientRect();
    const startPx = (timeToPercent(selectionStartSec) / 100) * rect.width + rect.left;
    const endPx = (timeToPercent(selectionEndSec) / 100) * rect.width + rect.left;

    if (Math.abs(clientX - startPx) < thresholdPx) return 'start';
    if (Math.abs(clientX - endPx) < thresholdPx) return 'end';
    return null;
  };

  // Normalize selection so start < end, and clear if too short
  const normalizeSelection = () => {
    if (selectionStartSec === null || selectionEndSec === null) return;
    if (selectionStartSec > selectionEndSec) {
      const temp = selectionStartSec;
      selectionStartSec = selectionEndSec;
      selectionEndSec = temp;
    }
    if (selectionEndSec - selectionStartSec < MIN_SELECTION_DURATION) {
      clearSelection();
    }
  };

  const handleSelectionMouseDown = (e: MouseEvent) => {
    if (!enableClipExtraction || duration <= 0) return;
    if (e.button !== 0) return;

    // Ignore clicks on interactive elements (toolbar buttons, controls, etc.)
    const target = e.target as HTMLElement;
    if (target.closest('button, select, a, [role="menu"], [role="menuitem"]')) return;

    dragOriginX = e.clientX;

    // Check if clicking near an existing handle
    const handle = detectHandleGrab(e.clientX, MOUSE_HANDLE_THRESHOLD);
    if (handle) {
      draggingHandle = handle;
      e.preventDefault();
      return;
    }

    // Start new selection
    const clickTime = xToTime(e.clientX);
    isDragSelecting = true;
    selectionStartSec = clickTime;
    selectionEndSec = clickTime;
    e.preventDefault();
  };

  const handleSelectionMouseMove = (e: MouseEvent) => {
    if (!enableClipExtraction) return;

    if (draggingHandle) {
      const newTime = xToTime(e.clientX);
      if (draggingHandle === 'start' && selectionEndSec !== null) {
        selectionStartSec = Math.min(newTime, selectionEndSec - MIN_SELECTION_DURATION);
      } else if (draggingHandle === 'end' && selectionStartSec !== null) {
        selectionEndSec = Math.max(newTime, selectionStartSec + MIN_SELECTION_DURATION);
      }
      e.preventDefault();
      return;
    }

    if (isDragSelecting) {
      selectionEndSec = xToTime(e.clientX);
      e.preventDefault();
    }
  };

  const handleSelectionMouseUp = (e: MouseEvent) => {
    if (!enableClipExtraction) return;

    if (draggingHandle) {
      draggingHandle = null;
      return;
    }

    if (isDragSelecting) {
      isDragSelecting = false;

      if (Math.abs(e.clientX - dragOriginX) < MIN_DRAG_DISTANCE) {
        selectionStartSec = null;
        selectionEndSec = null;
        return;
      }

      normalizeSelection();
    }
  };

  const clearSelection = () => {
    selectionStartSec = null;
    selectionEndSec = null;
    isDragSelecting = false;
    draggingHandle = null;
    extractionError = null;
  };

  const handleSelectionTouchStart = (e: TouchEvent) => {
    if (!enableClipExtraction || duration <= 0 || !e.touches[0]) return;

    // Ignore touches on interactive elements
    const target = e.target as HTMLElement;
    if (target.closest('button, select, a, [role="menu"], [role="menuitem"]')) return;

    if (playerContainer) playerContainer.style.touchAction = 'none';

    const touch = e.touches[0];
    dragOriginX = touch.clientX;

    // Check if touching near an existing handle
    const handle = detectHandleGrab(touch.clientX, TOUCH_HANDLE_THRESHOLD);
    if (handle) {
      draggingHandle = handle;
      e.preventDefault();
      return;
    }

    // Start new selection
    const clickTime = xToTime(touch.clientX);
    isDragSelecting = true;
    selectionStartSec = clickTime;
    selectionEndSec = clickTime;
    e.preventDefault();
  };

  const handleSelectionTouchMove = (e: TouchEvent) => {
    if (!enableClipExtraction || !e.touches[0]) return;
    const touch = e.touches[0];

    if (draggingHandle) {
      const newTime = xToTime(touch.clientX);
      if (draggingHandle === 'start' && selectionEndSec !== null) {
        selectionStartSec = Math.min(newTime, selectionEndSec - MIN_SELECTION_DURATION);
      } else if (draggingHandle === 'end' && selectionStartSec !== null) {
        selectionEndSec = Math.max(newTime, selectionStartSec + MIN_SELECTION_DURATION);
      }
      e.preventDefault();
      return;
    }

    if (isDragSelecting) {
      selectionEndSec = xToTime(touch.clientX);
      e.preventDefault();
    }
  };

  const handleSelectionTouchEnd = () => {
    if (!enableClipExtraction) return;
    if (playerContainer) playerContainer.style.touchAction = '';

    if (draggingHandle) {
      draggingHandle = null;
      return;
    }

    if (isDragSelecting) {
      isDragSelecting = false;
      normalizeSelection();
    }
  };

  const playSelection = () => {
    if (!audioElement || selectionStartSec === null || selectionEndSec === null) return;

    const start = Math.min(selectionStartSec, selectionEndSec);
    const end = Math.max(selectionStartSec, selectionEndSec);

    if (selectionPlaybackTimeout) {
      clearTimeout(selectionPlaybackTimeout);
      selectionPlaybackTimeout = undefined;
    }

    audioElement.currentTime = start;
    audioElement
      .play()
      .then(() => {
        isPlayingSelection = true;
        const durationMs = ((end - start) / playbackSpeed) * 1000;
        selectionPlaybackTimeout = setTimeout(() => {
          if (audioElement && isPlayingSelection) {
            audioElement.pause();
            isPlayingSelection = false;
          }
        }, durationMs);
      })
      .catch((err: unknown) => {
        logger.error('Failed to play selection:', err);
      });
  };

  const stopSelection = () => {
    if (!audioElement) return;
    if (selectionPlaybackTimeout) {
      clearTimeout(selectionPlaybackTimeout);
      selectionPlaybackTimeout = undefined;
    }
    audioElement.pause();
    isPlayingSelection = false;
  };

  const reviewSelection = () => {
    if (!audioElement || selectionStartSec === null || selectionEndSec === null) return;
    stopSelection();
    audioElement.currentTime = Math.min(selectionStartSec, selectionEndSec);
  };

  const toggleFormatMenu = () => {
    showFormatMenu = !showFormatMenu;
  };

  const selectFormatAndExtract = (format: string) => {
    extractionFormat = format;
    showFormatMenu = false;
    extractClip();
  };

  const extractClip = async () => {
    if (selectionStartSec === null || selectionEndSec === null || isExtracting) return;

    const start = Math.min(selectionStartSec, selectionEndSec);
    const end = Math.max(selectionStartSec, selectionEndSec);

    isExtracting = true;
    extractionError = null;

    try {
      const response = await fetch(
        buildAppUrl(`/api/v2/audio/${encodeURIComponent(detectionId)}/clip`),
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ start, end, format: extractionFormat }),
        }
      );

      if (!response.ok) {
        let errorMsg = t('components.audioPlayer.clipExtraction.extractError');
        try {
          const errorData: { message?: string } = await response.json();
          errorMsg = errorData.message ?? errorMsg;
        } catch {
          // Use default error message
        }
        throw new Error(errorMsg);
      }

      const blob = await response.blob();

      let ext = extractionFormat;
      if (ext === 'alac') ext = 'm4a';
      const label = clipLabel
        ? `${clipLabel}_${start.toFixed(1)}-${end.toFixed(1)}s`
        : `clip_${start.toFixed(1)}-${end.toFixed(1)}`;
      const filename = `${label}.${ext}`;

      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    } catch (err) {
      extractionError =
        err instanceof Error
          ? err.message
          : t('components.audioPlayer.clipExtraction.extractError');
      logger.error('Clip extraction failed:', err);
    } finally {
      isExtracting = false;
    }
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
        let errorMessage = t('components.audioPlayer.errors.generationFailedStatus', {
          status: response.status,
        });
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
      const errorMessage =
        err instanceof Error ? err.message : t('components.audioPlayer.errors.spectrogramFailed');
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
        audioRetryCount = 0;
        if (audioRetryTimer) {
          clearTimeout(audioRetryTimer);
          audioRetryTimer = undefined;
        }
        // Reset clip selection for new audio
        selectionStartSec = null;
        selectionEndSec = null;
        isDragSelecting = false;
        draggingHandle = null;
        extractionError = null;
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
        isPlayingSelection = false;
        if (selectionPlaybackTimeout) {
          clearTimeout(selectionPlaybackTimeout);
          selectionPlaybackTimeout = undefined;
        }
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
        // Reset retry state on successful load (may have succeeded after retries)
        audioRetryCount = 0;
        if (audioRetryTimer) {
          clearTimeout(audioRetryTimer);
          audioRetryTimer = undefined;
        }
      });

      addTrackedEventListener(audioElement, 'error', () => {
        // Clear timeout on error
        if (canplayTimeoutId) {
          clearTimeout(canplayTimeoutId);
          canplayTimeoutId = undefined;
        }

        // Retry loading if under the retry limit — the audio file may still
        // be encoding (backend returns 503 which the browser treats as a load error)
        if (audioRetryCount < MAX_AUDIO_LOAD_RETRIES) {
          if (audioRetryTimer) {
            clearTimeout(audioRetryTimer);
            audioRetryTimer = undefined;
          }
          audioRetryCount++;
          debugLog(`Audio load failed, retrying (${audioRetryCount}/${MAX_AUDIO_LOAD_RETRIES})`);
          audioRetryTimer = setTimeout(() => {
            if (audioElement) {
              audioElement.src = audioUrl;
              audioElement.load();
            }
          }, AUDIO_RETRY_DELAY_MS);
          return;
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

    // Global listeners for selection drag
    if (enableClipExtraction) {
      addTrackedEventListener(
        document as unknown as HTMLElement,
        'mousemove',
        handleSelectionMouseMove as EventListener
      );
      addTrackedEventListener(
        document as unknown as HTMLElement,
        'mouseup',
        handleSelectionMouseUp as EventListener
      );
      addTrackedEventListener(document as unknown as HTMLElement, 'keydown', ((
        e: KeyboardEvent
      ) => {
        if (e.key === 'Escape') {
          if (showFormatMenu) {
            showFormatMenu = false;
          } else if (selectionStartSec !== null || selectionEndSec !== null) {
            clearSelection();
          }
        }
      }) as EventListener);

      // Close format menu on click outside
      addTrackedEventListener(document as unknown as HTMLElement, 'mousedown', ((e: MouseEvent) => {
        if (showFormatMenu && formatMenuRef && !formatMenuRef.contains(e.target as Node)) {
          showFormatMenu = false;
        }
      }) as EventListener);

      // Prevent native browser selection during drag operations
      addTrackedEventListener(document as unknown as HTMLElement, 'selectstart', ((e: Event) => {
        if (isDragSelecting || draggingHandle) {
          e.preventDefault();
        }
      }) as EventListener);
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
      if (audioRetryTimer) {
        clearTimeout(audioRetryTimer);
        audioRetryTimer = undefined;
      }

      // Clear canplay safety timeout
      if (canplayTimeoutId) {
        clearTimeout(canplayTimeoutId);
        canplayTimeoutId = undefined;
      }

      // Clear selection playback timeout
      if (selectionPlaybackTimeout) {
        clearTimeout(selectionPlaybackTimeout);
        selectionPlaybackTimeout = undefined;
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

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  bind:this={playerContainer}
  class={cn('relative group overflow-hidden', className)}
  class:cursor-crosshair={enableClipExtraction}
  style={responsive
    ? ''
    : `width: ${typeof width === 'number' ? width + 'px' : width}; height: ${typeof height === 'number' ? height + 'px' : height};`}
  onmousedown={enableClipExtraction ? handleSelectionMouseDown : undefined}
  ontouchstart={enableClipExtraction ? handleSelectionTouchStart : undefined}
  ontouchmove={enableClipExtraction ? handleSelectionTouchMove : undefined}
  ontouchend={enableClipExtraction ? handleSelectionTouchEnd : undefined}
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
        class="absolute inset-0 flex items-center justify-center bg-[var(--color-base-200)]/75 rounded-md border border-[var(--color-base-300)]"
      >
        <div
          class="loading loading-spinner loading-sm md:loading-md text-[var(--color-primary)]"
          role="status"
          aria-label={t('components.audio.spectrogramLoadingAria')}
        ></div>
      </div>
    {/if}

    {#if spectrogramLoader.error}
      <!-- Error placeholder for failed spectrogram -->
      <div
        class="flex items-center justify-center bg-[var(--color-base-200)] rounded-md border border-[var(--color-base-300)]"
        style={responsive
          ? 'height: 80px;'
          : `width: ${typeof width === 'number' ? width + 'px' : width}; height: ${typeof height === 'number' ? height + 'px' : height};`}
      >
        <div class="text-center p-2">
          <XCircle
            class="size-6 sm:size-8 mx-auto mb-1 text-[var(--color-base-content)]/30"
            aria-hidden="true"
          />
          <span class="text-xs sm:text-sm text-[var(--color-base-content)]/50"
            >{t('components.audio.spectrogramUnavailable')}</span
          >
        </div>
      </div>
    {:else if spectrogramStatus?.status === 'queued' || spectrogramStatus?.status === 'generating'}
      <!-- Show generation status -->
      <div
        class="absolute inset-0 flex flex-col items-center justify-center bg-[var(--color-base-200)]/90 rounded-md border border-[var(--color-base-300)] p-2"
      >
        <div
          class="loading loading-spinner loading-xs sm:loading-sm md:loading-md"
          role="status"
          aria-label={t('components.audio.spectrogramGeneratingAria')}
        ></div>
        <div
          class="text-xs sm:text-sm text-[var(--color-base-content)] mt-1"
          role="status"
          aria-live="polite"
        >
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
          <div class="text-xs sm:text-sm text-[var(--color-base-content)]/70 mt-0.5">
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
          ? 'w-full h-auto object-contain rounded-md border border-[var(--color-base-300)]'
          : 'w-full h-full object-fill rounded-md border border-[var(--color-base-300)]'}
        class:opacity-0={spectrogramLoader.loading}
        class:select-none={enableClipExtraction}
        draggable={enableClipExtraction ? 'false' : undefined}
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

  <!-- Clip selection overlay -->
  {#if enableClipExtraction && selectionStartSec !== null && selectionEndSec !== null}
    {@const startPct = timeToPercent(Math.min(selectionStartSec, selectionEndSec))}
    {@const endPct = timeToPercent(Math.max(selectionStartSec, selectionEndSec))}

    <!-- Selection highlight -->
    <div
      class="absolute top-0 bottom-0 bg-primary/20 pointer-events-none"
      style:left="{startPct}%"
      style:width="{endPct - startPct}%"
      role="region"
      aria-label={t('components.audioPlayer.clipExtraction.rangeLabel', {
        start: Math.min(selectionStartSec, selectionEndSec).toFixed(1),
        end: Math.max(selectionStartSec, selectionEndSec).toFixed(1),
      })}
    ></div>

    <!-- Start handle: visible 2px line with wider 16px invisible hit zone -->
    {#if !isDragSelecting}
      <div
        class="absolute top-0 bottom-0 w-4 cursor-col-resize z-10 -translate-x-1/2 flex justify-center"
        style:left="{startPct}%"
        role="slider"
        tabindex="0"
        aria-label="Selection start"
        aria-valuemin={0}
        aria-valuemax={duration}
        aria-valuenow={Math.min(selectionStartSec, selectionEndSec)}
        onmousedown={(e: MouseEvent) => {
          if (e.button !== 0) return;
          draggingHandle = 'start';
          e.preventDefault();
          e.stopPropagation();
        }}
        ontouchstart={(e: TouchEvent) => {
          draggingHandle = 'start';
          e.preventDefault();
          e.stopPropagation();
        }}
        onkeydown={(e: KeyboardEvent) => {
          if (e.key === 'ArrowLeft' && selectionStartSec !== null) {
            selectionStartSec = Math.max(0, selectionStartSec - ARROW_KEY_STEP);
          } else if (
            e.key === 'ArrowRight' &&
            selectionStartSec !== null &&
            selectionEndSec !== null
          ) {
            selectionStartSec = Math.min(
              selectionStartSec + ARROW_KEY_STEP,
              selectionEndSec - MIN_SELECTION_DURATION
            );
          }
        }}
      >
        <div class="w-0.5 h-full bg-primary"></div>
      </div>

      <!-- End handle: visible 2px line with wider 16px invisible hit zone -->
      <div
        class="absolute top-0 bottom-0 w-4 cursor-col-resize z-10 -translate-x-1/2 flex justify-center"
        style:left="{endPct}%"
        role="slider"
        tabindex="0"
        aria-label="Selection end"
        aria-valuemin={0}
        aria-valuemax={duration}
        aria-valuenow={Math.max(selectionStartSec, selectionEndSec)}
        onmousedown={(e: MouseEvent) => {
          if (e.button !== 0) return;
          draggingHandle = 'end';
          e.preventDefault();
          e.stopPropagation();
        }}
        ontouchstart={(e: TouchEvent) => {
          draggingHandle = 'end';
          e.preventDefault();
          e.stopPropagation();
        }}
        onkeydown={(e: KeyboardEvent) => {
          if (e.key === 'ArrowRight' && selectionEndSec !== null) {
            selectionEndSec = Math.min(duration, selectionEndSec + ARROW_KEY_STEP);
          } else if (
            e.key === 'ArrowLeft' &&
            selectionEndSec !== null &&
            selectionStartSec !== null
          ) {
            selectionEndSec = Math.max(
              selectionEndSec - ARROW_KEY_STEP,
              selectionStartSec + MIN_SELECTION_DURATION
            );
          }
        }}
      >
        <div class="w-0.5 h-full bg-primary"></div>
      </div>

      <!-- Selection toolbar -->
      {@const toolbarLeftPct = (startPct + endPct) / 2}
      <div
        class="absolute z-20 flex items-center gap-1.5 px-2.5 py-1.5 rounded-full bg-[var(--color-base-300)]/90 backdrop-blur-sm shadow-md text-sm"
        style:top="4px"
        style:left="{toolbarLeftPct}%"
        style:transform="translateX(-50%)"
      >
        <!-- Time range display -->
        <span class="text-[var(--color-base-content)] whitespace-nowrap font-medium">
          {Math.min(selectionStartSec, selectionEndSec).toFixed(1)}s &ndash; {Math.max(
            selectionStartSec,
            selectionEndSec
          ).toFixed(1)}s
        </span>

        <!-- Divider -->
        <div class="w-px h-5 bg-[var(--color-base-content)]/20"></div>

        <!-- Review (jump to start) button -->
        <button
          class="p-1.5 rounded-full hover:bg-[var(--color-base-content)]/10 text-[var(--color-base-content)]"
          onclick={reviewSelection}
          aria-label={t('components.audioPlayer.clipExtraction.reviewSelection')}
          title={t('components.audioPlayer.clipExtraction.reviewSelection')}
        >
          <SkipBack class="size-4" />
        </button>

        <!-- Play/Pause selection button -->
        {#if isPlayingSelection}
          <button
            class="p-1.5 rounded-full hover:bg-[var(--color-base-content)]/10 text-[var(--color-base-content)]"
            onclick={stopSelection}
            aria-label={t('components.audioPlayer.clipExtraction.pauseSelection')}
            title={t('components.audioPlayer.clipExtraction.pauseSelection')}
          >
            <Pause class="size-4" />
          </button>
        {:else}
          <button
            class="p-1.5 rounded-full hover:bg-[var(--color-base-content)]/10 text-[var(--color-base-content)]"
            onclick={playSelection}
            aria-label={t('components.audioPlayer.clipExtraction.playSelection')}
            title={t('components.audioPlayer.clipExtraction.playSelection')}
          >
            <Play class="size-4" />
          </button>
        {/if}

        <!-- Divider -->
        <div class="w-px h-5 bg-[var(--color-base-content)]/20"></div>

        <!-- Download with format picker -->
        <div class="relative" bind:this={formatMenuRef}>
          <button
            class="p-1.5 rounded-full hover:bg-[var(--color-base-content)]/10 text-[var(--color-base-content)] flex items-center gap-0.5"
            onclick={toggleFormatMenu}
            disabled={isExtracting}
            aria-label={t('components.audioPlayer.clipExtraction.extractClip')}
            title={t('components.audioPlayer.clipExtraction.extractClip')}
            aria-expanded={showFormatMenu}
            aria-haspopup="menu"
          >
            {#if isExtracting}
              <div
                class="w-4 h-4 border-2 border-current border-t-transparent rounded-full animate-spin"
              ></div>
            {:else}
              <Download class="size-4" />
              <ChevronDown class="size-3" />
            {/if}
          </button>

          <!-- Format dropdown menu -->
          {#if showFormatMenu}
            <div
              class="absolute top-full left-1/2 -translate-x-1/2 mt-1 py-1 rounded-lg bg-[var(--color-base-100)] border border-[var(--color-base-300)] shadow-lg min-w-[8.5rem] z-30"
              role="menu"
            >
              {#each [{ value: 'wav', label: 'WAV', desc: 'Lossless' }, { value: 'flac', label: 'FLAC', desc: 'Lossless' }, { value: 'alac', label: 'ALAC', desc: 'Lossless' }, { value: 'mp3', label: 'MP3', desc: 'Lossy' }, { value: 'opus', label: 'Opus', desc: 'Lossy' }, { value: 'aac', label: 'AAC', desc: 'Lossy' }] as fmt (fmt.value)}
                <button
                  class="w-full px-3.5 py-2 text-left flex items-center justify-between gap-3 hover:bg-[var(--color-base-200)] text-[var(--color-base-content)] transition-colors"
                  onclick={() => selectFormatAndExtract(fmt.value)}
                  role="menuitem"
                >
                  <span class="font-medium">{fmt.label}</span>
                  <span class="text-[var(--color-base-content)]/50 text-xs">{fmt.desc}</span>
                </button>
              {/each}
            </div>
          {/if}
        </div>

        <!-- Clear selection button -->
        <button
          class="p-1.5 rounded-full hover:bg-[var(--color-base-content)]/10 text-[var(--color-base-content)]"
          onclick={clearSelection}
          aria-label={t('components.audioPlayer.clipExtraction.clearSelection')}
          title={t('components.audioPlayer.clipExtraction.clearSelection')}
        >
          <XCircle class="size-4" />
        </button>

        <!-- Extraction error -->
        {#if extractionError}
          <span class="text-[var(--color-error)] ml-1">{extractionError}</span>
        {/if}
      </div>
    {/if}
  {/if}

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
          class="bg-primary h-1.5 rounded-full transition-all duration-100"
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
      class="absolute inset-0 flex items-center justify-center bg-[color-mix(in_srgb,var(--color-error)_15%,transparent)] rounded-md"
    >
      <span class="text-[var(--color-error)] text-sm">{error}</span>
    </div>
  {/if}
</div>
