import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { playwright } from '@vitest/browser-playwright';

// Check if we should auto-start the backend
const autoBackend = process.env.INTEGRATION_AUTO_BACKEND === 'true';

/**
 * Vitest configuration for integration tests with browser mode.
 *
 * These tests run in a real browser (via Playwright) against a real BirdNET-Go backend.
 * They test real API calls, real DOM interactions, and end-to-end component behavior.
 *
 * Usage (manual backend):
 *   1. Start backend: `task integration-backend` (in separate terminal)
 *   2. Run tests: `npm run test:integration`
 *
 * Usage (auto backend):
 *   Run: `npm run test:integration:auto`
 *   Or: `task integration-test-full`
 *
 * @see https://vitest.dev/guide/browser/
 * @see https://vitest.dev/config/browser/playwright
 */
export default defineConfig({
  plugins: [
    svelte({
      compilerOptions: {
        hmr: false,
      },
    }),
  ],
  resolve: {
    alias: {
      $lib: '/src/lib',
    },
  },
  server: {
    proxy: {
      // Proxy API calls to the real backend
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  test: {
    // Use browser mode for real browser testing
    browser: {
      enabled: true,
      // Provider with shared options
      provider: playwright({
        actionTimeout: 10000,
      }),
      // At least one instance is required (Vitest 4.x API)
      instances: [{ browser: 'chromium' }],
      headless: true,
    },
    // Global setup for auto-starting backend (only when INTEGRATION_AUTO_BACKEND=true)
    globalSetup: autoBackend ? ['./src/test/integration-global-setup.ts'] : [],
    // Only include integration test files
    include: ['src/**/*.integration.{test,spec}.{js,ts}'],
    exclude: ['node_modules', 'dist', 'build', '.svelte-kit', 'coverage'],
    // Integration tests need longer timeouts for real API calls
    testTimeout: 30000,
    hookTimeout: 30000,
    // Run integration tests sequentially to avoid race conditions
    maxConcurrency: 1,
    // Retry flaky tests once (network issues, etc.)
    retry: 1,
    // Setup file for integration tests
    setupFiles: ['./src/test/integration-setup.ts'],
  },
});
