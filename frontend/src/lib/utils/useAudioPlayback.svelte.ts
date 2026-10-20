// frontend/src/lib/utils/useAudioPlayback.svelte.ts

/**
 * useAudioPlayback - Reusable audio playback engine composable
 *
 * Extracted from AudioPlayer.svelte to enable both full-featured and compact
 * audio player components to share the same battle-tested audio engine.
 *
 * Features:
 * - Dynamic Audio element creation (iOS Safari workaround)
 * - Web Audio API chain (gain, high-pass filter) via audioContextManager/audioNodes
 * - iOS canplay timeout fallback
 * - Audio retry on 503 (still encoding)
 * - Progress tracking with configurable interval
 * - SSE freeze/resume callbacks with configurable delay
 * - Playback speed with pitch preservation control
 *
 * Usage:
 *   const audio = useAudioPlayback({
 *     audioUrl: '/api/v2/audio/123',
 *     detectionId: '123',
 *   });
 *
 *   // In template: audio.isPlaying, audio.progress, audio.togglePlayPause()
 *   // On destroy: cleanup is automatic via onMount return
 */

import { onMount, untrack } from 'svelte';
import {
  applyPlaybackRate,
  dbToGain,
  PLAY_END_DELAY_MS,
  CANPLAY_TIMEOUT_MS,
  PROGRESS_UPDATE_INTERVAL_MS,
  MAX_AUDIO_LOAD_RETRIES,
  AUDIO_RETRY_DELAY_MS,
  isPlayInterruptedError,
  pendingRetryDelayMs,
  probeAudioSource,
} from '$lib/utils/audio';
import { isSpectrogramSourceKnownMissing } from '$lib/utils/spectrogramStatus';
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
import { t } from '$lib/i18n';
import { loggers } from '$lib/utils/logger';

const logger = loggers.audio;

/** Configuration for the audio playback composable */
export interface AudioPlaybackOptions {
  /** Initial audio source URL */
  audioUrl: string;
  /** Unique detection ID for logging, element IDs and the known-missing check; read once, so it must not change for one instance */
  detectionId: string;
  /** Default gain in dB (default: 0) */
  defaultGainDb?: number;
  /** Callback when playback starts - used to freeze SSE detection updates */
  onPlayStart?: () => void;
  /** Callback after playback stops (with PLAY_END_DELAY_MS delay) - resumes SSE */
  onPlayEnd?: () => void;
  /** Enable debug console logging */
  debug?: boolean;
}

/** Return type of useAudioPlayback composable */
export interface AudioPlaybackState {
  /** Whether audio is currently playing */
  readonly isPlaying: boolean;
  /** Current playback position in seconds */
  readonly currentTime: number;
  /** Total audio duration in seconds */
  readonly duration: number;
  /** Playback progress as percentage (0-100) */
  readonly progress: number;
  /** Whether audio is loading/buffering */
  readonly isLoading: boolean;
  /** Error message if playback failed, null otherwise */
  readonly error: string | null;
  /** Current gain value in dB */
  readonly gainValue: number;
  /** Current high-pass filter frequency in Hz */
  readonly filterFreq: number;
  /** Current playback speed multiplier */
  readonly playbackSpeed: number;
  /** Whether Web Audio API (gain/filter) is available */
  readonly audioContextAvailable: boolean;
  /** The underlying HTMLAudioElement (available after mount) */
  readonly audioElement: HTMLAudioElement | null;

  /** Toggle play/pause. Initializes AudioContext on first interaction. */
  togglePlayPause: () => Promise<void>;
  /** Seek to a specific time in seconds */
  seek: (timeSec: number) => void;
  /** Update gain in dB. Clamps to [0, MAX_GAIN_DB]. */
  updateGain: (db: number) => void;
  /** Update high-pass filter frequency in Hz */
  updateFilter: (freq: number) => void;
  /** Change playback speed */
  setPlaybackSpeed: (speed: number) => void;
  /** Update the audio source URL (e.g. when detection changes) */
  setAudioUrl: (url: string) => void;
}

