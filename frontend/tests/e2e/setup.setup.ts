import { test as setup } from '@playwright/test';

setup('Wait for server and new UI to be ready', async ({ request, page }) => {
  // Wait for the application server to be responsive
  let serverReady = false;
  let retries = 0;
  const maxRetries = 30; // 30 seconds

  while (retries < maxRetries && !serverReady) {
    try {
      const response = await request.get('/api/v2/health', { timeout: 2000 });
      if (response.ok()) {
        serverReady = true;
        // eslint-disable-next-line no-console
        console.log('Server is ready for E2E tests');
        break;
      }
    } catch {
      // Server not ready yet, continue waiting
    }

    retries++;
    await new Promise(resolve => setTimeout(resolve, 1000));
  }

  // Test new UI accessibility even if API health check fails
  let uiReady = false;
  try {
    await page.goto('/ui/', { timeout: 10000 });
    await page.waitForLoadState('networkidle', { timeout: 10000 });

    // Check if we can access the new UI
    const hasMainContent =
      (await page.locator('[data-testid="main-content"], main, [role="main"], body').count()) > 0;
    if (hasMainContent) {
      uiReady = true;
      // eslint-disable-next-line no-console
      console.log('New UI is accessible for E2E tests');
    }
  } catch (error) {
    // eslint-disable-next-line no-console
    console.warn('New UI accessibility test failed:', error);
  }

  // Require either server or UI to be ready for tests to proceed
  if (!serverReady && !uiReady) {
    throw new Error('Neither server API nor new UI is ready for testing within timeout period');
  }

  if (!serverReady) {
    // eslint-disable-next-line no-console
    console.warn('Server API not ready, but new UI is accessible - proceeding with limited tests');
  }

  if (!uiReady) {
    // eslint-disable-next-line no-console
    console.warn('New UI not accessible, but server API is ready - some tests may fail');
  }
});
