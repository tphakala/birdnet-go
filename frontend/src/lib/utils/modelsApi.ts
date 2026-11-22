// API client for the model gallery endpoints.
//
// Uses the shared api utility for CSRF-protected fetch calls and
// ReconnectingEventSource for SSE progress streams.

import type {
  CatalogResponse,
  DownloadProgress,
  InstalledModel,
  ModelDocument,
  ModelRegionsResponse,
} from '$lib/types/models';
import { api } from '$lib/utils/api';
import { getLogger } from '$lib/utils/logger';
import { buildAppUrl } from '$lib/utils/urlHelpers';
import { ReconnectingEventSource } from '$lib/utils/ReconnectingEventSource';

const logger = getLogger('modelsApi');

const BASE = '/api/v2/models';

/**
 * The error_key of the 409 the install, reinstall and remove endpoints return while
 * another model operation runs.
 */
export const MODEL_OPERATION_IN_PROGRESS_KEY = 'analysis.gallery.errors.operationInProgress';

/** error_key of the 409 for a cancel that can no longer be honoured (the download finished or activation began). */
export const MODEL_CANCEL_UNAVAILABLE_KEY = 'analysis.gallery.errors.cancelUnavailable';

/**
 * The error_key of the 409 the install and reinstall endpoints return while a
 * requires-agreement document of the entry has no recorded agreement. Its
 * error_params carry `{name, documents: [{repo, kind, sha256}]}`.
 */
export const MODEL_AGREEMENT_REQUIRED_KEY = 'analysis.gallery.errors.agreementRequired';

/** Fetch the full model catalog with install/compatibility status. */
export async function fetchCatalog(): Promise<CatalogResponse> {
  return api.get<CatalogResponse>(`${BASE}/catalog`);
}

/** Fetch all currently installed models. */
export async function fetchInstalled(): Promise<InstalledModel[]> {
  return api.get<InstalledModel[]>(`${BASE}/installed`);
}

/**
 * Fetch the region selector data for the model gallery: the saved mode, what the
 * configured coordinates resolve to under "auto", the selectable regions, and the
 * per-family resolution. Auth-gated on the server; the raw coordinates are never
 * returned.
 */
export async function fetchModelRegions(): Promise<ModelRegionsResponse> {
  return api.get<ModelRegionsResponse>(`${BASE}/regions`);
}

/**
 * Fetch the SVG coverage map for a region slug, as raw markup for inline
 * rendering. The endpoint is public and returns image/svg+xml, which the shared
 * api util yields as text. Rejects (404) when a region has no map; the caller
 * treats that as "no map" and falls back to the text-only country list.
 */
export async function fetchRegionCoverageMap(slug: string): Promise<string> {
  return api.get<string>(`${BASE}/regions/${encodeURIComponent(slug)}/map`);
}

/**
 * Start an asynchronous model install. Returns once the server accepts the
 * request. When `variantId` is given the server installs (or switches to) that
 * hardware/regional variant; omitting it installs the entry's default variant.
 * `acceptedDocuments` lists the sha256 of each document the user agreed to.
 * Without a variant or an accepted document no body is sent.
 */
export async function installModel(
  id: string,
  variantId?: string,
  acceptedDocuments?: string[]
): Promise<void> {
  const body = {
    ...(variantId ? { variantId } : {}),
    ...(acceptedDocuments?.length ? { acceptedDocuments } : {}),
  };
  await api.post(
    `${BASE}/install/${encodeURIComponent(id)}`,
    Object.keys(body).length > 0 ? body : undefined
  );
}

/**
 * Ask the server to cancel the install or variant swap running for a model. The
 * outcome (cancelled, or complete when activation had already started) arrives over
 * the progress stream. Rejects with an ApiError, errorKey
 * 'analysis.gallery.errors.cancelUnavailable' when the download can no longer be
 * cancelled.
 */
export async function cancelInstall(id: string): Promise<void> {
  await api.post(`${BASE}/install/${encodeURIComponent(id)}/cancel`);
}

