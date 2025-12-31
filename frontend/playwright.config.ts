import { defineConfig, devices } from '@playwright/test';

// Environment-specific configuration
const isCI = !!process.env['CI'];
const baseURL = process.env['BASE_URL'] ?? 'http://localhost:8080';

export default defineConfig({
  testDir: './tests/e2e',
  timeout: isCI ? 60000 : 30000, // Longer timeout in CI
  expect: { timeout: isCI ? 15000 : 10000 },
  fullyParallel: false, // Disabled to avoid flakiness with shared test data
  forbidOnly: isCI,
  retries: isCI ? 2 : 0,
  workers: isCI ? 1 : undefined,
  reporter: [['html'], ['junit', { outputFile: 'test-results/junit.xml' }], ['github']],

  use: {
    baseURL,
    navigationTimeout: isCI ? 60000 : 30000,
    actionTimeout: isCI ? 20000 : 10000,
    // Reduce network idle wait time for real-time apps with SSE
    launchOptions: {
      args: isCI ? ['--no-sandbox', '--disable-setuid-sandbox'] : undefined,
    },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },

  projects: [
    {
      name: 'setup',
      testMatch: /.*\.setup\.ts/,
    },
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
      dependencies: ['setup'],
    },
    {
      name: 'firefox',
      use: { ...devices['Desktop Firefox'] },
      dependencies: ['setup'],
    },
    {
      name: 'webkit',
      use: { ...devices['Desktop Safari'] },
      dependencies: ['setup'],
    },
    {
      name: 'mobile-chrome',
      use: { ...devices['Pixel 7'] },
      dependencies: ['setup'],
      // Skip mobile testing for /ui/ paths - only desktop and tablet UI implemented
      testIgnore: ['**/dashboard/**', '**/settings/**', '**/*ui-routing*', '**/*smoke*'],
    },
    {
      name: 'mobile-safari',
      use: { ...devices['iPhone 14'] },
      dependencies: ['setup'],
      // Skip mobile testing for /ui/ paths - only desktop and tablet UI implemented
      testIgnore: ['**/dashboard/**', '**/settings/**', '**/*ui-routing*', '**/*smoke*'],
    },
    {
      name: 'tablet',
      use: { ...devices['iPad Pro'] },
      dependencies: ['setup'],
    },
  ],

  webServer: {
    // Build frontend first to ensure E2E tests use latest code
    // dev_server only runs 'air realtime' which serves static assets
    command: 'task frontend-build && task dev_server',
    port: 8080,
    reuseExistingServer: !process.env['CI'],
    timeout: 240000, // 4 minutes for cold machine startup
  },
});
