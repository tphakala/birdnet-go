/**
 * Web Audio API node chain utilities.
 *
 * Provides reusable audio processing node creation for consistent
 * audio enhancement across all player components.
 */

import { dbToGain } from './audio';

/**
 * Audio processing node chain structure.
 * Contains all nodes needed for gain control, filtering, and optional compression.
 */
export interface AudioNodeChain {
  source: MediaElementAudioSourceNode;
  gain: GainNode;
  highPass: BiquadFilterNode;
  compressor?: DynamicsCompressorNode;
}

/**
 * Options for creating an audio node chain.
 */
export interface AudioNodeOptions {
  /** Initial gain in decibels (default: 0) */
  gainDb?: number;
  /** High-pass filter cutoff frequency in Hz (default: 20) */
  highPassFreq?: number;
  /** Include dynamics compressor for loud audio (default: false) */
  includeCompressor?: boolean;
}

/** Default high-pass filter frequency (20 Hz - below audible range) */
export const DEFAULT_HIGH_PASS_FREQ = 20;

/** Maximum high-pass filter frequency (10 kHz) */
export const MAX_HIGH_PASS_FREQ = 10000;

/** Maximum gain boost in decibels */
export const MAX_GAIN_DB = 24;

/** High-pass filter quality factor */
const HIGH_PASS_Q = 1;

/** Compressor threshold in decibels */
const COMPRESSOR_THRESHOLD_DB = -24;

/** Compressor knee width in decibels */
const COMPRESSOR_KNEE_DB = 30;

/** Compressor compression ratio */
const COMPRESSOR_RATIO = 12;

/** Compressor attack time in seconds */
const COMPRESSOR_ATTACK_SECONDS = 0.003;

/** Compressor release time in seconds */
const COMPRESSOR_RELEASE_SECONDS = 0.25;

/**
 * Source nodes already created per (context, element). createMediaElementSource
 * may run at most once per pair (a second call throws InvalidStateError). Keyed
 * by context first so a recreated context never reuses a stale node.
 */
const sourceNodeCache = new WeakMap<
  BaseAudioContext,
  WeakMap<HTMLMediaElement, MediaElementAudioSourceNode>
>();

/**
 * Get the media element source node for this context and element, creating it on
 * first use. Every caller that routes an element through Web Audio must use this
 * instead of calling createMediaElementSource directly.
 *
 * @param ctx - AudioContext that owns the node
 * @param element - media element to capture
 * @returns the cached source node, or a new one
 */
export function getOrCreateMediaElementSource(
  ctx: AudioContext,
  element: HTMLMediaElement
): { source: MediaElementAudioSourceNode; cached: boolean } {
  let sourcesByElement = sourceNodeCache.get(ctx);
  if (!sourcesByElement) {
    sourcesByElement = new WeakMap();
    sourceNodeCache.set(ctx, sourcesByElement);
  }
  const existing = sourcesByElement.get(element);
  if (existing) return { source: existing, cached: true };
  const source = ctx.createMediaElementSource(element);
  sourcesByElement.set(element, source);
  return { source, cached: false };
}

/**
 * Create a complete audio processing node chain.
 *
 * Chain: source -> highPass -> gain -> [compressor] -> destination
 *
 * Once the media element source exists the element is captured by the context
 * and silent unless the source is connected. If building the rest of the chain
 * throws, the source is connected straight to the destination so the element
 * stays audible, and the error is rethrown. A later call for the same element
 * reuses the cached source, disconnects all of its routes (the fallback route
 * and any live chain) and rebuilds. A second chain for an element that already
 * has a live one therefore cuts the first.
 *
 * @param ctx - AudioContext to create nodes in
 * @param audioElement - HTML audio element to use as source
 * @param options - Configuration options
 * @returns AudioNodeChain with all connected nodes
 */
