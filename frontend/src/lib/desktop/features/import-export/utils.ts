import { ReconnectingEventSource } from '$lib/utils/ReconnectingEventSource';
import { loggers } from '$lib/utils/logger';
import type {
  ImportErrorEvent,
  ImportProgress,
  ImportSourcesResponse,
  SourceCandidate,
  SourceStepState,
} from './types';

const logger = loggers.ui;

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

/** Percent complete for an import progress snapshot, clamped to 0-100. */
export function importProgressPercent(progress: ImportProgress | null): number {
  if (
    !progress ||
    !Number.isFinite(progress.total) ||
    !Number.isFinite(progress.processed) ||
    progress.total <= 0
  ) {
    return 0;
  }
  return Math.max(0, Math.min(100, Math.round((progress.processed / progress.total) * 100)));
}

/**
 * Consecutive failed (re)connect attempts before a stalled progress stream
 * triggers a status reconcile. With the default 3s backoff cap this is up to
 * ~3.5s of failed reconnects, long enough to rule out a transient blip.
 */
export const STREAM_STALL_THRESHOLD = 4;

/**
 * True when a stalled stream (attempts consecutive failed reconnects without an
 * open) should trigger a status reconcile: at the threshold and every
 * STREAM_STALL_THRESHOLD failures after, so re-polls stay rate-limited while a
 * server stays unreachable.
 */
export function shouldReconcileStalledStream(attempts: number): boolean {
  return attempts >= STREAM_STALL_THRESHOLD && attempts % STREAM_STALL_THRESHOLD === 0;
}

/** Callbacks for the import job SSE progress stream. */
export interface ImportProgressStreamHandlers {
  onProgress: (progress: ImportProgress) => void;
  /** Terminal: job finished successfully. Progress is null on a parse failure. */
  onComplete: (progress: ImportProgress | null) => void;
  /** Terminal: job was cancelled. Progress is null on a parse failure. */
  onCancelled: (progress: ImportProgress | null) => void;
  /** Terminal: job failed server-side (data-carrying 'error' event only). */
  onError: (progress: ImportProgress | null) => void;
  /**
   * Non-terminal: the stream has been failing to (re)connect long enough that
   * the caller should reconcile against GET /api/v2/import/status (the job may
   * be gone after a server restart). The stream keeps retrying regardless.
   */
  onStalled?: () => void;
}

function parseProgressEvent(event: Event, eventName: string): ImportProgress | null {
  try {
    return JSON.parse((event as MessageEvent).data) as ImportProgress;
  } catch (e) {
    logger.error(`Failed to parse import ${eventName} event`, e);
    return null;
  }
}

/**
 * Subscribe to an import job's SSE progress stream with the shared protocol
 * rules: payloads are parsed with logging on failure, and transport 'error'
 * events without data are NOT terminal (ReconnectingEventSource reconnects
 * those); only data-carrying 'error' events reach onError. A stream that keeps
 * failing to (re)connect (e.g. a 404 after a server restart) invokes the
 * optional onStalled handler so the caller can reconcile against
 * /api/v2/import/status. The server also emits 'heartbeat' keep-alives, which
 * need no listener. The caller owns the returned source and must close() it on
 * terminal events and teardown.
 */
export function connectImportProgressStream(
  jobId: string,
  handlers: ImportProgressStreamHandlers
): ReconnectingEventSource {
  const es = new ReconnectingEventSource(`/api/v2/import/jobs/${jobId}/progress`);

  es.onreconnectfailed = attempts => {
    if (handlers.onStalled && shouldReconcileStalledStream(attempts)) handlers.onStalled();
  };

  es.addEventListener('progress', (event: Event) => {
    const progress = parseProgressEvent(event, 'progress');
    if (progress) handlers.onProgress(progress);
  });

  es.addEventListener('complete', (event: Event) => {
    handlers.onComplete(parseProgressEvent(event, 'complete'));
  });

  es.addEventListener('cancelled', (event: Event) => {
    handlers.onCancelled(parseProgressEvent(event, 'cancelled'));
  });

  es.addEventListener('error', (event: Event) => {
    if (!(event instanceof MessageEvent) || typeof event.data !== 'string') {
      return;
    }
    let progress: ImportProgress | null = null;
    try {
      const data = JSON.parse(event.data) as ImportErrorEvent;
      progress = {
        total: data.total,
        processed: data.processed,
        inserted: data.inserted,
        skipped: data.skipped,
        errors: data.errors,
        phase: data.phase,
      };
    } catch (e) {
      logger.error('Failed to parse import error event', e);
    }
    handlers.onError(progress);
  });

  return es;
}
