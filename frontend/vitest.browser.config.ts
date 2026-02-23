import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { playwright } from '@vitest/browser-playwright';

/**
 * Vitest configuration for browser-mode component tests.
 *
 * These tests run in a real browser (via Playwright) to catch runtime-only
 * issues that jsdom and the Svelte compiler cannot detect, such as:
 * - Duplicate key warnings in {#each} blocks
 * - DOM-specific rendering bugs
 * - Browser API behavior differences
 *
 * Unlike integration tests, these do NOT require a running backend.
 * They use thin wrapper Svelte components to isolate specific patterns.
 *
 * Usage:
 *   npm run test:browser
 *
 * @see https://vitest.dev/guide/browser/
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
  test: {
    browser: {
      enabled: true,
      provider: playwright({
        actionTimeout: 10000,
      }),
      instances: [{ browser: 'chromium' }],
      headless: true,
    },
    // Only include browser test files
    include: ['src/**/*.browser.{test,spec}.{js,ts}'],
    exclude: ['node_modules', 'dist', 'build', '.svelte-kit', 'coverage'],
    testTimeout: 15000,
    hookTimeout: 15000,
  },
});
