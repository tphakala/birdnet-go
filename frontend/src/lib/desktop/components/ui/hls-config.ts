/**
 * HLS.js Configuration for Audio Streaming
 *
 * This configuration is optimized for low-latency audio streaming use cases,
 * specifically for real-time bird song monitoring where continuous playback
 * is more important than perfect buffering.
 *
 * @see https://github.com/video-dev/hls.js/blob/master/docs/API.md#fine-tuning
 */

import type Hls from 'hls.js';

/**
 * Audio streaming optimized HLS configuration
 *
 * Key design decisions:
 * - Prioritize continuous playback over perfect buffering
 * - Tolerate brief stalls for lower latency
 * - Optimize for live audio streams rather than video content
 */
export const HLS_AUDIO_CONFIG: Partial<Hls['config']> = {
  // Core settings
  debug: false,
  enableWorker: true,
  lowLatencyMode: false, // Disabled - we handle latency manually

  // Buffer management for live audio
  backBufferLength: 10, // Keep 10s of back buffer for seeking
  liveSyncDurationCount: 5, // Start playback 5 segments from live edge
  liveMaxLatencyDurationCount: 30, // Max 30 segments behind live

  // Buffer hole tolerance - increased for audio streams
  maxBufferHole: 1.0, // Default: 0.5s - Increased to tolerate audio gaps

  // Stall detection tuning
  /**
   * Reduced watchdog frequency to avoid false stall reports
   * Audio streams can have natural pauses that shouldn't trigger stalls
   * Default: 2s, Audio optimized: 5s
   */
  highBufferWatchdogPeriod: 5,

  // Recovery behavior
  /**
   * Allow more nudge attempts before reporting stalls
   * Audio streams benefit from more aggressive recovery attempts
   * Default: 3, Audio optimized: 5
   */
  nudgeMaxRetry: 5,

  /**
   * Larger nudge offset for audio content
   * Audio can tolerate slightly larger time jumps than video
   * Default: 0.1s, Audio optimized: 0.2s
   */
  nudgeOffset: 0.2,

  /**
   * Fragment lookup tolerance for live streams
   * More forgiving for audio where exact timing is less critical
   * Default: 0.25s, Audio optimized: 0.5s
   */
  maxFragLookUpTolerance: 0.5,

  // Buffer length settings for audio
  /**
   * Target buffer length for audio streams
   * Shorter than video to reduce latency while maintaining stability
   * Default: 60s, Audio optimized: 30s
   */
  maxBufferLength: 30,

  /**
   * Maximum allowed buffer length
   * Generous limit for long-running audio monitoring sessions
   * Default: 600s (10 minutes) - kept as reasonable maximum
   */
  maxMaxBufferLength: 600,
};

/**
 * Fragment buffering strategy constants
 */
export const BUFFERING_STRATEGY = {
  /**
   * Number of fragments to buffer before starting playback
   *
   * Why 2 fragments?
   * - 1 fragment: Too aggressive, causes buffer stalls
   * - 2 fragments: Sweet spot for audio - provides runway without excessive latency
   * - 3+ fragments: Adds unnecessary latency for live audio monitoring
   *
   * Typical fragment duration is 2-6 seconds, so 2 fragments = 4-12s initial delay
   */
  MIN_FRAGMENTS_BEFORE_PLAY: 2,

  /**
   * Fragment buffer target for stable playback
   * Maintain at least this many fragments ahead of playback position
   */
  TARGET_BUFFER_FRAGMENTS: 3,
} as const;

/**
 * Error handling configuration
 */
export const ERROR_HANDLING = {
  /**
   * Buffer stall errors are expected in low-latency audio streaming
   * HLS.js will automatically recover by buffering more segments
   */
  EXPECTED_STALL_ERRORS: [
    'BUFFER_STALLED_ERROR',
    'BUFFER_SEEK_OVER_HOLE',
    'BUFFER_NUDGE_ON_STALL',
  ] as const,

  /**
   * Recoverable media errors that should trigger automatic recovery
   */
  RECOVERABLE_MEDIA_ERRORS: [
    'BUFFER_APPEND_ERROR',
    'BUFFER_APPENDING_ERROR',
    'FRAG_PARSING_ERROR',
  ] as const,
} as const;
