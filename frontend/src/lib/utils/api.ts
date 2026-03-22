/**
 * SECURITY-HARDENED API utilities for making HTTP requests with CSRF protection
 *
 * Security improvements:
 * - Secure error message handling (prevents information leakage)
 * - Request validation and sanitization
 * - Enhanced CSRF protection
 * - Timeout controls
 * - Request size limits
 */

import { loggers } from '$lib/utils/logger';
import {
  getCsrfToken as getAppStateCsrfToken,
  isSentryEnabled,
  refreshCsrfToken,
} from '$lib/stores/appState.svelte';
import { buildAppUrl } from '$lib/utils/urlHelpers';
import { t } from '$lib/i18n';
import { markOnline } from '$lib/stores/connectionState.svelte';

const logger = loggers.api;

// SECURITY: Define request limits to prevent DoS
const MAX_REQUEST_SIZE = 10 * 1024 * 1024; // 10MB
const DEFAULT_TIMEOUT = 30000; // 30 seconds
const CSRF_HEADER_NAME = 'X-CSRF-Token';

// Lazy Sentry capture — only loaded when telemetry is enabled.
// Uses ApiErrorLike interface to avoid coupling to the ApiError class.
type ApiErrorLike = { message: string; status: number; isNetworkError: boolean };
let _captureApiError: ((error: ApiErrorLike, context?: Record<string, string>) => void) | null =
  null;
let _captureAttempted = false;

async function getSentryCaptureApiError(): Promise<typeof _captureApiError> {
  // Check opt-in state first — if disabled (or not yet initialized), return null
  // without caching the failure, so we can retry after initApp() completes.
  if (!isSentryEnabled()) return null;

  if (_captureAttempted) return _captureApiError;
  _captureAttempted = true;
  try {
    const mod = await import('$lib/telemetry/sentry');
    _captureApiError = mod.captureApiError;
  } catch {
    // Sentry not available — allow retry on later errors
    _captureAttempted = false;
  }
  return _captureApiError;
}

/**
 * Custom error class for API errors with secure messaging
 */
export class ApiError extends Error {
  status: number;
  response: Response;
  userMessage: string;
  isNetworkError: boolean;

  constructor(message: string, status: number, response: Response, isNetworkError = false) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.response = response;
    this.userMessage = message;
    this.isNetworkError = isNetworkError;
  }
}

/**
 * SECURITY: CSRF token retrieval from centralized app state.
 * The token is fetched from /api/v2/app/config during app initialization
 * and stored in appState.
 *
 * @returns The CSRF token or null if not available
 */
export function getCsrfToken(): string | null {
  const token = getAppStateCsrfToken();
  return token && token.length > 0 ? token : null;
}

/**
 * SECURITY: Enhanced default headers with validation
 */
function getDefaultHeaders(): Headers {
  const headers = new Headers({
    'Content-Type': 'application/json',
  });

  const csrfToken = getCsrfToken();
  if (csrfToken) {
    headers.set(CSRF_HEADER_NAME, csrfToken);
  } else {
    logger.warn('No valid CSRF token found - request may be rejected');
  }

  return headers;
}

/**
 * SECURITY: Secure error message mapping to prevent information leakage
 */
function getSecureErrorMessage(status: number): string {
  // SECURITY: Never expose internal server errors to prevent information leakage
  switch (status) {
    case 400:
      return t('errors.api.badRequest');
    case 401:
      return t('errors.api.unauthorized');
    case 403:
      return t('errors.api.forbidden');
    case 404:
      return t('errors.api.notFound');
    case 409:
      return t('errors.api.conflict');
    case 422:
      return t('errors.api.validationFailed');
    case 429:
      return t('errors.api.tooManyRequests');
    case 500:
    case 502:
    case 503:
    case 504:
      return t('errors.api.serverError');
    default:
      // SECURITY: For unknown errors, provide generic message
      if (status >= 400 && status < 500) {
        return t('errors.api.clientError');
      } else if (status >= 500) {
        return t('errors.api.serverError');
      }
      return t('errors.api.unknownError');
  }
}

