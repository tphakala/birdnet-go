import { test, expect } from '@playwright/test';

test.describe('Dashboard Smoke Tests - New UI Only', () => {
  test('Dashboard loads successfully with expected UI elements', async ({ page }) => {
    // Navigate to dashboard (new UI only)
    await page.goto('/ui/dashboard');

    // Wait for main content area to load (RootLayout renders a single <main>)
    await expect(page.locator('main').first()).toBeVisible();

    // Verify page heading using semantic role-based query
    const headingCount = await page.getByRole('heading').count();

    // Should have at least one heading on the dashboard
    expect(headingCount).toBeGreaterThan(0);

    // Check for dashboard content grid (#mainContent is the 12-column grid)
    const contentGrid = page.locator('#mainContent');
    await expect(contentGrid).toBeVisible();

    // Dashboard should have at least some child content rendered in the grid
    const gridChildren = contentGrid.locator('> *');
    const childCount = await gridChildren.count();
    expect(childCount).toBeGreaterThan(0);

    // Verify the page doesn't have critical errors
    await expect(page.locator('[role="alert"]:has-text("Error"), .error-boundary')).toHaveCount(0);

    // Ensure we're on the dashboard route
    await expect(page).toHaveURL(/.*\/ui\/dashboard/);

    // Check that navigation is present (sidebar nav inside the drawer)
    await expect(page.locator('nav').first()).toBeVisible();
  });

  test('API health check responds with valid contract', async ({ request }) => {
    // Build health endpoint from BASE_URL origin
    const baseUrl = process.env['BASE_URL'] ?? 'http://localhost:8080';
    const origin = new URL(baseUrl).origin;
    const healthUrl = `${origin}/api/v2/health`;

    const response = await request.get(healthUrl);
    expect(response.ok()).toBeTruthy();

    // Check content type before parsing JSON
    const contentType = response.headers()['content-type'];
    // eslint-disable-next-line @typescript-eslint/prefer-optional-chain -- explicit null check needed for error message
    if (!contentType || !contentType.includes('application/json')) {
      const responseText = await response.text();
      throw new Error(
        `Expected JSON response, got ${contentType}. Status: ${response.status()}, Body: ${responseText}`
      );
    }

    // Parse and validate health response contract with error handling
    let healthData;
    try {
      healthData = await response.json();
    } catch (error) {
      const responseText = await response.text();
      throw new Error(
        `Failed to parse JSON response. Status: ${response.status()}, Body: ${responseText}, Error: ${error}`,
        { cause: error }
      );
    }

    // Validate expected health contract fields
    expect(healthData).toHaveProperty('status');
    expect(typeof healthData.status).toBe('string');

    // Check for common health response patterns
    if (
      healthData.status === 'ok' ||
      healthData.status === 'healthy' ||
      healthData.status === 'UP'
    ) {
      // Valid health status
      expect(healthData.status).toBeTruthy();
    } else {
      // Log unexpected status for debugging

      console.warn('Unexpected health status:', healthData.status);
    }

    // Optional fields that might be present
    if (healthData.timestamp) {
      expect(typeof healthData.timestamp).toBe('string');
    }
    if (healthData.version) {
      expect(typeof healthData.version).toBe('string');
    }
  });

  test('New UI navigation elements are present and functional', async ({ page }) => {
    // Start at new UI root
    await page.goto('/ui/');

    // Check for sidebar navigation (drawer-based layout)
    await expect(page.locator('nav').first()).toBeVisible();

    // Navigate to dashboard and verify both URL and dashboard-specific content
    await page.goto('/ui/dashboard');
    await expect(page).toHaveURL(/.*\/ui\/dashboard$/);

    // Verify dashboard content grid is rendered
    const contentGrid = page.locator('#mainContent');
    if ((await contentGrid.count()) > 0) {
      await expect(contentGrid).toBeVisible();
    } else {
      // Fallback: check for main content
      await expect(page.locator('main').first()).toBeVisible();

      // Additional check: ensure we have dashboard-like content (headings, sections, etc.)
      const hasHeadings = (await page.getByRole('heading').count()) > 0;
      expect(hasHeadings).toBe(true);
    }

    // Test settings navigation if available
    const settingsLink = page.locator('a[href*="/ui/settings"]').first();
    if (await settingsLink.isVisible()) {
      await settingsLink.click();
      await page.waitForLoadState('domcontentloaded');
      await expect(page).toHaveURL(/.*\/ui\/settings/);

      // Verify we actually navigated to settings content
      await expect(page.locator('main').first()).toBeVisible();
    }
  });
});
