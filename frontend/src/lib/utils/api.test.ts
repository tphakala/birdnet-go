import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Unmock the logger module for API tests since API depends on logger
vi.unmock('$lib/utils/logger');

// Mock appState module to control CSRF token and security state in tests
let mockCsrfToken = '';
let mockGuestMode = false;
let mockPrivateMode = false;
vi.mock('$lib/stores/appState.svelte', () => ({
  appState: {
    get security() {
      return { privateMode: mockPrivateMode };
    },
  },
  getCsrfToken: () => mockCsrfToken,
  isGuestMode: () => mockGuestMode,
  isSentryEnabled: () => false,
  refreshCsrfToken: vi.fn().mockResolvedValue(false),
}));

import { getCsrfToken, fetchWithCSRF, api, resetRedirectGuard } from './api';

describe('API utilities', () => {
  beforeEach(() => {
    // Reset cookies
    document.cookie = '';
    // Clear all mocks
    vi.clearAllMocks();

    // Set up a default CSRF token for all tests to prevent warning logs
    mockCsrfToken = 'test-csrf-token-default';
    mockGuestMode = false;
    mockPrivateMode = false;
  });

  afterEach(() => {
    vi.restoreAllMocks();
    mockCsrfToken = '';
  });

  describe('getCsrfToken', () => {
    beforeEach(() => {
      // Reset token for getCsrfToken specific tests
      mockCsrfToken = '';
    });

    it('returns null when no csrf token exists', () => {
      mockCsrfToken = '';
      expect(getCsrfToken()).toBeNull();
    });

    it('returns csrf token from appState', () => {
      mockCsrfToken = 'test-token';
      expect(getCsrfToken()).toBe('test-token');
    });

    it('handles special characters in csrf token', () => {
      mockCsrfToken = 'test/token=value';
      expect(getCsrfToken()).toBe('test/token=value');
    });

    it('returns null when token is empty string', () => {
      mockCsrfToken = '';
      expect(getCsrfToken()).toBeNull();
    });
  });

  describe('fetchWithCSRF', () => {
    let mockFetch: ReturnType<typeof vi.fn>;

    beforeEach(() => {
      mockFetch = vi.fn();
      global.fetch = mockFetch as unknown as typeof fetch;
    });

    it('makes GET request with default headers', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: () => Promise.resolve({ data: 'test' }),
      });

      const result = await fetchWithCSRF('/api/test');

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/test',
        expect.objectContaining({
          method: 'GET',
          credentials: 'same-origin',
          headers: expect.any(Headers),
        })
      );

      expect(result).toEqual({ data: 'test' });
    });

    it('includes CSRF token in headers', async () => {
      // Set a specific test token via mock
      mockCsrfToken = 'test-csrf-token-specific';

      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: () => Promise.resolve({}),
      });

      await fetchWithCSRF('/api/test');

      const [, options] = mockFetch.mock.calls[0];
      expect(options.headers.get('X-CSRF-Token')).toBe('test-csrf-token-specific');
    });

    it('stringifies JSON body for POST requests', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: () => Promise.resolve({}),
      });

      const data = { foo: 'bar' };
      await fetchWithCSRF('/api/test', { method: 'POST', body: data });

      const [, options] = mockFetch.mock.calls[0];
      expect(options.body).toBe(JSON.stringify(data));
    });

    it('preserves FormData body', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: () => Promise.resolve({}),
      });

      const formData = new FormData();
      formData.append('file', 'test');

      await fetchWithCSRF('/api/upload', { method: 'POST', body: formData });

      const [, options] = mockFetch.mock.calls[0];
      expect(options.body).toBe(formData);
    });

    it('throws ApiError on non-ok response', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 404,
        statusText: 'Not Found',
        headers: new Headers(),
        json: () => Promise.resolve({ error: 'Resource not found' }),
      });

      await expect(fetchWithCSRF('/api/test')).rejects.toThrow('errors.api.notFound');
    });

    it('handles non-JSON error responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        statusText: 'Internal Server Error',
        headers: new Headers(),
        json: () => Promise.reject(new Error('Invalid JSON')),
      });

      await expect(fetchWithCSRF('/api/test')).rejects.toThrow('errors.api.serverError');
    });

    it('returns null for empty responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 204,
        headers: new Headers({ 'content-length': '0' }),
      });

      const result = await fetchWithCSRF('/api/test');
      expect(result).toBeNull();
    });

    it('retries with a fresh CSRF token when a state-changing request gets 403', async () => {
      // Override refreshCsrfToken to return true for this test
      const appState = await import('$lib/stores/appState.svelte');
      vi.mocked(appState.refreshCsrfToken).mockResolvedValueOnce(true);

      // First call: 403 (CSRF token expired)
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 403,
        statusText: 'Forbidden',
        headers: new Headers(),
        json: () => Promise.resolve({ error: 'CSRF token invalid' }),
      });

      // Second call (retry): 200 OK
      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: () => Promise.resolve({ saved: true }),
      });

      const result = await fetchWithCSRF('/api/settings', { method: 'POST', body: { key: 'val' } });

      expect(appState.refreshCsrfToken).toHaveBeenCalledOnce();
      expect(mockFetch).toHaveBeenCalledTimes(2);
      expect(result).toEqual({ saved: true });
    });

    it('returns text for non-JSON responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: new Headers({ 'content-type': 'text/plain' }),
        text: () => Promise.resolve('Plain text response'),
      });

      const result = await fetchWithCSRF('/api/test');
      expect(result).toBe('Plain text response');
    });

    it('rejects protocol-relative and backslash-trick URLs to prevent CSRF token leakage', async () => {
      // "//evil.com/x" satisfies includes('//') yet also starts with '/', so it
      // would slip past the absolute-URL SSRF guard. Backslash variants like
      // "/\evil.com" resolve to a foreign origin via URL normalization. Both
      // must be rejected before any fetch so the CSRF-bearing request can never
      // target a foreign origin.
      for (const evil of ['//evil.com/steal', '/\\evil.com/steal']) {
        await expect(fetchWithCSRF(evil)).rejects.toMatchObject({
          status: 400,
          message: 'Protocol-relative URLs are not allowed',
        });
      }
      expect(mockFetch).not.toHaveBeenCalled();
    });
  });

  describe('401 redirect to login', () => {
    let mockFetch: ReturnType<typeof vi.fn>;
    const originalLocation = window.location;

    beforeEach(() => {
      mockFetch = vi.fn();
      global.fetch = mockFetch as unknown as typeof fetch;

      // Reset the redirect guard and cooldown before each test
      resetRedirectGuard();
      sessionStorage.removeItem('last_401_redirect');

      // Mock window.location.href as writable
      Object.defineProperty(window, 'location', {
        value: { ...originalLocation, href: originalLocation.href },
        writable: true,
        configurable: true,
      });
    });

    afterEach(() => {
      // Restore original window.location
      Object.defineProperty(window, 'location', {
        value: originalLocation,
        writable: true,
        configurable: true,
      });
    });

    it('redirects to login on 401 response', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        headers: new Headers(),
      });

      // The promise should never resolve (hangs until page navigates away).
      // Use Promise.race with a short timeout to verify it does not reject.
      const result = await Promise.race([
        fetchWithCSRF('/api/test').then(() => 'resolved'),
        new Promise<string>(resolve => setTimeout(() => resolve('pending'), 50)),
      ]);

      expect(result).toBe('pending');
      expect(window.location.href).toBe('/ui/');
    });

    it('does not throw ApiError on 401', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        headers: new Headers(),
      });

      // The call should NOT reject — it should hang.
      let threw = false;
      const raceResult = await Promise.race([
        fetchWithCSRF('/api/test').catch(() => {
          threw = true;
          return 'rejected';
        }),
        new Promise<string>(resolve => setTimeout(() => resolve('pending'), 50)),
      ]);

      expect(threw).toBe(false);
      expect(raceResult).toBe('pending');
    });

    it('only redirects once for multiple concurrent 401s', async () => {
      mockFetch.mockResolvedValue({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        headers: new Headers(),
      });

      // Fire multiple requests that all get 401
      const promises = [
        fetchWithCSRF('/api/test1'),
        fetchWithCSRF('/api/test2'),
        fetchWithCSRF('/api/test3'),
      ];

      // Wait a tick for all to process
      await Promise.race([Promise.all(promises), new Promise(resolve => setTimeout(resolve, 50))]);

      // window.location.href should have been set exactly once
      // (the guard prevents subsequent assignments)
      expect(window.location.href).toBe('/ui/');
    });

    it('throws ApiError instead of redirecting in guest mode', async () => {
      mockGuestMode = true;

      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        headers: new Headers(),
      });

      await expect(fetchWithCSRF('/api/test')).rejects.toMatchObject({
        message: 'errors.api.unauthorized',
        status: 401,
      });
      // Should NOT redirect
      expect(window.location.href).not.toBe('/ui/');
    });

    it('throws ApiError instead of redirecting when the login endpoint returns 401', async () => {
      // The login endpoint is the only place a user can recover from 401;
      // redirecting away would prevent LoginModal from showing the error.
      mockPrivateMode = true;

      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        headers: new Headers(),
      });

      await expect(fetchWithCSRF('/api/v2/auth/login')).rejects.toMatchObject({
        message: 'errors.api.unauthorized',
        status: 401,
      });
      expect(window.location.href).not.toBe('/ui/');
    });

    it('redirects to login in private mode even when in guest mode', async () => {
      mockGuestMode = true;
      mockPrivateMode = true;

      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        headers: new Headers(),
      });

      const result = await Promise.race([
        fetchWithCSRF('/api/test').then(() => 'resolved'),
        new Promise<string>(resolve => setTimeout(() => resolve('pending'), 50)),
      ]);

      expect(result).toBe('pending');
      expect(window.location.href).toBe('/ui/');
    });

    it('throws ApiError instead of hanging when the redirect is suppressed by the cooldown', async () => {
      // A very recent prior redirect trips the reload-loop cooldown, so
      // redirectToLogin suppresses navigation. In that branch it must reject
      // (not return a never-resolving promise) so the awaiting caller can stop
      // its loading state. Regression test for the never-resolving-promise hang.
      sessionStorage.setItem('last_401_redirect', Date.now().toString());

      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        headers: new Headers(),
      });

      await expect(fetchWithCSRF('/api/test')).rejects.toMatchObject({
        status: 401,
        message: 'errors.api.unauthorized',
      });
      // The suppressed branch must not navigate.
      expect(window.location.href).not.toBe('/ui/');
    });
  });

  describe('api client', () => {
    let mockFetch: ReturnType<typeof vi.fn>;

    beforeEach(() => {
      mockFetch = vi.fn(() =>
        Promise.resolve({
          ok: true,
          status: 200,
          headers: new Headers({ 'content-type': 'application/json' }),
          json: () => Promise.resolve({ success: true }),
        })
      );
      global.fetch = mockFetch as unknown as typeof fetch;
    });

    it('makes GET requests', async () => {
      await api.get('/api/users');
      expect(mockFetch).toHaveBeenCalledWith(
        '/api/users',
        expect.objectContaining({
          method: 'GET',
        })
      );
    });

    it('makes POST requests with data', async () => {
      const data = { name: 'test' };
      await api.post('/api/users', data);

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/users',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify(data),
        })
      );
    });

    it('makes PUT requests with data', async () => {
      const data = { name: 'updated' };
      await api.put('/api/users/1', data);

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/users/1',
        expect.objectContaining({
          method: 'PUT',
          body: JSON.stringify(data),
        })
      );
    });

    it('makes PATCH requests with data', async () => {
      const data = { name: 'patched' };
      await api.patch('/api/users/1', data);

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/users/1',
        expect.objectContaining({
          method: 'PATCH',
          body: JSON.stringify(data),
        })
      );
    });

    it('makes DELETE requests', async () => {
      await api.delete('/api/users/1');

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/users/1',
        expect.objectContaining({
          method: 'DELETE',
        })
      );
    });
  });

  // Note: handleApiError was removed in security hardening - error handling is now integrated into fetchWithCSRF
});