/**
 * SECURITY: Enhanced response handler with secure error messaging
 */
async function handleResponse<T = unknown>(response: Response): Promise<T> {
  if (!response.ok) {
    let serverMessage = '';

    try {
      // SECURITY: Limit response size for error parsing
      const contentLength = response.headers.get('content-length');
      if (contentLength && parseInt(contentLength) > 1024 * 1024) {
        // 1MB limit
        throw new Error('Error response too large');
      }

      const errorData = await response.json();

      // SECURITY: Only extract safe error fields - never expose raw server errors
      if (errorData && typeof errorData === 'object') {
        // Try i18n translation via error_key first
        if (errorData.error_key && typeof errorData.error_key === 'string') {
          const params =
            errorData.error_params &&
            typeof errorData.error_params === 'object' &&
            !Array.isArray(errorData.error_params)
              ? (errorData.error_params as Record<string, unknown>)
              : {};
          const translated = t(errorData.error_key, params);
          if (translated !== errorData.error_key) {
            serverMessage = translated;
          }
        }

        // Fall back to specific safe cases for 422/409
        if (!serverMessage) {
          if (response.status === 422 && errorData.validationErrors) {
            serverMessage = t('errors.api.validationCheck');
          } else if (response.status === 409 && errorData.conflict) {
            serverMessage = t('errors.api.conflictData');
          }
        }
      }
    } catch (parseError) {
      // If we can't parse the error response, use generic message
      logger.debug('Could not parse error response:', parseError);
    }

    // Use translated message if available, otherwise fall back to secure generic message
    const userMessage = serverMessage || getSecureErrorMessage(response.status);
    throw new ApiError(userMessage, response.status, response);
  }

  // Handle empty responses
  const contentLength = response.headers.get('content-length');
  if (contentLength === '0' || response.status === 204) {
    return null as T;
  }

  // SECURITY: Validate content type before parsing
  const contentType = response.headers.get('content-type');
  if (contentType?.includes('application/json')) {
    // SECURITY: Limit JSON response size
    if (contentLength && parseInt(contentLength) > MAX_REQUEST_SIZE) {
      throw new ApiError('Response too large', 413, response);
    }
    return response.json() as Promise<T>;
  }

  // Return text for other content types (with size limit)
  if (contentLength && parseInt(contentLength) > MAX_REQUEST_SIZE) {
    throw new ApiError('Response too large', 413, response);
  }
  return response.text() as Promise<T>;
}

/**
 * SECURITY: Request body validation and sanitization
 */
function validateAndSanitizeBody(body: unknown): string | FormData | undefined {
  if (!body) return undefined;

  if (body instanceof FormData) {
    // SECURITY: Validate FormData size with encoding overhead buffer
    let totalSize = 0;
    for (const [, value] of body) {
      if (typeof value === 'string') {
        totalSize += value.length;
      } else if (value instanceof Blob) {
        // Blob covers both Blob and File (File extends Blob)
        totalSize += value.size;
      }
    }

    // Add 25% buffer to account for FormData encoding overhead (boundaries, headers, etc.)
    const estimatedEncodedSize = Math.floor(totalSize * 1.25);
    if (estimatedEncodedSize > MAX_REQUEST_SIZE) {
      throw new Error('Request body too large');
    }
    return body;
  }

  if (typeof body === 'string') {
    // SECURITY: Validate string body size
    if (body.length > MAX_REQUEST_SIZE) {
      throw new Error('Request body too large');
    }
    return body;
  }

  // For objects, stringify and validate
  const jsonString = JSON.stringify(body);
  if (jsonString.length > MAX_REQUEST_SIZE) {
    throw new Error('Request body too large');
  }

  return jsonString;
}

/**
 * Fetch options with proper typing
 */
