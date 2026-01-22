import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { get } from 'svelte/store';

// Mock the logger before importing auth
vi.mock('$lib/utils/logger', () => ({
  loggers: {
    auth: {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
  },
  getLogger: vi.fn().mockReturnValue({
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  }),
}));

import { auth } from './auth';

describe('Auth Store', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let originalHref: string;

  beforeEach(() => {
    // Reset the store state
    auth.init(false, true);

    // Mock fetch
    mockFetch = vi.fn();
    global.fetch = mockFetch as unknown as typeof fetch;

    // Save and mock window.location.href
    originalHref = window.location.href;
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { href: '', pathname: '/ui/dashboard' },
    });
  });

  afterEach(() => {
    vi.clearAllMocks();
    // Restore window.location
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { href: originalHref },
    });
  });

  describe('init', () => {
    it('should initialize with security disabled', () => {
      auth.init(false, true);
      const state = get(auth);
      expect(state.security.enabled).toBe(false);
      expect(state.security.accessAllowed).toBe(true);
    });

    it('should initialize with security enabled', () => {
      auth.init(true, false);
      const state = get(auth);
      expect(state.security.enabled).toBe(true);
      expect(state.security.accessAllowed).toBe(false);
    });
  });

  describe('setLoggedIn', () => {
    it('should set logged in state', () => {
      auth.setLoggedIn(true);
      const state = get(auth);
      expect(state.isLoggedIn).toBe(true);
    });

    it('should clear logged in state', () => {
      auth.setLoggedIn(true);
      auth.setLoggedIn(false);
      const state = get(auth);
      expect(state.isLoggedIn).toBe(false);
    });
  });

  describe('setSecurity', () => {
    it('should update security settings', () => {
      auth.setSecurity(true, true);
      const state = get(auth);
      expect(state.security.enabled).toBe(true);
      expect(state.security.accessAllowed).toBe(true);
    });
  });

  describe('logout', () => {
    it('should call v2 logout endpoint with POST method', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
      });

      await auth.logout();

      expect(mockFetch).toHaveBeenCalledWith('/api/v2/auth/logout', {
        method: 'POST',
        credentials: 'include',
        headers: {
          'Content-Type': 'application/json',
        },
      });
    });

    it('should clear auth state on successful logout', async () => {
      // Set initial logged in state
      auth.setLoggedIn(true);
      auth.setSecurity(true, true);

      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
      });

      await auth.logout();

      const state = get(auth);
      expect(state.isLoggedIn).toBe(false);
      expect(state.security.enabled).toBe(true);
      expect(state.security.accessAllowed).toBe(false);
    });

    it('should redirect to /ui/ on successful logout', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
      });

      await auth.logout();

      expect(window.location.href).toBe('/ui/');
    });

    it('should throw error on failed logout', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      });

      await expect(auth.logout()).rejects.toThrow('Logout failed: Unauthorized');
    });

    it('should throw error on network failure', async () => {
      mockFetch.mockRejectedValueOnce(new Error('Network error'));

      await expect(auth.logout()).rejects.toThrow('Network error');
    });
  });

  describe('needsLogin', () => {
    it('should return true when security enabled and not logged in', () => {
      const state = {
        isLoggedIn: false,
        security: {
          enabled: true,
          accessAllowed: false,
        },
      };
      expect(auth.needsLogin(state)).toBe(true);
    });

    it('should return false when security disabled', () => {
      const state = {
        isLoggedIn: false,
        security: {
          enabled: false,
          accessAllowed: true,
        },
      };
      expect(auth.needsLogin(state)).toBe(false);
    });

    it('should return false when logged in', () => {
      const state = {
        isLoggedIn: true,
        security: {
          enabled: true,
          accessAllowed: true,
        },
      };
      expect(auth.needsLogin(state)).toBe(false);
    });

    it('should return false when access allowed', () => {
      const state = {
        isLoggedIn: false,
        security: {
          enabled: true,
          accessAllowed: true,
        },
      };
      expect(auth.needsLogin(state)).toBe(false);
    });
  });
});
