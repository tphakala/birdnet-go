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

import { t } from '$lib/i18n';
import type { Detection } from '$lib/types/detection.types';
import { getCsrfToken } from '$lib/utils/api';
import { downloadBlob } from '$lib/utils/fileHelpers';
import { buildAppUrl } from '$lib/utils/urlHelpers';

/** Fallback base name when a detection has no common name. */
const DEFAULT_DOWNLOAD_NAME = 'detection';
/** Extension for downloaded clips. */
const AUDIO_FILE_EXTENSION = '.wav';
const CSRF_HEADER_NAME = 'X-CSRF-Token';
const DEFAULT_ORIGINAL_EXTENSION = 'wav';
const SUPPORTED_ORIGINAL_EXTENSIONS = new Set(['aac', 'flac', 'm4a', 'mp3', 'ogg', 'opus', 'wav']);

export type RecordingDownloadFormat = 'original' | 'wav' | 'flac' | 'mp3' | 'aac' | 'opus' | 'alac';

export interface RecordingDownloadFormatOption {
  id: RecordingDownloadFormat;
  label: string;
  labelKey?: string;
}

export const RECORDING_DOWNLOAD_FORMATS: readonly RecordingDownloadFormatOption[] = [
  {
    id: 'original',
    label: 'Original',
    labelKey: 'components.audioPlayer.processing.exportOriginal',
  },
  { id: 'wav', label: 'WAV' },
  { id: 'flac', label: 'FLAC' },
  { id: 'mp3', label: 'MP3' },
  { id: 'aac', label: 'AAC' },
  { id: 'opus', label: 'Opus' },
  { id: 'alac', label: 'ALAC' },
];

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

function extensionForFormat(format: Exclude<RecordingDownloadFormat, 'original'>): string {
  switch (format) {
    case 'aac':
    case 'alac':
      return 'm4a';
    case 'opus':
      return 'ogg';
    case 'wav':
    case 'flac':
    case 'mp3':
      return format;
  }
}

function safeRecordingBaseName(detection: Detection): string {
  const species = detection.commonName || DEFAULT_DOWNLOAD_NAME;
  const dateTime =
    detection.date && detection.time
      ? `${detection.date}_${detection.time.replace(/:/g, '-')}`
      : String(detection.id);
  const safeName = `${species}_${dateTime}`
    .replace(/[^a-zA-Z0-9 ._-]/g, '_')
    .replace(/\s+/g, '_')
    .trim();
  return safeName || `${DEFAULT_DOWNLOAD_NAME}_${detection.id}`;
}

function originalRecordingExtension(detection: Detection): string {
  const extension = detection.clipName?.match(/\.([a-zA-Z0-9]+)$/)?.[1]?.toLowerCase();
  return extension && SUPPORTED_ORIGINAL_EXTENSIONS.has(extension)
    ? extension
    : DEFAULT_ORIGINAL_EXTENSION;
}

function triggerOriginalDownload(detection: Detection): void {
  const link = document.createElement('a');
  link.href = buildAppUrl(`/api/v2/audio/${encodeURIComponent(String(detection.id))}`);
  link.download = `${safeRecordingBaseName(detection)}.${originalRecordingExtension(detection)}`;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
}

export function recordingDownloadErrorMessage(error: unknown): string {
  return error instanceof Error && error.message ? error.message : t('media.audio.error');
}

export async function downloadDetectionRecording(
  detection: Detection,
  format: RecordingDownloadFormat = 'original',
  signal?: AbortSignal
): Promise<void> {
  signal?.throwIfAborted();

  if (format === 'original') {
    triggerOriginalDownload(detection);
    return;
  }

  const headers = new Headers({ 'Content-Type': 'application/json' });
  const csrfToken = getCsrfToken();
  if (csrfToken) {
    headers.set(CSRF_HEADER_NAME, csrfToken);
  }

  const response = await fetch(
    buildAppUrl(`/api/v2/audio/${encodeURIComponent(String(detection.id))}/export`),
    {
      method: 'POST',
      credentials: 'same-origin',
      headers,
      body: JSON.stringify({ format }),
      signal,
    }
  );

  if (!response.ok) {
    throw new Error(t('media.audio.error'));
  }

  const blob = await response.blob();
  signal?.throwIfAborted();
  downloadBlob(blob, `${safeRecordingBaseName(detection)}.${extensionForFormat(format)}`);
}
