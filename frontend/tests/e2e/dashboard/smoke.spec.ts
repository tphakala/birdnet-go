import { test, expect } from '@playwright/test';
import { DashboardPage } from '../../support/pages/dashboard.page';

test.describe('Dashboard Smoke Tests', () => {
  test('Dashboard loads successfully', async ({ page }) => {
    const dashboard = new DashboardPage(page);

    // Navigate to dashboard
    await dashboard.navigate();

    // Verify basic page elements are visible
    await expect(page.locator('h1, h2')).toBeVisible();
    await expect(page.locator('body')).toContainText(/dashboard|bird|detection/i);

    // Verify the page doesn't have any critical errors
    await expect(page.locator('[role="alert"]')).not.toBeVisible();

    // Basic accessibility check - page should have a main heading
    await expect(page.locator('h1, h2, [aria-label]')).toBeVisible();
  });

  test('API health check responds', async ({ request }) => {
    // Test that the API is available for E2E tests
    const response = await request.get('/api/v2/health');
    expect(response.ok()).toBeTruthy();
  });

  test('Navigation elements are present', async ({ page }) => {
    await page.goto('/');

    // Look for navigation elements that should be present
    const navigation = page.locator('nav, [role="navigation"], [data-testid*="nav"]');
    await expect(navigation.first()).toBeVisible();

    // Verify we can navigate to dashboard
    await page.goto('/dashboard');
    await expect(page).toHaveURL(/.*\/dashboard$/);
  });
});
