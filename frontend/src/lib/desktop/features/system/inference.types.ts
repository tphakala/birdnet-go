/**
 * TypeScript mirror of the GET /api/v2/system/inference snapshot response.
 *
 * The contract uses camelCase JSON and is the single source of truth for the
 * AI Models & Inference page. Optional fields use `?` and reflect omitempty /
 * nullable values on the backend (see internal/api/v2/inference_status.go):
 * `stats.rtf` is absent when invocations == 0, `memory.approxRssBytes` is
 * absent when the measurement is unavailable, and `runtimeBaselineBytes` is
 * omitempty.
 */

/**
 * Single-board computer the host runs on, as named by its device tree. Absent
 * on hosts with no device tree, which is every PC.
 */
export interface InferenceBoard {
  /** Board family, e.g. "raspberry-pi" or "generic". */
  kind: string;
  /** Device-tree model string, e.g. "Raspberry Pi 5 Model B Rev 1.0". */
  model?: string;
  /** System-on-chip identifier, e.g. "bcm2712". */
  soc?: string;
  /** Performance band ("pi5", "pi4", "pi3"); absent for undistinguished boards. */
  tier?: string;
}

/** Reason codes the server can attach to an accelerator. */
export type GpuReasonCode = 'render-node-unavailable' | 'render-node-permission' | 'no-runtime';

/**
 * A GPU present on the host. Reported whether or not it can be reached, so the
 * panel can explain an unreachable one instead of hiding it.
 */
export interface InferenceAccelerator {
  /** "igpu" or "dgpu". */
  kind: string;
  /** "intel", "amd" or "nvidia". */
  vendor: string;
  /**
   * Display name pairing the vendor with the PCI IDs. Not unique: two identical
   * cards produce the same name, so it must never be used as a list key.
   */
  name?: string;
  /**
   * Whether the server can open this device's DRM render node. This is not a
   * prediction that inference will run here; the device a model actually uses
   * is reported per model in `models[].device`.
   */
  accessible: boolean;
  /**
   * Every reason code explaining why this GPU is not an inference target, most
   * fundamental first. Each is rendered by `gpuReasonLabel`, which maps it to a
   * flat `system.inference.gpuReason<Code>` translation key.
   *
   * A list because the blockers stack: a card can be both unreachable and of a
   * vendor no build supports, and learning that one restart at a time is the
   * outcome worth avoiding.
   */
  reasons?: GpuReasonCode[];
}

/** Host hardware and runtime environment the models run on. */
export interface InferenceHardware {
  arch: string;
  cpuModel: string;
  environment: string;
  fp16: boolean;
  board?: InferenceBoard;
  accelerators?: InferenceAccelerator[];
  /** Effective memory ceiling: host RAM clamped by any cgroup limit. */
  totalRamBytes?: number;
  physicalCores?: number;
  /**
   * Capability tokens this host matches, in the model manifests' vocabulary.
   *
   * Rendered by SystemInference's Advanced disclosure on the hardware card. Most
   * tokens duplicate a fact the card already states, but two do not and are only
   * visible here: `low-ram` (set below the RAM threshold in
   * internal/hwprofile/capabilities.go) and `openvino-gpu-intel-gen<N>` (the
   * per-generation Intel GPU token). Do not delete this without dropping the Go
   * field too (internal/api/v2/system/inference_status.go).
   */
  capabilities?: string[];
}

/** Availability state for a compiled-in inference backend. */
export interface BackendStatus {
  available: boolean;
  initialized?: boolean;
  version?: string;
}

/** OpenVINO backend status, including the devices it can target. */
export interface OpenVINOBackendStatus {
  supported: boolean;
  active: boolean;
  devices?: string[];
}

/** Status for each supported inference backend. */
export interface InferenceBackends {
  tflite: BackendStatus;
  onnx: BackendStatus;
  openvino: OpenVINOBackendStatus;
}

/** Audio input spec a model expects. */
export interface ModelSpec {
  sampleRate: number;
  clipLengthSec: number;
}

/** Aggregate inference statistics for a model. `rtf` is absent when invocations == 0. */
export interface ModelStats {
  invocations: number;
  avgMs: number;
  maxMs: number;
  rtf?: number;
  errorRate?: number;
  loadFailures?: number;
}

/** Approximate host RAM (RSS) attributed to a model. `approxRssBytes` is absent when unavailable. */
export interface ModelMemory {
  approxRssBytes?: number;
  approximate: boolean;
}

/** An audio source attached to a model. */
export interface ModelSource {
  id: string;
  name: string;
  type?: string;
  fallback?: boolean;
}

/** Ring-buffer metric keys used to look up per-model time series. */
export interface ModelMetricKeys {
  avgMs: string;
  rtf: string;
  throughput: string;
  errorRate: string;
}

