/**
 * Reading the spectrogram generation status (GET /api/v2/spectrogram/:id/status).
 */

/** Generation status of a detection's spectrogram, as the status endpoint reports it. */
export interface SpectrogramGenerationStatus {
  /** For example 'not_started', 'queued', 'generating', 'generated', 'exists' or 'failed'. */
  status: string;
  /** Position in the generation queue; 0 when not queued. */
  queuePosition: number;
  /** Server message for display; empty when none. */
  message: string;
}

/**
 * Reads a status endpoint body, in the v2 envelope (`{ data: {...} }`) or bare.
 * Returns undefined when the body carries no status string.
 */
export function parseSpectrogramStatus(body: unknown): SpectrogramGenerationStatus | undefined {
  const payload = typeof body === 'object' && body !== null && 'data' in body ? body.data : body;
  if (typeof payload !== 'object' || payload === null || !('status' in payload)) return undefined;
  if (typeof payload.status !== 'string') return undefined;
  return {
    status: payload.status,
    queuePosition:
      'queuePosition' in payload && typeof payload.queuePosition === 'number'
        ? payload.queuePosition
        : 0,
    message: 'message' in payload && typeof payload.message === 'string' ? payload.message : '',
  };
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
