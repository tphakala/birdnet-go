/**
 * False positive filter cadence math for the analysis settings page.
 *
 * Mirrors the backend confirmation count (internal/analysis/processor
 * MinDetectionsForModel) and the per-model analysis step (internal/classifier
 * ResolveModelOverlap and ModelSpec.BufferInterval), including the backend's
 * integer nanosecond durations, so an estimate shown before saving matches what
 * the server computes. A shared fixture
 * (internal/analysis/processor/testdata/fp_confirmations.json) pins the parity
 * from both sides.
 *
 * The saved state is never recomputed here: the readout for the running
 * configuration uses the counts the server reports in `analysisCadence`.
 */
import type {
  AnalysisCadenceInfo,
  AnalysisCadenceModel,
} from '$lib/desktop/features/system/inference.types';
import { safeArrayAccess } from '$lib/utils/security';

/** Share of the reference window's analysis windows that must agree, per level (backend getThresholdForLevel). */
const LEVEL_THRESHOLDS: number[] = [0, 0.2, 0.3, 0.5, 0.6, 0.7];
/** Backend default for a level outside 0-5 (Moderate). */
const UNKNOWN_LEVEL_THRESHOLD = 0.3;

const NS_PER_SECOND = 1_000_000_000;
const NS_PER_MS = 1_000_000;
const MS_PER_SECOND = 1000;
/** Clip length the base overlap (birdnet.overlap) is defined against (classifier.AnalysisBaseClipLength). */
const BASE_CLIP_NS = 3 * NS_PER_SECOND;
const BASE_CLIP_SECONDS = 3;
/** Smallest buffer step (classifier minAnalysisStep, 1 ms). */
const MIN_ANALYSIS_STEP_NS = NS_PER_MS;
/** Window within which confirmations are counted (processor.ReferenceWindowSeconds). */
export const REFERENCE_WINDOW_SECONDS = 6;
/** Floor of the analysis step in the confirmation math (fpMinSegmentLength). */
const MIN_SEGMENT_SECONDS = 0.1;
/** Absorbs floating point rounding before the ceiling (fpEpsilon). */
const FLOAT_EPSILON = 1e-9;
/** Tolerance when comparing overlaps given in seconds. */
const OVERLAP_TOLERANCE_SECONDS = 0.001;

/** Registry ID of the bat model, which has its own fixed cadence and filter. */
export const BAT_MODEL_ID = 'Bat';

/** Lowest filter level at which the backend caps the cadence (cadence.FilterActive). */
export const FILTER_ACTIVE_MIN_LEVEL = 1;

/** Go's time.Duration.Seconds() for an integer nanosecond count. */
function durationSeconds(ns: number): number {
  const whole = Math.trunc(ns / NS_PER_SECOND);
  return whole + (ns - whole * NS_PER_SECOND) / NS_PER_SECOND;
}

/** Go's time.Duration(seconds * float64(time.Second)), which truncates toward zero. */
export function overlapSecondsToNs(seconds: number): number {
  return Math.trunc(seconds * NS_PER_SECOND);
}

/**
 * Confirmations required within the reference window for an analysis step
 * (backend minDetectionsForSegment). Level 0 disables the filter (always 1).
 */
export function minDetectionsForStep(stepSeconds: number, level: number): number {
  if (level === 0) return 1;
  const threshold = safeArrayAccess(LEVEL_THRESHOLDS, level) ?? UNKNOWN_LEVEL_THRESHOLD;
  const segment = Math.max(stepSeconds, MIN_SEGMENT_SECONDS);
  const required = (REFERENCE_WINDOW_SECONDS / segment) * threshold - FLOAT_EPSILON;
  return Math.max(1, Math.ceil(required));
}

/**
 * Analysis step in seconds of a non-bat model at a base overlap, as the backend
 * uses it in the confirmation math: the 3 s path subtracts the overlap from the
 * base clip, any other clip ratio-scales the overlap and takes the buffer step.
 */
export function modelStepSeconds(clipMs: number, baseOverlapNs: number): number {
  const clipNs = clipMs * NS_PER_MS;
  if (clipNs === BASE_CLIP_NS) {
    return BASE_CLIP_SECONDS - durationSeconds(baseOverlapNs);
  }
  const scaled = Math.round((baseOverlapNs * clipNs) / BASE_CLIP_NS);
  const maxOverlap = Math.max(clipNs - MIN_ANALYSIS_STEP_NS, 0);
  const overlap = Math.min(Math.max(scaled, 0), maxOverlap);
  return durationSeconds(Math.max(clipNs - overlap, MIN_ANALYSIS_STEP_NS));
}