interface FetchOptions extends Omit<RequestInit, 'body'> {
  body?: unknown;
  timeout?: number;
  /** Internal flag to prevent infinite CSRF retry loops */
  _csrfRetried?: boolean;
}

/**
 * SECURITY: Enhanced fetch with comprehensive protection
 */
export async function fetchWithCSRF<T = unknown>(
  url: string,
  options: FetchOptions = {}
): Promise<T> {
  // SECURITY: Validate URL
  if (!url || typeof url !== 'string') {
    throw new ApiError('Invalid URL provided', 400, new Response());
  }

  // SECURITY: Enhanced SSRF protection with comprehensive URL validation
  if (url.includes('//') && !url.startsWith('/')) {
    // For absolute URLs, perform strict validation
    try {
      const parsedUrl = new URL(url);

      // Only allow http and https protocols
      if (!['http:', 'https:'].includes(parsedUrl.protocol)) {
        throw new ApiError(
          'Invalid URL protocol - only http and https allowed',
          400,
          new Response()
        );
      }

      // Ensure URL points to same origin
      if (parsedUrl.origin !== window.location.origin) {
        throw new ApiError('Cross-origin requests not allowed', 400, new Response());
      }

      // Additional hostname validation
      const hostname = parsedUrl.hostname;
      if (
        hostname === 'localhost' ||
        hostname === '127.0.0.1' ||
        hostname.startsWith('192.168.') ||
        hostname.startsWith('10.') ||
        hostname.startsWith('172.')
      ) {
        // Only allow if it matches current origin
        if (parsedUrl.origin !== window.location.origin) {
          throw new ApiError('Private network access not allowed', 400, new Response());
        }
      }
    } catch (urlError) {
      if (urlError instanceof ApiError) throw urlError;
      throw new ApiError('Malformed URL', 400, new Response());
    }
  }

  const timeout = options.timeout ?? DEFAULT_TIMEOUT;
  const controller = new AbortController();

  // SECURITY: Set up timeout
  const timeoutId = setTimeout(() => {
    controller.abort();
  }, timeout);

  const defaultOptions: RequestInit = {
    method: 'GET',
    headers: getDefaultHeaders(),
    credentials: 'same-origin',
    signal: controller.signal,
  };

  // Merge headers securely
  if (options.headers) {
    const customHeaders = new Headers(options.headers);
    const defaultHeaders = defaultOptions.headers as Headers;

    // SECURITY: Validate custom headers
    for (const [key, value] of customHeaders) {
      // Prevent header injection
      if (
        key.includes('\n') ||
        key.includes('\r') ||
        value.includes('\n') ||
        value.includes('\r')
      ) {
        throw new ApiError('Invalid header format', 400, new Response());
      }
      defaultHeaders.set(key, value);
    }

    options.headers = defaultHeaders;
  }

  // SECURITY: Validate and sanitize body
  let body: BodyInit | undefined;
  try {
    body = validateAndSanitizeBody(options.body) as BodyInit;
  } catch {
    // Intentionally not using the error to avoid information leakage
    throw new ApiError('Invalid request body', 400, new Response());
  }

  // Wire caller signal AFTER all synchronous validations pass, so the
  // listener is only attached when we're about to actually fetch.
  let onCallerAbort: (() => void) | null = null;
  if (options.signal) {
    if (options.signal.aborted) {
      controller.abort();
    } else {
      onCallerAbort = () => controller.abort();
      options.signal.addEventListener('abort', onCallerAbort, { once: true });
    }
  }

  // Strip signal from options so caller's signal doesn't overwrite the internal one
  const { signal: _ignored, ...restOptions } = options;
  void _ignored; // Consumed via addEventListener above; destructured only to exclude from spread

  const finalOptions: RequestInit = {
    ...defaultOptions,
    ...restOptions,
    body,
  };

  try {
    logger.debug(`Fetching ${finalOptions.method} ${url}`);
    const response = await fetch(buildAppUrl(url), finalOptions);

    clearTimeout(timeoutId);

    // Any HTTP response (even 4xx/5xx) proves the backend is reachable
    markOnline();

    // If 403 on a state-changing request, the CSRF token may have expired.
    // Refresh the token and retry once.
    const method = (finalOptions.method ?? 'GET').toUpperCase();
    if (
      response.status === 403 &&
      method !== 'GET' &&
      method !== 'HEAD' &&
      method !== 'OPTIONS' &&
      !options._csrfRetried
    ) {
      logger.warn('Got 403 on state-changing request, attempting CSRF token refresh');
      const refreshed = await refreshCsrfToken();
      if (refreshed) {
        return fetchWithCSRF<T>(url, { ...options, _csrfRetried: true });
      }
    }

    const result = await handleResponse<T>(response);
    logger.debug(`Response from ${url} received successfully`);
    return result;
  } catch (error) {
    clearTimeout(timeoutId);

    // Fire-and-forget Sentry capture — never blocks error flow
    const method = (finalOptions.method ?? 'GET').toUpperCase();
    if (error instanceof ApiError) {
      getSentryCaptureApiError().then(capture => {
        capture?.(error, { endpoint: url, method });
      });
    } else if (error instanceof Error) {
      // Skip caller-initiated cancellation (expected flow), capture everything else
      // including internal timeouts (AbortError from our timeout, not caller's signal)
      const isCallerAbort = error.name === 'AbortError' && options.signal?.aborted;
      if (!isCallerAbort) {
        getSentryCaptureApiError().then(capture => {
          capture?.(
            { message: error.message, status: 0, isNetworkError: true },
            { endpoint: url, method }
          );
        });
      }
    }

    if (error instanceof ApiError) {
      // Re-throw API errors as-is (they already have secure messages)
      throw error;
    }

    // SECURITY: Handle network and other errors securely
    if (error instanceof Error) {
      if (error.name === 'AbortError') {
        // Distinguish caller-initiated cancellation from internal timeout
        if (options.signal?.aborted) {
          throw error; // Preserve original AbortError for caller cancellation
        }
        throw new ApiError(t('errors.api.requestTimeout'), 408, new Response(), true);
      }
      if (error.message.includes('Failed to fetch') || error.message.includes('NetworkError')) {
        throw new ApiError(t('errors.api.networkError'), 0, new Response(), true);
      }
    }

    // SECURITY: Generic error for unknown cases
    throw new ApiError(t('errors.api.unknownError'), 0, new Response());
  } finally {
    // Clean up caller abort listener to prevent accumulation on long-lived signals
    if (options.signal && onCallerAbort) {
      options.signal.removeEventListener('abort', onCallerAbort);
    }
  }
}

