import { chromium } from '@playwright/test';
import type { FullConfig } from '@playwright/test';
import { TestDataManager } from '../fixtures/test-data';

async function globalSetup(_config: FullConfig) {
  const browser = await chromium.launch();
  const page = await browser.newPage();

  // Setup mock endpoints for test data management
  TestDataManager.setupMockEndpoints(page);

  // Setup test environment (will use mocks if endpoints don't exist)
  try {
    await TestDataManager.cleanup();
    await TestDataManager.createTestUser();
    await TestDataManager.setupAudioSource('microphone');
  } catch (error) {
    // eslint-disable-next-line no-console -- Test setup debugging
    console.warn('Test data setup using fallback/mock data:', error);
  }

  // Verify server is responding to at least the health endpoint
  try {
    const response = await page.request.get('/api/v2/health');
    if (!response.ok()) {
      // eslint-disable-next-line no-console -- Test setup debugging
      console.warn(`Server health check returned ${response.status()}, but continuing with tests`);
    }
  } catch (error) {
    // eslint-disable-next-line no-console -- Test setup debugging
    console.warn('Server health check failed, but continuing with tests:', error);
    // Don't fail the setup - tests should be able to run even if backend is not fully ready
  }

  // Test navigation to new UI to ensure it's accessible
  try {
    await page.goto('/ui/');
    await page.waitForLoadState('networkidle');
  } catch (error) {
    // eslint-disable-next-line no-console -- Test setup debugging
    console.warn('New UI navigation test failed during setup:', error);
  }

  await browser.close();
}

export default globalSetup;
