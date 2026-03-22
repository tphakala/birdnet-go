/**
 * Frontend Sentry integration for BirdNET-Go.
 *
 * Lazy-loaded only when telemetry is enabled. Provides:
 * - Global error capture (window.onerror, unhandledrejection)
 * - API error capture with severity mapping
 * - Privacy-first beforeSend filtering
 */
import * as Sentry from '@sentry/browser';

/** Configuration passed from appState after config fetch. */
export interface SentryConfig {
  dsn: string;
  systemId: string;
  version: string;
}

/** API error shape matching ApiError from api.ts. */
interface ApiErrorLike {
  message: string;
  status: number;
  isNetworkError: boolean;
}

/**
 * Initialize Sentry with privacy filtering.
 * Called once from appState.svelte.ts when telemetry is enabled.
 */
export function initSentry(config: SentryConfig): void {
  Sentry.init({
    dsn: config.dsn,
    release: `birdnet-go@${config.version}`,
    environment: 'production',
    sampleRate: 1.0,
    tracesSampleRate: 0,
    maxBreadcrumbs: 20,
    beforeSend,
    initialScope: {
      tags: {
        systemId: config.systemId,
        source: 'frontend',
      },
    },
  });
}

/**
 * Capture an API error with structured context.
 * Skips auth errors (401/403) as they are expected flow, not bugs.
 */
export function captureApiError(error: ApiErrorLike, context?: Record<string, string>): void {
  // Skip auth-related errors — these are expected flow, not bugs
  if (error.status === 401 || error.status === 403) return;

  const severity = error.isNetworkError || error.status >= 500 ? 'error' : 'warning';

  Sentry.withScope(scope => {
    scope.setLevel(severity);
    scope.setTag('error.type', 'api');

    if (context) {
      // Scrub endpoint URL to path only
      const scrubbed = { ...context };
      if (scrubbed.endpoint) {
        try {
          const url = new URL(scrubbed.endpoint, globalThis.location.origin);
          scrubbed.endpoint = url.pathname;
        } catch {
          scrubbed.endpoint = '[scrubbed]';
        }
      }
      scope.setContext('api', scrubbed);
    }

    if (error.status) {
      scope.setTag('http.status_code', String(error.status));
    }
    if (error.isNetworkError) {
      scope.setTag('error.network', 'true');
    }

    Sentry.captureException(error);
  });
}

/**
 * Capture a non-API error from logger.error() calls.
 * Used via dependency injection from logger.ts to avoid circular imports.
 */
export function captureError(
  error: Error,
  context?: { category?: string; [key: string]: unknown }
): void {
  Sentry.withScope(scope => {
    scope.setLevel('error');
    scope.setTag('error.type', 'logger');
    if (context?.category) {
      scope.setTag('logger.category', context.category);
    }
    if (context) {
      const rest = Object.fromEntries(
        Object.entries(context).filter(([key]) => key !== 'category')
      );
      if (Object.keys(rest).length > 0) {
        scope.setContext('logger', rest);
      }
    }
    Sentry.captureException(error);
  });
}

/**
 * Privacy-first event filtering. Scrubs PII before events leave the browser.
 */
function beforeSend(event: Sentry.ErrorEvent, _hint: Sentry.EventHint): Sentry.ErrorEvent | null {
  // 1. Strip user data (Sentry auto-collects IP)
  delete event.user;

  // 2. Strip server name
  delete event.server_name;

  // 3. Scrub main event request — remove query params, headers, cookies, body
  if (event.request) {
    if (event.request.url) {
      try {
        const url = new URL(event.request.url, globalThis.location.origin);
        event.request.url = url.pathname;
      } catch {
        event.request.url = '[scrubbed]';
      }
    }
    delete event.request.data;
    delete event.request.cookies;
    delete event.request.headers;
  }

  // 4. Scrub breadcrumb URLs and bodies
  if (event.breadcrumbs) {
    for (const breadcrumb of event.breadcrumbs) {
      if (breadcrumb.category === 'fetch' || breadcrumb.category === 'xhr') {
        if (breadcrumb.data?.url) {
          try {
            const url = new URL(breadcrumb.data.url, globalThis.location.origin);
            breadcrumb.data.url = url.pathname;
          } catch {
            breadcrumb.data.url = '[scrubbed]';
          }
        }
      }
      // 5. Strip request/response bodies from breadcrumb data
      if (breadcrumb.data) {
        delete breadcrumb.data.request_body;
        delete breadcrumb.data.response_body;
        delete breadcrumb.data.body;
      }
    }
  }

  return event;
}