/**
 * API client with common HTTP methods and enhanced security
 */
export const api = {
  /**
   * GET request
   */
  get<T = unknown>(url: string, options: FetchOptions = {}): Promise<T> {
    return fetchWithCSRF<T>(url, { ...options, method: 'GET' });
  },

  /**
   * POST request
   */
  post<T = unknown>(url: string, data?: unknown, options: FetchOptions = {}): Promise<T> {
    return fetchWithCSRF<T>(url, { ...options, method: 'POST', body: data });
  },

  /**
   * PUT request
   */
  put<T = unknown>(url: string, data?: unknown, options: FetchOptions = {}): Promise<T> {
    return fetchWithCSRF<T>(url, { ...options, method: 'PUT', body: data });
  },

  /**
   * PATCH request
   */
  patch<T = unknown>(url: string, data?: unknown, options: FetchOptions = {}): Promise<T> {
    return fetchWithCSRF<T>(url, { ...options, method: 'PATCH', body: data });
  },

  /**
   * DELETE request
   */
  delete<T = unknown>(url: string, options: FetchOptions = {}): Promise<T> {
    return fetchWithCSRF<T>(url, { ...options, method: 'DELETE' });
  },
};

/**
 * Create an abort controller for cancellable requests
 */
export function createAbortController(): AbortController {
  return new AbortController();
}
