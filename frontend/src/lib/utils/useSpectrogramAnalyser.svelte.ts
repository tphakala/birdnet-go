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
 * - On browsers whose AnalyserNode cannot read HLS-backed media (Safari/WebKit)
 *   the bins come from the server instead; see ./serverSpectrumFallback
 */

import {
  getAudioContext,
  isAudioContextSupported,
  releaseAudioContext,
} from './audioContextManager';
import { dbToGain } from './audio';
import { loggers } from './logger';
import { computeWallClockAtPlayhead } from './detectionOverlay';
import { createServerSpectrumFallback, type AnalyserProbe } from './serverSpectrumFallback';

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
  /**
   * Current HLS program date at the playhead, if the player exposes one
   * (hls.js `playingDate`). Lets the server-spectrum fallback line its columns
   * up with the buffered audio; without it the seekable-range estimate is used,
   * as elsewhere in the live UI.
   */
  getPlayingDate?: () => Date | null;
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
  const analyserFftSize = options?.fftSize ?? DEFAULT_FFT_SIZE;
  const binCount = analyserFftSize / 2;

  // Reactive state (exposed to consumers)
  let analyser = $state<AnalyserNode | null>(null);
  let frequencyData = $state<Uint8Array<ArrayBuffer>>(new Uint8Array(binCount));
  let isActive = $state(false);
  let sampleRate = $state(48000);
  let fftSize = $state(analyserFftSize);
  let audioOutput = $state(options?.audioOutput ?? false);
  let gainDb = $state(options?.gainDb ?? 0);
  let usingServerSpectrum = $state(false);

  // Non-reactive internal nodes
  let audioContext: AudioContext | null = null;
  let sourceNode: MediaElementAudioSourceNode | null = null;
  let gainNode: GainNode | null = null;
  let outputGainNode: GainNode | null = null;
  let highPassNode: BiquadFilterNode | null = null;
  let analyserNode: AnalyserNode | null = null;
  let mediaEl: HTMLMediaElement | null = null;
  let probeBuffer: Uint8Array<ArrayBuffer> | null = null;
  /**
   * Bumped by every disconnect. connect() checks it after its await, so a stop
   * or a source switch mid-flight cannot be undone by the call it replaced.
   */
  let generation = 0;

  const fallback = createServerSpectrumFallback({
    probeAnalyser,
    playheadWallClock: () =>
      mediaEl
        ? computeWallClockAtPlayhead(
            mediaEl as HTMLAudioElement,
            options?.getPlayingDate?.() ?? null,
            Date.now() / 1000
          )
        : 0,
    onColumn: bins => {
      if (frequencyData.length !== bins.length) {
        frequencyData = new Uint8Array(bins.length);
        fftSize = bins.length * 2;
      }
      frequencyData.set(bins);
    },
    onAdopt: info => {
      usingServerSpectrum = true;
      if (info.sampleRate > 0) sampleRate = info.sampleRate;
      // The canvas is gated on isActive, and the graph may never have come up.
      isActive = true;
    },
  });

  /** Report what the local analyser can see; see AnalyserProbe for the values. */
  function probeAnalyser(): AnalyserProbe {
    if (!mediaEl || mediaEl.paused) return 'idle';
    if (!analyserNode || !audioContext) return 'absent';
    if (audioContext.state !== 'running') return 'idle';

    if (probeBuffer?.length !== analyserNode.frequencyBinCount) {
      probeBuffer = new Uint8Array(analyserNode.frequencyBinCount);
    }
    analyserNode.getByteFrequencyData(probeBuffer);
    return probeBuffer.some(v => v > 0) ? 'working' : 'silent';
  }

  /**
   * Connect to a media element and set up the Web Audio graph.
   *
   * @param mediaElement - element to analyse
   * @param sourceID - audio source to request server-computed bins for. Pass it
   *   whenever one is known: it is what lets the spectrogram survive a browser
   *   whose AnalyserNode cannot read HLS-backed media (Safari/WebKit).
   */
  async function connect(mediaElement: HTMLMediaElement, sourceID?: string | null): Promise<void> {
    // Disconnect any existing graph first (this bumps the generation)
    disconnect();

    const mine = generation;
    mediaEl = mediaElement;

    if (!isAudioContextSupported()) {
      logger.error('AudioContext not supported');
      return;
    }

    try {
      const ctx = await getAudioContext();
      // A stop or a source switch while getAudioContext() was pending owns the
      // composable now; installing this graph would clobber theirs.
      if (mine !== generation) return;
      audioContext = ctx;
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
      analyserNode.fftSize = analyserFftSize;
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
        fftSize: analyserFftSize,
        sampleRate: audioContext.sampleRate,
        audioOutput,
      });
    } catch (error) {
      if (mine !== generation) return;
      logger.error('Failed to connect spectrogram analyser', error);
      // Clean up any partially built graph. disconnect() bumps the generation,
      // so re-adopt it: this call is still the current owner.
      disconnect();
      generation = mine;
      mediaEl = mediaElement;
    }

    // Started last, and outside the try, so it also covers the case where the
    // graph above failed outright and there is no analyser to compare against.
    if (sourceID && mine === generation) fallback.start(sourceID);
  }

  /** Disconnect the audio graph and the server-spectrum fallback */
  function disconnect(): void {
    // Invalidate every in-flight await before releasing any state it may touch.
    generation++;
    fallback.stop();
    usingServerSpectrum = false;
    fftSize = analyserFftSize;
    mediaEl = null;
    probeBuffer = null;

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
    /**
     * True when `frequencyData` is fed by the server instead of the analyser.
     * Renderers must stop calling `analyser.getByteFrequencyData()` while this
     * is set, or they will overwrite the server column with zeros.
     */
    get usingServerSpectrum() {
      return usingServerSpectrum;
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
