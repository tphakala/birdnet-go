import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { getCsrfToken, fetchWithCSRF, api } from './api';

describe('API utilities', () => {
  beforeEach(() => {
    // Reset cookies
    document.cookie = '';
    // Clear all mocks
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('getCsrfToken', () => {
    afterEach(() => {
      // Clean up meta tags after each test
      const existingMeta = document.querySelector('meta[name="csrf-token"]');
      if (existingMeta) {
        existingMeta.remove();
      }
    });

    it('returns null when no csrf token exists', () => {
      expect(getCsrfToken()).toBeNull();
    });

    it('returns csrf token from meta tag', () => {
      const meta = document.createElement('meta');
      meta.name = 'csrf-token';
      meta.content = 'test-token';
      document.head.appendChild(meta);

      expect(getCsrfToken()).toBe('test-token');
    });

    it('handles encoded csrf token in meta tag', () => {
      const meta = document.createElement('meta');
      meta.name = 'csrf-token';
      meta.content = 'test/token=value';
      document.head.appendChild(meta);

      expect(getCsrfToken()).toBe('test/token=value');
    });

    it('returns null when meta tag has empty content', () => {
      const meta = document.createElement('meta');
      meta.name = 'csrf-token';
      meta.content = '';
      document.head.appendChild(meta);

      expect(getCsrfToken()).toBeNull();
    });
  });

  describe('fetchWithCSRF', () => {
    let mockFetch: ReturnType<typeof vi.fn>;

    beforeEach(() => {
      mockFetch = vi.fn();
      global.fetch = mockFetch;
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
      // Set up meta tag with CSRF token
      const meta = document.createElement('meta');
      meta.name = 'csrf-token';
      meta.content = 'test-csrf-token';
      document.head.appendChild(meta);

      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: () => Promise.resolve({}),
      });

      await fetchWithCSRF('/api/test');

      const [, options] = mockFetch.mock.calls[0];
      expect(options.headers.get('X-CSRF-Token')).toBe('test-csrf-token');

      // Clean up
      meta.remove();
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

      await expect(fetchWithCSRF('/api/test')).rejects.toThrow(
        'The requested resource was not found.'
      );
    });

    it('handles non-JSON error responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        statusText: 'Internal Server Error',
        headers: new Headers(),
        json: () => Promise.reject(new Error('Invalid JSON')),
      });

      await expect(fetchWithCSRF('/api/test')).rejects.toThrow(
        'A server error occurred. Please try again later.'
      );
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
      global.fetch = mockFetch;
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
