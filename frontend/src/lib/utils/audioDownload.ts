import { t } from '$lib/i18n';
import type { Detection } from '$lib/types/detection.types';
import { getCsrfToken } from '$lib/utils/api';
import { downloadBlob } from '$lib/utils/fileHelpers';
import { buildAppUrl } from '$lib/utils/urlHelpers';

const CSRF_HEADER_NAME = 'X-CSRF-Token';

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

function jsonHeadersWithCsrf(): Headers {
  const headers = new Headers({ 'Content-Type': 'application/json' });
  const csrfToken = getCsrfToken();
  if (csrfToken) {
    headers.set(CSRF_HEADER_NAME, csrfToken);
  }
  return headers;
}

function extensionForFormat(format: RecordingDownloadFormat): string {
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
    case 'original':
      return '';
  }
}

function safeRecordingBaseName(detection: Detection): string {
  const species = detection.commonName || 'detection';
  const dateTime =
    detection.date && detection.time
      ? `${detection.date}_${detection.time.replace(/:/g, '-')}`
      : String(detection.id);
  const baseName = `${species}_${dateTime}`;
  const safeName = baseName
    .replace(/[^a-zA-Z0-9 ._-]/g, '_')
    .replace(/\s+/g, '_')
    .trim();
  return safeName || `detection_${detection.id}`;
}

function recordingFilename(detection: Detection, format: RecordingDownloadFormat): string {
  const ext = extensionForFormat(format);
  return ext ? `${safeRecordingBaseName(detection)}.${ext}` : safeRecordingBaseName(detection);
}

function triggerOriginalDownload(detection: Detection) {
  const link = document.createElement('a');
  link.href = buildAppUrl(`/api/v2/audio/${encodeURIComponent(String(detection.id))}`);
  link.download = '';
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
}

async function readAudioExportError(response: Response): Promise<string> {
  try {
    const data: { message?: string } = await response.json();
    if (data.message) {
      return data.message;
    }
  } catch {
    // Use fallback below when the server did not return a JSON error envelope.
  }
  return t('media.audio.error');
}

export function recordingDownloadErrorMessage(error: unknown): string {
  return error instanceof Error && error.message ? error.message : t('media.audio.error');
}

export async function downloadDetectionRecording(
  detection: Detection,
  format: RecordingDownloadFormat = 'original'
): Promise<void> {
  if (format === 'original') {
    triggerOriginalDownload(detection);
    return;
  }

  const response = await fetch(
    buildAppUrl(`/api/v2/audio/${encodeURIComponent(String(detection.id))}/export`),
    {
      method: 'POST',
      credentials: 'same-origin',
      headers: jsonHeadersWithCsrf(),
      body: JSON.stringify({ format }),
    }
  );

  if (!response.ok) {
    throw new Error(await readAudioExportError(response));
  }

  const blob = await response.blob();
  downloadBlob(blob, recordingFilename(detection, format));
}
