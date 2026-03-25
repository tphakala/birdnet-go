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

import { onMount } from 'svelte';
import {
  applyPlaybackRate,
  dbToGain,
  PLAY_END_DELAY_MS,
  CANPLAY_TIMEOUT_MS,
  PROGRESS_UPDATE_INTERVAL_MS,
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
import { t } from '$lib/i18n';
import { loggers } from '$lib/utils/logger';

const logger = loggers.audio;

/** Configuration for the audio playback composable */
export interface AudioPlaybackOptions {
  /** Initial audio source URL */
  audioUrl: string;
  /** Unique detection ID for logging and element IDs */
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
  }

  function clearPlayEndTimeout() {
    if (playEndTimeout) {
      clearTimeout(playEndTimeout);
      playEndTimeout = undefined;
    }
  }

  async function initAudioContext(): Promise<boolean> {
    if (audioContext) return true;
    if (isInitializingContext) return false;
    if (!isAudioContextSupported()) {
      audioContextAvailable = false;
      return false;
    }

    isInitializingContext = true;
    try {
      // getAudioContext() is async - returns Promise<AudioContext>
      // It creates/resumes the shared singleton and handles suspended state
      audioContext = await getAudioContext();
      audioContextAvailable = true;

      if (audioElement) {
        // Note: intentionally omits includeCompressor (AudioPlayer uses it for
        // clipping protection at high gain). Compact players don't expose gain
        // controls, so the compressor is unnecessary overhead.
        audioNodes = createAudioNodeChain(audioContext, audioElement, {
          gainDb: gainValue,
          highPassFreq: filterFreq,
        });
      }
      isInitializingContext = false;
      return true;
    } catch (err) {
      logger.warn('AudioContext initialization failed', err as Error);
      audioContextAvailable = false;
      isInitializingContext = false;
      return false;
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

    if (audioElement.paused) {
      await initAudioContext();
      try {
        await audioElement.play();
      } catch (err) {
        logger.error('Playback failed', err as Error);
        error = t('media.audio.playError');
      }
    } else {
      audioElement.pause();
    }
  }

  function seek(timeSec: number) {
    if (!audioElement || duration <= 0) return;
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
        audioElement.src = audioUrl;
        // Reset playback state
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

    addTrackedEventListener(audio, 'pause', () => {
      isPlaying = false;
      stopInterval();
      clearPlayEndTimeout();
      playEndTimeout = setTimeout(() => {
        onPlayEnd?.();
      }, PLAY_END_DELAY_MS);
    });

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
      if (audioRetryTimer) {
        clearTimeout(audioRetryTimer);
        audioRetryTimer = undefined;
      }
    });

    addTrackedEventListener(audio, 'error', () => {
      if (canplayTimeoutId) {
        clearTimeout(canplayTimeoutId);
        canplayTimeoutId = undefined;
      }

      // Retry on 503 (audio still encoding by FFmpeg)
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
      if (audioRetryTimer) {
        clearTimeout(audioRetryTimer);
        audioRetryTimer = undefined;
      }

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
