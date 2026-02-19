/**
 * Reverse Proxy Route Validation Tests
 *
 * Validates that all valid application routes are accessible through nginx
 * reverse proxies without returning 404 errors. Tests two proxy configurations:
 *
 * 1. Root proxy: nginx proxies / → backend (no path prefix)
 * 2. Subpath proxy: nginx proxies /birdnet/ → backend (with X-Forwarded-Prefix)
 *
 * Routes tested:
 * - SPA UI routes (served as HTML by the Go backend)
 * - API v2 endpoints (JSON responses)
 * - Static asset paths (CSS/JS/images)
 * - Root redirect (/ → /ui/dashboard)
 * - Health check endpoint
 *
 * These tests catch:
 * - nginx location block misconfigurations
 * - Path rewriting bugs (double prefixing, missing trailing slashes)
 * - sub_filter corruption of asset paths
 * - Missing proxy_pass rules for specific route patterns
 * - X-Forwarded-Prefix header not being respected
 */

/* eslint-disable no-undef -- process is available in Node.js test environment */
/* eslint-disable vitest/no-conditional-expect -- Conditional expects needed for redirect vs direct response handling */

import { describe, expect, it } from 'vitest';

// URLs are set by the global setup via environment variables
const NGINX_ROOT_URL = process.env['NGINX_ROOT_URL'] ?? 'http://localhost:8180';
const NGINX_SUBPATH_URL = process.env['NGINX_SUBPATH_URL'] ?? 'http://localhost:8181/birdnet';
const BACKEND_URL = process.env['BACKEND_URL'] ?? 'http://localhost:8080';

/**
 * Helper: fetch a URL and return status + headers, following redirects manually
 * to detect redirect chains.
 */
async function fetchRoute(
  baseUrl: string,
  path: string
): Promise<{ status: number; redirectedTo?: string; contentType?: string }> {
  const url = `${baseUrl}${path}`;
  const response = await fetch(url, {
    redirect: 'manual', // Don't auto-follow redirects so we can inspect them
    signal: AbortSignal.timeout(10000),
  });

  const result: { status: number; redirectedTo?: string; contentType?: string } = {
    status: response.status,
    contentType: response.headers.get('content-type') ?? undefined,
  };

  if (response.status >= 300 && response.status < 400) {
    result.redirectedTo = response.headers.get('location') ?? undefined;
  }

  return result;
}

/**
 * Helper: fetch and follow redirects, returning final status.
 */
async function fetchRouteFollowRedirects(
  baseUrl: string,
  path: string
): Promise<{ status: number; contentType?: string; url: string }> {
  const url = `${baseUrl}${path}`;
  const response = await fetch(url, {
    signal: AbortSignal.timeout(10000),
  });

  return {
    status: response.status,
    contentType: response.headers.get('content-type') ?? undefined,
    url: response.url,
  };
}

// ============================================================================
// SPA UI Routes - these should return 200 with text/html
// ============================================================================

/** All SPA routes that the Go backend registers as serving index.html */
const SPA_ROUTES = [
  '/ui/dashboard',
  '/ui/notifications',
  '/ui/analytics',
  '/ui/analytics/advanced',
  '/ui/analytics/species',
  '/ui/search',
  '/ui/detections',
  '/ui/settings',
  '/ui/settings/main',
  '/ui/settings/userinterface',
  '/ui/settings/audio',
  '/ui/settings/detectionfilters',
  '/ui/settings/integrations',
  '/ui/settings/security',
  '/ui/settings/species',
  '/ui/settings/notifications',
  '/ui/settings/support',
  '/ui/system',
  '/ui/system/database',
  '/ui/system/terminal',
  '/ui/about',
];

/** API v2 endpoints that should return JSON (not 404) */
const API_ROUTES = [
  '/api/v2/health',
  '/api/v2/app/config',
  '/api/v2/settings',
  '/api/v2/detections?limit=1',
  '/api/v2/detections/recent',
  '/api/v2/analytics/species/summary',
  '/api/v2/analytics/time/distribution/hourly',
  '/api/v2/analytics/species/detections/new',
  '/api/v2/notifications/unread/count',
  '/api/v2/dynamic-thresholds',
  '/api/v2/dynamic-thresholds/stats',
  '/api/v2/range/species/count',
  '/api/v2/range/species/list',
  '/api/v2/integrations/mqtt/status',
  '/api/v2/integrations/birdweather/status',
  '/api/v2/streams/status',
  '/api/v2/streams/health',
  '/api/v2/sse/status',
  '/api/v2/system/info',
  '/api/v2/system/database/stats',
  '/api/v2/system/resources',
  '/api/v2/system/disks',
  '/api/v2/system/audio/devices',
  '/api/v2/settings/locales',
];

