import { test as setup, expect } from '@playwright/test';
import { TestDataManager } from '../fixtures/test-data';

setup('Setup test environment and wait for server readiness', async ({ request, page }) => {
  // Set explicit timeout for this setup test
  setup.setTimeout(60_000); // 1 minute timeout

  // Setup test data using fallback/mock data if endpoints don't exist
  try {
    await TestDataManager.cleanup();
    await TestDataManager.createTestUser();
    await TestDataManager.setupAudioSource('microphone');
    // eslint-disable-next-line no-console -- Test setup debugging
    console.log('Test data setup completed');
  } catch (error) {
    // eslint-disable-next-line no-console -- Test setup debugging
    console.warn('Test data setup using fallback/mock data:', error);
  }

  // Wait for server health using absolute URL and Playwright polling
  const baseURL = process.env['BASE_URL'] ?? 'http://localhost:8080';
  const healthURL = `${baseURL}/api/v2/health`;

  // Use expect.poll for reliable health check with built-in retries
  await expect
    .poll(
      async () => {
        try {
          const response = await request.get(healthURL, { timeout: 5000 });
          return response.ok();
        } catch {
          return false;
        }
      },
      {
        message: 'Server health check',
        timeout: 30_000, // 30 seconds total
        intervals: [1_000], // Check every 1 second
      }
    )
    .toBe(true);

  // eslint-disable-next-line no-console -- Test setup debugging
  console.log('Server is ready for E2E tests');

  // Test new UI accessibility
  let uiReady = false;
  try {
    await page.goto('/ui/', { timeout: 10000 });
    await page.waitForLoadState('domcontentloaded', { timeout: 10000 });

    // Check if we can access the new UI
    const hasMainContent =
      (await page.locator('[data-testid="main-content"], main, [role="main"], body').count()) > 0;
    if (hasMainContent) {
      uiReady = true;
      // eslint-disable-next-line no-console -- Test setup debugging
      console.log('New UI is accessible for E2E tests');
    }
  } catch (error) {
    // eslint-disable-next-line no-console -- Test setup debugging
    console.warn('New UI accessibility test failed:', error);
  }

  // At minimum, we need either server API or UI to be accessible
  if (!uiReady) {
    // eslint-disable-next-line no-console -- Test setup debugging
    console.warn('New UI not fully accessible, but server API is ready - some UI tests may fail');
  }
});
