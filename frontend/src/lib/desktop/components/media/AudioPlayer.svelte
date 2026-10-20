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
  - modelType: AI model type ('bird', 'bat'); selects the frequency axis
  - clipSampleRate: Clip sample rate in Hz; sets the bat frequency axis range
  - onPlayStart: Callback when audio starts playing (freezes SSE updates)
  - onPlayEnd: Callback when audio stops playing after 3s delay (resumes SSE updates)
-->

<script lang="ts">
  import { onMount } from 'svelte';
  import { cn } from '$lib/utils/cn.js';
  import { Play, Pause, Download, XCircle } from '@lucide/svelte';
  import AudioSettingsButton from '$lib/desktop/features/dashboard/components/AudioSettingsButton.svelte';
  import AudibleBatsButton from '$lib/desktop/features/dashboard/components/AudibleBatsButton.svelte';
  import { useAudibleBats } from '$lib/utils/useAudibleBats.svelte';
  import AudioToolbar from './AudioToolbar.svelte';
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
    isPlayInterruptedError,
    pendingRetryDelayMs,
    playButtonLabelKey,
    probeAudioSource,
  } from '$lib/utils/audio';
  import {
    getAudioContext,
    isAudioContextSupported,
    releaseAudioContext,
  } from '$lib/utils/audioContextManager';
  import {
    attachAudioGraphWhenRunning,
    disconnectAudioNodes,
    type AudioNodeChain,
  } from '$lib/utils/audioNodes';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import {
    CLIP_CONTAINER_EXTENSION,
    extensionFromContentDisposition,
  } from '$lib/utils/audioDownload';
  import { getCsrfToken } from '$lib/utils/api';
  import { isKnownSampleRate, lookupClipSampleRate } from '$lib/utils/clipSampleRateLookup';
  import {
    SPECTROGRAM_REASON,
    fetchSpectrogramStatus,
    isTerminalSpectrogramReason,
    isRetryableSpectrogramReason,
    isSpectrogramSourceKnownMissing,
    markSpectrogramSourceMissing,
    parseSpectrogramStatus,
    type SpectrogramGenerationStatus,
  } from '$lib/utils/spectrogramStatus';
  import { batFrequencyAxis } from '$lib/utils/spectrogramAxis';
  import { get } from 'svelte/store';
  import { dashboardSettings } from '$lib/stores/settings';

  const logger = loggers.audio;

  // Build JSON headers with CSRF token (server rejects mutating requests without it).
  function jsonHeadersWithCsrf(): Record<string, string> {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    const csrfToken = getCsrfToken();
    if (csrfToken) {
      headers['X-CSRF-Token'] = csrfToken;
    }
    return headers;
  }

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
    /** AI model type (e.g. 'bird', 'bat'); selects the spectrogram frequency axis range */
    modelType?: string;
    /**
     * Sample rate of the clip in Hz. Bat spectrograms span 0 to half this rate;
     * when it is missing the player asks the detection endpoint for it once.
     */
    clipSampleRate?: number;
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
    modelType = '',
    clipSampleRate,
  }: Props = $props();

  // Audio and UI elements
  let audioElement: HTMLAudioElement;
  let playerContainer: HTMLDivElement;
  // svelte-ignore non_reactive_update
  let playPauseButton!: HTMLButtonElement; // Template-only binding
  // svelte-ignore non_reactive_update
  let progressBar: HTMLDivElement; // Template-only binding
  // svelte-ignore non_reactive_update
  let spectrogramImage: HTMLImageElement; // Template-only binding

  // Audio state
  let isPlaying = $state(false);
  let currentTime = $state(0);
  let duration = $state(0);
  let audioContextAvailable = $state(true);
  // Mutual-exclusion signals: bumping one forces the sibling popup (Audible
  // Bats vs Audio Settings) closed, so only one is ever open at a time.
  let closeAudibleBatsSignal = $state(0);
  let closeAudioSettingsSignal = $state(0);
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

  // Audio load retry state: handles race condition where detection DB record
  // exists but audio file is still being encoded by FFmpeg (backend returns 503)
  let audioRetryCount = 0;
  let audioRetryTimer: ReturnType<typeof setTimeout> | undefined;
  // Aborts the one byte probe of the audio URL that runs beside the first retry timer.
  let audioProbeController: AbortController | undefined;
  // True when the user pressed play while a load retry was pending; consumed once
  // by 'loadedmetadata' or 'canplay' so the play intent is not lost when the
  // transient error is suppressed.
  let playRequestedAfterRetry = false;

  // Spectrogram generation status
  let spectrogramStatus = $state<SpectrogramGenerationStatus | null>(null);
  let statusPollTimer: ReturnType<typeof setTimeout> | undefined;
  let statusPollStartTime: number | undefined;
  let statusPollAbortController: AbortController | undefined;
  // True once the status poll answered failed for the current attempt; ends the image
  // retries, including one already in flight. Cleared whenever a new attempt starts.
  let spectrogramStatusFailed = false;

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
    element: HTMLElement | Document | Window | null;
    event: string;
    handler: EventListener;
  }> = [];

  // Control state
  // Read persistent default from settings store (one-time, not reactive:
  // changing the setting while a player is mounted should not cause gain to jump)
  const defaultGainDb = get(dashboardSettings)?.defaultAudioGain ?? 0;
  let gainValue = $state(defaultGainDb); // dB
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
  let mouseDownInPlayer = false; // Track if mousedown originated in the player container
  let isScrubbing = $state(false);
  let scrubStartTime = $state(0); // time at drag origin when scrubbing
  let scrubSelectionDuration = $state(0); // locked selection width during scrub

  // Clip extraction state
  let extractionFormat = $state('wav');
  let isExtracting = $state(false);
  let extractionError = $state<string | null>(null);
  let isPlayingSelection = $state(false);
  let selectionPlaybackTimeout: ReturnType<typeof setTimeout> | undefined;
  let loopEnabled = $state(false);

  // Processing state
  let processingDenoise = $state('');
  let processingNormalize = $state(false);
  let isProcessing = $state(false);
  let processedAudioUrl = $state<string | null>(null);
  let processedSpectrogramUrl = $state<string | null>(null);
  let isSpectrogramProcessing = $state(false);
  let processingActive = $derived(processingDenoise !== '' || processingNormalize);
  let processAbortController: AbortController | null = null;
  let processDebounceTimer: ReturnType<typeof setTimeout> | null = null;

  // Audible bats derived playback. When active, the player swaps its audio source
  // to a server-generated time-expanded (slowed) copy so ultrasonic bat calls fall
  // into the human hearing range. The spectrogram still spans the full original
  // clip; because the derived audio's duration scales by the expansion factor, the
  // playhead and seeking stay proportionally in sync automatically. The request
  // lifecycle lives in the shared composable; this component owns the actual
  // source swap (so position is preserved across the duration change).
  const audibleBats = useAudibleBats({
    getDetectionId: () => detectionId,
    onActivate: url => swapAudioSource(url),
    onDeactivate: () => swapAudioSource(processedAudioUrl ?? audioUrl),
  });
  // Proportional position (0-1) to restore after an audio source swap, applied
  // once the new source's metadata (and therefore its duration) is available.
  let audibleBatsPendingFraction: number | null = null;
  // Whether playback should resume once the swapped-in source's metadata (and
  // restored position) are ready, which avoids a race where play() starts from 0
  // before handleLoadedMetadata applies audibleBatsPendingFraction.
  let audibleBatsPendingAutoplay = false;

  // Constants
  const GAIN_MAX_DB = 24;
  const FILTER_HP_MIN_FREQ = 20;
  const FILTER_HP_MAX_FREQ = 10000;

  // Frequency scale overlay. Bird spectrograms are drawn for 24 kHz, so their
  // axis is a fixed 0-12 kHz. Bat spectrograms are rendered at the clip's own
  // sample rate, so their axis runs from 0 to half the clip sample rate. Without a
  // known rate the bat axis is hidden rather than guessed.
  const BIRD_NYQUIST_KHZ = 12;
  const BIRD_TICKS_KHZ = [12, 10, 8, 6, 5, 4, 3, 2, 1];
  const MODEL_TYPE_BAT = 'bat';
  // Query parameter that tells the server the rate the axis is drawn from; the
  // server then serves only the exact render for that rate (404 otherwise).
  const SPECTROGRAM_RATE_PARAM = 'sr';
  // Longest wait for the status check of a failed image URL that carries the
  // rate. It is longer than the server's 5 s wait for a missing audio file, so a
  // 404 from that path still arrives in time.
  const SPECTROGRAM_PROBE_TIMEOUT_MS = 8000;
  const HTTP_STATUS_NOT_FOUND = 404;
  const HTTP_STATUS_REQUEST_TIMEOUT = 408;
  const HTTP_STATUS_TOO_MANY_REQUESTS = 429;
  const HTTP_STATUS_SERVER_ERROR_MIN = 500;
  // Generation statuses that mean the exact-rate render is coming or already
  // there, so a 404 for it is not a missing render.
  const RENDER_PENDING_OR_READY_STATUSES = new Set([
    'queued',
    'generating',
    'generated',
    'completed',
    'exists',
  ]);

  let isBatSpectrogram = $derived(modelType === MODEL_TYPE_BAT);

  // Rate learned from GET /api/v2/detections/:id, keyed by the detection it was
  // resolved for so a new detection never reuses an old rate. rate is undefined
  // when the resolve finished without one (error, timeout, field omitted).
  let rateResolve = $state<{ detectionId: string; rate: number | undefined } | null>(null);
  // Detection whose exact-rate spectrogram failed to load; it is shown without sr
  // and without an axis instead.
  let overlayFallbackDetectionId = $state<string | null>(null);

  let needsRateResolve = $derived(
    isBatSpectrogram && showSpectrogram && !isKnownSampleRate(clipSampleRate) && detectionId !== ''
  );
  let rateResolvePending = $derived(needsRateResolve && rateResolve?.detectionId !== detectionId);
  let effectiveClipSampleRate = $derived(
    isKnownSampleRate(clipSampleRate)
      ? clipSampleRate
      : rateResolve?.detectionId === detectionId
        ? rateResolve.rate
        : undefined
  );
  // Rate the spectrogram URL carries and the bat axis is drawn from.
  let overlayRate = $derived(
    isBatSpectrogram && overlayFallbackDetectionId !== detectionId
      ? effectiveClipSampleRate
      : undefined
  );
  let batAxis = $derived(isBatSpectrogram ? batFrequencyAxis(overlayRate) : null);
  let freqNyquistKHz = $derived(isBatSpectrogram ? (batAxis?.nyquistKHz ?? 0) : BIRD_NYQUIST_KHZ);
  let freqTicksKHz = $derived(isBatSpectrogram ? (batAxis?.ticksKHz ?? []) : BIRD_TICKS_KHZ);
  // PLAY_END_DELAY_MS imported from $lib/utils/audio
  // Spinner delay is now handled by useDelayedLoading utility

  // Computed values
  // Base URL without cache-busting parameters. It stays null while a bat rate
  // is being resolved, so an image cached under a URL without the rate is never
  // shown first under an axis it does not match.
  const spectrogramBaseUrl = $derived(
    showSpectrogram && !rateResolvePending
      ? buildAppUrl(
          `/api/v2/spectrogram/${encodeURIComponent(detectionId)}?size=${spectrogramSize}${spectrogramRaw ? '&raw=true' : ''}${overlayRate !== undefined ? `&${SPECTROGRAM_RATE_PARAM}=${overlayRate}` : ''}`
        )
      : null
  );

  // Active URL with cache-busting for reactive reloads (retry count + cache key)
  // Uses processed spectrogram URL when audio processing is active
  const spectrogramUrl = $derived(
    processedSpectrogramUrl ??
      (spectrogramBaseUrl
        ? `${spectrogramBaseUrl}&cache=${spectrogramCacheKey}${spectrogramRetryCount > 0 ? `&retry=${spectrogramRetryCount}` : ''}`
        : null)
  );

  // The frequency axis overlay: drawn over a spectrogram that has not failed,
  // with a known range; a bat axis only once its image has loaded.
  const showFreqAxis = $derived(
    showSpectrogram &&
      spectrogramUrl !== null &&
      !spectrogramLoader.error &&
      freqNyquistKHz > 0 &&
      !(isBatSpectrogram && spectrogramLoader.loading)
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
    if (!playerContainer || !isFinite(duration) || duration <= 0) return 0;
    const rect = playerContainer.getBoundingClientRect();
    const relativeX = Math.max(0, Math.min(clientX - rect.left, rect.width));
    return (relativeX / rect.width) * duration;
  };

  // Convert time in seconds to percentage of container width
  const timeToPercent = (timeSec: number): number => {
    if (!isFinite(duration) || duration <= 0) return 0;
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
  const MIN_DRAG_DISTANCE = 5; // Pixels: distinguishes click from drag
  const MOUSE_HANDLE_THRESHOLD = 16; // Pixels: snap zone for mouse handle grab
  const TOUCH_HANDLE_THRESHOLD = 24; // Pixels: larger snap zone for touch
  const MIN_SELECTION_DURATION = 0.05; // Seconds: minimum useful selection
  const ARROW_KEY_STEP = 0.1; // Seconds: keyboard handle adjustment
  const PROCESS_DEBOUNCE_MS = 300; // Debounce delay for server-side processing requests
  const INTERACTIVE_ELEMENTS_SELECTOR =
    'button, select, input, label, a, [role="button"], [role="dialog"]';

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

  // Check if a time position falls inside the current selection
  const isInsideSelection = (timeSec: number): boolean => {
    if (selectionStartSec === null || selectionEndSec === null) return false;
    const lo = Math.min(selectionStartSec, selectionEndSec);
    const hi = Math.max(selectionStartSec, selectionEndSec);
    return timeSec >= lo && timeSec <= hi;
  };

  const hasActiveSelection = (): boolean => {
    return selectionStartSec !== null && selectionEndSec !== null;
  };

  const handleSelectionMouseDown = (e: MouseEvent) => {
    if (!enableClipExtraction || !isFinite(duration) || duration <= 0) return;
    if (e.button !== 0) return;

    // Ignore clicks on interactive elements (toolbar buttons, controls, sliders, popups, etc.)
    const target = e.target as HTMLElement;
    if (target.closest(INTERACTIVE_ELEMENTS_SELECTOR)) return;

    dragOriginX = e.clientX;
    mouseDownInPlayer = true;

    // Check if clicking near an existing handle
    const handle = detectHandleGrab(e.clientX, MOUSE_HANDLE_THRESHOLD);
    if (handle) {
      draggingHandle = handle;
      e.preventDefault();
      return;
    }

    // Check if clicking inside existing selection: start scrubbing
    const clickTime = xToTime(e.clientX);
    if (isInsideSelection(clickTime) && selectionStartSec !== null && selectionEndSec !== null) {
      isScrubbing = true;
      scrubStartTime = clickTime;
      scrubSelectionDuration =
        Math.max(selectionStartSec, selectionEndSec) - Math.min(selectionStartSec, selectionEndSec);
      e.preventDefault();
      return;
    }

    // Don't start selection yet; wait for mousemove to confirm it's a drag.
    // Single click will seek the playhead in mouseUp handler.
    e.preventDefault();
  };

  // Shared logic for updating handle position during drag (clamped to [0, duration])
  const updateHandleDrag = (clientX: number) => {
    const newTime = xToTime(clientX);
    if (draggingHandle === 'start' && selectionEndSec !== null) {
      selectionStartSec = Math.max(0, Math.min(newTime, selectionEndSec - MIN_SELECTION_DURATION));
    } else if (draggingHandle === 'end' && selectionStartSec !== null) {
      selectionEndSec = Math.min(
        duration,
        Math.max(newTime, selectionStartSec + MIN_SELECTION_DURATION)
      );
    }
  };

  // Shared logic for moving entire selection during scrub
  const updateScrubPosition = (clientX: number) => {
    if (selectionStartSec === null || selectionEndSec === null) return;
    const currentTime = xToTime(clientX);
    const delta = currentTime - scrubStartTime;
    const lo = Math.min(selectionStartSec, selectionEndSec);
    let newStart = lo + delta;
    // Clamp to [0, duration - selectionWidth]
    const clampedStart = Math.max(0, Math.min(newStart, duration - scrubSelectionDuration));
    selectionStartSec = clampedStart;
    selectionEndSec = clampedStart + scrubSelectionDuration;
    // Only advance scrubStartTime by the amount actually moved (prevents jump on re-entry)
    scrubStartTime += clampedStart - lo;
  };

  const handleSelectionMouseMove = (e: MouseEvent) => {
    if (!enableClipExtraction) return;

    if (draggingHandle) {
      updateHandleDrag(e.clientX);
      e.preventDefault();
      return;
    }

    if (isScrubbing) {
      updateScrubPosition(e.clientX);
      e.preventDefault();
      return;
    }

    if (isDragSelecting) {
      selectionEndSec = xToTime(e.clientX);
      e.preventDefault();
      return;
    }

    // Start new selection only after dragging beyond threshold (and mousedown was in player)
    if (
      mouseDownInPlayer &&
      e.buttons === 1 &&
      Math.abs(e.clientX - dragOriginX) >= MIN_DRAG_DISTANCE
    ) {
      isDragSelecting = true;
      selectionStartSec = xToTime(dragOriginX);
      selectionEndSec = xToTime(e.clientX);
      e.preventDefault();
    }
  };

  const handleSelectionMouseUp = (e: MouseEvent) => {
    if (!enableClipExtraction) return;

    const wasInPlayer = mouseDownInPlayer;
    mouseDownInPlayer = false;

    if (draggingHandle) {
      draggingHandle = null;
      return;
    }

    if (isScrubbing) {
      isScrubbing = false;
      return;
    }

    if (isDragSelecting) {
      isDragSelecting = false;
      normalizeSelection();
      return;
    }

    // Single click (no drag): seek or clear selection
    // Only if the mousedown originated within the spectrogram container
    if (wasInPlayer && Math.abs(e.clientX - dragOriginX) < MIN_DRAG_DISTANCE) {
      const clickTime = xToTime(e.clientX);

      // If there's an active selection and click is outside it, clear the selection
      if (hasActiveSelection() && !isInsideSelection(clickTime)) {
        clearSelection();
        return;
      }

      // Seek playhead to clicked position
      if (audioElement && duration > 0) {
        audioElement.currentTime = clickTime;
        currentTime = clickTime;
        progress = (clickTime / duration) * 100;
      }
    }
  };

  const clearSelection = () => {
    stopSelection(); // Cancel any ongoing selection preview playback
    selectionStartSec = null;
    selectionEndSec = null;
    isDragSelecting = false;
    draggingHandle = null;
    isScrubbing = false;
    extractionError = null;
  };

  const handleSelectionTouchStart = (e: TouchEvent) => {
    if (!enableClipExtraction || !isFinite(duration) || duration <= 0 || !e.touches[0]) return;

    // Ignore touches on interactive elements (buttons, sliders, popups, etc.)
    const target = e.target as HTMLElement;
    if (target.closest(INTERACTIVE_ELEMENTS_SELECTOR)) return;

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

    // Check if touching inside existing selection: start scrubbing
    const clickTime = xToTime(touch.clientX);
    if (isInsideSelection(clickTime) && selectionStartSec !== null && selectionEndSec !== null) {
      isScrubbing = true;
      scrubStartTime = clickTime;
      scrubSelectionDuration =
        Math.max(selectionStartSec, selectionEndSec) - Math.min(selectionStartSec, selectionEndSec);
      e.preventDefault();
      return;
    }

    // Start new selection
    isDragSelecting = true;
    selectionStartSec = clickTime;
    selectionEndSec = clickTime;
    e.preventDefault();
  };

  const handleSelectionTouchMove = (e: TouchEvent) => {
    if (!enableClipExtraction || !e.touches[0]) return;
    const touch = e.touches[0];

    if (draggingHandle) {
      updateHandleDrag(touch.clientX);
      e.preventDefault();
      return;
    }

    if (isScrubbing) {
      updateScrubPosition(touch.clientX);
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

    if (isScrubbing) {
      isScrubbing = false;
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
        scheduleSelectionEnd(start, end);
      })
      .catch((err: unknown) => {
        logger.error('Failed to play selection:', err);
      });
  };

  const scheduleSelectionEnd = (start: number, end: number) => {
    if (selectionPlaybackTimeout) {
      clearTimeout(selectionPlaybackTimeout);
    }
    const durationMs = ((end - start) / playbackSpeed) * 1000;
    selectionPlaybackTimeout = setTimeout(() => {
      if (!audioElement || !isPlayingSelection) return;
      if (loopEnabled) {
        // Loop: restart from selection start
        audioElement.currentTime = start;
        scheduleSelectionEnd(start, end);
      } else {
        audioElement.pause();
        isPlayingSelection = false;
      }
    }, durationMs);
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

  const extractClip = async (startOverride?: number, endOverride?: number) => {
    const start =
      startOverride ??
      (selectionStartSec !== null && selectionEndSec !== null
        ? Math.min(selectionStartSec, selectionEndSec)
        : null);
    const end =
      endOverride ??
      (selectionStartSec !== null && selectionEndSec !== null
        ? Math.max(selectionStartSec, selectionEndSec)
        : null);
    if (start === null || end === null || isExtracting) return;

    isExtracting = true;
    extractionError = null;

    try {
      const response = await fetch(
        buildAppUrl(`/api/v2/audio/${encodeURIComponent(detectionId)}/clip`),
        {
          method: 'POST',
          headers: jsonHeadersWithCsrf(),
          body: JSON.stringify({
            start,
            end,
            format: extractionFormat,
            normalize: processingNormalize,
            denoise: processingDenoise,
            gain_db: gainValue,
          }),
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

      // Name the download for the container the server produced (m4a for AAC, ogg
      // for Opus), taken from the response's Content-Disposition filename.
      const ext = extensionFromContentDisposition(
        response.headers.get('Content-Disposition'),
        CLIP_CONTAINER_EXTENSION[extractionFormat] ?? extractionFormat
      );
      // Sanitize label for filesystem safety (remove reserved chars, normalize whitespace)
      const safeLabel = clipLabel
        ? clipLabel.replace(/[<>:"/\\|?*]/g, '-').replace(/\s+/g, '_')
        : '';
      const label = safeLabel
        ? `${safeLabel}_${start.toFixed(1)}-${end.toFixed(1)}s`
        : `clip_${start.toFixed(1)}-${end.toFixed(1)}`;
      const filename = `${label}.${ext}`;

      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      // Defer revocation to allow browser download dialog to complete
      setTimeout(() => URL.revokeObjectURL(url), 1000);
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

  // --- Audio processing (denoise / normalize preview) ---

  function triggerProcessing() {
    if (processDebounceTimer) clearTimeout(processDebounceTimer);
    processDebounceTimer = setTimeout(() => {
      processAudio();
    }, PROCESS_DEBOUNCE_MS);
  }

  async function processAudio() {
    // Abort any in-flight processing request
    if (processAbortController) {
      processAbortController.abort();
    }
    // The source is about to change, so a queued play intent or load retry
    // belongs to the old clip and must not autoplay the processed one.
    resetRetryState();

    if (!processingDenoise && !processingNormalize) {
      // No server-side processing needed; revert to original audio and spectrogram
      if (processedAudioUrl) {
        URL.revokeObjectURL(processedAudioUrl);
        processedAudioUrl = null;
      }
      if (processedSpectrogramUrl) {
        URL.revokeObjectURL(processedSpectrogramUrl);
        processedSpectrogramUrl = null;
      }
      if (audioElement) {
        const pos = audioElement.currentTime;
        replaceSource(audioUrl);
        audioElement.currentTime = pos;
      }
      return;
    }

    isProcessing = true;
    // Pause playback while backend applies filters
    if (isPlaying && audioElement) {
      audioElement.pause();
      isPlaying = false;
    }
    const controller = new AbortController();
    processAbortController = controller;

    try {
      const response = await fetch(
        buildAppUrl(`/api/v2/audio/${encodeURIComponent(detectionId)}/process`),
        {
          method: 'POST',
          headers: jsonHeadersWithCsrf(),
          signal: controller.signal,
          body: JSON.stringify({
            normalize: processingNormalize,
            denoise: processingDenoise,
            gain_db: 0, // gain applied client-side via Web Audio API
          }),
        }
      );

      if (!response.ok) {
        throw new Error(`Processing failed: ${response.status}`);
      }

      const blob = await response.blob();

      // Discard result if a newer request superseded this one
      if (processAbortController !== controller) return;

      // Revoke previous object URL to prevent memory leak
      if (processedAudioUrl) {
        URL.revokeObjectURL(processedAudioUrl);
      }

      processedAudioUrl = URL.createObjectURL(blob);

      if (audioElement) {
        const pos = audioElement.currentTime;
        replaceSource(processedAudioUrl);
        audioElement.currentTime = pos;
      }

      // Update spectrogram to reflect processed audio
      if (enableClipExtraction) {
        updateProcessedSpectrogram(controller.signal);
      }
    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') {
        return; // Superseded by newer request, don't touch state
      }
      // Discard error handling if a newer request superseded this one
      if (processAbortController !== controller) return;

      logger.error('Audio processing failed', err as Error);
      // Revert to original audio and reset processing state
      processingDenoise = '';
      processingNormalize = false;
      if (processedAudioUrl) {
        URL.revokeObjectURL(processedAudioUrl);
        processedAudioUrl = null;
      }
      if (processedSpectrogramUrl) {
        URL.revokeObjectURL(processedSpectrogramUrl);
        processedSpectrogramUrl = null;
      }
      if (audioElement) {
        const pos = audioElement.currentTime;
        replaceSource(audioUrl);
        audioElement.currentTime = pos;
      }
    } finally {
      // Only clear state if this is still the active request (avoid stale finally)
      if (processAbortController === controller) {
        isProcessing = false;
        processAbortController = null;
      }
    }
  }

  async function updateProcessedSpectrogram(signal: AbortSignal) {
    isSpectrogramProcessing = true;
    try {
      const response = await fetch(
        buildAppUrl(
          `/api/v2/spectrogram/${encodeURIComponent(detectionId)}/process?size=${spectrogramSize}${spectrogramRaw ? '&raw=true' : ''}`
        ),
        {
          method: 'POST',
          headers: jsonHeadersWithCsrf(),
          signal,
          body: JSON.stringify({
            normalize: processingNormalize,
            denoise: processingDenoise,
            gain_db: 0,
          }),
        }
      );

      if (!response.ok) {
        logger.warn('Processed spectrogram generation failed', { status: response.status });
        return;
      }

      if (signal.aborted) return;

      const blob = await response.blob();
      if (signal.aborted) return;

      // Revoke previous processed spectrogram URL
      if (processedSpectrogramUrl) {
        URL.revokeObjectURL(processedSpectrogramUrl);
      }
      processedSpectrogramUrl = URL.createObjectURL(blob);
    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') return;
      logger.warn('Failed to update processed spectrogram', err as Error);
    } finally {
      isSpectrogramProcessing = false;
    }
  }

  function handleDenoiseChange(preset: string) {
    processingDenoise = preset;
    triggerProcessing();
  }

  function handleNormalizeToggle() {
    processingNormalize = !processingNormalize;
    triggerProcessing();
  }

  function handleToolbarExport(format: string) {
    if (format === 'original') {
      // Download original file directly; an empty download attribute lets the
      // browser use the server's Content-Disposition filename
      const a = document.createElement('a');
      a.href = audioUrl;
      a.download = '';
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      return;
    }

    extractionFormat = format;
    if (selectionStartSec === null || selectionEndSec === null) {
      // No selection: export full file (guard against unloaded metadata)
      if (!isFinite(duration) || duration <= 0) return;
      extractClip(0, duration);
    } else {
      extractClip();
    }
  }

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
      // Availability is derived by the caller from whether the graph actually
      // attached (audioNodes !== null), so it is not reported true while the
      // context is still suspended.
      return audioContext;
    } catch {
      logger.warn('Web Audio API is not supported in this browser');
      audioContextAvailable = false;
      return null;
    }
  };

  // The source the element should currently load: the audible-bats clip or the
  // processed clip when active, otherwise the original.
  const currentSource = () =>
    audibleBats.active && audibleBats.url ? audibleBats.url : (processedAudioUrl ?? audioUrl);

  // Also ends the probe: it only matters while the retry it may cancel is pending.
  function clearAudioRetryTimer() {
    audioProbeController?.abort();
    audioProbeController = undefined;
    if (audioRetryTimer !== undefined) {
      clearTimeout(audioRetryTimer);
      audioRetryTimer = undefined;
    }
  }

  // A src assignment or load() pauses the element without a 'pause' event, so
  // mirror the pause listener when the flag still says playing.
  function markPlaybackStopped() {
    if (isPlaying) handlePause();
  }

  // Resume playback the user asked for during a retry, at most once.
  function consumePendingPlay() {
    if (!playRequestedAfterRetry) return;
    playRequestedAfterRetry = false;
    if (audioElement?.paused) {
      void audioElement.play().catch(handlePlayRejection);
    }
  }

  // Handle a rejected play(). While a retry timer is pending the retry can
  // still succeed, so only remember the play intent.
  function handlePlayRejection(err: unknown) {
    if (audioRetryTimer !== undefined) {
      playRequestedAfterRetry = true;
      return;
    }
    if (isPlayInterruptedError(err)) {
      logger.debug('Audio play interrupted', { detectionId });
      return;
    }
    // A reload's loadstart set isLoading, which disables the play button, so an
    // error must never sit behind a disabled spinner button.
    isLoading = false;
    if (audioElement?.error) {
      error ??= t('media.audio.error');
      logger.warn('Audio play failed after a load error', { detectionId });
      return;
    }
    logger.error('Error playing audio:', err);
    error = t('media.audio.playError');
  }

  // Drop any queued play intent and pending retry, and restore the full budget.
  function resetRetryState() {
    playRequestedAfterRetry = false;
    clearAudioRetryTimer();
    audioRetryCount = 0;
  }

  // Reload the active source after delayMs. A media element cannot read the HTTP status.
  function scheduleLoadRetry(delayMs: number) {
    if (audioRetryTimer !== undefined) clearTimeout(audioRetryTimer);
    audioRetryTimer = setTimeout(() => {
      clearAudioRetryTimer();
      if (audioElement) {
        // Retry against whichever source is currently active; reverting
        // to the original audioUrl here would silently drop out of an
        // active audible-bats or processed-audio derived clip while
        // leaving its state flagged as still active.
        replaceSource(currentSource());
        audioElement.load();
      }
    }, delayMs);
  }

  // Gives up without retrying: the clip is gone, so the error shows now.
  function failLoadPermanently() {
    clearAudioRetryTimer();
    error = t('media.audio.error');
    isLoading = false;
    playRequestedAfterRetry = false;
    markPlaybackStopped();
  }

  // On the first failure of a load, ask the server what the failure was. The retry is
  // already scheduled, so a 404 only has to cancel it and a 503 to move it later;
  // any other answer leaves the scheduled blind retry in place. Only the original clip is
  // probed: a processed or audible-bats source is a local blob URL.
  function probeFailedLoad() {
    if (currentSource() !== audioUrl) return;
    const controller = new AbortController();
    audioProbeController = controller;
    const probedUrl = audioUrl;
    void probeAudioSource(probedUrl, controller.signal).then(outcome => {
      // An answer for a clip that is no longer the current source says nothing about it
      if (controller.signal.aborted || probedUrl !== audioUrl) return;
      audioProbeController = undefined;
      if (outcome.kind === 'missing') {
        debugLog('Audio probe: clip is gone, not retrying');
        failLoadPermanently();
      } else if (outcome.kind === 'pending') {
        scheduleLoadRetry(pendingRetryDelayMs(outcome, AUDIO_RETRY_DELAY_MS));
      }
    });
  }

  // Point the element at a new source. The assignment pauses the element without
  // a 'pause' event, so the playing flag is cleared first.
  function replaceSource(url: string) {
    if (!audioElement) return;
    markPlaybackStopped();
    audioElement.src = url;
  }

  // Reload a failed clip at once, cancelling any pending retry and restoring the
  // full retry budget. Runs inside the click so the gesture reaches play().
  function reloadAfterFailure() {
    if (!audioElement) return;
    resetRetryState();
    replaceSource(currentSource());
    audioElement.load();
  }

  // Audio event handlers
  const handlePlayPause = async () => {
    if (!audioElement) return;

    if (!audioElement.paused && !audioElement.error) {
      audioElement.pause();
      return;
    }

    error = null;
    // A failed element cannot play (retries exhausted, or a retry timer pending):
    // reload it now instead of swallowing the click.
    if (audioElement.error) reloadAfterFailure();

    // Initialize the audio context on first play, and re-enter whenever the
    // graph is not attached OR the context has fallen back to suspended
    // (e.g. tab backgrounding / audio-session interruption) so
    // getAudioContext() resumes it. Without the state re-check, an
    // already-attached graph on a re-suspended context plays silently.
    // Guard against rapid clicks that could create multiple AudioContexts.
    if ((!audioNodes || audioContext?.state !== 'running') && !isInitializingContext) {
      isInitializingContext = true;
      try {
        audioContext = await initializeAudioContext();
        // Attach the Web Audio graph only when the context is running;
        // while suspended the element plays through native output and the
        // graph is deferred to a later play (see attachAudioGraphWhenRunning).
        audioNodes = attachAudioGraphWhenRunning(audioContext, audioElement, audioNodes, {
          gainDb: gainValue,
          highPassFreq: filterFreq,
          includeCompressor: true,
        });
        // Reflect whether the EQ/gain graph is actually live: attached AND
        // the context running. A previously-attached graph on a re-suspended
        // context that failed to resume is inert, so the audio settings
        // control must not be enabled in that case.
        audioContextAvailable = audioNodes !== null && audioContext?.state === 'running';
      } catch (err) {
        // A failed graph build must not block playback: the element still
        // plays through its native output.
        logger.warn('Audio graph setup failed, playing without processing', err);
        audioContextAvailable = false;
      } finally {
        isInitializingContext = false;
      }
    }

    // Safety check: component may have unmounted during async initialization
    if (!audioElement) return;

    try {
      await audioElement.play();
    } catch (err) {
      handlePlayRejection(err);
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

  const handlePause = () => {
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
    // Restore the proportional playback position after an audible-bats source
    // swap (the derived clip has a different duration than the original).
    if (audibleBatsPendingFraction !== null && isFinite(duration) && duration > 0) {
      audioElement.currentTime = audibleBatsPendingFraction * duration;
      currentTime = audioElement.currentTime;
      progress = (currentTime / duration) * 100;
    }
    audibleBatsPendingFraction = null;

    if (audibleBatsPendingAutoplay) {
      audibleBatsPendingAutoplay = false;
      audioElement.play().catch(handlePlayRejection);
    }
    consumePendingPlay();
  };

  const handleProgressClick = (event: MouseEvent) => {
    if (!audioElement || !isFinite(duration) || duration <= 0) return;

    // Use the toolbar's own progress bar if the event target is inside one,
    // otherwise fall back to playerContainer (always rendered).
    const targetEl = (event.target as HTMLElement)?.closest('.progress-bar') as HTMLElement | null;
    const refEl = targetEl ?? progressBar ?? playerContainer;
    if (!refEl) return;

    const rect = refEl.getBoundingClientRect();
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

  // --- Audible bats derived playback ---

  // Swap the player's audio source, preserving the proportional playback
  // position and play/pause state across the change. The actual seek (and any
  // resumed playback) happens in handleLoadedMetadata once the new source
  // reports its duration, avoiding a race where play() would otherwise start
  // from position 0 before the proportional position is restored.
  function swapAudioSource(newSrc: string) {
    if (!audioElement) return;
    // Read the live element position rather than the reactive `currentTime`
    // state, which only updates on the progress interval/timeupdate and can
    // lag a recent direct seek.
    const liveCurrentTime = audioElement.currentTime;
    const frac = isFinite(duration) && duration > 0 ? liveCurrentTime / duration : 0;
    audibleBatsPendingFraction = frac;
    audibleBatsPendingAutoplay = isPlaying || playRequestedAfterRetry;
    resetRetryState();
    replaceSource(newSrc);
    audioElement.load();
  }

  // The generate/disable lifecycle lives in the useAudibleBats composable; the
  // onActivate/onDeactivate callbacks above route back into swapAudioSource so the
  // playback position is preserved across the source change.

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
  // URL of this detection's spectrogram generation status.
  const spectrogramStatusUrl = () =>
    buildAppUrl(
      `/api/v2/spectrogram/${encodeURIComponent(detectionId)}/status?size=${spectrogramSize}${spectrogramRaw ? '&raw=true' : ''}`
    );

  // Schedules the next poll without touching statusPollStartTime, so
  // MAX_POLL_DURATION bounds the whole polling run and not one request.
  const scheduleNextStatusPoll = () => {
    if (statusPollTimer) clearTimeout(statusPollTimer);
    statusPollTimer = setTimeout(pollSpectrogramStatus, SPECTROGRAM_POLL_INTERVAL);
  };

  // Ends the attempt: no more retries, polls or probes, and the unavailable state shows.
  const endAttemptUnavailable = () => {
    clearStatusPollTimer();
    clearSpectrogramRetryTimer();
    clearSpectrogramProbe();
    spectrogramStatusFailed = true;
    spectrogramLoader.setError();
  };

  // The server reported a reason retrying cannot change (the source clip is gone or
  // cannot be rendered). source_missing describes the audio, not the image: a request
  // that carried the clip rate is first tried without it, which the server answers
  // from any render it kept. Otherwise every retry, poll and probe ends and the
  // unavailable state shows.
  const endOnTerminalReason = (reason: string | undefined) => {
    debugLog('pollSpectrogramStatus: terminal reason', { reason });
    if (reason === SPECTROGRAM_REASON.sourceMissing) {
      markSpectrogramSourceMissing(detectionId);
    }
    if (
      reason === SPECTROGRAM_REASON.sourceMissing &&
      overlayRate !== undefined &&
      !processedSpectrogramUrl
    ) {
      fallBackToImageWithoutRate();
      return;
    }
    endAttemptUnavailable();
  };

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

    // Create new AbortController for this poll request, ending any request still in flight
    statusPollAbortController?.abort();
    const controller = new AbortController();
    statusPollAbortController = controller;

    const statusUrl = spectrogramStatusUrl();
    debugLog('pollSpectrogramStatus: fetching', { url: statusUrl });

    try {
      const response = await fetch(statusUrl, { signal: controller.signal });

      debugLog('pollSpectrogramStatus: response', {
        ok: response.ok,
        status: response.status,
      });

      if (response.ok) {
        const status = parseSpectrogramStatus(await response.json());
        // The poll was cancelled (unmount, new detection, image loaded) while the body was read
        if (controller.signal.aborted) return;
        spectrogramStatus = status ?? null;
        if (!status) return;

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
          spectrogramStatusFailed = false;
          debugLog('pollSpectrogramStatus: cache key updated', { cacheKey: spectrogramCacheKey });
        } else if (status.status === 'queued' || status.status === 'generating') {
          // Still processing, continue polling
          debugLog('pollSpectrogramStatus: still processing, scheduling next poll');
          scheduleNextStatusPoll();
        } else if (
          (status.status === 'failed' || status.status === 'not_started') &&
          isTerminalSpectrogramReason(status.reason)
        ) {
          endOnTerminalReason(status.reason);
        } else if (status.status === 'failed' && !isRetryableSpectrogramReason(status.reason)) {
          // Generation failed and the server does not say a later request can succeed:
          // stop polling and the image retries
          debugLog('pollSpectrogramStatus: generation failed');
          endAttemptUnavailable();
        } else {
          // not_started, an unknown status, or a failure the server marks retryable (the
          // clip may still be written, the server was busy, a render failed): end polling
          // and leave the image retries to decide the outcome.
          debugLog('pollSpectrogramStatus: nothing in progress, ending poll', {
            status: status.status,
          });
          clearStatusPollTimer();
        }
      } else if (!controller.signal.aborted) {
        // The status request itself failed. A server error, timeout or rate limit is
        // transient: try again at the poll interval, bounded by the MAX_POLL_DURATION
        // check on entry. Any other client error (unknown detection) is permanent.
        const transient =
          response.status >= HTTP_STATUS_SERVER_ERROR_MIN ||
          response.status === HTTP_STATUS_REQUEST_TIMEOUT ||
          response.status === HTTP_STATUS_TOO_MANY_REQUESTS;
        debugLog('pollSpectrogramStatus: non-OK response', { status: response.status, transient });
        if (transient) {
          scheduleNextStatusPoll();
        } else {
          clearStatusPollTimer();
        }
      }
    } catch (err) {
      // Ignore abort errors
      if (err instanceof Error && err.name === 'AbortError') {
        logger.debug('Spectrogram status poll aborted', { detectionId });
        debugLog('pollSpectrogramStatus: aborted');
        return;
      }
      // A cancelled poll must not reschedule itself: clearStatusPollTimer already reset the
      // start time, so the rescheduled chain would have no MAX_POLL_DURATION bound.
      if (controller.signal.aborted) return;
      logger.error('Failed to poll spectrogram status', { detectionId, error: err });
      debugLog('pollSpectrogramStatus: error', err);
      scheduleNextStatusPoll();
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

  // Status check of a failed spectrogram image that carries the clip rate.
  let spectrogramProbeAbortController: AbortController | undefined;

  const clearSpectrogramProbe = () => {
    spectrogramProbeAbortController?.abort();
    spectrogramProbeAbortController = undefined;
  };

  // Loads the render the server has for this detection without the rate, and
  // without an axis, instead of the exact-rate render.
  const fallBackToImageWithoutRate = () => {
    overlayFallbackDetectionId = detectionId;
    clearSpectrogramRetryTimer();
    clearStatusPollTimer();
    clearSpectrogramProbe();
  };

  // An <img> cannot read the response status, so the failed URL is requested
  // once more with fetch. A 404 means the exact-rate render does not exist
  // (sr requests are answered from the exact render only), so the player falls
  // back at once instead of spending its retries on it, unless the detection's
  // spectrogram status reports a generation that is queued, running or just
  // finished, which the status poll then waits for or reloads as before. Any other outcome (503 while a clip
  // is written, a network error, the timeout) leaves the retries to run as
  // before.
  const probeExactRateRender = async (url: string) => {
    clearSpectrogramProbe();
    const controller = new AbortController();
    spectrogramProbeAbortController = controller;
    const probedBaseUrl = spectrogramBaseUrl;
    const timeoutId = setTimeout(() => controller.abort(), SPECTROGRAM_PROBE_TIMEOUT_MS);
    const stillCurrent = () =>
      spectrogramBaseUrl === probedBaseUrl && overlayRate !== undefined && !processedSpectrogramUrl;
    try {
      const response = await fetch(url, { signal: controller.signal });
      // Only the status is needed; drop the body.
      void response.body?.cancel();
      if (response.status !== HTTP_STATUS_NOT_FOUND || !stillCurrent()) return;

      // Ask for the generation status here rather than reading the status
      // poll's state, which may not have answered yet. If it cannot be read
      // (network error, the timeout), the 404 decides on its own.
      const generation = await fetchSpectrogramStatus(spectrogramStatusUrl(), controller.signal);
      // Cancelled by an image load, a URL change or unmount: nothing to do.
      if (spectrogramProbeAbortController !== controller || !stillCurrent()) return;
      if (generation && RENDER_PENDING_OR_READY_STATUSES.has(generation.status)) return;
      debugLog('probeExactRateRender: 404, dropping the rate and the axis');
      fallBackToImageWithoutRate();
    } catch {
      // Aborted, timed out or failed: the retries carry on unchanged.
    } finally {
      clearTimeout(timeoutId);
      if (spectrogramProbeAbortController === controller) {
        spectrogramProbeAbortController = undefined;
      }
    }
  };

  // Spectrogram loading handlers
  const handleSpectrogramLoad = () => {
    debugLog('handleSpectrogramLoad: success');
    // reset() also clears a sticky error left by a failure that a late load outran
    spectrogramLoader.reset();
    spectrogramStatus = null;
    spectrogramRetryCount = 0; // Reset retry count on successful load
    spectrogramStatusFailed = false;
    clearSpectrogramRetryTimer();
    clearStatusPollTimer(); // Also clear status polling
    clearSpectrogramProbe();
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
    if (spectrogramRetryCount === 0 && !spectrogramStatusFailed) {
      debugLog('handleSpectrogramError: first error, starting status polling');
      statusPollStartTime = Date.now();
      pollSpectrogramStatus();
      // An image requested with the clip rate: ask the server why it failed.
      if (overlayRate !== undefined && !processedSpectrogramUrl && spectrogramUrl) {
        void probeExactRateRender(spectrogramUrl);
      }
    }

    // Retry on any error (likely 503 or temporary failure) up to max retries, unless the
    // status poll already ended the attempt (a request in flight when it said failed)
    if (spectrogramRetryCount < MAX_SPECTROGRAM_RETRIES && !spectrogramStatusFailed) {
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

    // The image requested with the clip rate still fails after the retries and
    // the status check on the first failure did not fall back (it timed out,
    // the failure was not a missing render, or a 404 came with a pending or
    // finished generation). Any persistent failure lands
    // here unless the status poll reports a queued or running generation
    // (handled above): load whatever render the server has, without the rate
    // and without an axis, before giving up.
    if (overlayRate !== undefined && !processedSpectrogramUrl) {
      debugLog('handleSpectrogramError: exact-rate render unavailable, dropping the axis');
      fallBackToImageWithoutRate();
      return;
    }

    // Really failed - stop the image retries. A status poll that is still scheduled (the
    // status endpoint kept failing) is left to run: it ends on its own at a terminal
    // status or MAX_POLL_DURATION, and an exists answer reloads the image.
    debugLog('handleSpectrogramError: giving up after max retries');
    spectrogramLoader.setError();
    clearSpectrogramRetryTimer();

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
        headers: jsonHeadersWithCsrf(),
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
        spectrogramStatusFailed = false;
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
        spectrogramStatusFailed = false;
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

  // Resolve a missing bat clip sample rate once per detection. The detection
  // endpoint reads it from the clip for older detections; list and search
  // responses never carry it for those. The spectrogram request waits for the
  // answer, at most CLIP_RATE_LOOKUP_TIMEOUT_MS, and goes out without a rate
  // (and without an axis) when none comes back. Players for the same detection
  // share one lookup (lookupClipSampleRate).
  $effect(() => {
    if (!rateResolvePending) return;

    const resolvingId = detectionId;
    const controller = new AbortController();
    let active = true;
    spectrogramLoader.setLoading(true);

    void lookupClipSampleRate(resolvingId, controller.signal).then(rate => {
      if (active) rateResolve = { detectionId: resolvingId, rate };
    });

    return () => {
      active = false;
      controller.abort();
    };
  });

  // Track previous base URL to detect when detection changes (not just cache key updates)
  let previousSpectrogramBaseUrl = $state<string | null>(null);

  // Handle spectrogram base URL changes (different detection) with proper loading state
  $effect(() => {
    // Only reset loading state if base URL actually changed (new detection)
    if (spectrogramBaseUrl && spectrogramBaseUrl !== previousSpectrogramBaseUrl) {
      debugLog('spectrogramBaseUrl changed', {
        from: previousSpectrogramBaseUrl,
        to: spectrogramBaseUrl,
      });

      previousSpectrogramBaseUrl = spectrogramBaseUrl;

      // Mark as loading so the img element stays hidden until the image is ready
      spectrogramLoader.setLoading(true);

      // Reset retry/cache state for new spectrogram
      spectrogramRetryCount = 0;
      spectrogramStatusFailed = false;
      spectrogramCacheKey = 0;
      spectrogramStatus = null;
      clearSpectrogramRetryTimer();
      // Abort any in-flight status polling and status check when URL changes
      clearStatusPollTimer();
      clearSpectrogramProbe();
      // Revoke stale processed spectrogram from previous detection
      if (processedSpectrogramUrl) {
        URL.revokeObjectURL(processedSpectrogramUrl);
        processedSpectrogramUrl = null;
      }
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
        // Cancel in-flight processing and clean up state from previous audio
        if (processDebounceTimer) {
          clearTimeout(processDebounceTimer);
          processDebounceTimer = null;
        }
        if (processAbortController) {
          processAbortController.abort();
          processAbortController = null;
        }
        if (processedAudioUrl) {
          URL.revokeObjectURL(processedAudioUrl);
          processedAudioUrl = null;
        }
        processingDenoise = '';
        processingNormalize = false;
        isProcessing = false;
        // Reset audible-bats derived playback for the new detection (the source
        // is reset to audioUrl above; this just clears the derived-copy state).
        audibleBats.reset();
        audibleBatsPendingFraction = null;
        // Clear any pending source-swap autoplay so a swap that was in flight for
        // the previous detection can't auto-start playback on the new one.
        audibleBatsPendingAutoplay = false;
        // Reset playback state for new audio
        isPlaying = false;
        currentTime = 0;
        progress = 0;
        duration = 0;
        error = null;
        audioRetryCount = 0;
        clearAudioRetryTimer();
        // Reset clip selection for new audio
        clearSelection();
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
        error = null;
        startInterval();
        // Clear any pending delay timeout and immediately signal play start
        clearPlayEndTimeout();
        // Apply playback rate when starting
        applyPlaybackRate(audioElement, playbackSpeed);
        if (onPlayStart) {
          onPlayStart();
        }
      });

      addTrackedEventListener(audioElement, 'pause', handlePause);

      addTrackedEventListener(audioElement, 'ended', () => {
        // When playing a selection, scheduleSelectionEnd handles looping, so ignore ended event
        if (isPlayingSelection) return;

        if (loopEnabled && audioElement) {
          // Loop full audio: restart from beginning
          audioElement.currentTime = 0;
          audioElement.play().catch((err: unknown) => {
            logger.warn('Loop playback failed', err as Error);
          });
          return;
        }
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
        clearAudioRetryTimer();
        consumePendingPlay();
      });

      addTrackedEventListener(audioElement, 'error', () => {
        // Clear timeout on error
        if (canplayTimeoutId) {
          clearTimeout(canplayTimeoutId);
          canplayTimeoutId = undefined;
        }

        // The original clip, already reported gone for good, is not retried or probed. A
        // processed or audible-bats source is a local blob and keeps its retries.
        if (currentSource() === audioUrl && isSpectrogramSourceKnownMissing(detectionId)) {
          failLoadPermanently();
          return;
        }

        // Retry loading if under the retry limit; the audio file may still
        // be encoding (backend returns 503 which the browser treats as a load error)
        if (audioRetryCount < MAX_AUDIO_LOAD_RETRIES) {
          const isFirstFailure = audioRetryCount === 0;
          clearAudioRetryTimer();
          audioRetryCount++;
          debugLog(`Audio load failed, retrying (${audioRetryCount}/${MAX_AUDIO_LOAD_RETRIES})`);
          scheduleLoadRetry(AUDIO_RETRY_DELAY_MS);
          if (isFirstFailure) probeFailedLoad();
          return;
        }

        error = t('media.audio.error');
        isLoading = false;
        playRequestedAfterRetry = false;
        markPlaybackStopped();
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
          if (selectionStartSec !== null || selectionEndSec !== null) {
            clearSelection();
          }
        }
      }) as EventListener);

      // Prevent native browser selection during drag operations
      addTrackedEventListener(document as unknown as HTMLElement, 'selectstart', ((e: Event) => {
        if (isDragSelecting || draggingHandle || isScrubbing) {
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
      clearSpectrogramProbe();
      spectrogramLoader.cleanup();
      clearAudioRetryTimer();

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
        element?.removeEventListener(event, handler);
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

  // Cleanup processing resources on component destroy
  $effect(() => {
    return () => {
      if (processedAudioUrl) {
        URL.revokeObjectURL(processedAudioUrl);
      }
      if (processedSpectrogramUrl) {
        URL.revokeObjectURL(processedSpectrogramUrl);
      }
      if (processDebounceTimer) {
        clearTimeout(processDebounceTimer);
      }
      if (processAbortController) {
        processAbortController.abort();
      }
      audibleBats.cleanup();
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
  class:spectrogram-skeleton={responsive &&
    showSpectrogram &&
    ((spectrogramUrl !== null && (spectrogramLoader.loading || spectrogramLoader.error)) ||
      rateResolvePending)}
  style={responsive
    ? showSpectrogram
      ? 'aspect-ratio: 2 / 1;'
      : ''
    : `width: ${typeof width === 'number' ? width + 'px' : width}; height: ${typeof height === 'number' ? height + 'px' : height};`}
  onmousedown={enableClipExtraction ? handleSelectionMouseDown : undefined}
  ontouchstart={enableClipExtraction ? handleSelectionTouchStart : undefined}
  ontouchmove={enableClipExtraction ? handleSelectionTouchMove : undefined}
  ontouchend={enableClipExtraction ? handleSelectionTouchEnd : undefined}
>
  <!-- Screen reader announcement for the spectrogram state (the rate resolve counts
       as loading). Always rendered, so screen readers hear each change of its text. -->
  <div class="sr-only" role="status">
    {#if spectrogramUrl || rateResolvePending}
      {spectrogramLoader.loading || rateResolvePending
        ? t('components.audio.spectrogramLoading')
        : t('components.audio.spectrogramLoaded')}
    {/if}
  </div>

  {#if spectrogramUrl || rateResolvePending}
    <!-- Loading spinner overlay (hidden from screen readers: the status region above
         already announces the loading state) -->
    {#if spectrogramLoader.showSpinner}
      <div
        class="absolute inset-0 flex items-center justify-center bg-[var(--color-base-200)]/75 rounded-md border border-[var(--color-base-300)]"
      >
        <div
          class="loading loading-spinner loading-sm md:loading-md text-[var(--color-primary)]"
          aria-hidden="true"
        ></div>
      </div>
    {/if}
  {/if}

  {#if spectrogramUrl}
    <!-- Always render the img so the browser cache works across state transitions -->
    <img
      bind:this={spectrogramImage}
      id={`spectrogram-${detectionId}`}
      src={spectrogramUrl}
      alt={t('components.audio.spectrogramAlt')}
      decoding="async"
      class={responsive
        ? enableClipExtraction
          ? 'w-full h-auto'
          : 'w-full h-auto object-contain rounded-md border border-[var(--color-base-300)]'
        : 'w-full h-full object-fill rounded-md border border-[var(--color-base-300)]'}
      class:invisible={spectrogramLoader.loading || spectrogramLoader.error}
      class:select-none={enableClipExtraction}
      draggable={enableClipExtraction ? 'false' : undefined}
      style={responsive
        ? ''
        : `width: ${typeof width === 'number' ? width + 'px' : width}; height: ${typeof height === 'number' ? height + 'px' : height};`}
      width={responsive ? 400 : undefined}
      onload={handleSpectrogramLoad}
      onerror={handleSpectrogramError}
    />

    {#if spectrogramLoader.error}
      <!-- Error overlay for failed spectrogram -->
      <div
        class="absolute inset-0 flex items-center justify-center bg-[var(--color-base-200)] rounded-md border border-[var(--color-base-300)]"
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
      <!-- Generation status overlay -->
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
    {/if}
  {/if}

  <!-- Spectrogram processing overlay -->
  {#if isSpectrogramProcessing}
    <div
      class="absolute inset-0 flex items-center justify-center bg-black/40 rounded-md pointer-events-none"
    >
      <div
        class="loading loading-spinner loading-sm text-white"
        role="status"
        aria-label={t('components.audio.spectrogramGeneratingAria')}
      ></div>
    </div>
  {/if}

  <!-- Frequency scale overlay (linear; range follows the model type and, for bats,
       the clip sample rate; a bat axis without a known rate has no ticks, and a
       bat axis is drawn only once its image has loaded) -->
  {#if showFreqAxis}
    {#each freqTicksKHz as freq (freq)}
      <span class="freq-label" style:bottom="{(freq / freqNyquistKHz) * 100}%" aria-hidden="true"
        >{freq}k</span
      >
      <div
        class="freq-line"
        style:bottom="{(freq / freqNyquistKHz) * 100}%"
        aria-hidden="true"
      ></div>
    {/each}
  {/if}

  <!-- Audio element is created dynamically in onMount for iOS Safari compatibility -->

  <!-- Audio settings button (top-right), with the audible-bats button alongside
       it for bat detections. -->
  {#if showControls}
    <div
      class="absolute top-2 right-2 flex items-center gap-1 transition-opacity duration-200"
      class:opacity-0={!isMobile}
      class:group-hover:opacity-100={!isMobile}
    >
      {#if isBatSpectrogram}
        <AudibleBatsButton
          active={audibleBats.active}
          generating={audibleBats.generating}
          error={audibleBats.error}
          disabled={!audioContextAvailable}
          closeSignal={closeAudibleBatsSignal}
          onEnable={settings => audibleBats.enable(settings)}
          onDisable={() => audibleBats.disable()}
          onMenuOpen={() => closeAudioSettingsSignal++}
        />
      {/if}
      <AudioSettingsButton
        {gainValue}
        {filterFreq}
        {playbackSpeed}
        defaultGainValue={defaultGainDb}
        onGainChange={updateGain}
        onFilterChange={updateFilter}
        onSpeedChange={handleSpeedChange}
        disabled={!audioContextAvailable}
        closeSignal={closeAudioSettingsSignal}
        onMenuOpen={() => closeAudibleBatsSignal++}
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
      class="absolute top-0 bottom-0 bg-primary/20 cursor-grab"
      class:cursor-grabbing={isScrubbing}
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
        aria-label={t('components.audioPlayer.clipExtraction.selectionStart')}
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
        aria-label={t('components.audioPlayer.clipExtraction.selectionEnd')}
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
    {/if}
  {/if}

  <!-- Processing active badge (only shown when toolbar is not used) -->
  {#if processingActive && !enableClipExtraction}
    <div class="processing-badge" role="status">
      {t('components.audioPlayer.processing.processingActive')}
    </div>
  {/if}

  <!-- Bottom overlay controls (hidden when toolbar is used) -->
  {#if !enableClipExtraction}
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
          aria-label={t(playButtonLabelKey(isLoading, isPlaying))}
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
              handleProgressClick(new MouseEvent('click', { clientX: centerX }));
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
  {/if}

  <!-- Error message -->
  {#if error}
    <div
      role="alert"
      class="pointer-events-none absolute inset-0 flex items-center justify-center bg-[color-mix(in_srgb,var(--color-error)_15%,transparent)] rounded-md"
    >
      <span class="text-[var(--color-error)] text-sm">{error}</span>
    </div>
  {/if}
</div>

{#if enableClipExtraction}
  <AudioToolbar
    {isPlaying}
    isLoading={isLoading || isProcessing}
    {currentTime}
    {duration}
    {progress}
    loop={loopEnabled}
    onPlayPause={handlePlayPause}
    onSeek={handleProgressClick}
    onLoopToggle={() => (loopEnabled = !loopEnabled)}
    selectionStart={selectionStartSec !== null && selectionEndSec !== null
      ? Math.min(selectionStartSec, selectionEndSec)
      : null}
    selectionEnd={selectionStartSec !== null && selectionEndSec !== null
      ? Math.max(selectionStartSec, selectionEndSec)
      : null}
    hasSelection={selectionStartSec !== null && selectionEndSec !== null}
    gainDb={gainValue}
    denoise={processingDenoise}
    normalize={processingNormalize}
    {isProcessing}
    {isPlayingSelection}
    {isExtracting}
    {extractionError}
    onPlaySelection={playSelection}
    onStopSelection={stopSelection}
    onSkipToSelection={reviewSelection}
    onClearSelection={clearSelection}
    onGainChange={updateGain}
    onDenoiseChange={handleDenoiseChange}
    onNormalizeToggle={handleNormalizeToggle}
    onExport={handleToolbarExport}
  />
{/if}

<style>
  .processing-badge {
    position: absolute;
    top: 0.5rem;
    right: 0.5rem;
    padding: 0.25rem 0.5rem;
    background: var(--color-primary);
    color: var(--color-primary-content, #fff);
    border-radius: var(--radius-selector);
    font-size: 0.6875rem;
    font-weight: 500;
    z-index: 5;
    opacity: 0.85;
  }

  .freq-label {
    position: absolute;
    left: 4px;
    transform: translateY(50%);
    font-size: 0.6875rem;
    font-weight: 600;
    color: rgb(255 255 255 / 0.75);
    background: none;
    text-shadow:
      0 0 3px rgb(0 0 0 / 1),
      0 0 6px rgb(0 0 0 / 0.8),
      1px 1px 2px rgb(0 0 0 / 0.9);
    line-height: 1;
    pointer-events: none;
    z-index: 3;
  }

  .freq-line {
    position: absolute;
    left: 0;
    right: 0;
    height: 1px;
    background: rgb(255 255 255 / 0);
    pointer-events: none;
    z-index: 3;
    transition: background 0.2s ease;
  }

  :global(.group:hover) .freq-line {
    background: rgb(255 255 255 / 0.12);
  }

  .spectrogram-skeleton {
    background: var(--color-base-200);
    border-radius: var(--radius-box);
    border: 1px solid var(--color-base-300);
    animation: skeleton-pulse 1.5s ease-in-out infinite;
  }

  @keyframes skeleton-pulse {
    0%,
    100% {
      opacity: 1;
    }

    50% {
      opacity: 0.5;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .spectrogram-skeleton {
      animation: none;
    }
  }
</style>