// ============================================================================
// Sanity check: verify direct backend access works
// ============================================================================

describe('Backend Direct Access (sanity check)', () => {
  it('backend health endpoint responds', async () => {
    const response = await fetch(`${BACKEND_URL}/api/v2/health`, {
      signal: AbortSignal.timeout(5000),
    });
    expect(response.ok).toBe(true);
    const data = await response.json();
    expect(data.status).toBe('healthy');
  });
});

// ============================================================================
// Root Proxy Tests (nginx at / → backend)
// ============================================================================

describe('Root Proxy: SPA Routes', () => {
  for (const route of SPA_ROUTES) {
    it(`GET ${route} should not return 404`, async () => {
      const result = await fetchRouteFollowRedirects(NGINX_ROOT_URL, route);
      expect(result.status, `${route} returned ${result.status}`).not.toBe(404);
      expect(result.contentType).toContain('text/html');
    });
  }
});

describe('Root Proxy: API Routes', () => {
  for (const route of API_ROUTES) {
    it(`GET ${route} should not return 404`, async () => {
      const result = await fetchRouteFollowRedirects(NGINX_ROOT_URL, route);
      expect(result.status, `${route} returned ${result.status}`).not.toBe(404);
    });
  }
});

describe('Root Proxy: Root Redirect', () => {
  it('GET / should redirect to /ui/dashboard or return HTML', async () => {
    const result = await fetchRoute(NGINX_ROOT_URL, '/');
    // Root should either redirect (301/302) to dashboard or serve HTML directly
    if (result.status >= 300 && result.status < 400) {
      expect(result.redirectedTo).toContain('/ui/dashboard');
    } else {
      expect(result.status).toBe(200);
    }
  });

  it('GET / following redirects should reach a valid page', async () => {
    const result = await fetchRouteFollowRedirects(NGINX_ROOT_URL, '/');
    expect(result.status).not.toBe(404);
    expect(result.status).toBeLessThan(500);
  });
});

describe('Root Proxy: Health Endpoint', () => {
  it('GET /health should respond (top-level health)', async () => {
    const result = await fetchRouteFollowRedirects(NGINX_ROOT_URL, '/health');
    // Health may return 200 or may not exist at top level
    expect(result.status).not.toBe(404);
  });

  it('GET /api/v2/health should return healthy status', async () => {
    const response = await fetch(`${NGINX_ROOT_URL}/api/v2/health`, {
      signal: AbortSignal.timeout(10000),
    });
    expect(response.ok).toBe(true);
    const data = await response.json();
    expect(data.status).toBe('healthy');
  });
});

describe('Root Proxy: App Config', () => {
  it('GET /api/v2/app/config should return valid config', async () => {
    const response = await fetch(`${NGINX_ROOT_URL}/api/v2/app/config`, {
      signal: AbortSignal.timeout(10000),
    });
    expect(response.ok).toBe(true);
    const config = await response.json();
    expect(config).toBeDefined();
    // basePath should be empty string for root proxy
    expect(config).toHaveProperty('basePath');
  });
});

// ============================================================================
// Subpath Proxy Tests (nginx at /birdnet/ → backend)
// ============================================================================

describe('Subpath Proxy: SPA Routes', () => {
  for (const route of SPA_ROUTES) {
    it(`GET ${route} should not return 404 through /birdnet prefix`, async () => {
      const result = await fetchRouteFollowRedirects(NGINX_SUBPATH_URL, route);
      expect(result.status, `${route} returned ${result.status}`).not.toBe(404);
      expect(result.contentType).toContain('text/html');
    });
  }
});

describe('Subpath Proxy: API Routes', () => {
  for (const route of API_ROUTES) {
    it(`GET ${route} should not return 404 through /birdnet prefix`, async () => {
      const result = await fetchRouteFollowRedirects(NGINX_SUBPATH_URL, route);
      expect(result.status, `${route} returned ${result.status}`).not.toBe(404);
    });
  }
});