export function createAudioNodeChain(
  ctx: AudioContext,
  audioElement: HTMLAudioElement,
  options: AudioNodeOptions = {}
): AudioNodeChain {
  const { gainDb = 0, highPassFreq = DEFAULT_HIGH_PASS_FREQ, includeCompressor = false } = options;

  const { source, cached } = getOrCreateMediaElementSource(ctx, audioElement);
  // The cached source loses all of its routes, not only an earlier fallback, so
  // a second chain for an element with a live one cuts the first.
  if (cached) source.disconnect();

  try {
    // Create gain node for volume control
    const gain = ctx.createGain();
    gain.gain.value = dbToGain(gainDb);

    // Create high-pass filter to reduce low-frequency noise
    const highPass = ctx.createBiquadFilter();
    highPass.type = 'highpass';
    highPass.frequency.value = highPassFreq;
    highPass.Q.value = HIGH_PASS_Q;

    if (includeCompressor) {
      // Create dynamics compressor for consistent volume
      const compressor = ctx.createDynamicsCompressor();
      compressor.threshold.value = COMPRESSOR_THRESHOLD_DB;
      compressor.knee.value = COMPRESSOR_KNEE_DB;
      compressor.ratio.value = COMPRESSOR_RATIO;
      compressor.attack.value = COMPRESSOR_ATTACK_SECONDS;
      compressor.release.value = COMPRESSOR_RELEASE_SECONDS;

      // Connect chain with compressor
      source.connect(highPass).connect(gain).connect(compressor).connect(ctx.destination);

      return { source, gain, highPass, compressor };
    }

    // Connect chain without compressor
    source.connect(highPass).connect(gain).connect(ctx.destination);

    return { source, gain, highPass };
  } catch (err) {
    try {
      source.disconnect();
      source.connect(ctx.destination);
    } catch {
      // The fallback route is best effort; the original error is what matters.
    }
    throw err;
  }
}

/**
 * Attach the Web Audio graph to the element only when the context is running.
 *
 * getAudioContext() can return a still-suspended context (it stops awaiting
 * resume() after a timeout). On iOS, createMediaElementSource permanently
 * reroutes the element's output away from the default device, so building the
 * graph on a suspended context plays silently while currentTime still advances.
 * Callers must therefore let the element play through its native output while
 * the context is suspended and call this again on a later play once the context
 * has resumed.
 *
 * @param ctx - AudioContext, or null if unavailable
 * @param audioElement - HTML audio element to route, or null
 * @param existing - already-attached chain for this element, if any
 * @param options - node chain configuration
 * @returns the existing chain if already attached; a freshly built chain when
 *   the context is running; otherwise null (graph not attached, retry on a later
 *   play). Returning the existing chain unchanged also upholds the
 *   once-per-element createMediaElementSource constraint. Throws when the graph
 *   build fails; the element then plays through a direct source-to-output route
 *   (see createAudioNodeChain), so callers should catch, play on, and may call
 *   this again on a later play.
 */
export function attachAudioGraphWhenRunning(
  ctx: AudioContext | null,
  audioElement: HTMLAudioElement | null,
  existing: AudioNodeChain | null,
  options: AudioNodeOptions = {}
): AudioNodeChain | null {
  if (existing) return existing;
  if (!ctx || !audioElement || ctx.state !== 'running') return null;
  return createAudioNodeChain(ctx, audioElement, options);
}

/**
 * Safely disconnect all nodes in an audio chain.
 * Handles cases where nodes may already be disconnected.
 *
 * @param nodes - AudioNodeChain to disconnect
 */
export function disconnectAudioNodes(nodes: AudioNodeChain | null): void {
  if (!nodes) return;

  try {
    nodes.source.disconnect();
  } catch {
    // Node may already be disconnected
  }

  try {
    nodes.gain.disconnect();
  } catch {
    // Node may already be disconnected
  }

  try {
    nodes.highPass.disconnect();
  } catch {
    // Node may already be disconnected
  }

  if (nodes.compressor) {
    try {
      nodes.compressor.disconnect();
    } catch {
      // Node may already be disconnected
    }
  }
}
