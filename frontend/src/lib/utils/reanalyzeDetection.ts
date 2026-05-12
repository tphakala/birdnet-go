import { fetchWithCSRF } from './api';
import { loggers } from './logger';

const logger = loggers.ui;

/** A model that participated in a reanalysis run, returned in modelsRun. */
export interface ReanalyzeModelInfo {
  id: string;
  name: string;
  sampleRate: number;
  windowCount: number;
}

/** Top-N prediction from a multi-model reanalysis. ByModel maps each
 *  participating model's registry ID to the max confidence that model
 *  produced; absent keys mean the model didn't see the species at all. */
export interface ReanalyzePrediction {
  scientificName: string;
  commonName?: string;
  byModel: Record<string, number>;
}

/** Full response shape from POST /api/v2/detections/:id/reanalyze. */
export interface ReanalyzeResult {
  detectionId: number;
  clipDurationSec: number;
  modelsRun: ReanalyzeModelInfo[];
  predictions: ReanalyzePrediction[];
}

/** Body for POST /api/v2/detections/:id/correct-species. */
export interface CorrectSpeciesRequest {
  scientificName: string;
  modelId: string;
  confidence: number;
}

/** Response shape from the correction endpoint. */
export interface CorrectSpeciesResult {
  detectionId: number;
  scientificName: string;
  commonName: string;
  modelId: string;
  modelName: string;
  confidence: number;
  verified: string;
}

const inFlightReanalyze = new Set<number>();
const inFlightCorrection = new Set<number>();

// fetchWithCSRF's default 30s timeout is too aggressive here. Reanalyze on a
// busy install runs N model windows back-to-back, each contending with the
// realtime pipeline for the same per-model locks; a 45s clip across BirdNET
// v2.4 + Perch v2 can take 20s+ wall-time when the realtime path is busy. Use
// 2 minutes — the backend's hard ceiling on decode is 60s and inference adds
// only seconds on top, so anything past that is genuinely a hang worth
// surfacing as an error.
const REANALYZE_TIMEOUT_MS = 120000;

/**
 * Re-run inference on a saved detection's audio clip. By default runs every
 * loaded compatible classifier; pass `modelIds` to restrict. The server does
 * NOT persist any prediction returned here — callers display it transiently.
 *
 * Drops duplicate requests for the same detection while one is in-flight to
 * avoid burning CPU on double-clicks. Returns `null` in that case.
 */
export async function reanalyzeDetection(
  detectionId: number,
  modelIds?: string[]
): Promise<ReanalyzeResult | null> {
  if (inFlightReanalyze.has(detectionId)) return null;

  inFlightReanalyze.add(detectionId);
  try {
    return await fetchWithCSRF<ReanalyzeResult>(`/api/v2/detections/${detectionId}/reanalyze`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ modelIds: modelIds ?? [] }),
      timeout: REANALYZE_TIMEOUT_MS,
    });
  } catch (err) {
    logger.error('Reanalyze request failed:', err, {
      component: 'reanalyzeDetection',
      detectionId,
      modelIds,
    });
    throw err;
  } finally {
    inFlightReanalyze.delete(detectionId);
  }
}

/**
 * Apply a species correction to a saved detection. Replaces the detection's
 * label_id + model_id + confidence with the user-chosen prediction and marks
 * verified='correct' in one transaction.
 *
 * Drops duplicate requests for the same detection while one is in-flight.
 * Returns `null` in that case.
 */
export async function correctDetectionSpecies(
  detectionId: number,
  payload: CorrectSpeciesRequest
): Promise<CorrectSpeciesResult | null> {
  if (inFlightCorrection.has(detectionId)) return null;

  inFlightCorrection.add(detectionId);
  try {
    return await fetchWithCSRF<CorrectSpeciesResult>(
      `/api/v2/detections/${detectionId}/correct-species`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      }
    );
  } catch (err) {
    logger.error('Correction request failed:', err, {
      component: 'correctDetectionSpecies',
      detectionId,
      payload,
    });
    throw err;
  } finally {
    inFlightCorrection.delete(detectionId);
  }
}
