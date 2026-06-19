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

/** Host hardware and runtime environment the models run on. */
export interface InferenceHardware {
  arch: string;
  cpuModel: string;
  environment: string;
  fp16: boolean;
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
  backend: string;
  detectionName?: string;
  detectionVersion?: string;
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

/** Full inference status snapshot. `models` is the single source of truth. */
export interface InferenceStatusResponse {
  hardware: InferenceHardware;
  backends: InferenceBackends;
  models: InferenceModel[];
  audio?: InferenceAudio;
  runtimeBaselineBytes?: number;
  snapshotAtUnix: number;
}
