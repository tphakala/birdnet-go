import type { ImportSourcesResponse, SourceCandidate, SourceStepState } from './types';

/** Build a detections-filter URL for the BirdNET-Pi source after import. */
export function buildDetectionsFilterUrl(): string {
  return '/ui/detections?source=birdnet-pi';
}

/**
 * Returns true when the candidate exists but cannot be read by the service
 * user (permission_denied). Used to determine whether the elevation panel
 * should be shown.
 */
export function isUnreadable(c: SourceCandidate): boolean {
  return !c.valid && c.reason === 'permission_denied';
}

/**
 * Derives the source-step display state from the discovery response. Returns
 * 'candidates' when at least one candidate was found, 'zero-candidates'
 * otherwise (including when the response is null during the initial fetch).
 */
export function deriveSourceStepState(resp: ImportSourcesResponse | null): SourceStepState {
  return resp !== null && resp.candidates.length > 0 ? 'candidates' : 'zero-candidates';
}
