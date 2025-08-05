import { describe, it, expect } from 'vitest';
import { extractRelativePath, isRelativePath, normalizePath } from './urlHelpers';

describe('URL Helpers', () => {
  describe('extractRelativePath', () => {
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
});
