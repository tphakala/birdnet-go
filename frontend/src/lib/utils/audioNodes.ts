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

/**
 * Create a complete audio processing node chain.
 *
 * Chain: source -> highPass -> gain -> [compressor] -> destination
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

  // Create source from audio element
  const source = ctx.createMediaElementSource(audioElement);

  // Create gain node for volume control
  const gain = ctx.createGain();
  gain.gain.value = dbToGain(gainDb);

  // Create high-pass filter to reduce low-frequency noise
  const highPass = ctx.createBiquadFilter();
  highPass.type = 'highpass';
  highPass.frequency.value = highPassFreq;
  highPass.Q.value = 1;

  if (includeCompressor) {
    // Create dynamics compressor for consistent volume
    const compressor = ctx.createDynamicsCompressor();
    compressor.threshold.value = -24;
    compressor.knee.value = 30;
    compressor.ratio.value = 12;
    compressor.attack.value = 0.003;
    compressor.release.value = 0.25;

    // Connect chain with compressor
    source.connect(highPass).connect(gain).connect(compressor).connect(ctx.destination);

    return { source, gain, highPass, compressor };
  }

  // Connect chain without compressor
  source.connect(highPass).connect(gain).connect(ctx.destination);

  return { source, gain, highPass };
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
