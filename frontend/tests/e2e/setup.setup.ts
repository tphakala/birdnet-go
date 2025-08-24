import { test as setup } from '@playwright/test';

setup('Wait for server to be ready', async ({ request }) => {
  // Wait for the application server to be responsive
  let retries = 0;
  const maxRetries = 30; // 30 seconds

  while (retries < maxRetries) {
    try {
      const response = await request.get('/api/v2/health', { timeout: 2000 });
      if (response.ok()) {
        // eslint-disable-next-line no-console
        console.log('Server is ready for E2E tests');
        return;
      }
    } catch {
      // Server not ready yet, continue waiting
    }

    retries++;
    await new Promise(resolve => setTimeout(resolve, 1000));
  }

  throw new Error('Server failed to become ready within timeout period');
});
