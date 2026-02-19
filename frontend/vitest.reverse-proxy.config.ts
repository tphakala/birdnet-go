import { defineConfig } from 'vitest/config';

/**
 * Vitest configuration for reverse proxy integration tests.
 *
 * These tests validate that all valid application routes work correctly
 * when accessed through an nginx reverse proxy. They use fetch-based
 * HTTP validation (not browser rendering) to verify that no routes
 * return 404 errors when behind a reverse proxy.
 *
 * Two proxy configurations are tested:
 * - Root proxy: nginx at http://localhost:8180/ → backend at :8080
 * - Subpath proxy: nginx at http://localhost:8181/birdnet/ → backend at :8080
 *
 * Prerequisites:
 *   - Docker must be running (for nginx container)
 *   - Backend must be running on :8080 OR use auto mode
 *
 * Usage:
 *   # Manual backend (start backend first):
 *   npm run test:reverse-proxy
 *
 *   # Auto backend (starts backend + nginx automatically):
 *   npm run test:reverse-proxy:auto
 *
 * @see src/test/reverse-proxy-global-setup.ts
 */
export default defineConfig({
  test: {
    // Use Node.js environment - these are HTTP-level tests, not browser rendering tests
    environment: 'node',
    // Global setup starts backend + nginx containers
    globalSetup: ['./src/test/reverse-proxy-global-setup.ts'],
    // Only include reverse proxy test files
    include: ['src/**/*.reverse-proxy.{test,spec}.{js,ts}'],
    exclude: ['node_modules', 'dist', 'build', '.svelte-kit', 'coverage'],
    // Longer timeouts for container startup and HTTP calls through proxy
    testTimeout: 30000,
    hookTimeout: 60000,
    // Run sequentially to avoid port conflicts
    maxConcurrency: 1,
    // Retry flaky network tests once
    retry: 1,
  },
});
