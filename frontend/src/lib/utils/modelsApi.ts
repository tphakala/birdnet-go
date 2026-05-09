// API client for the model gallery endpoints.
//
// Uses the shared api utility for CSRF-protected fetch calls and
// ReconnectingEventSource for SSE progress streams.

import type { CatalogResponse, DownloadProgress, InstalledModel } from '$lib/types/models';
import { api } from '$lib/utils/api';
import { buildAppUrl } from '$lib/utils/urlHelpers';
import ReconnectingEventSource from 'reconnecting-eventsource';

const BASE = '/api/v2/models';

/** Fetch the full model catalog with install/compatibility status. */
export async function fetchCatalog(): Promise<CatalogResponse> {
  return api.get<CatalogResponse>(`${BASE}/catalog`);
}

/** Fetch all currently installed models. */
export async function fetchInstalled(): Promise<InstalledModel[]> {
  return api.get<InstalledModel[]>(`${BASE}/installed`);
}

/** Start an asynchronous model install. Returns once the server accepts the request. */
export async function installModel(id: string): Promise<void> {
  await api.post(`${BASE}/install/${encodeURIComponent(id)}`);
}

/** Remove an installed model from disk. */
export async function uninstallModel(id: string): Promise<void> {
  await api.delete(`${BASE}/installed/${encodeURIComponent(id)}`);
}

/**
 * Subscribe to SSE progress events for an ongoing model install.
 *
 * Returns a cleanup function that closes the EventSource connection.
 */
export function subscribeInstallProgress(
  id: string,
  onProgress: (state: DownloadProgress) => void,
  onComplete: () => void,
  onError: (err: string) => void
): () => void {
  const url = buildAppUrl(`${BASE}/install/${encodeURIComponent(id)}/progress`);
  const source = new ReconnectingEventSource(url, {
    max_retry_time: 5000,
  });

  source.addEventListener('progress', (event: Event) => {
    const messageEvent = event as MessageEvent;
    const data = JSON.parse(messageEvent.data as string) as DownloadProgress;
    onProgress(data);

    if (data.status === 'complete') {
      onComplete();
      source.close();
    } else if (data.status === 'failed') {
      onError(data.error ?? 'Unknown error');
      source.close();
    }
  });

  source.onerror = () => {
    // ReconnectingEventSource handles reconnection automatically.
    // Only surface a user-visible error if the connection is permanently lost,
    // which the caller can detect when progress stops arriving.
  };

  return () => source.close();
}
