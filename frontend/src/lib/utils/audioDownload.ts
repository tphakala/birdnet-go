/**
 * audioDownload.ts
 *
 * Shared helper for downloading a detection's audio clip. Used by the dashboard
 * DetectionCard and the mobile DetectionCardMobile so the filename logic lives
 * in one place.
 *
 * The actual bytes are served by GET /api/v2/audio/{id}; the `download`
 * attribute only hints the saved filename, so the sanitization here is for a
 * sensible filename, not server-side path safety.
 */

import type { Detection } from '$lib/types/detection.types';
import { buildAppUrl } from '$lib/utils/urlHelpers';

/** Fallback base name when a detection has no common name. */
const DEFAULT_DOWNLOAD_NAME = 'detection';
/** Extension for downloaded clips. */
const AUDIO_FILE_EXTENSION = '.wav';

/**
 * Build a safe download filename for a detection's audio clip:
 * `<common name>_<date>_<time>.wav`, falling back to the id when date/time are
 * absent. Strips characters that are not alphanumeric, space, dot, underscore,
 * or hyphen.
 */
export function buildDetectionAudioFilename(detection: Detection): string {
  const safeCommonName = (detection.commonName || DEFAULT_DOWNLOAD_NAME).replace(
    /[^a-zA-Z0-9 ._-]/g,
    '_'
  );
  const dateTime =
    detection.date && detection.time
      ? `${detection.date}_${detection.time.replace(/:/g, '-')}`
      : String(detection.id);
  return `${safeCommonName}_${dateTime}${AUDIO_FILE_EXTENSION}`;
}

/**
 * Trigger a browser download of the detection's audio clip via a temporary
 * anchor element.
 */
export function downloadDetectionAudio(detection: Detection): void {
  const link = document.createElement('a');
  link.href = buildAppUrl(`/api/v2/audio/${detection.id}`);
  link.download = buildDetectionAudioFilename(detection);
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
}
