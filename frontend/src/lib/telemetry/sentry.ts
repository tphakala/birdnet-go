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

/** HTTP status codes for gateway/proxy errors (infrastructure, not app bugs). */
const HTTP_BAD_GATEWAY = 502;
const HTTP_SERVICE_UNAVAILABLE = 503;
const HTTP_GATEWAY_TIMEOUT = 504;

/** Minimum HTTP status considered a server-side error (5xx). */
const HTTP_SERVER_ERROR_MIN = 500;

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

/** Check whether an error is a gateway/proxy error (502/503/504). */
function isGatewayError(err: unknown): boolean {
  if (typeof err !== 'object' || err === null) return false;
  const status = (err as Record<string, unknown>).status;
  return (
    status === HTTP_BAD_GATEWAY ||
    status === HTTP_SERVICE_UNAVAILABLE ||
    status === HTTP_GATEWAY_TIMEOUT
  );
}

/**
 * Lowercase substrings identifying lazy-route asset/chunk load failures:
 * Safari: "Importing a module script failed."
 * Vite: "Unable to preload CSS for ..."
 * Chrome/Firefox: "Failed to fetch / error loading dynamically imported module"
 */
const ASSET_LOADING_ERROR_PATTERNS = [
  'importing a module script failed',
  'unable to preload css',
  'dynamically imported module',
] as const;

/** Messages with zero diagnostic value (minified/anonymous callbacks, empty strings). */
const GENERIC_MESSAGES = new Set(['', '<anonymous>', 'callback']);

/** Lowercase substrings for browser-internal noise that is never an app bug. */
const NOISE_MESSAGE_PATTERNS = ['resizeobserver loop'] as const;

/** Check whether an error is a chunk/asset loading failure from lazy routes. */
function isAssetLoadingError(err: unknown): boolean {
  if (!(err instanceof Error)) return false;
  const msg = err.message.toLowerCase();
  return ASSET_LOADING_ERROR_PATTERNS.some(pattern => msg.includes(pattern));
}

/**
 * Check whether an error is a browser-level network TypeError.
 * Chrome/Firefox: "Failed to fetch", Safari: "Load failed",
 * older Firefox: "NetworkError when attempting to fetch resource".
 */
function isNetworkTypeError(err: unknown): boolean {
  if (!(err instanceof TypeError)) return false;
  const msg = err.message;
  return (
    msg.includes('Failed to fetch') || msg.includes('Load failed') || msg.includes('NetworkError')
  );
}

/** Check whether a normalized message matches known noise patterns. */
function isNoiseMessage(raw: string): boolean {
  const msg = raw.trim().toLowerCase();
  return GENERIC_MESSAGES.has(msg) || NOISE_MESSAGE_PATTERNS.some(p => msg.includes(p));
}

/** Check whether an error has no actionable diagnostic information. */
function hasNoDiagnosticValue(hint: Sentry.EventHint): boolean {
  const err = hint.originalException;
  if (typeof err === 'string') return isNoiseMessage(err);
  if (err instanceof Error) return isNoiseMessage(err.message);
  return false;
}

/**
 * URL scheme prefixes that identify a stack frame as injected browser-extension
 * or otherwise cross-origin-masked code rather than our own app bundle. Safari
 * rewrites extension and cross-origin script URLs to `webkit-masked-url://`;
 * Chromium and Firefox expose their extension origins directly.
 */
const EXTENSION_FRAME_PREFIXES = [
  'webkit-masked-url:',
  'chrome-extension:',
  'moz-extension:',
  'safari-extension:',
  'safari-web-extension:',
];

/** Does this stack-frame filename/path belong to injected extension code? */
function isExtensionFrame(location: string | undefined): boolean {
  if (!location) return false;
  return EXTENSION_FRAME_PREFIXES.some(prefix => location.startsWith(prefix));
}

/**
 * Check whether an error originates entirely from a browser extension. Safari
 * extensions (and other injected content scripts) run in the page context, so
 * their unhandled rejections are captured by our global handlers even though
 * they are not our code. When every stack frame is extension-injected, the
 * event carries no signal about BirdNET-Go and is dropped (Sentry
 * BIRDNET-GO-2EM: `shouldBeEnabled` at `webkit-masked-url://hidden/`).
 *
 * This assumes the app bundle is served same-origin, which is the standard
 * deployment: Safari's `webkit-masked-url:` masking then applies only to
 * extension and cross-origin scripts, so real BirdNET-Go frames keep their
 * `/ui/assets/...` paths and short-circuit the drop. A non-standard deployment
 * serving assets cross-origin without `Timing-Allow-Origin` would let Safari
 * mask app frames too, so a genuine error could be dropped; that is an accepted
 * loss of telemetry visibility, not a user-facing regression. Requiring an
 * explicit `*-extension:` frame instead would fail to drop the reported case,
 * whose frames are all `webkit-masked-url:`.
 */
function isBrowserExtensionError(event: Sentry.ErrorEvent): boolean {
  const values = event.exception?.values;
  if (!values || values.length === 0) return false;

  let sawFrame = false;
  for (const value of values) {
    const frames = value.stacktrace?.frames;
    if (!frames) continue;
    for (const frame of frames) {
      sawFrame = true;
      // Any frame from our own bundle means this is not pure extension noise.
      if (!isExtensionFrame(frame.filename) && !isExtensionFrame(frame.abs_path)) {
        return false;
      }
    }
  }

  return sawFrame;
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

  // Skip gateway errors — 502/503/504 are proxy/infrastructure issues, not app bugs
  if (isGatewayError(error)) return;

  // Skip network errors - connectivity issues are infrastructure noise, not app bugs
  if (error.isNetworkError) return;

  const severity = error.status >= HTTP_SERVER_ERROR_MIN ? 'error' : 'warning';

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

    if (error.status > 0) {
      scope.setFingerprint(['ApiError', String(error.status)]);
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

  // Skip network TypeErrors - connectivity issues are infrastructure noise, not app bugs
  if (isNetworkTypeError(error)) return;

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

  // Drop gateway errors — 502/503/504 from reverse proxies during backend restarts
  if (isGatewayError(hint.originalException)) return null;

  // Drop network TypeErrors (connectivity issues are infrastructure noise, not app bugs)
  if (isNetworkTypeError(hint.originalException)) return null;

  // Drop chunk/asset loading failures from lazy-loaded routes (stale manifests, transient network)
  if (isAssetLoadingError(hint.originalException)) return null;

  // Drop events with no diagnostic value (empty/generic messages, browser noise)
  if (hasNoDiagnosticValue(hint)) return null;

  // Drop errors thrown entirely by injected browser-extension code (Safari
  // masks these as webkit-masked-url); they are not BirdNET-Go bugs.
  if (isBrowserExtensionError(event)) return null;

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

  // Fingerprint API errors by type+status to merge locale variants into one Sentry issue.
  // The 'isNetworkError' property discriminates ApiErrorLike from other error types.
  const origErr = hint.originalException;
  if (
    origErr &&
    typeof origErr === 'object' &&
    'isNetworkError' in origErr &&
    'status' in origErr
  ) {
    const status = (origErr as Record<string, unknown>).status;
    if (typeof status === 'number' && status > 0) {
      event.fingerprint = ['ApiError', String(status)];
    }
  }

  return event;
}