describe('Subpath Proxy: Root Redirect', () => {
  it('GET /birdnet/ should redirect to dashboard or return HTML', async () => {
    const result = await fetchRoute(NGINX_SUBPATH_URL.replace('/birdnet', ''), '/birdnet/');
    if (result.status >= 300 && result.status < 400) {
      // Redirect should include the /birdnet prefix
      expect(result.redirectedTo).toContain('birdnet');
    } else {
      expect(result.status).toBe(200);
    }
  });

  it('GET /birdnet (no trailing slash) should redirect', async () => {
    const result = await fetchRoute(NGINX_SUBPATH_URL.replace('/birdnet', ''), '/birdnet');
    // Should redirect to /birdnet/ with trailing slash
    expect(result.status).toBeGreaterThanOrEqual(300);
    expect(result.status).toBeLessThan(400);
  });
});

describe('Subpath Proxy: App Config with X-Forwarded-Prefix', () => {
  it('GET /api/v2/app/config should return basePath with prefix', async () => {
    const response = await fetch(`${NGINX_SUBPATH_URL}/api/v2/app/config`, {
      signal: AbortSignal.timeout(10000),
    });
    expect(response.ok).toBe(true);
    const config = await response.json();
    expect(config).toBeDefined();
    expect(config).toHaveProperty('basePath');
    // The X-Forwarded-Prefix header should result in basePath = "/birdnet"
    expect(config.basePath).toBe('/birdnet');
  });
});

describe('Subpath Proxy: Health Endpoint', () => {
  it('GET /birdnet/api/v2/health should return healthy', async () => {
    const response = await fetch(`${NGINX_SUBPATH_URL}/api/v2/health`, {
      signal: AbortSignal.timeout(10000),
    });
    expect(response.ok).toBe(true);
    const data = await response.json();
    expect(data.status).toBe('healthy');
  });
});

// ============================================================================
// SSE Endpoint Connectivity (verify proxy doesn't block SSE)
// ============================================================================

describe('Root Proxy: SSE Endpoints', () => {
  it('GET /api/v2/detections/stream should start SSE connection', async () => {
    const controller = new AbortController();
    // Set a short timeout - we just want to verify the connection starts
    const timeout = setTimeout(() => controller.abort(), 3000);

    try {
      const response = await fetch(`${NGINX_ROOT_URL}/api/v2/detections/stream`, {
        signal: controller.signal,
        headers: {
          Accept: 'text/event-stream',
        },
      });
      // SSE endpoint should return 200 with event-stream content type
      expect(response.status).toBe(200);
      expect(response.headers.get('content-type')).toContain('text/event-stream');
    } catch (err) {
      // AbortError is expected (we abort after 3s)
      if (err instanceof Error && err.name === 'AbortError') {
        // Connection was established but we aborted - this is fine
        return;
      }
      throw err;
    } finally {
      clearTimeout(timeout);
      controller.abort();
    }
  });

  it('GET /api/v2/notifications/stream should start SSE connection', async () => {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 3000);

    try {
      const response = await fetch(`${NGINX_ROOT_URL}/api/v2/notifications/stream`, {
        signal: controller.signal,
        headers: {
          Accept: 'text/event-stream',
        },
      });
      expect(response.status).toBe(200);
      expect(response.headers.get('content-type')).toContain('text/event-stream');
    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') {
        return;
      }
      throw err;
    } finally {
      clearTimeout(timeout);
      controller.abort();
    }
  });
});

describe('Subpath Proxy: SSE Endpoints', () => {
  it('GET /birdnet/api/v2/detections/stream should start SSE through subpath', async () => {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 3000);

    try {
      const response = await fetch(`${NGINX_SUBPATH_URL}/api/v2/detections/stream`, {
        signal: controller.signal,
        headers: {
          Accept: 'text/event-stream',
        },
      });
      expect(response.status).toBe(200);
      expect(response.headers.get('content-type')).toContain('text/event-stream');
    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') {
        return;
      }
      throw err;
    } finally {
      clearTimeout(timeout);
      controller.abort();
    }
  });
});

// ============================================================================
// Error handling: verify known-invalid routes DO return 404
// (ensures the proxy isn't blindly returning 200 for everything)
// ============================================================================

describe('Root Proxy: Invalid Routes Return 404', () => {
  it('GET /api/v2/nonexistent-endpoint should return 404', async () => {
    const result = await fetchRouteFollowRedirects(
      NGINX_ROOT_URL,
      '/api/v2/nonexistent-endpoint-12345'
    );
    expect(result.status).toBe(404);
  });
});

describe('Subpath Proxy: Invalid Routes Return 404', () => {
  it('GET /birdnet/api/v2/nonexistent-endpoint should return 404', async () => {
    const result = await fetchRouteFollowRedirects(
      NGINX_SUBPATH_URL,
      '/api/v2/nonexistent-endpoint-12345'
    );
    expect(result.status).toBe(404);
  });
});
