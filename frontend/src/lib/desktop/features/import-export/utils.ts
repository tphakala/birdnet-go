import type { ExternalMediaResponse, SourceAccessState } from './types';

/** Derive the source-access state from the external-media discovery response. */
export function deriveSourceAccessState(media: ExternalMediaResponse): SourceAccessState {
  if (!media.containerized) return 'native';
  if (media.mount_present) return 'container-mount';
  return 'container-missing';
}

/** Build a detections-filter URL for the BirdNET-Pi source after import. */
export function buildDetectionsFilterUrl(): string {
  return '/ui/detections?source=birdnet-pi';
}

/** Generate a simple unique id without crypto.randomUUID (plain HTTP fallback). */
export function generateId(): string {
  return Math.random().toString(36).slice(2, 10);
}
