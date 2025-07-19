/**
 * csrf.ts
 *
 * CSRF (Cross-Site Request Forgery) token management store for secure API requests.
 * Handles token retrieval, storage, and automatic inclusion in request headers.
 *
 * Usage:
 * - API utilities that need CSRF protection
 * - Form submissions requiring CSRF tokens
 * - Any server communication that modifies state
 * - Header generation for fetch requests
 *
 * Features:
 * - Multiple token source support (meta tags, cookies)
 * - Automatic token retrieval and caching
 * - Header generation for API requests
 * - Fallback mechanisms for token availability
 *
 * Security:
 * - Prevents CSRF attacks on state-changing operations
 * - Validates server-generated tokens
 * - Integrates with server-side CSRF middleware
 * - Follows OWASP CSRF prevention guidelines
 *
 * Token Sources (in priority order):
 * 1. HTML meta tag: <meta name="csrf-token" content="...">
 * 2. HTTP cookie: csrf_token=...
 *
 * API Integration:
 * - Automatically includes X-CSRF-Token header
 * - Used by fetchWithCSRF utility function
 * - Required for POST/PUT/DELETE requests
 */
/* eslint-disable @typescript-eslint/no-unnecessary-condition */
import { writable } from 'svelte/store';

interface CSRFStore {
  token: string | null;
}

function createCSRFStore() {
  const { subscribe, set } = writable<CSRFStore>({
    token: null,
  });

  /**
   * Get CSRF token from meta tag
   */
  const getTokenFromMeta = (): string | null => {
    const metaTag = document.querySelector('meta[name="csrf-token"]');
    return metaTag?.getAttribute('content') ?? null;
  };

  /**
   * Get CSRF token from cookie
   */
  const getTokenFromCookie = (cookieName: string = 'csrf_token'): string | null => {
    const cookies = document.cookie.split(';');
    for (const cookie of cookies) {
      const [name, value] = cookie.trim().split('=');
      if (name === cookieName) {
        return decodeURIComponent(value);
      }
    }
    return null;
  };

  return {
    subscribe,

    /**
     * Initialize CSRF token from available sources
     * First tries meta tag, then falls back to cookie
     */
    init: (): void => {
      let token = getTokenFromMeta();

      token ??= getTokenFromCookie();

      set({ token });
    },

    /**
     * Get current CSRF token
     * If not initialized, attempts to retrieve it
     */
    getToken: (): string | null => {
      let currentToken: string | null = null;

      subscribe(state => {
        currentToken = state.token;
      })();

      // If no token stored, try to get it
      if (!currentToken) {
        currentToken = getTokenFromMeta() ?? getTokenFromCookie();
        if (currentToken) {
          set({ token: currentToken });
        }
      }

      return currentToken;
    },

    /**
     * Update CSRF token
     */
    setToken: (token: string | null): void => {
      set({ token });
    },

    /**
     * Get headers object with CSRF token included
     * Useful for API requests
     */
    getHeaders: (): Record<string, string> => {
      let token: string | null = null;

      subscribe(state => {
        token = state.token;
      })();

      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
      };

      if (token) {
        headers['X-CSRF-Token'] = token;
      }

      return headers;
    },
  };
}

export const csrf = createCSRFStore();
