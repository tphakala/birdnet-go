import { test, expect } from '@playwright/test';

test.describe('Dashboard Smoke Tests - New UI Only', () => {
  test('Dashboard loads successfully with expected UI elements', async ({ page }) => {
    // Navigate to dashboard (new UI only)
    await page.goto('/ui/dashboard');

    // Wait for main content area to load
    await expect(page.locator('[data-testid="main-content"], main, [role="main"]')).toBeVisible();

    // Verify page heading using semantic role-based query
    const primaryHeading = page.getByRole('heading', { level: 1 });
    const headingCount = await page.getByRole('heading').count();

    // Should have at least one heading on the dashboard
    expect(headingCount).toBeGreaterThan(0);

    // If primary heading exists, it should be visible
    if ((await primaryHeading.count()) > 0) {
      await expect(primaryHeading.first()).toBeVisible();
    }

    // Check for key dashboard sections using stable test IDs
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

    // Check that specific navigation is present using controlled selector
    const mainNav = page
      .locator('[data-testid="main-navigation"], nav[data-testid*="nav"]')
      .first();
    if ((await mainNav.count()) > 0) {
      await expect(mainNav).toBeVisible();
    } else {
      // Fallback: check for any semantic navigation
      await expect(page.locator('nav').first()).toBeVisible();
    }
  });

  test('API health check responds with valid contract', async ({ request }) => {
    // Build absolute URL to avoid baseURL path issues
    const baseUrl = process.env['BASE_URL'] ?? 'http://localhost:8080';
    const healthUrl = `${baseUrl.replace(/\/ui$/, '')}/api/v2/health`;

    const response = await request.get(healthUrl);
    expect(response.ok()).toBeTruthy();

    // Parse and validate health response contract
    const healthData = await response.json();

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
      // eslint-disable-next-line no-console -- Test debugging output
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

    // Look for specific controlled navigation elements first
    const specificNav = page.locator(
      '[data-testid="main-navigation"], [data-testid="app-sidebar"]'
    );
    const hasSpecificNav = (await specificNav.count()) > 0;

    if (hasSpecificNav) {
      await expect(specificNav.first()).toBeVisible();
    } else {
      // Fallback to semantic navigation with aria-label
      const semanticNav = page.locator('nav[aria-label*="main"], nav[aria-label*="primary"]');
      if ((await semanticNav.count()) > 0) {
        await expect(semanticNav.first()).toBeVisible();
      } else {
        // Final fallback: any navigation element
        await expect(page.locator('nav').first()).toBeVisible();
      }
    }

    // Navigate to dashboard and verify both URL and dashboard-specific content
    await page.goto('/ui/dashboard');
    await expect(page).toHaveURL(/.*\/ui\/dashboard$/);

    // Verify dashboard-specific content is present
    const dashboardRoot = page.locator(
      '[data-testid="dashboard-root"], [data-testid="dashboard-page"]'
    );
    if ((await dashboardRoot.count()) > 0) {
      await expect(dashboardRoot.first()).toBeVisible();
    } else {
      // Fallback: check for main content with dashboard indicators
      const mainContent = page.locator('[data-testid="main-content"], main');
      await expect(mainContent.first()).toBeVisible();

      // Additional check: ensure we have dashboard-like content (headings, sections, etc.)
      const hasHeadings = (await page.getByRole('heading').count()) > 0;
      expect(hasHeadings).toBe(true);
    }

    // Test settings navigation if available
    const settingsLink = page
      .locator('[data-testid="settings-link"], a[href*="/ui/settings"]')
      .first();
    if (await settingsLink.isVisible()) {
      await settingsLink.click();
      await page.waitForLoadState('networkidle');
      await expect(page).toHaveURL(/.*\/ui\/settings/);

      // Verify we actually navigated to settings content
      const settingsContent = page.locator(
        '[data-testid="settings-page"], [data-testid="main-content"]'
      );
      await expect(settingsContent.first()).toBeVisible();
    }
  });
});