/** One model's line in the readout. */
export interface CadenceReadoutRow {
  id: string;
  name: string;
  /** Analysis windows that must agree within the reference window. */
  confirmations: number;
  /** Analysis windows within the reference window (may be fractional). */
  windows: number;
  /** Seconds between analysis windows. */
  stepSeconds: number;
}

/** Bird models the readout covers; the bat model has its own filter and section. */
function birdModels(cadence: AnalysisCadenceInfo): AnalysisCadenceModel[] {
  return cadence.models.filter(m => m.id !== BAT_MODEL_ID);
}

/** Readout of the running configuration, from the server's own counts. */
export function savedReadout(cadence: AnalysisCadenceInfo): CadenceReadoutRow[] {
  return birdModels(cadence).map(m => ({
    id: m.id,
    name: m.name || m.id,
    confirmations: m.confirmations,
    windows: m.windowsInReference,
    stepSeconds: m.stepMs / MS_PER_SECOND,
  }));
}

/**
 * Base overlap the backend is expected to use for an edited level and overlap,
 * from what the published plan says about this device. With the filter off the
 * configured overlap is used as is. An overloaded device runs at zero overlap.
 * An unchanged overlap keeps the plan's effective value; a changed one is
 * capped at the smallest step the device sustains, when the plan knows it.
 */
export function previewEffectiveOverlapSeconds(
  cadence: AnalysisCadenceInfo,
  overlapSeconds: number,
  level: number
): number {
  if (level < FILTER_ACTIVE_MIN_LEVEL) return overlapSeconds;
  if (cadence.status === 'overloaded') return 0;
  if (
    cadence.status !== 'filterOff' &&
    Math.abs(overlapSeconds - cadence.configuredOverlapSec) < OVERLAP_TOLERANCE_SECONDS
  ) {
    return cadence.effectiveOverlapSec;
  }
  if (cadence.minBaseStepMs > 0) {
    return Math.min(overlapSeconds, BASE_CLIP_SECONDS - cadence.minBaseStepMs / MS_PER_SECOND);
  }
  return overlapSeconds;
}

/** Estimated readout for an edited level and overlap, before the server applies them. */
export function previewReadout(
  cadence: AnalysisCadenceInfo,
  overlapSeconds: number,
  level: number
): CadenceReadoutRow[] {
  const effectiveNs = overlapSecondsToNs(
    previewEffectiveOverlapSeconds(cadence, overlapSeconds, level)
  );
  return birdModels(cadence).map(m => {
    const stepSeconds = modelStepSeconds(m.clipMs, effectiveNs);
    return {
      id: m.id,
      name: m.name || m.id,
      confirmations: minDetectionsForStep(stepSeconds, level),
      windows: stepSeconds > 0 ? REFERENCE_WINDOW_SECONDS / stepSeconds : 0,
      stepSeconds,
    };
  });
}

/** True when two overlaps in seconds are the same setting. */
export function sameOverlap(a: number, b: number): boolean {
  return Math.abs(a - b) < OVERLAP_TOLERANCE_SECONDS;
}

/**
 * True when the snapshot does not yet reflect the saved settings: the saved
 * overlap differs from the plan's configured one, the saved level is on the
 * other side of the filter on/off line, or a bird model's confirmations were
 * computed for another level than the saved one (the server computes them per
 * request, so a level change that does not re-plan shows up only on the next
 * fetch). A fetch after the save clears it.
 */
export function planLagsSettings(
  cadence: AnalysisCadenceInfo,
  savedOverlapSeconds: number,
  savedLevel: number
): boolean {
  const planFilterOn = cadence.status !== 'filterOff';
  const savedFilterOn = savedLevel >= FILTER_ACTIVE_MIN_LEVEL;
  return (
    !sameOverlap(savedOverlapSeconds, cadence.configuredOverlapSec) ||
    planFilterOn !== savedFilterOn ||
    birdModels(cadence).some(
      m =>
        m.windowsInReference > 0 &&
        m.confirmations !==
          minDetectionsForStep(REFERENCE_WINDOW_SECONDS / m.windowsInReference, savedLevel)
    )
  );
}
