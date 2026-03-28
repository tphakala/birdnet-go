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

/** HTTP status codes for expected-flow errors (not bugs). */
const HTTP_UNAUTHORIZED = 401;
const HTTP_FORBIDDEN = 403;
const HTTP_CONFLICT = 409;

/** API error shape matching ApiError from api.ts. */
interface ApiErrorLike {
  message: string;
  status: number;
  isNetworkError: boolean;
}

/** Check whether an unknown value looks like an ApiError with an expected-flow status. */
function isExpectedApiError(err: unknown): boolean {
  if (typeof err !== 'object' || err === null) return false;
  const status = (err as Record<string, unknown>).status;
  return status === HTTP_UNAUTHORIZED || status === HTTP_FORBIDDEN || status === HTTP_CONFLICT;
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
 * Skips expected-flow errors (401/403/409) as they are not bugs.
 */
export function captureApiError(error: ApiErrorLike, context?: Record<string, string>): void {
  // Skip expected-flow errors — auth (401/403) and conflict (409, e.g. v2 database not available)
  if (isExpectedApiError(error)) return;

  const severity = error.isNetworkError || error.status >= 500 ? 'error' : 'warning';

  Sentry.withScope(scope => {
    scope.setLevel(severity);
    scope.setTag('error.type', 'api');

    if (context) {
      // Scrub endpoint URL — strip query params; preserve domain for external APIs
      const scrubbed = { ...context };
      if (scrubbed.endpoint) {
        scrubbed.endpoint = scrubUrl(scrubbed.endpoint);
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
  // Skip expected-flow errors — 401/403/409 are not bugs
  if (isExpectedApiError(error)) return;

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
        scope.setContext('logger', scrubContext(rest));
      }
    }
    Sentry.captureException(error);
  });
}

/** Property names whose values are always redacted (case-insensitive match). */
const SENSITIVE_KEYS =
  /^(token|password|secret|apikey|api_key|authorization|cookie|session|sessionid|session_id|ip|ip_address|email|credentials?)$/i;

/** Heuristic: does a string value look like a URL? */
function looksLikeUrl(value: string): boolean {
  return /^https?:\/\//i.test(value) || (value.startsWith('/') && value.includes('/', 1));
}

/**
 * Scrub a single context entry for PII.
 * - Sensitive key names are redacted entirely.
 * - String values that look like URLs are run through scrubUrl().
 * - Everything else passes through unchanged.
 */
function scrubContextValue(key: string, value: unknown): unknown {
  if (SENSITIVE_KEYS.test(key)) return '[redacted]';
  if (typeof value === 'string' && looksLikeUrl(value)) return scrubUrl(value);
  return value;
}

/** Scrub all entries in a context record for PII before sending to Sentry. */
function scrubContext(context: Record<string, unknown>): Record<string, unknown> {
  const scrubbed: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(context)) {
    scrubbed[key] = scrubContextValue(key, value); // eslint-disable-line security/detect-object-injection -- iterating own Record entries
  }
  return scrubbed;
}

/** Scrub a URL for privacy: strip query params and fragments, keep path. */
function scrubUrl(raw: string): string {
  try {
    const url = new URL(raw, globalThis.location.origin);
    const isLocal = url.origin === globalThis.location.origin;
    return isLocal ? url.pathname : `${url.origin}${url.pathname}`;
  } catch {
    return '[scrubbed]';
  }
}

/**
 * Privacy-first event filtering. Scrubs PII before events leave the browser.
 * Also drops auth errors (401/403) which are expected when users aren't logged in.
 */
function beforeSend(event: Sentry.ErrorEvent, hint: Sentry.EventHint): Sentry.ErrorEvent | null {
  // Drop expected-flow errors — 401/403/409 can arrive via unhandled rejections or logger.error().
  if (isExpectedApiError(hint.originalException)) return null;

  // 1. Strip user data (Sentry auto-collects IP)
  delete event.user;

  // 2. Strip server name
  delete event.server_name;

  // 3. Scrub main event request — remove query params, headers, cookies, body
  if (event.request) {
    if (event.request.url) {
      event.request.url = scrubUrl(event.request.url);
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
          breadcrumb.data.url = scrubUrl(breadcrumb.data.url);
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