export function useAudioPlayback(options: AudioPlaybackOptions): AudioPlaybackState {
  const { detectionId, defaultGainDb = 0, onPlayStart, onPlayEnd, debug = false } = options;

  // Debug logging helper
  const debugLog = (message: string, data?: unknown) => {
    if (debug) {
      // eslint-disable-next-line no-console
      console.log(`[useAudioPlayback:${detectionId}] ${message}`, data ?? '');
    }
  };

  // --- Reactive state ---
  let audioUrl = $state(options.audioUrl);
  let audioElement = $state<HTMLAudioElement | null>(null);
  let isPlaying = $state(false);
  let currentTime = $state(0);
  let duration = $state(0);
  let progress = $state(0);
  let isLoading = $state(false);
  let error = $state<string | null>(null);
  let audioContextAvailable = $state(true);
  let gainValue = $state(defaultGainDb);
  let filterFreq = $state(20);
  let playbackSpeed = $state(1.0);

  // --- Non-reactive internal state ---
  let audioContext: AudioContext | null = null;
  let isInitializingContext = false;
  let audioNodes: AudioNodeChain | null = null;
  let updateInterval: ReturnType<typeof setInterval> | undefined;
  let playEndTimeout: ReturnType<typeof setTimeout> | undefined;
  let canplayTimeoutId: ReturnType<typeof setTimeout> | undefined;
  let audioRetryCount = 0;
  let audioRetryTimer: ReturnType<typeof setTimeout> | undefined;
  // Aborts the one byte probe of the audio URL that runs beside the first retry timer.
  let audioProbeController: AbortController | undefined;
  // True when the user pressed play during an active load retry; consumed by the
  // 'canplay' handler to resume playback once the reloaded clip is ready, so the
  // play intent is not lost when the transient error is suppressed.
  let playRequestedAfterRetry = false;
  let eventListeners: Array<{
    element: HTMLElement | HTMLAudioElement | Document | Window | null;
    event: string;
    handler: EventListener;
  }> = [];

  // --- Constants ---
  const GAIN_MAX_DB = 24;
  const FILTER_HP_MIN_FREQ = 20;
  const FILTER_HP_MAX_FREQ = 10000;

  // --- Helper functions ---

  function addTrackedEventListener(
    element: HTMLElement | HTMLAudioElement | Document | Window,
    event: string,
    handler: EventListener
  ) {
    element.addEventListener(event, handler);
    eventListeners.push({ element, event, handler });
  }

  function startInterval() {
    if (updateInterval) return;
    updateInterval = setInterval(() => {
      if (audioElement && !audioElement.paused) {
        currentTime = audioElement.currentTime;
        if (duration > 0) {
          progress = (currentTime / duration) * 100;
        }
      }
    }, PROGRESS_UPDATE_INTERVAL_MS);
  }

  function stopInterval() {
    if (updateInterval) {
      clearInterval(updateInterval);
      updateInterval = undefined;
    }
  }

  function handleTimeUpdate() {
    if (audioElement) {
      currentTime = audioElement.currentTime;
      if (duration > 0) {
        progress = (currentTime / duration) * 100;
      }
    }
  }

  function handleLoadedMetadata() {
    if (audioElement) {
      duration = audioElement.duration;
    }
    // Metadata is enough to play: do not wait for 'canplay', which may not fire
    // under preload=metadata.
    isLoading = false;
    if (canplayTimeoutId) {
      clearTimeout(canplayTimeoutId);
      canplayTimeoutId = undefined;
    }
    consumePendingPlay();
  }

  // Stop bookkeeping shared by the pause listener and by deliberate resets.
  function handlePause() {
    isPlaying = false;
    stopInterval();
    clearPlayEndTimeout();
    playEndTimeout = setTimeout(() => {
      onPlayEnd?.();
    }, PLAY_END_DELAY_MS);
  }

  // A src assignment or load() pauses the element without a 'pause' event, so
  // mirror the pause listener when the flag still says playing.
  function markPlaybackStopped() {
    if (isPlaying) handlePause();
  }

  // Also ends the probe: it only matters while the retry it may cancel is pending.
  function clearRetryTimer() {
    audioProbeController?.abort();
    audioProbeController = undefined;
    if (audioRetryTimer !== undefined) {
      clearTimeout(audioRetryTimer);
      audioRetryTimer = undefined;
    }
  }

  // Retry the load after delayMs. A media element cannot read the HTTP status.
  function scheduleLoadRetry(delayMs: number) {
    if (audioRetryTimer !== undefined) clearTimeout(audioRetryTimer);
    audioRetryTimer = setTimeout(() => {
      clearRetryTimer();
      if (audioElement) {
        markPlaybackStopped();
        audioElement.src = audioUrl;
        audioElement.load();
      }
    }, delayMs);
  }

  // Gives up without retrying: the clip is gone, so the error shows now.
  function failLoadPermanently() {
    clearRetryTimer();
    error = t('media.audio.error');
    isLoading = false;
    markPlaybackStopped();
    // Drop any pending play intent so a late 'canplay' can't resume a failed clip.
    playRequestedAfterRetry = false;
  }

  // On the first failure of a load, ask the server what the failure was. The retry is
  // already scheduled, so a 404 only has to cancel it and a 503 to move it later;
  // any other answer leaves the scheduled blind retry in place.
  function probeFailedLoad() {
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
      logger.debug('Playback interrupted', { detectionId });
      return;
    }
    // A reload's loadstart set isLoading and the button is disabled while it is
    // set, so an error must never sit behind a disabled spinner button.
    isLoading = false;
    if (audioElement?.error) {
      error ??= t('media.audio.error');
      logger.warn('Playback failed after a load error', { detectionId });
      return;
    }
    logger.error('Playback failed', err);
    error = t('media.audio.playError');
  }

  // Reload a failed clip at once, cancelling any pending retry and restoring the
  // full retry budget. Runs inside the click so the gesture reaches play().
  function reloadAfterFailure() {
    if (!audioElement) return;
    clearRetryTimer();
    audioRetryCount = 0;
    playRequestedAfterRetry = false;
    markPlaybackStopped();
    audioElement.src = audioUrl;
    audioElement.load();
  }

  function clearPlayEndTimeout() {
    if (playEndTimeout) {
      clearTimeout(playEndTimeout);
      playEndTimeout = undefined;
    }
  }

  async function initAudioContext(): Promise<boolean> {
    // Already fully initialized: context running and the graph attached.
    if (audioContext?.state === 'running' && audioNodes) return true;
    if (isInitializingContext) return false;
    if (!isAudioContextSupported()) {
      audioContextAvailable = false;
      return false;
    }

    isInitializingContext = true;
    try {
      // getAudioContext() is async - returns Promise<AudioContext>. It
      // creates/resumes the shared singleton; called here from a user gesture
      // (togglePlayPause) so iOS honours the resume.
      audioContext = await getAudioContext();

      // Attach the Web Audio graph only when the context is running; while it
      // is suspended the element plays through native output and the graph is
      // deferred to a later play (see attachAudioGraphWhenRunning). Note:
      // intentionally omits includeCompressor (AudioPlayer uses it for clipping
      // protection at high gain); compact players don't expose gain controls,
      // so the compressor is unnecessary overhead.
      audioNodes = attachAudioGraphWhenRunning(audioContext, audioElement, audioNodes, {
        gainDb: gainValue,
        highPassFreq: filterFreq,
      });
      // Reflect whether the processing graph is actually live: the graph is
      // attached AND the context is running. A previously-attached graph on a
      // re-suspended context that failed to resume is inert, so gain/filter
      // controls must not be reported available in that case.
      audioContextAvailable = audioNodes !== null && audioContext.state === 'running';
      return true;
    } catch (err) {
      logger.warn('AudioContext initialization failed', err);
      audioContextAvailable = false;
      return false;
    } finally {
      isInitializingContext = false;
    }
  }

  function updateGainInternal(db: number) {
    gainValue = Math.max(0, Math.min(GAIN_MAX_DB, db));
    if (audioNodes?.gain) {
      audioNodes.gain.gain.value = dbToGain(gainValue);
    }
  }

  function updateFilterInternal(freq: number) {
    filterFreq = Math.max(FILTER_HP_MIN_FREQ, Math.min(FILTER_HP_MAX_FREQ, freq));
    if (audioNodes?.highPass) {
      audioNodes.highPass.frequency.value = filterFreq;
    }
  }

  function setPlaybackSpeedInternal(speed: number) {
    playbackSpeed = speed;
    if (audioElement) {
      applyPlaybackRate(audioElement, speed);
    }
  }

  async function togglePlayPause(): Promise<void> {
    if (!audioElement) return;

    if (!audioElement.paused && !audioElement.error) {
      audioElement.pause();
      return;
    }

    error = null;
    // A failed element cannot play (retries exhausted, or a retry timer pending):
    // reload it now instead of swallowing the click.
    if (audioElement.error) reloadAfterFailure();
    await initAudioContext();
    try {
      await audioElement.play();
    } catch (err) {
      handlePlayRejection(err);
    }
  }

  function seek(timeSec: number) {
    if (!audioElement || !isFinite(duration) || duration <= 0) return;
    const clamped = Math.max(0, Math.min(timeSec, duration));
    audioElement.currentTime = clamped;
    currentTime = clamped;
    progress = (clamped / duration) * 100;
  }

  function setAudioUrl(url: string) {
    if (url === audioUrl) return;
    audioUrl = url;
  }

  // --- Audio URL change effect ---
  $effect(() => {
    if (audioElement && audioUrl) {
      const absoluteUrl = new URL(audioUrl, window.location.origin).href;
      if (audioElement.src !== absoluteUrl) {
        debugLog('audioUrl changed, updating src', { from: audioElement.src, to: audioUrl });
        untrack(() => markPlaybackStopped());
        audioElement.src = audioUrl;
        // Reset playback state
        isPlaying = false;
        currentTime = 0;
        progress = 0;
        duration = 0;
        error = null;
        audioRetryCount = 0;
        playRequestedAfterRetry = false;
        clearRetryTimer();
        if (canplayTimeoutId) {
          clearTimeout(canplayTimeoutId);
          canplayTimeoutId = undefined;
        }
      }
    }
  });

  // --- Lifecycle ---
  onMount(() => {
    debugLog('onMount: creating audio element');

    // Create audio element dynamically (iOS Safari workaround:
    // DOM-bound audio elements don't fire canplay events reliably)
    const audio = new globalThis.Audio();
    audio.preload = 'metadata';
    audio.src = audioUrl;
    audioElement = audio;

    // --- Event listeners ---
    addTrackedEventListener(audio, 'play', () => {
      isPlaying = true;
      error = null;
      startInterval();
      clearPlayEndTimeout();
      applyPlaybackRate(audio, playbackSpeed);
      onPlayStart?.();
    });

    addTrackedEventListener(audio, 'pause', handlePause);

    addTrackedEventListener(audio, 'ended', () => {
      isPlaying = false;
      stopInterval();
      clearPlayEndTimeout();
      playEndTimeout = setTimeout(() => {
        onPlayEnd?.();
      }, PLAY_END_DELAY_MS);
    });

    addTrackedEventListener(audio, 'timeupdate', handleTimeUpdate);
    addTrackedEventListener(audio, 'loadedmetadata', handleLoadedMetadata);

    // iOS Safari canplay timeout fallback
    addTrackedEventListener(audio, 'loadstart', () => {
      isLoading = true;
      if (canplayTimeoutId) clearTimeout(canplayTimeoutId);
      canplayTimeoutId = setTimeout(() => {
        if (isLoading) {
          logger.warn('canplay timeout - assuming audio ready', { detectionId });
          isLoading = false;
        }
      }, CANPLAY_TIMEOUT_MS);
    });

    addTrackedEventListener(audio, 'canplay', () => {
      if (canplayTimeoutId) {
        clearTimeout(canplayTimeoutId);
        canplayTimeoutId = undefined;
      }
      isLoading = false;
      error = null;
      audioRetryCount = 0;
      clearRetryTimer();
      // If the user pressed play while the clip was still loading/encoding and we
      // suppressed the transient error, resume playback now that it can play.
      consumePendingPlay();
    });

    addTrackedEventListener(audio, 'error', () => {
      if (canplayTimeoutId) {
        clearTimeout(canplayTimeoutId);
        canplayTimeoutId = undefined;
      }

      // Retry on load failure (audio still encoding by FFmpeg, served as 503/404).
      // A media element reports such HTTP errors as MediaError.code 4
      // (MEDIA_ERR_SRC_NOT_SUPPORTED), indistinguishable from a genuinely
      // unsupported format, so the error code alone cannot end the retries without
      // breaking the still-encoding path. probeAudioSource asks the server on the
      // first failure instead. Retrying is bounded by MAX_AUDIO_LOAD_RETRIES.

      // A clip the server already reported gone for good is not retried or probed.
      if (isSpectrogramSourceKnownMissing(detectionId)) {
        failLoadPermanently();
        return;
      }
      if (audioRetryCount < MAX_AUDIO_LOAD_RETRIES) {
        const isFirstFailure = audioRetryCount === 0;
        clearRetryTimer();
        audioRetryCount++;
        debugLog(`Audio load failed, retrying (${audioRetryCount}/${MAX_AUDIO_LOAD_RETRIES})`);
        scheduleLoadRetry(AUDIO_RETRY_DELAY_MS);
        if (isFirstFailure) probeFailedLoad();
        return;
      }

      error = t('media.audio.error');
      isLoading = false;
      markPlaybackStopped();
      // Retries are exhausted; drop any pending play intent so a late 'canplay'
      // can't resume a clip that has permanently failed.
      playRequestedAfterRetry = false;
    });

    if (!audio.paused) {
      startInterval();
    }

    // Cleanup
    return () => {
      debugLog('cleanup: destroying');
      const shouldNotifyPlayEnd = isPlaying || playEndTimeout !== undefined;
      stopInterval();
      clearPlayEndTimeout();
      if (canplayTimeoutId) {
        clearTimeout(canplayTimeoutId);
        canplayTimeoutId = undefined;
      }
      clearRetryTimer();

      // Remove all tracked event listeners
      eventListeners.forEach(({ element, event, handler }) => {
        element?.removeEventListener(event, handler);
      });
      eventListeners = [];

      // Notify play end if playback was active
      if (shouldNotifyPlayEnd) {
        onPlayEnd?.();
      }

      // Stop audio
      if (audioElement) {
        audioElement.pause();
        audioElement.src = '';
      }

      // Clean up Web Audio API
      disconnectAudioNodes(audioNodes);
      audioNodes = null;
      releaseAudioContext();
      audioContext = null;
    };
  });

  return {
    get isPlaying() {
      return isPlaying;
    },
    get currentTime() {
      return currentTime;
    },
    get duration() {
      return duration;
    },
    get progress() {
      return progress;
    },
    get isLoading() {
      return isLoading;
    },
    get error() {
      return error;
    },
    get gainValue() {
      return gainValue;
    },
    get filterFreq() {
      return filterFreq;
    },
    get playbackSpeed() {
      return playbackSpeed;
    },
    get audioContextAvailable() {
      return audioContextAvailable;
    },
    get audioElement() {
      return audioElement;
    },

    togglePlayPause,
    seek,
    updateGain: updateGainInternal,
    updateFilter: updateFilterInternal,
    setPlaybackSpeed: setPlaybackSpeedInternal,
    setAudioUrl,
  };
}
