import { chromium } from '@playwright/test';
import type { FullConfig } from '@playwright/test';
import { TestDataManager } from '../fixtures/test-data';

async function globalSetup(_config: FullConfig) {
  const browser = await chromium.launch();
  const page = await browser.newPage();

  // Setup test environment
  await TestDataManager.cleanup();
  await TestDataManager.createTestUser();
  await TestDataManager.setupAudioSource('microphone');

  // Verify server is responding
  const response = await page.request.get('/api/v2/health');
  if (!response.ok()) {
    throw new Error(`Server health check failed: ${response.status()}`);
  }

  await browser.close();
}

export default globalSetup;
