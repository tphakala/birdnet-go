import { describe, it, expect, afterEach, vi } from 'vitest';
import {
  extractRelativePath,
  isRelativePath,
  normalizePath,
  getAppBasePath,
  buildAppUrl,
  setBasePath,
  resetBasePath,
} from './urlHelpers';
import { loggers } from './logger';

describe('URL Helpers', () => {
  // Store original window.location descriptor for tests that mock it
  const originalLocationDescriptor = Object.getOwnPropertyDescriptor(window, 'location');

  afterEach(() => {
    // Reset cached base path to null (heuristic mode) between tests
    resetBasePath();

    // Restore original window.location after tests that mock it
    if (originalLocationDescriptor) {
      Object.defineProperty(window, 'location', originalLocationDescriptor);
    }
  });

  describe('extractRelativePath', () => {
    describe('input validation', () => {
      it('should handle undefined inputs', () => {
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath(undefined, '/ui/')).toBe('');
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath('/ui/dashboard', undefined)).toBe('/ui/dashboard');
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath(undefined, undefined)).toBe('');
      });

      it('should handle null inputs', () => {
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath(null, '/ui/')).toBe('');
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath('/ui/dashboard', null)).toBe('/ui/dashboard');
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath(null, null)).toBe('');
      });

      it('should handle empty string inputs', () => {
        expect(extractRelativePath('', '/ui/')).toBe('');
        expect(extractRelativePath('/ui/dashboard', '')).toBe('/ui/dashboard');
        expect(extractRelativePath('', '')).toBe('');
      });

      it('should handle whitespace-only inputs', () => {
        expect(extractRelativePath('   ', '/ui/')).toBe('   ');
        expect(extractRelativePath('/ui/dashboard', '   ')).toBe('/ui/dashboard');
        expect(extractRelativePath('   ', '   ')).toBe('   ');
      });

      it('should handle non-string inputs', () => {
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath(123, '/ui/')).toBe('');
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath('/ui/dashboard', 123)).toBe('/ui/dashboard');
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath({}, '/ui/')).toBe('');
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath('/ui/dashboard', [])).toBe('/ui/dashboard');
      });

      it('should handle boolean inputs', () => {
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath(true, '/ui/')).toBe('');
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath(false, '/ui/')).toBe('');
        // @ts-expect-error - Testing runtime behavior with invalid types
        expect(extractRelativePath('/ui/dashboard', true)).toBe('/ui/dashboard');
      });
    });

    it('should extract relative path when fullPath contains basePath', () => {
      expect(extractRelativePath('/ui/dashboard', '/ui/')).toBe('/dashboard');
      expect(extractRelativePath('/ui/analytics/species', '/ui/')).toBe('/analytics/species');
      expect(extractRelativePath('/ui/settings/main', '/ui/')).toBe('/settings/main');
    });

    it('should handle paths without trailing slash in basePath', () => {
      expect(extractRelativePath('/ui/dashboard', '/ui')).toBe('/dashboard');
      expect(extractRelativePath('/app/settings', '/app')).toBe('/settings');
    });

    it('should return unchanged when fullPath does not contain basePath', () => {
      expect(extractRelativePath('/custom/path', '/ui/')).toBe('/custom/path');
      expect(extractRelativePath('/app/dashboard', '/ui/')).toBe('/app/dashboard');
      expect(extractRelativePath('/different', '/ui/')).toBe('/different');
    });

    it('should return unchanged when fullPath equals basePath', () => {
      expect(extractRelativePath('/ui/', '/ui/')).toBe('/ui/');
      expect(extractRelativePath('/app/', '/app/')).toBe('/app/');
      expect(extractRelativePath('/', '/')).toBe('/');
    });

    it('should ensure extracted path starts with slash', () => {
      // Even if the extraction would result in no leading slash
      expect(extractRelativePath('/uidashboard', '/ui')).toBe('/dashboard');
      expect(extractRelativePath('/appsettings', '/app')).toBe('/settings');
    });

    it('should handle complex nested paths', () => {
      expect(extractRelativePath('/ui/detections/12345', '/ui/')).toBe('/detections/12345');
      expect(extractRelativePath('/ui/analytics/species/robin', '/ui/')).toBe(
        '/analytics/species/robin'
      );
      expect(extractRelativePath('/ui/settings/integration/mqtt', '/ui/')).toBe(
        '/settings/integration/mqtt'
      );
    });

    it('should handle edge cases', () => {
      // Empty paths
      expect(extractRelativePath('', '')).toBe('');
      expect(extractRelativePath('/', '')).toBe('/');

      // Paths with query strings (not typical but should handle)
      expect(extractRelativePath('/ui/dashboard?tab=1', '/ui/')).toBe('/dashboard?tab=1');

      // Paths with hash fragments
      expect(extractRelativePath('/ui/settings#audio', '/ui/')).toBe('/settings#audio');
    });

    it('should be case-sensitive', () => {
      expect(extractRelativePath('/UI/dashboard', '/ui/')).toBe('/UI/dashboard'); // No match
      expect(extractRelativePath('/ui/dashboard', '/UI/')).toBe('/ui/dashboard'); // No match
      expect(extractRelativePath('/ui/dashboard', '/ui/')).toBe('/dashboard'); // Match
    });

    it('should handle multiple occurrences of basePath', () => {
      // Should only remove the first occurrence
      expect(extractRelativePath('/ui/ui/dashboard', '/ui/')).toBe('/ui/dashboard');
      expect(extractRelativePath('/ui/path/ui/nested', '/ui/')).toBe('/path/ui/nested');
    });
  });

  describe('isRelativePath', () => {
    describe('input validation', () => {
      it('should handle invalid inputs', () => {
        // @ts-expect-error - Testing runtime behavior
        expect(isRelativePath(undefined)).toBe(false);
        // @ts-expect-error - Testing runtime behavior
        expect(isRelativePath(null)).toBe(false);
        expect(isRelativePath('')).toBe(false);
        // @ts-expect-error - Testing runtime behavior
        expect(isRelativePath(123)).toBe(false);
        // @ts-expect-error - Testing runtime behavior
        expect(isRelativePath({})).toBe(false);
        // @ts-expect-error - Testing runtime behavior
        expect(isRelativePath([])).toBe(false);
        // @ts-expect-error - Testing runtime behavior
        expect(isRelativePath(true)).toBe(false);
      });
    });

    it('should return true for valid relative paths', () => {
      expect(isRelativePath('/')).toBe(true);
      expect(isRelativePath('/dashboard')).toBe(true);
      expect(isRelativePath('/ui/settings')).toBe(true);
      expect(isRelativePath('/path/to/resource')).toBe(true);
    });

    it('should return false for protocol-relative URLs', () => {
      expect(isRelativePath('//evil.com')).toBe(false);
      expect(isRelativePath('//example.com/path')).toBe(false);
    });

    it('should return false for absolute URLs', () => {
      expect(isRelativePath('http://example.com')).toBe(false);
      expect(isRelativePath('https://example.com')).toBe(false);
      expect(isRelativePath('mailto:test@example.com')).toBe(false);
    });

    it('should return false for paths without leading slash', () => {
      expect(isRelativePath('dashboard')).toBe(false);
      expect(isRelativePath('ui/settings')).toBe(false);
      expect(isRelativePath('')).toBe(false);
    });
  });

  describe('normalizePath', () => {
    describe('input validation', () => {
      it('should handle invalid inputs', () => {
        expect(normalizePath(undefined)).toBe('/');
        expect(normalizePath(null)).toBe('/');
        expect(normalizePath(123)).toBe('/123');
        expect(normalizePath(true)).toBe('/true');
        expect(normalizePath(false)).toBe('/false');
        expect(normalizePath({})).toBe('/[object Object]');
      });
    });

    it('should add leading slash when missing', () => {
      expect(normalizePath('dashboard')).toBe('/dashboard');
      expect(normalizePath('ui/settings')).toBe('/ui/settings');
      expect(normalizePath('')).toBe('/');
    });

    it('should preserve existing leading slash', () => {
      expect(normalizePath('/dashboard')).toBe('/dashboard');
      expect(normalizePath('/ui/settings')).toBe('/ui/settings');
      expect(normalizePath('/')).toBe('/');
    });

    it('should handle trailing slash based on parameter', () => {
      // Default: remove trailing slash
      expect(normalizePath('/dashboard/')).toBe('/dashboard');
      expect(normalizePath('/ui/')).toBe('/ui');
      expect(normalizePath('/', false)).toBe('/'); // Root is special case

      // With addTrailingSlash=true
      expect(normalizePath('/dashboard', true)).toBe('/dashboard/');
      expect(normalizePath('/ui', true)).toBe('/ui/');
      expect(normalizePath('/', true)).toBe('/');
    });

    it('should handle multiple slashes', () => {
      expect(normalizePath('//dashboard')).toBe('//dashboard'); // Preserves double slash (protocol-relative)
      expect(normalizePath('/dashboard//')).toBe('/dashboard/'); // Preserves internal structure
      expect(normalizePath('dashboard/', true)).toBe('/dashboard/');
    });

    it('should handle edge cases', () => {
      expect(normalizePath('', false)).toBe('/');
      expect(normalizePath('', true)).toBe('/');
      expect(normalizePath('///', false)).toBe('///'); // Preserves unusual patterns
    });
  });

  describe('Integration scenarios', () => {
    it('should work together for login redirect flow', () => {
      const currentPath = '/ui/analytics/species';
      const basePath = '/ui/';

      // Validate it's a relative path
      expect(isRelativePath(currentPath)).toBe(true);

      // Extract relative portion
      const relativePath = extractRelativePath(currentPath, basePath);
      expect(relativePath).toBe('/analytics/species');

      // Ensure proper formatting
      const normalized = normalizePath(relativePath);
      expect(normalized).toBe('/analytics/species');
    });

    it('should handle various base path configurations', () => {
      const testCases = [
        { full: '/ui/dashboard', base: '/ui/', expected: '/dashboard' },
        { full: '/app/settings', base: '/app/', expected: '/settings' },
        { full: '/admin/users/123', base: '/admin/', expected: '/users/123' },
        { full: '/custom', base: '/ui/', expected: '/custom' }, // No match
      ];

      for (const { full, base, expected } of testCases) {
        const result = extractRelativePath(full, base);
        expect(result).toBe(expected);
        expect(isRelativePath(result)).toBe(true);
      }
    });
  });

  describe('getAppBasePath', () => {
    it('should return empty string for direct access (no proxy prefix)', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/ui/dashboard' };
      expect(getAppBasePath()).toBe('');
    });

    it('should return empty string for root /ui path', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/ui/' };
      expect(getAppBasePath()).toBe('');
    });

    it('should extract Home Assistant Ingress prefix', () => {
      // @ts-expect-error - Mocking window.location
      window.location = {
        pathname: '/api/hassio_ingress/JNTY7napnEu_u3o-sW3pCx0lp_TsZKcsJ13o3lcoZ90/ui/dashboard',
      };
      expect(getAppBasePath()).toBe(
        '/api/hassio_ingress/JNTY7napnEu_u3o-sW3pCx0lp_TsZKcsJ13o3lcoZ90'
      );
    });

    it('should extract simple proxy prefix', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/proxy/birdnet/ui/detections' };
      expect(getAppBasePath()).toBe('/proxy/birdnet');
    });

    it('should handle paths with detection IDs', () => {
      // @ts-expect-error - Mocking window.location
      window.location = {
        pathname: '/api/hassio_ingress/TOKEN123/ui/detections/33518',
      };
      expect(getAppBasePath()).toBe('/api/hassio_ingress/TOKEN123');
    });

    it('should handle paths with query parameters (pathname only)', () => {
      // Note: pathname doesn't include query string, but path might have /ui in multiple places
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/myproxy/ui/analytics' };
      expect(getAppBasePath()).toBe('/myproxy');
    });

    it('should return empty string when pathname has no /ui', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/some/other/path' };
      expect(getAppBasePath()).toBe('');
    });

    it('should handle root path', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/' };
      expect(getAppBasePath()).toBe('');
    });

    it('should return empty string for path "/ui" (ui at root)', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/ui' };
      expect(getAppBasePath()).toBe('');
    });
  });

  describe('buildAppUrl', () => {
    it('should return path unchanged for direct access', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/ui/dashboard' };
      expect(buildAppUrl('/ui/detections/123')).toBe('/ui/detections/123');
    });

    it('should prepend ingress prefix to path', () => {
      // @ts-expect-error - Mocking window.location
      window.location = {
        pathname: '/api/hassio_ingress/TOKEN/ui/dashboard',
      };
      expect(buildAppUrl('/ui/detections/123')).toBe('/api/hassio_ingress/TOKEN/ui/detections/123');
    });

    it('should handle paths with query parameters', () => {
      // @ts-expect-error - Mocking window.location
      window.location = {
        pathname: '/api/hassio_ingress/TOKEN/ui/dashboard',
      };
      expect(buildAppUrl('/ui/detections/123?tab=review')).toBe(
        '/api/hassio_ingress/TOKEN/ui/detections/123?tab=review'
      );
    });

    it('should handle paths with hash fragments', () => {
      // @ts-expect-error - Mocking window.location
      window.location = {
        pathname: '/proxy/ui/settings',
      };
      expect(buildAppUrl('/ui/settings#audio')).toBe('/proxy/ui/settings#audio');
    });

    it('should work with various path formats', () => {
      // @ts-expect-error - Mocking window.location
      window.location = {
        pathname: '/custom-proxy/ui/analytics/species',
      };

      expect(buildAppUrl('/ui/')).toBe('/custom-proxy/ui/');
      expect(buildAppUrl('/ui/dashboard')).toBe('/custom-proxy/ui/dashboard');
      expect(buildAppUrl('/ui/detections/33518?tab=review')).toBe(
        '/custom-proxy/ui/detections/33518?tab=review'
      );
    });

    it('should prevent open redirect with protocol-relative URLs', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/ui/dashboard' };

      // Protocol-relative URLs should be rejected and return safe fallback
      const loggerSpy = vi.spyOn(loggers.ui, 'error').mockImplementation(() => {});
      expect(buildAppUrl('//evil.com/path')).toBe('/ui/dashboard');
      expect(loggerSpy).toHaveBeenCalledWith(
        'buildAppUrl was called with a non-relative path:',
        '//evil.com/path'
      );
      loggerSpy.mockRestore();
    });

    it('should prevent open redirect with absolute URLs', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/proxy/ui/dashboard' };

      const loggerSpy = vi.spyOn(loggers.ui, 'error').mockImplementation(() => {});
      expect(buildAppUrl('https://evil.com')).toBe('/proxy/ui/dashboard');
      expect(loggerSpy).toHaveBeenCalledWith(
        'buildAppUrl was called with a non-relative path:',
        'https://evil.com'
      );
      loggerSpy.mockRestore();
    });
  });

  describe('Ingress integration scenarios', () => {
    it('should correctly build review detection URL through ingress', () => {
      // Simulate being on dashboard through Home Assistant Ingress
      // @ts-expect-error - Mocking window.location
      window.location = {
        pathname: '/api/hassio_ingress/JNTY7napnEu_u3o-sW3pCx0lp_TsZKcsJ13o3lcoZ90/ui/dashboard',
      };

      const detectionId = 33518;
      const reviewUrl = buildAppUrl(`/ui/detections/${detectionId}?tab=review`);

      expect(reviewUrl).toBe(
        '/api/hassio_ingress/JNTY7napnEu_u3o-sW3pCx0lp_TsZKcsJ13o3lcoZ90/ui/detections/33518?tab=review'
      );
    });

    it('should work correctly without proxy (direct access)', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/ui/dashboard' };

      const detectionId = 33518;
      const reviewUrl = buildAppUrl(`/ui/detections/${detectionId}?tab=review`);

      expect(reviewUrl).toBe('/ui/detections/33518?tab=review');
    });
  });

  describe('setBasePath and getAppBasePath cache priority', () => {
    it('should return empty string when setBasePath is called with empty string', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/proxy/birdnet/ui/dashboard' };

      // Before setBasePath, heuristic would return '/proxy/birdnet'
      expect(getAppBasePath()).toBe('/proxy/birdnet');

      // After setBasePath(''), cache takes priority over URL heuristic
      setBasePath('');
      expect(getAppBasePath()).toBe('');
    });

    it('should return the cached prefix when setBasePath is called with a prefix', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/ui/dashboard' };

      // Before setBasePath, heuristic returns '' (direct access)
      expect(getAppBasePath()).toBe('');

      // After setBasePath, cached value takes priority
      setBasePath('/api/hassio_ingress/TOKEN');
      expect(getAppBasePath()).toBe('/api/hassio_ingress/TOKEN');
    });

    it('should fall back to URL heuristic before setBasePath is called', () => {
      // resetBasePath() is called in afterEach, so _cachedBasePath is null here
      // @ts-expect-error - Mocking window.location
      window.location = {
        pathname: '/api/hassio_ingress/TOKEN/ui/dashboard',
      };

      // No setBasePath called, so heuristic should be used
      expect(getAppBasePath()).toBe('/api/hassio_ingress/TOKEN');
    });

    it('should override heuristic even when heuristic would give a different value', () => {
      // @ts-expect-error - Mocking window.location
      window.location = {
        pathname: '/api/hassio_ingress/TOKEN/ui/dashboard',
      };

      // Heuristic would return '/api/hassio_ingress/TOKEN'
      // But backend says the actual base path is different
      setBasePath('/custom/prefix');
      expect(getAppBasePath()).toBe('/custom/prefix');
    });

    it('should restore heuristic mode after resetBasePath', () => {
      // @ts-expect-error - Mocking window.location
      window.location = { pathname: '/proxy/birdnet/ui/dashboard' };

      setBasePath('/override');
      expect(getAppBasePath()).toBe('/override');

      resetBasePath();
      expect(getAppBasePath()).toBe('/proxy/birdnet');
    });
  });

  describe('buildAppUrl idempotency', () => {
    it('should not double-prefix when path already starts with basePath', () => {
      setBasePath('/api/hassio_ingress/TOKEN');

      const result = buildAppUrl('/api/hassio_ingress/TOKEN/ui/dashboard');
      expect(result).toBe('/api/hassio_ingress/TOKEN/ui/dashboard');
    });

    it('should prefix when path does not start with basePath', () => {
      setBasePath('/api/hassio_ingress/TOKEN');

      const result = buildAppUrl('/ui/dashboard');
      expect(result).toBe('/api/hassio_ingress/TOKEN/ui/dashboard');
    });

    it('should prefix when path has basePath as non-segment-boundary substring', () => {
      // basePath='/birdnet', path='/birdnet-extra/ui' -- "birdnet" is a prefix of
      // "birdnet-extra" but NOT on a segment boundary, so it should still prefix
      setBasePath('/birdnet');

      const result = buildAppUrl('/birdnet-extra/ui');
      expect(result).toBe('/birdnet/birdnet-extra/ui');
    });

    it('should not prefix when basePath is empty string', () => {
      setBasePath('');

      expect(buildAppUrl('/ui/dashboard')).toBe('/ui/dashboard');
      expect(buildAppUrl('/api/v2/detections/123')).toBe('/api/v2/detections/123');
    });

    it('should be idempotent when called multiple times with same input', () => {
      setBasePath('/api/hassio_ingress/TOKEN');

      const firstCall = buildAppUrl('/ui/dashboard');
      expect(firstCall).toBe('/api/hassio_ingress/TOKEN/ui/dashboard');

      // Calling buildAppUrl again with the result should not double-prefix
      const secondCall = buildAppUrl(firstCall);
      expect(secondCall).toBe('/api/hassio_ingress/TOKEN/ui/dashboard');
    });

    it('should detect segment boundary at end of path', () => {
      setBasePath('/api/hassio_ingress/TOKEN');

      // Path is exactly the basePath with no trailing content
      const result = buildAppUrl('/api/hassio_ingress/TOKEN');
      expect(result).toBe('/api/hassio_ingress/TOKEN');
    });
  });

  describe('buildAppUrl with API and asset paths', () => {
    it('should correctly prefix API v2 paths', () => {
      setBasePath('/api/hassio_ingress/TOKEN');

      expect(buildAppUrl('/api/v2/detections/123')).toBe(
        '/api/hassio_ingress/TOKEN/api/v2/detections/123'
      );
    });

    it('should correctly prefix static asset paths', () => {
      setBasePath('/api/hassio_ingress/TOKEN');

      expect(buildAppUrl('/ui/assets/messages/en.json')).toBe(
        '/api/hassio_ingress/TOKEN/ui/assets/messages/en.json'
      );
    });

    it('should correctly prefix SSE event stream paths', () => {
      setBasePath('/api/hassio_ingress/TOKEN');

      expect(buildAppUrl('/api/v2/events')).toBe('/api/hassio_ingress/TOKEN/api/v2/events');
    });

    it('should handle API paths with query parameters', () => {
      setBasePath('/api/hassio_ingress/TOKEN');

      expect(buildAppUrl('/api/v2/detections?limit=10&offset=0')).toBe(
        '/api/hassio_ingress/TOKEN/api/v2/detections?limit=10&offset=0'
      );
    });

    it('should handle API paths without proxy prefix', () => {
      setBasePath('');

      expect(buildAppUrl('/api/v2/detections/123')).toBe('/api/v2/detections/123');
      expect(buildAppUrl('/ui/assets/messages/en.json')).toBe('/ui/assets/messages/en.json');
    });
  });
});
