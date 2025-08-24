import { test, expect } from '@playwright/test';

test.describe('Dashboard Smoke Tests - New UI Only', () => {
  test('Dashboard loads successfully with expected UI elements', async ({ page }) => {
    // Navigate to dashboard (new UI only)
    await page.goto('/ui/dashboard');

    // Wait for main content area to load
    await expect(page.locator('[data-testid="main-content"], main, [role="main"]')).toBeVisible();

    // Verify specific dashboard elements are present
    await expect(
      page.locator('[data-testid="dashboard-header"], h1:has-text("Dashboard")')
    ).toBeVisible();

    // Check for key dashboard sections
    const detectionSection = page
      .locator('[data-testid="recent-detections"], [data-testid="detections-card"]')
      .first();
    const statusSection = page
      .locator('[data-testid="status-indicator"], [data-testid="system-status"]')
      .first();

    // At least one of these should be visible on the dashboard
    const hasDashboardContent =
      (await detectionSection.isVisible()) || (await statusSection.isVisible());
    expect(hasDashboardContent).toBe(true);

    // Verify the page doesn't have critical errors
    await expect(
      page.locator('[role="alert"]:has-text("Error"), .error-boundary')
    ).not.toBeVisible();

    // Ensure we're on the new UI (not HTMX)
    await expect(page).toHaveURL(/.*\/ui\/dashboard/);

    // Check that essential navigation is present
    await expect(
      page.locator('nav, [data-testid="sidebar"], [data-testid="navigation"]')
    ).toBeVisible();
  });

  test('API health check responds', async ({ request }) => {
    // Test that the API is available for E2E tests
    const response = await request.get('/api/v2/health');
    expect(response.ok()).toBeTruthy();
  });

  test('New UI navigation elements are present and functional', async ({ page }) => {
    // Start at new UI root
    await page.goto('/ui/');

    // Look for navigation elements that should be present in the new UI
    const navigation = page.locator(
      'nav, [role="navigation"], [data-testid*="nav"], [data-testid="sidebar"]'
    );
    await expect(navigation.first()).toBeVisible();

    // Verify we can navigate to dashboard within the new UI
    await page.goto('/ui/dashboard');
    await expect(page).toHaveURL(/.*\/ui\/dashboard$/);

    // Check that we can navigate to other sections (if they exist)
    const settingsLink = page.locator('a[href*="/ui/settings"], [data-testid*="settings"]').first();
    if (await settingsLink.isVisible()) {
      await settingsLink.click();
      await expect(page).toHaveURL(/.*\/ui\/settings/);
    }
  });
});
