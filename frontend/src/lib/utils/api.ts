/**
 * API utilities for making HTTP requests with CSRF protection
 */

/**
 * Custom error class for API errors
 */
export class ApiError extends Error {
  status: number;
  response: Response;
  userMessage: string;

  constructor(message: string, status: number, response: Response) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.response = response;
    this.userMessage = message;
  }
}

/**
 * Get CSRF token from meta tag or cookie
 */
export function getCsrfToken(): string | null {
  // First try meta tag (primary source)
  const metaTag = document.querySelector('meta[name="csrf-token"]');
  if (metaTag) {
    const token = metaTag.getAttribute('content');
    if (token) return token;
  }

  // Fallback to cookie (though it's HttpOnly so this won't work)
  const cookies = document.cookie.split(';');
  for (const cookie of cookies) {
    const [name, value] = cookie.trim().split('=');
    if (name === 'csrf') {
      return decodeURIComponent(value);
    }
  }
  return null;
}

/**
 * Default headers for API requests
 */
function getDefaultHeaders(): Headers {
  const headers = new Headers({
    'Content-Type': 'application/json',
  });

  const csrfToken = getCsrfToken();
  if (csrfToken) {
    headers.set('X-CSRF-Token', csrfToken);
  }

  return headers;
}

/**
 * Handle API response and errors
 */
async function handleResponse<T = unknown>(response: Response): Promise<T> {
  if (!response.ok) {
    let errorMessage = `HTTP ${response.status}: ${response.statusText}`;

    try {
      const errorData = await response.json();
      if (errorData.error) {
        errorMessage = errorData.error;
      } else if (errorData.message) {
        errorMessage = errorData.message;
      }
    } catch {
      // If response is not JSON, use default error message
    }

    throw new ApiError(errorMessage, response.status, response);
  }

  // Handle empty responses
  const contentLength = response.headers.get('content-length');
  if (contentLength === '0' || response.status === 204) {
    return null as T;
  }

  // Try to parse as JSON
  const contentType = response.headers.get('content-type');
  if (contentType?.includes('application/json')) {
    return response.json() as Promise<T>;
  }

  // Return text for other content types
  return response.text() as Promise<T>;
}

/**
 * Fetch options with proper typing
 */
interface FetchOptions extends Omit<RequestInit, 'body'> {
  body?: unknown;
}

/**
 * Fetch with CSRF token and error handling
 */
export async function fetchWithCSRF<T = unknown>(
  url: string,
  options: FetchOptions = {}
): Promise<T> {
  const defaultOptions: RequestInit = {
    method: 'GET',
    headers: getDefaultHeaders(),
    credentials: 'same-origin',
  };

  // Merge headers
  if (options.headers) {
    const customHeaders = new Headers(options.headers);
    const defaultHeaders = defaultOptions.headers as Headers;

    // Copy custom headers to default headers
    for (const [key, value] of customHeaders) {
      defaultHeaders.set(key, value);
    }

    options.headers = defaultHeaders;
  }

  // For non-GET requests with body, ensure it's stringified
  let body: BodyInit | undefined;
  if (options.body) {
    if (options.body instanceof FormData || typeof options.body === 'string') {
      body = options.body as BodyInit;
    } else {
      body = JSON.stringify(options.body);
    }
  }

  const finalOptions: RequestInit = {
    ...defaultOptions,
    ...options,
    body,
  };

  try {
    const response = await fetch(url, finalOptions);
    const result = await handleResponse<T>(response);
    return result;
  } catch (error) {
    if (error instanceof ApiError) {
      handleApiError(error);
    }
    throw error;
  }
}

/**
 * Standardized error handling
 */
export function handleApiError(error: Error): void {
  // Log error for debugging - disabled for production
  // console.error('API Error:', error);

  // Return user-friendly error messages
  if (error instanceof ApiError) {
    if (error.name === 'AbortError') {
      error.userMessage = 'Request was cancelled';
    } else if (error.status === 401) {
      error.userMessage = 'You need to log in to access this resource';
    } else if (error.status === 403) {
      error.userMessage = 'You do not have permission to access this resource';
    } else if (error.status === 404) {
      error.userMessage = 'The requested resource was not found';
    } else if (error.status >= 500) {
      error.userMessage = 'A server error occurred. Please try again later';
    } else if (!navigator.onLine) {
      error.userMessage = 'No internet connection';
    }
  }
}

/**
 * API client with common HTTP methods
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