/** A recent detection in a model's "Last heard" feed (throttled per species). */
export interface InferenceLastDetection {
  species: string;
  scientificName: string;
  confidence: number;
  atUnix: number;
  /**
   * Whether the species passes the range filter. True when in range or the range
   * filter is inactive (e.g. no location configured). When the range filter is
   * active it is false for out-of-range birds and for non-avian and human classes,
   * which are shown for diagnostics but are not saved as detections.
   */
  inRange: boolean;
}

/** A single loaded model and its current state. */
export interface InferenceModel {
  id: string;
  name: string;
  /**
   * Inference execution backend the model is actually running on ("TFLite",
   * "ONNX", or "OpenVINO"), resolved from the live instance, not the model file
   * type. An ONNX model executed through the OpenVINO runtime reports "OpenVINO".
   * Falls back to the static file type when the model is not loaded.
   */
  backend: string;
  detectionName?: string;
  detectionVersion?: string;
  /**
   * Effective runtime precision ("INT8"/"FP16"/"FP32"), which can differ from the
   * weight precision in the file (e.g. an FP32 ONNX model executed on OpenVINO at
   * FP16). Absent when unknown.
   */
  quantization?: string;
  isStock: boolean;
  spec: ModelSpec;
  numSpecies: number;
  stats: ModelStats;
  memory: ModelMemory;
  sources: ModelSource[];
  metricKeys: ModelMetricKeys;
  lastDetection?: InferenceLastDetection;
  /** Compute device the model's inference runs on ("CPU", "GPU", "NPU", or "Unknown"). */
  device?: string;
  /** True when the model is currently paused by a schedule (e.g. bat night schedule). */
  paused?: boolean;
  /** Human-readable reason the model is paused, when paused (e.g. "Night schedule"). */
  scheduleLabel?: string;
  /** Most recent above-threshold predictions, newest first (up to 20). */
  recentDetections?: InferenceLastDetection[];
}

/** Ring-buffer metric keys used to look up audio pipeline time series. */
export interface InferenceAudioMetricKeys {
  queueDepth: string;
}

/** Audio pipeline metrics snapshot for the inference page. */
export interface InferenceAudio {
  queueDepth: number;
  droppedChunksTotal: number;
  queueCapacity: number;
  metricKeys: InferenceAudioMetricKeys;
}

/** Lifetime inference statistics for the VAD speech gate. */
export interface InferenceVADStats {
  invocations: number;
  avgMs: number;
  maxMs: number;
  speechHits: number;
}

/** One recent VAD speech hit in the dashboard history feed. */
export interface InferenceVADHit {
  /** Unix seconds of the speech hit. */
  atUnix: number;
  /** VAD speech probability [0,1] that tripped the gate. */
  probability: number;
  /** Display name of the audio source; may be absent. */
  source?: string;
}

/**
 * Privacy-filter Silero VAD speech-gate status. Present only when the privacy
 * filter is enabled (see internal/api/v2/system/inference_status.go); a nil `vad`
 * hides the dashboard panel. Stats are lifetime totals that survive detector
 * reloads and are sourced from always-on counters, so they populate even with
 * Prometheus telemetry disabled.
 */
export interface InferenceVAD {
  /** The configured VAD gate toggle (realtime.privacyfilter.vad.enabled). */
  enabled: boolean;
  /**
   * Whether a model source resolves (an embedded model is present, or a modelpath
   * override is set). When false the gate is inert even if enabled (e.g. a noembed
   * build with no modelpath).
   */
  available: boolean;
  /** True when a detector is currently held (loaded and scoring). */
  loaded: boolean;
  /** Configured speech-probability gate threshold. */
  threshold: number;
  /** "embedded", "path", or absent when unloaded. Never the on-disk path. */
  modelSource?: string;
  /** Active windowing strategy ("sequence"); absent when unloaded. */
  strategy?: string;
  /** Native sample rate of the loaded Silero VAD model (16 kHz); absent when unloaded. */
  sampleRate?: number;
  stats: InferenceVADStats;
  /** Unix seconds of the most recent speech hit; absent when none since start. */
  lastSpeechAtUnix?: number;
  /** Probability [0,1] of the most recent speech hit (pairs with lastSpeechAtUnix); absent when none since start. */
  lastSpeechProbability?: number;
  /** Newest-first history of recent speech hits (up to 10). The backend always
   * sends it (empty when none); optional here so partial fixtures stay valid. */
  recentHits?: InferenceVADHit[];
}

/** Full inference status snapshot. `models` is the single source of truth. */
export interface InferenceStatusResponse {
  hardware: InferenceHardware;
  backends: InferenceBackends;
  models: InferenceModel[];
  audio?: InferenceAudio;
  /** Privacy-filter VAD speech gate; absent when the privacy filter is off. */
  vad?: InferenceVAD;
  runtimeBaselineBytes?: number;
  snapshotAtUnix: number;
}