/** Remove an installed model from disk. */
export async function uninstallModel(id: string): Promise<void> {
  await api.delete(`${BASE}/installed/${encodeURIComponent(id)}`);
}

/**
 * Reinstall an already-installed model, re-downloading missing or corrupt files.
 * `acceptedDocuments` lists the sha256 of each document the user agreed to; a
 * body is sent only when it is not empty.
 */
export async function reinstallModel(id: string, acceptedDocuments?: string[]): Promise<void> {
  const body = acceptedDocuments?.length ? { acceptedDocuments } : undefined;
  await api.post(`${BASE}/reinstall/${encodeURIComponent(id)}`, body);
}

/**
 * Fetch the verified text of a hosted licence or policy document by its sha256.
 * Rejects with an ApiError whose errorKey is
 * 'analysis.gallery.errors.documentUnavailable' when the server could not fetch
 * and verify the text.
 */
export async function fetchModelDocument(sha256: string): Promise<ModelDocument> {
  return api.get<ModelDocument>(`${BASE}/documents/${encodeURIComponent(sha256)}`);
}

// The server reports model-download failures as raw strings (EnhancedError.Error()
// forwards the bare message). These three shapes are exactly the CategoryNetwork
// download failures a mirror endpoint can remedy (internal/classifier/model_manager.go):
//   "HTTP request failed for <url>: <cause>"  (DNS, connection refused, TLS, timeout)
//   "HTTP <status> for <url>"                 (host blocked the request or rate-limited)
//   "read error downloading <url>: <cause>"   (connection dropped mid-transfer)
// Deliberately NOT matched: the frontend-generated "Connection to server lost"
// (the BirdNET-Go server dropped, not the model host, so a mirror will not help),
// checksum/disk errors, and generic install-timeout messages. A miss only omits the
// mirror hint, so the check fails safe. A structured error category on the download
// state is the cleaner long-term fix and is tracked separately.
const NETWORK_DOWNLOAD_ERROR = /HTTP request failed for |HTTP \d{3} for |read error downloading /;

/** True when a model-install failure message looks like a reachability problem the
 * configurable download source (mirror endpoint) could work around. */
export function isNetworkDownloadError(message: string): boolean {
  return NETWORK_DOWNLOAD_ERROR.test(message);
}

/**
 * Subscribe to SSE progress events for an ongoing model install. `onCancelled` is
 * called when the install ended because the user cancelled it.
 *
 * Returns a cleanup function that closes the EventSource connection.
 */
export function subscribeInstallProgress(
  id: string,
  onProgress: (state: DownloadProgress) => void,
  onComplete: () => void,
  onError: (err: string) => void,
  onCancelled?: () => void
): () => void {
  const url = buildAppUrl(`${BASE}/install/${encodeURIComponent(id)}/progress`);
  const source = new ReconnectingEventSource(url, {
    max_retry_time: 5000,
  });

  let terminalReceived = false;

  source.addEventListener('progress', (event: Event) => {
    const messageEvent = event as MessageEvent;
    let data: DownloadProgress;
    try {
      const parsed: unknown = JSON.parse(messageEvent.data as string);
      if (!parsed || typeof parsed !== 'object') {
        throw new Error('Event data is not an object');
      }
      data = parsed as DownloadProgress;
    } catch (error) {
      logger.warn('Failed to parse SSE progress event', error, { component: 'modelsApi' });
      return;
    }
    onProgress(data);
    errorCount = 0;

    if (data.status === 'complete') {
      terminalReceived = true;
      onComplete();
      source.close();
    } else if (data.status === 'failed') {
      terminalReceived = true;
      onError(data.error ?? 'Unknown error');
      source.close();
    } else if (data.status === 'cancelled') {
      // A user cancel is not an error: it never reaches onError, so the mirror hint
      // (isNetworkDownloadError) is never consulted.
      terminalReceived = true;
      onCancelled?.();
      source.close();
    }
  });

  let errorCount = 0;
  source.onerror = () => {
    errorCount++;
    if (!terminalReceived && errorCount > 3) {
      onError('Connection to server lost');
      source.close();
    }
  };

  return () => source.close();
}
