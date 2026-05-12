import { fetchWithCSRF } from './api';
import { loggers } from './logger';

const logger = loggers.ui;

/** Top-N predictions returned by the reanalyze endpoint for a clip. */
export interface ReanalyzePrediction {
  species: string;
  confidence: number;
}

/** Full response shape from POST /api/v2/detections/:id/reanalyze. */
export interface ReanalyzeResult {
  detectionId: number;
  modelId: string;
  modelName: string;
  sampleRate: number;
  clipDurationSec: number;
  windowCount: number;
  predictions: ReanalyzePrediction[];
}

const inFlightIds = new Set<number>();

/**
 * Re-run inference on a saved detection's audio clip using a different model
 * than the one that originally produced the detection. The call is read-only:
 * the server does NOT persist the alternate prediction; callers display it
 * transiently in a modal.
 *
 * Drops duplicate requests for the same detection while one is in-flight to
 * avoid burning CPU when the user clicks the button twice. Returns `null` in
 * that case; callers should check before treating the response as fresh.
 *
 * modelId accepts either the orchestrator registry ID (e.g. "Perch_V2") or
 * the user-facing config alias (e.g. "perch_v2"); the backend resolves
 * aliases internally.
 */
export async function reanalyzeDetection(
  detectionId: number,
  modelId: string
): Promise<ReanalyzeResult | null> {
  if (inFlightIds.has(detectionId)) return null;

  inFlightIds.add(detectionId);
  try {
    // fetchWithCSRF parses JSON and raises ApiError on non-2xx; no need for
    // manual response inspection here.
    return await fetchWithCSRF<ReanalyzeResult>(`/api/v2/detections/${detectionId}/reanalyze`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ modelId }),
    });
  } catch (err) {
    logger.error('Reanalyze request failed:', err, {
      component: 'reanalyzeDetection',
      detectionId,
      modelId,
    });
    throw err;
  } finally {
    inFlightIds.delete(detectionId);
  }
}
