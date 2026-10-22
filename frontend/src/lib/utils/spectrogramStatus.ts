/**
 * Reading the spectrogram generation status (GET /api/v2/spectrogram/:id/status).
 */

/**
 * Why the server could not render a spectrogram, as the status endpoint reports it
 * (the `reason` field; see "Spectrogram reasons" in internal/api/v2/README.md).
 */
export const SPECTROGRAM_REASON = {
  /** The source clip is on disk and no render exists yet: ask for generation. */
  notGenerated: 'not_generated',
  /** The clip is not on disk but may still be written: retry later, a bounded number of times. */
  sourcePending: 'source_pending',
  /** The clip is gone for good. It describes the audio: a kept render may still be served. */
  sourceMissing: 'source_missing',
  /** The clip exists but cannot be rendered. */
  sourceInvalid: 'source_invalid',
  /** Generation was refused or interrupted by load: retry with backoff. */
  busy: 'busy',
  /** Generation failed for any other reason: retry a bounded number of times. */
  renderFailed: 'render_failed',
} as const;

/** A reason the server reports, or any other string a newer server may send. */
export type SpectrogramReason =
  (typeof SPECTROGRAM_REASON)[keyof typeof SPECTROGRAM_REASON] | (string & {});

/** True for a reason that retrying cannot change, so the client stops asking. */
export function isTerminalSpectrogramReason(reason: string | undefined): boolean {
  return reason === SPECTROGRAM_REASON.sourceMissing || reason === SPECTROGRAM_REASON.sourceInvalid;
}

/** True for a reason where a later request can succeed, so a bounded number of retries is worthwhile. */
export function isRetryableSpectrogramReason(reason: string | undefined): boolean {
  return (
    reason === SPECTROGRAM_REASON.sourcePending ||
    reason === SPECTROGRAM_REASON.busy ||
    reason === SPECTROGRAM_REASON.renderFailed
  );
}

/** Most detections remembered as having no source clip; the oldest is forgotten first. */
export const MAX_SOURCE_MARKS = 1000;

// Insertion ordered, so the first entry is the oldest.
const sourceMissingMarks = new Set<string>();

/**
 * Records that the server reported the detection's source clip gone for good
 * (`source_missing`), so a later reader (the audio player) need not request the
 * clip to find out.
 */
export function markSpectrogramSourceMissing(detectionId: number | string): void {
  const key = String(detectionId);
  sourceMissingMarks.delete(key);
  sourceMissingMarks.add(key);
  if (sourceMissingMarks.size > MAX_SOURCE_MARKS) {
    const oldest = sourceMissingMarks.values().next();
    if (!oldest.done) sourceMissingMarks.delete(oldest.value);
  }
}

/** True when the server reported this detection's source clip gone for good in this page's lifetime. */
export function isSpectrogramSourceKnownMissing(detectionId: number | string): boolean {
  return sourceMissingMarks.has(String(detectionId));
}

/** Forgets every mark. For tests. */
export function clearSpectrogramSourceMarks(): void {
  sourceMissingMarks.clear();
}

/** Generation status of a detection's spectrogram, as the status endpoint reports it. */
export interface SpectrogramGenerationStatus {
  /** For example 'not_started', 'queued', 'generating', 'generated', 'exists' or 'failed'. */
  status: string;
  /** Position in the generation queue; 0 when not queued. */
  queuePosition: number;
  /** Server message for display; empty when none. */
  message: string;
  /** Why the status is what it is, when the server knows; see SPECTROGRAM_REASON. */
  reason?: SpectrogramReason;
}

/**
 * Reads a status endpoint body, in the v2 envelope (`{ data: {...} }`) or bare.
 * Returns undefined when the body carries no status string.
 */
export function parseSpectrogramStatus(body: unknown): SpectrogramGenerationStatus | undefined {
  const payload = typeof body === 'object' && body !== null && 'data' in body ? body.data : body;
  if (typeof payload !== 'object' || payload === null || !('status' in payload)) return undefined;
  if (typeof payload.status !== 'string') return undefined;
  const result: SpectrogramGenerationStatus = {
    status: payload.status,
    queuePosition:
      'queuePosition' in payload && typeof payload.queuePosition === 'number'
        ? payload.queuePosition
        : 0,
    message: 'message' in payload && typeof payload.message === 'string' ? payload.message : '',
  };
  if ('reason' in payload && typeof payload.reason === 'string') result.reason = payload.reason;
  return result;
}

/**
 * Fetches and parses the status at `url`. Returns undefined on any failure:
 * a network error, an abort, a non-ok answer or a body without a status.
 */
export async function fetchSpectrogramStatus(
  url: string,
  signal: AbortSignal
): Promise<SpectrogramGenerationStatus | undefined> {
  try {
    const response = await fetch(url, { signal });
    if (!response.ok) return undefined;
    return parseSpectrogramStatus(await response.json());
  } catch {
    return undefined;
  }
}
