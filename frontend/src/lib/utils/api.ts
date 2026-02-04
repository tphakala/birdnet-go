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
import { getCsrfToken as getAppStateCsrfToken } from '$lib/stores/appState.svelte';
import { buildAppUrl } from '$lib/utils/urlHelpers';

const logger = loggers.api;

// SECURITY: Define request limits to prevent DoS
const MAX_REQUEST_SIZE = 10 * 1024 * 1024; // 10MB
const DEFAULT_TIMEOUT = 30000; // 30 seconds
const CSRF_HEADER_NAME = 'X-CSRF-Token';

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
function getSecureErrorMessage(status: number, _serverMessage?: string): string {
  // SECURITY: Never expose internal server errors to prevent information leakage
  switch (status) {
    case 400:
      return 'Invalid request. Please check your input and try again.';
    case 401:
      return 'Authentication required. Please log in and try again.';
    case 403:
      return 'You do not have permission to perform this action.';
    case 404:
      return 'The requested resource was not found.';
    case 409:
      return 'This action conflicts with the current state. Please refresh and try again.';
    case 422:
      return 'Invalid input provided. Please check your data and try again.';
    case 429:
      return 'Too many requests. Please wait before trying again.';
    case 500:
    case 502:
    case 503:
    case 504:
      return 'A server error occurred. Please try again later.';
    default:
      // SECURITY: For unknown errors, provide generic message
      if (status >= 400 && status < 500) {
        return 'Client error occurred. Please check your request and try again.';
      } else if (status >= 500) {
        return 'Server error occurred. Please try again later.';
      }
      return 'An unexpected error occurred. Please try again.';
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
        // Only use server message for specific safe cases
        if (response.status === 422 && errorData.validationErrors) {
          // For validation errors, we can show field-specific messages
          serverMessage = 'Validation failed. Please check your input.';
        } else if (response.status === 409 && errorData.conflict) {
          serverMessage = 'This action conflicts with existing data.';
        }
        // For all other cases, use secure generic messages
      }
    } catch (parseError) {
      // If we can't parse the error response, use generic message
      logger.debug('Could not parse error response:', parseError);
    }

    // SECURITY: Always use secure error messages
    const secureMessage = getSecureErrorMessage(response.status, serverMessage);
    throw new ApiError(secureMessage, response.status, response);
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
      } else if (value instanceof File) {
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

  const finalOptions: RequestInit = {
    ...defaultOptions,
    ...options,
    body,
  };

  try {
    logger.debug(`Fetching ${finalOptions.method} ${url}`);
    const response = await fetch(buildAppUrl(url), finalOptions);

    clearTimeout(timeoutId);
    const result = await handleResponse<T>(response);
    logger.debug(`Response from ${url} received successfully`);
    return result;
  } catch (error) {
    clearTimeout(timeoutId);

    if (error instanceof ApiError) {
      // Re-throw API errors as-is (they already have secure messages)
      throw error;
    }

    // SECURITY: Handle network and other errors securely
    if (error instanceof Error) {
      if (error.name === 'AbortError') {
        throw new ApiError('Request timeout', 408, new Response(), true);
      }
      if (error.message.includes('Failed to fetch') || error.message.includes('NetworkError')) {
        throw new ApiError(
          'Network error occurred. Please check your connection.',
          0,
          new Response(),
          true
        );
      }
    }

    // SECURITY: Generic error for unknown cases
    throw new ApiError('An unexpected error occurred. Please try again.', 0, new Response());
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
