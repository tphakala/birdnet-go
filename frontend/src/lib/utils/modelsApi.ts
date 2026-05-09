// API client for the model gallery endpoints.
//
// Uses the shared api utility for CSRF-protected fetch calls and
// ReconnectingEventSource for SSE progress streams.

import type { CatalogResponse, DownloadProgress, InstalledModel } from '$lib/types/models';
import { api } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';
import { buildAppUrl } from '$lib/utils/urlHelpers';
import ReconnectingEventSource from 'reconnecting-eventsource';

const logger = loggers.api;

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

  let terminalReceived = false;

  source.addEventListener('progress', (event: Event) => {
    const messageEvent = event as MessageEvent;
    let data: DownloadProgress;
    try {
      data = JSON.parse(messageEvent.data as string) as DownloadProgress;
    } catch (error) {
      logger.warn('Failed to parse SSE progress event', error, { component: 'modelsApi' });
      return;
    }
    onProgress(data);

    if (data.status === 'complete') {
      terminalReceived = true;
      onComplete();
      source.close();
    } else if (data.status === 'failed') {
      terminalReceived = true;
      onError(data.error ?? 'Unknown error');
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
