/**
 * useSpectrogramAnalyser — Web Audio composable for live spectrogram
 *
 * Connects to an HTMLMediaElement (HLS.js attached), creates an AnalyserNode,
 * and exposes the frequency data buffer for rendering by SpectrogramCanvas.
 *
 * Audio graph:
 *   source → highpass → gainNode (visualization) → analyserNode → outputGainNode → destination
 *
 * The visualization gain (gainNode) controls the FFT input for the spectrogram.
 * The output gain (outputGainNode) controls audio to speakers — mute sets it to 0.
 * This separation means muting audio does not affect the spectrogram visualization.
 *
 * Key constraints:
 * - createMediaElementSource() can only be called once per element (guarded by WeakMap)
 * - Uses the shared audioContextManager singleton
 * - Does NOT use onMount — exposes connect()/disconnect() for parent control
 */

import {
  getAudioContext,
  isAudioContextSupported,
  releaseAudioContext,
} from './audioContextManager';
import { dbToGain } from './audio';
import { loggers } from './logger';

const logger = loggers.audio;

/**
 * Guard: createMediaElementSource() can only be called once per element per AudioContext.
 * Keyed by AudioContext first, then HTMLMediaElement — so if the AudioContext is
 * recreated (after close), stale source nodes from the old context won't be reused.
 */
const sourceNodeMap = new WeakMap<
  AudioContext,
  WeakMap<HTMLMediaElement, MediaElementAudioSourceNode>
>();

export interface SpectrogramAnalyserOptions {
  /** FFT size — must be power of 2 (default: 1024) */
  fftSize?: number;
  /** Whether to route audio to speakers (default: false) */
  audioOutput?: boolean;
  /** Gain in dB (default: 0) */
  gainDb?: number;
}

const DEFAULT_FFT_SIZE = 1024;
const HIGH_PASS_FREQ = 20;
const HIGH_PASS_Q = 1;
const ANALYSER_SMOOTHING = 0.8;
const OUTPUT_GAIN_UNMUTED = 1;
const OUTPUT_GAIN_MUTED = 0;
/** Short ramp duration (seconds) to avoid audible clicks when muting/unmuting */
const GAIN_RAMP_DURATION = 0.01;

export function useSpectrogramAnalyser(options?: SpectrogramAnalyserOptions) {
  const fftSize = options?.fftSize ?? DEFAULT_FFT_SIZE;
  const binCount = fftSize / 2;

  // Reactive state (exposed to consumers)
  let analyser = $state<AnalyserNode | null>(null);
  let frequencyData = $state<Uint8Array<ArrayBuffer>>(new Uint8Array(binCount));
  let isActive = $state(false);
  let sampleRate = $state(48000);
  let audioOutput = $state(options?.audioOutput ?? false);
  let gainDb = $state(options?.gainDb ?? 0);

  // Non-reactive internal nodes
  let audioContext: AudioContext | null = null;
  let sourceNode: MediaElementAudioSourceNode | null = null;
  let gainNode: GainNode | null = null;
  let outputGainNode: GainNode | null = null;
  let highPassNode: BiquadFilterNode | null = null;
  let analyserNode: AnalyserNode | null = null;

  /** Connect to a media element and set up the Web Audio graph */
  async function connect(mediaElement: HTMLMediaElement): Promise<void> {
    // Disconnect any existing graph first
    disconnect();

    if (!isAudioContextSupported()) {
      logger.error('AudioContext not supported');
      return;
    }

    try {
      audioContext = await getAudioContext();
      sampleRate = audioContext.sampleRate;

      // Guard: reuse existing source node for this element + context combination
      let contextCache = sourceNodeMap.get(audioContext);
      if (!contextCache) {
        contextCache = new WeakMap<HTMLMediaElement, MediaElementAudioSourceNode>();
        sourceNodeMap.set(audioContext, contextCache);
      }
      const existingSource = contextCache.get(mediaElement);
      if (existingSource) {
        sourceNode = existingSource;
      } else {
        sourceNode = audioContext.createMediaElementSource(mediaElement);
        contextCache.set(mediaElement, sourceNode);
      }

      // Create processing nodes
      highPassNode = audioContext.createBiquadFilter();
      highPassNode.type = 'highpass';
      highPassNode.frequency.value = HIGH_PASS_FREQ;
      highPassNode.Q.value = HIGH_PASS_Q;

      gainNode = audioContext.createGain();
      gainNode.gain.value = dbToGain(gainDb);

      analyserNode = audioContext.createAnalyser();
      analyserNode.fftSize = fftSize;
      analyserNode.smoothingTimeConstant = ANALYSER_SMOOTHING;

      // Output gain node controls audio to speakers (mute sets to 0)
      outputGainNode = audioContext.createGain();
      outputGainNode.gain.value = audioOutput ? OUTPUT_GAIN_UNMUTED : OUTPUT_GAIN_MUTED;

      // Connect chain: source → highpass → gain → analyser → outputGain → destination
      sourceNode.connect(highPassNode);
      highPassNode.connect(gainNode);
      gainNode.connect(analyserNode);
      analyserNode.connect(outputGainNode);
      outputGainNode.connect(audioContext.destination);

      // Allocate buffer matching analyser bin count
      frequencyData = new Uint8Array(analyserNode.frequencyBinCount);
      analyser = analyserNode;
      isActive = true;

      logger.debug('Spectrogram analyser connected', {
        fftSize,
        sampleRate: audioContext.sampleRate,
        audioOutput,
      });
    } catch (error) {
      logger.error('Failed to connect spectrogram analyser', error);
      // Clean up any partially built graph
      disconnect();
    }
  }

  /** Disconnect the audio graph */
  function disconnect(): void {
    try {
      if (outputGainNode) outputGainNode.disconnect();
      if (analyserNode) analyserNode.disconnect();
      if (gainNode) gainNode.disconnect();
      if (highPassNode) highPassNode.disconnect();
      if (sourceNode) sourceNode.disconnect();
    } catch {
      // Nodes may already be disconnected
    }

    outputGainNode = null;
    analyserNode = null;
    gainNode = null;
    highPassNode = null;
    sourceNode = null;
    analyser = null;
    isActive = false;
  }

  /** Toggle audio output to speakers via the output gain node */
  function setAudioOutput(enabled: boolean): void {
    audioOutput = enabled;
    if (!outputGainNode || !audioContext) return;

    // Cancel any pending ramp to handle rapid mute/unmute clicks,
    // then use a short ramp to avoid audible clicks
    const now = audioContext.currentTime;
    outputGainNode.gain.cancelScheduledValues(now);
    outputGainNode.gain.linearRampToValueAtTime(
      enabled ? OUTPUT_GAIN_UNMUTED : OUTPUT_GAIN_MUTED,
      now + GAIN_RAMP_DURATION
    );
  }

  /** Update gain in dB */
  function setGain(db: number): void {
    gainDb = db;
    if (gainNode) {
      gainNode.gain.value = dbToGain(db);
    }
  }

  /** Full cleanup — disconnects graph and releases AudioContext */
  function destroy(): void {
    disconnect();
    releaseAudioContext();
  }

  // Auto-cleanup on component destroy
  $effect(() => {
    return () => destroy();
  });

  return {
    get analyser() {
      return analyser;
    },
    get frequencyData() {
      return frequencyData;
    },
    get isActive() {
      return isActive;
    },
    get sampleRate() {
      return sampleRate;
    },
    get fftSize() {
      return fftSize;
    },
    connect,
    disconnect,
    setAudioOutput,
    setGain,
    destroy,
  };
}
